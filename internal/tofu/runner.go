// Package tofu 封装 OpenTofu 二进制的调用：每个云实例一个独立的
// workspace 工作目录，模板按 provider 复制进去后依次执行
// init / apply / destroy，并通过 output 读取公私网 IP。
//
// 契约签名见仓库根 ARCHITECTURE.md（internal/tofu 一节）。
package tofu

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Runner 管理每个实例的 OpenTofu 工作目录（跨包契约，签名必须与
// ARCHITECTURE.md 保持精确一致；scheduler / api 层测试可注入 fake 实现）。
type Runner interface {
	// Apply 首次调用会把 templates/<provider>/ 下的 .tf 模板复制进
	// workspace 目录，写入 terraform.tfvars.json，然后依次执行
	// tofu init 与 tofu apply -auto-approve。
	Apply(ctx context.Context, workspace string, vars map[string]string) error
	// Destroy 销毁 workspace 对应的基础设施（tofu destroy -auto-approve）。
	Destroy(ctx context.Context, workspace string) error
	// OutputIP 从 tofu state 读取 public_ip / private_ip 输出值。
	OutputIP(ctx context.Context, workspace string) (public, private string, err error)
}

//go:embed templates
var templateFS embed.FS

// Credentials 按 provider 注入的云凭证，例如
// {"alicloud": {"access_key": "...", "secret_key": "..."}}。
// 凭证只通过 TF_VAR_ 环境变量传给 tofu 进程，绝不写盘。
type Credentials map[string]map[string]string

// ChangeSummary 来自 `tofu apply -json` 的 change_summary 事件，
// 表示本次变更将创建/更新/删除的资源数量。
type ChangeSummary struct {
	Create int `json:"create"`
	Update int `json:"update"`
	Delete int `json:"delete"`
	NoOp   int `json:"no-op"`
}

// ProgressFunc 可选的进度回调：apply 过程中每解析到一个
// change_summary 事件就回调一次。
type ProgressFunc func(summary ChangeSummary)

// Config tofu Runner 的可注入配置。
type Config struct {
	// TofuPath tofu 二进制路径，默认 "tofu"；测试可指向 fake 脚本。
	TofuPath string
	// DataDir 数据目录；workspace 位于 DataDir/workspaces/<name>，
	// 插件缓存位于 DataDir/plugin-cache。
	DataDir string
	// Credentials 按 provider 的云凭证（以 TF_VAR_ 环境变量注入）。
	Credentials Credentials
	// OnProgress 可选 change_summary 进度回调。
	OnProgress ProgressFunc
	// Templates 可选模板文件系统（按 provider 子目录组织），
	// 默认使用包内 embed 的 templates 目录；测试可注入内存 FS。
	Templates fs.FS
	// OnLog 可选：apply 过程事件回调（人类可读行，供控制台展示创建过程）。
	OnLog func(line string)
	// ExtraEnv 追加到 tofu 进程的额外环境变量（如代理：
	// HTTPS_PROXY=...；registry.opentofu.org 通常直连更快，可配合
	// NO_PROXY 精细分流，仅让 GitHub 下载走代理）。
	ExtraEnv []string
}

// NewRunner 按配置构造 Runner。
func NewRunner(cfg Config) Runner {
	if cfg.TofuPath == "" {
		cfg.TofuPath = "tofu"
	}
	if cfg.Templates == nil {
		cfg.Templates = templateFS
	}
	return &runner{cfg: cfg}
}

type runner struct {
	cfg Config
	// mu 保护同 workspace 的 init/apply 串行化（简单互斥足够；
	// scheduler 层保证同一实例不会并发创建/销毁，这里兜底）。
	mu sync.Mutex
}

// EnvVars 返回指定 provider 的云凭证环境变量（TF_VAR_ 前缀），
// 供 exec 注入；凭证不落盘。
func (r *runner) EnvVars(provider string) []string {
	creds := r.cfg.Credentials[provider]
	env := make([]string, 0, len(creds))
	for k, v := range creds {
		env = append(env, fmt.Sprintf("TF_VAR_%s=%s", k, v))
	}
	sort.Strings(env)
	return env
}

func (r *runner) Apply(ctx context.Context, workspace string, vars map[string]string) error {
	if err := validWorkspace(workspace); err != nil {
		return err
	}
	provider, ok := vars["provider"]
	if !ok || provider == "" {
		return errors.New("tofu: vars 缺少 provider 字段（用于选择模板与凭证）")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	wsDir := r.workspaceDir(workspace)
	// 插件缓存目录必须用绝对路径：tofu 以 workspace 为工作目录执行，
	// 相对路径会解析到 workspace 内部导致 "cannot be opened"。
	if _, err := r.pluginCacheDir(); err != nil {
		return err
	}
	if err := ensureWorkspace(wsDir, provider, r.cfg.Templates); err != nil {
		return err
	}
	if err := writeTFVars(wsDir, vars); err != nil {
		return err
	}
	// 记录 provider 元数据：tfvars 不含 provider 键（模板未声明），
	// destroy/output 需要它来恢复云凭据。
	if err := os.WriteFile(filepath.Join(wsDir, ".ocw-provider"), []byte(provider), 0o600); err != nil {
		return err
	}

	env := r.env(provider)
	// tofu init：禁用交互输入，插件缓存指向 DataDir/plugin-cache，
	// 避免每次 workspace 都重新下载 provider。
	// 注意：TF_PLUGIN_CACHE_DIR 只跳过二进制下载，init 仍会联网到
	// registry 做 checksums 签名校验（依赖 GitHub，弱网下必超时）。
	// 因此当缓存里已有该 provider 的二进制时，改用 -plugin-dir 完全
	// 离线安装（不访问 registry）。
	initArgs := []string{"init", "-input=false", "-no-color"}
	if r.pluginCacheHas(provider) {
		if dir, err := r.pluginCacheDir(); err == nil {
			initArgs = append(initArgs, "-plugin-dir="+dir)
			// -plugin-dir 与 TF_PLUGIN_CACHE_DIR 互斥：同时设置时 tofu
			// 会尝试把 provider “安装回缓存自身”而失败，须剔除该变量。
			env = envWithout(env, "TF_PLUGIN_CACHE_DIR")
		}
	}
	if err := r.run(ctx, wsDir, env, nil, initArgs...); err != nil {
		return fmt.Errorf("tofu init: %w", err)
	}
	return r.apply(ctx, wsDir, env)
}

// providerRegistryNS 把平台 provider 名映射为 registry 命名空间/名称。
func providerRegistryNS(provider string) (string, string, bool) {
	switch provider {
	case "alicloud":
		return "aliyun", "alicloud", true
	case "volcengine":
		return "volcengine", "volcengine", true
	}
	return "", "", false
}

// pluginCacheHas 判断插件缓存中是否已有该 provider 当前平台的二进制
// （任一版本，配合模板的 >= 约束可离线安装）。
func (r *runner) pluginCacheHas(provider string) bool {
	ns, name, ok := providerRegistryNS(provider)
	if !ok {
		return false
	}
	plat := runtime.GOOS + "_" + runtime.GOARCH
	root := filepath.Join(r.pluginCacheDirPath(), "registry.opentofu.org", ns, name)
	versions, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, v := range versions {
		dir := filepath.Join(root, v.Name(), plat)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "terraform-provider-") {
				return true
			}
		}
	}
	return false
}

func (r *runner) Destroy(ctx context.Context, workspace string) error {
	if err := validWorkspace(workspace); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	wsDir := r.workspaceDir(workspace)
	if _, err := os.Stat(filepath.Join(wsDir, "terraform.tfstate")); err != nil {
		if os.IsNotExist(err) {
			return nil // 尚未 apply 过，视为已销毁（幂等）
		}
		return err
	}
	provider := r.detectProvider(wsDir)
	if err := r.run(ctx, wsDir, r.env(provider), nil,
		"destroy", "-auto-approve", "-no-color"); err != nil {
		return fmt.Errorf("tofu destroy: %w", err)
	}
	return nil
}

func (r *runner) OutputIP(ctx context.Context, workspace string) (public, private string, err error) {
	if err := validWorkspace(workspace); err != nil {
		return "", "", err
	}
	wsDir := r.workspaceDir(workspace)
	var out bytes.Buffer
	if err := r.run(ctx, wsDir, r.env(r.detectProvider(wsDir)), &out, "output", "-json"); err != nil {
		return "", "", fmt.Errorf("tofu output: %w", err)
	}
	var raw map[string]struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		return "", "", fmt.Errorf("tofu output: 解析失败: %w", err)
	}
	return outputValue(raw, "public_ip"), outputValue(raw, "private_ip"), nil
}

// outputValue 兼容 string 与 []string 两种输出形式。
func outputValue(raw map[string]struct {
	Value json.RawMessage `json:"value"`
}, key string) string {
	o, ok := raw[key]
	if !ok || len(o.Value) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(o.Value, &s) == nil {
		return s
	}
	var list []string
	if json.Unmarshal(o.Value, &list) == nil && len(list) > 0 {
		return list[0]
	}
	return ""
}

func (r *runner) workspaceDir(workspace string) string {
	return filepath.Join(r.cfg.DataDir, "workspaces", workspace)
}

// env 组装 tofu 进程环境：插件缓存目录 + TF_VAR_ 凭证。
func (r *runner) env(provider string) []string {
	env := append(os.Environ(),
		"TF_IN_AUTOMATION=1",
		"TF_PLUGIN_CACHE_DIR="+r.pluginCacheDirPath(),
	)
	env = append(env, r.cfg.ExtraEnv...)
	return append(env, r.EnvVars(provider)...)
}

// pluginCacheDir 创建（若缺）并返回插件缓存目录的绝对路径。
func (r *runner) pluginCacheDir() (string, error) {
	dir, err := filepath.Abs(filepath.Join(r.cfg.DataDir, "plugin-cache"))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// pluginCacheDirPath 已知目录存在时直接取绝对路径（env 组装用）。
func (r *runner) pluginCacheDirPath() string {
	dir, _ := filepath.Abs(filepath.Join(r.cfg.DataDir, "plugin-cache"))
	return dir
}

// detectProvider 从 workspace 已写入的 tfvars 里读回 provider，
// 用于 destroy/output 时注入对应凭证。
func (r *runner) detectProvider(wsDir string) string {
	// 首选 Apply 写入的元数据文件；兼容旧 workspace 回落读 tfvars
	// （旧 tfvars 全字符串，且 provider 键实际已被剔除，通常为空）。
	if b, err := os.ReadFile(filepath.Join(wsDir, ".ocw-provider")); err == nil {
		if p := strings.TrimSpace(string(b)); p != "" {
			return p
		}
	}
	data, err := os.ReadFile(filepath.Join(wsDir, "terraform.tfvars.json"))
	if err != nil {
		return ""
	}
	var vars map[string]any
	if json.Unmarshal(data, &vars) != nil {
		return ""
	}
	p, _ := vars["provider"].(string)
	return p
}

// validWorkspace 防止 workspace 名逃逸目录（路径穿越）。
func validWorkspace(name string) error {
	if name == "" || name == "." || name == ".." ||
		strings.ContainsAny(name, `/\`) || strings.Contains(name, string(filepath.Separator)) {
		return fmt.Errorf("tofu: 非法 workspace 名 %q", name)
	}
	return nil
}

// ensureWorkspace 首次使用时把模板目录（templates/<provider>/）下的
// .tf 文件复制进 workspace；已初始化的目录保持不动（幂等）。
func ensureWorkspace(wsDir, provider string, templates fs.FS) error {
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(wsDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tf") {
			return nil // 已初始化
		}
	}
	root := fmt.Sprintf("templates/%s", provider)
	tfs, err := fs.Glob(templates, root+"/*.tf")
	if err != nil {
		return err
	}
	if len(tfs) == 0 {
		return fmt.Errorf("tofu: 未找到 provider %q 的模板", provider)
	}
	for _, name := range tfs {
		data, err := fs.ReadFile(templates, name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(wsDir, filepath.Base(name)), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// writeTFVars 把 vars 序列化为 terraform.tfvars.json；
// "provider" 仅用于路由模板/凭证，不写入（模板未声明该变量）。
// 纯数字字符串写成 JSON number，以匹配模板中 number 类型的变量
// （如 public_bandwidth），否则 tofu 会因类型不匹配拒绝加载。
func writeTFVars(wsDir string, vars map[string]string) error {
	clean := make(map[string]any, len(vars))
	for k, v := range vars {
		if k == "provider" {
			continue
		}
		if n, err := strconv.Atoi(v); err == nil {
			clean[k] = n
		} else {
			clean[k] = v
		}
	}
	data, err := json.MarshalIndent(clean, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(wsDir, "terraform.tfvars.json"), data, 0o600)
}

// apply 执行 `tofu apply -auto-approve -json`，流式解析 JSON 输出：
//   - diagnostic 事件（severity=error）提取 summary/detail 作为错误信息；
//   - change_summary 事件触发可选进度回调。
func (r *runner) apply(ctx context.Context, wsDir string, env []string) error {
	cmd := exec.CommandContext(ctx, r.cfg.TofuPath,
		"apply", "-auto-approve", "-no-color", "-json")
	cmd.Dir = wsDir
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("tofu apply: 启动 %s: %w", r.cfg.TofuPath, err)
	}

	// 解析 -json 事件流（每行一个 JSON 对象，见 OpenTofu/Terraform
	// JSON 输出格式文档：https://opentofu.org/docs/internals/json-format/）。
	var diags []string
	dec := json.NewDecoder(stdout)
	for {
		var msg struct {
			Level string `json:"@level"`
			Type  string `json:"type"`
			Hook  *struct {
				Resource *struct {
					Addr string `json:"addr"`
				} `json:"resource"`
				Action *string `json:"action"`
			} `json:"hook"`
			Diagnostic *struct {
				Severity string `json:"severity"`
				Summary  string `json:"summary"`
				Detail   string `json:"detail"`
			} `json:"diagnostic"`
			Changes *ChangeSummary `json:"changes"`
		}
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			break // 输出被截断时退出码兜底
		}
		if msg.Type == "diagnostic" && msg.Diagnostic != nil {
			d := msg.Diagnostic.Severity + ": " + msg.Diagnostic.Summary
			if msg.Diagnostic.Detail != "" {
				d += " — " + msg.Diagnostic.Detail
			}
			diags = append(diags, d)
		}
		if msg.Type == "change_summary" && msg.Changes != nil && r.cfg.OnProgress != nil {
			r.cfg.OnProgress(*msg.Changes)
		}
		// 创建过程事件 → 可读日志行（供控制台实时展示）。
		if r.cfg.OnLog != nil {
			switch msg.Type {
			case "apply_start":
				if msg.Hook != nil && msg.Hook.Resource != nil {
					r.cfg.OnLog("开始应用: " + msg.Hook.Resource.Addr)
				}
			case "apply_progress":
				if msg.Hook != nil && msg.Hook.Resource != nil {
					r.cfg.OnLog("进行中: " + msg.Hook.Resource.Addr)
				}
			case "apply_complete":
				if msg.Hook != nil && msg.Hook.Resource != nil {
					r.cfg.OnLog("完成: " + msg.Hook.Resource.Addr)
				}
			case "diagnostic":
				if msg.Diagnostic != nil {
					d := msg.Diagnostic.Severity + ": " + msg.Diagnostic.Summary
					if msg.Diagnostic.Detail != "" {
						d += " — " + msg.Diagnostic.Detail
					}
					r.cfg.OnLog(d)
				}
			case "change_summary":
				if msg.Changes != nil {
					r.cfg.OnLog(fmt.Sprintf("变更汇总: 新建 %d / 更新 %d / 删除 %d",
						msg.Changes.Create, msg.Changes.Update, msg.Changes.Delete))
				}
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("tofu apply: %w（%s）", ctx.Err(), err)
		}
		if len(diags) > 0 {
			return fmt.Errorf("tofu apply: %s", strings.Join(diags, "; "))
		}
		tail := strings.TrimSpace(stderr.String())
		if tail == "" {
			return fmt.Errorf("tofu apply: %w", err)
		}
		return fmt.Errorf("tofu apply: %s", clip(tail, 2000))
	}
	return nil
}

// run 执行一个普通 tofu 子命令（init/destroy/output），捕获输出。
// stdout 与 stderr 各用独立 buffer：exec 对两路输出各起一个 goroutine
// 并发拷贝，共用一个非线程安全的 buffer 会构成数据竞争。
func (r *runner) run(ctx context.Context, wsDir string, env []string, stdout io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, r.cfg.TofuPath, args...)
	cmd.Dir = wsDir
	cmd.Env = env
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	if stdout != nil {
		cmd.Stdout = io.MultiWriter(&outBuf, stdout)
	}
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("%w（%s）", ctx.Err(), err)
	}
	combined := strings.TrimSpace(errBuf.String())
	if combined == "" {
		combined = strings.TrimSpace(outBuf.String())
	}
	return fmt.Errorf("%s", clip(combined, 2000))
}

// clip 截断过长输出，避免错误信息撑爆日志。
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…(截断)"
}

// Preload 仅执行模板准备与 `tofu init`：把 provider 插件下载进共享缓存，
// 不创建任何云资源（用于控制台预下载与状态展示）。
func (r *runner) Preload(ctx context.Context, provider string, progress io.Writer) error {
	if provider != "alicloud" && provider != "volcengine" {
		return fmt.Errorf("tofu: unknown provider %q", provider)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.pluginCacheDir(); err != nil {
		return err
	}
	wsDir := r.workspaceDir("preload-" + provider)
	if err := ensureWorkspace(wsDir, provider, r.cfg.Templates); err != nil {
		return err
	}
	env := r.env(provider) // init 不调云 API，凭证为空无影响
	if err := r.run(ctx, wsDir, env, progress, "init", "-input=false", "-no-color"); err != nil {
		return fmt.Errorf("tofu init: %w", err)
	}
	return nil
}

// CachedProvider 描述插件缓存里的一个 provider 二进制。
type CachedProvider struct {
	Namespace string    `json:"namespace"` // 如 volcengine
	Name      string    `json:"name"`      // 如 volcengine
	Version   string    `json:"version"`   // 如 0.0.196
	Platform  string    `json:"platform"`  // 如 darwin_amd64
	Size      int64     `json:"size"`      // 字节
	ModTime   time.Time `json:"modTime"`   // 下载完成时间
}

// ListCachedProviders 扫描插件缓存目录（DataDir/plugin-cache），返回已
// 下载的 provider 列表。目录层级：
// registry.opentofu.org/<ns>/<name>/<version>/<os_arch>/<binary>
func ListCachedProviders(dataDir string) []CachedProvider {
	root := filepath.Join(dataDir, "plugin-cache", "registry.opentofu.org")
	out := []CachedProvider{}
	entries, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, ns := range entries {
		if !ns.IsDir() {
			continue
		}
		names, err := os.ReadDir(filepath.Join(root, ns.Name()))
		if err != nil {
			continue
		}
		for _, name := range names {
			if !name.IsDir() {
				continue
			}
			versions, err := os.ReadDir(filepath.Join(root, ns.Name(), name.Name()))
			if err != nil {
				continue
			}
			for _, ver := range versions {
				if !ver.IsDir() {
					continue
				}
				platforms, err := os.ReadDir(filepath.Join(root, ns.Name(), name.Name(), ver.Name()))
				if err != nil {
					continue
				}
				for _, plat := range platforms {
					pdir := filepath.Join(root, ns.Name(), name.Name(), ver.Name(), plat.Name())
					if !plat.IsDir() {
						continue
					}
					files, err := os.ReadDir(pdir)
					if err != nil {
						continue
					}
					for _, f := range files {
						if f.IsDir() {
							continue
						}
						// 只统计 provider 主二进制，排除 LICENSE/README/锁文件。
						if !strings.HasPrefix(f.Name(), "terraform-provider-") {
							continue
						}
						info, err := f.Info()
						if err != nil {
							continue
						}
						out = append(out, CachedProvider{
							Namespace: ns.Name(), Name: name.Name(), Version: ver.Name(),
							Platform: plat.Name(), Size: info.Size(), ModTime: info.ModTime(),
						})
					}
				}
			}
		}
	}
	return out
}

// envWithout 从环境变量切片中剔除指定键（返回新切片）。
func envWithout(env []string, key string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, key+"=") {
			continue
		}
		out = append(out, e)
	}
	return out
}
