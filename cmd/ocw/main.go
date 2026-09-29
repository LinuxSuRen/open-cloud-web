// Command ocw 是 OpenCloudLab 的主程序入口，负责装配各内部包：
// config、store、auth、api、cloud、tofu 与 scheduler，并处理信号优雅关停。
//
// 跨包调用签名依据 ARCHITECTURE.md 契约与各包当前已发布的构造函数书写；
// internal/api、internal/cloud 等仍在并行开发中，若其类型尚未收敛
// （如 auth.User 与 model.User 的归并），本文件可能暂不编译——签名以契约为准。
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/linuxsuren/open-cloud-web/internal/api"
	"github.com/linuxsuren/open-cloud-web/internal/auth"
	"github.com/linuxsuren/open-cloud-web/internal/config"
	"github.com/linuxsuren/open-cloud-web/internal/model"
	"github.com/linuxsuren/open-cloud-web/internal/scheduler"
	"github.com/linuxsuren/open-cloud-web/internal/secrets"
	"github.com/linuxsuren/open-cloud-web/internal/store"
	"github.com/linuxsuren/open-cloud-web/internal/tofu"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("ocw: %v", err)
	}
}

func run() error {
	// 1. 配置（一律来自环境变量，OCW_ 前缀，见 internal/config）。
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.JWTSecretGenerated {
		log.Printf("[ocw] warning: OCW_JWT_SECRET 未设置，已生成临时随机值（重启后登录会话失效；生产环境请显式设置）")
		// 零配置开发模式：同时给 admin 引导口令一个默认值，否则无任何登录途径。
		if cfg.AdminBootstrapToken == "" {
			cfg.AdminBootstrapToken = "admin12345"
			log.Printf("[ocw] warning: OCW_ADMIN_BOOTSTRAP_TOKEN 未设置，开发模式默认 admin/admin12345（生产环境务必显式设置强口令）")
		}
	}
	log.Printf("[ocw] starting: listen=%s db=%s dataDir=%s", cfg.ListenAddr, cfg.DBPath, cfg.DataDir)

	// 2. 存储（契约见 ARCHITECTURE.md store.Store）；自动创建数据目录。
	if dir := filepath.Dir(cfg.DBPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create data dir %s: %w", dir, err)
		}
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir %s: %w", cfg.DataDir, err)
	}
	st, err := store.OpenSQLitePath(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	// 3. admin bootstrap：AdminBootstrapToken 非空且当前无 admin 用户时创建。
	if err := bootstrapAdmin(st, cfg.AdminBootstrapToken); err != nil {
		return err
	}
	seedSecurityGroups(st)

	// 4. 认证：JWT manager + OAuth state 管理 + 飞书 OAuth。
	mgr := auth.NewManager(cfg.JWTSecret)
	states := auth.NewStateManager(cfg.JWTSecret)
	feishu := &auth.FeishuOAuth{
		AppID:       cfg.FeishuClientID,
		AppSecret:   cfg.FeishuClientSecret,
		RedirectURL: cfg.FeishuRedirectURL,
	}

	// 5. RunnerFactory：云凭据来自“用户添加的云账号”（数据库加密存储），
	//    不再使用环境变量写死的全局凭据。
	secretKey := cfg.SecretKey
	if secretKey == "" {
		secretKey = cfg.JWTSecret // 回落 JWT secret（生产建议独立设置 OCW_SECRET_KEY）
	}
	// RunnerFactory 每次调用时读取最新代理设置（admin 可在控制台随时修改）。
	// registry.opentofu.org 固定直连（经代理常更慢），仅 GitHub 下载走代理。
	newRunner := func(provider, ak, sk, session string, onLog func(string)) tofu.Runner {
		var extra []string
		if proxy, _ := st.GetSetting("http_proxy_tofu"); proxy != "" {
			extra = []string{
				"HTTPS_PROXY=" + proxy,
				"HTTP_PROXY=" + proxy,
				"NO_PROXY=registry.opentofu.org,127.0.0.1,localhost",
			}
			log.Printf("[ocw] tofu 使用代理 %s（registry.opentofu.org 直连）", proxy)
		}
		return tofu.NewRunner(tofu.Config{
			TofuPath: cfg.TofuBinary,
			DataDir:  cfg.DataDir,
			OnLog:    onLog,
			ExtraEnv: extra,
			Credentials: tofu.Credentials{
				provider: {"access_key": ak, "secret_key": sk, "session_token": session},
			},
		})
	}

	// 6. 生命周期调度器：storeSchedulerAdapter 把 store.Store（model.Instance）
	// 适配为 scheduler.InstanceStore；accountDestroyer 按 workspace 恢复账号凭据。
	sched := scheduler.New(
		&storeSchedulerAdapter{store: st},
		&accountDestroyer{store: st, secretKey: secretKey, newRunner: func(p, a, k string) tofu.Runner { return newRunner(p, a, k, "", nil) }},
		func(userID int64, action, detail string) {
			_ = st.CreateAuditLog(&model.AuditLog{UserID: userID, Action: action, Detail: detail, CreatedAt: time.Now().UTC()})
		},
		scheduler.WithConfig(scheduler.Config{
			Interval:               time.Duration(cfg.SchedulerIntervalSec) * time.Second,
			CreatingTimeout:        time.Duration(cfg.CreatingTimeoutSec) * time.Second,
			DestroyingStuckTimeout: 15 * time.Minute,
			MaxBackoff:             10 * time.Minute,
		}),
	)

	// 7. HTTP API（/api/v1，含 GET /healthz 健康检查）。
	handler := api.NewHandler(
		st, mgr, feishu, states,
		func(provider, ak, sk, session string, onLog func(string)) api.Runner {
			return newRunner(provider, ak, sk, session, onLog)
		},
		api.Config{
			DefaultDurationSec: cfg.DefaultDurationSec,
			MaxDurationSec:     cfg.MaxDurationSec,
			DefaultRenewTimes:  cfg.DefaultRenewTimes,
			CORSAllowedOrigin:  cfg.CORSAllowedOrigin,
			SecretKey:          secretKey,
			DataDir:            cfg.DataDir,
		},
	)
	// provider 预下载：init 不调云 API，用空凭据的 runner 即可。
	handler.PreloadProvider = func(ctx context.Context, provider string, w io.Writer) error {
		return newRunner(provider, "", "", "", nil).(interface {
			Preload(ctx context.Context, provider string, progress io.Writer) error
		}).Preload(ctx, provider, w)
	}

	srv := &http.Server{Addr: cfg.ListenAddr, Handler: handler.Routes()}

	// 9. 信号处理：SIGINT/SIGTERM 优雅关停（等在途 destroy 完成）。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go sched.Run(ctx) // 内部等待在途 destroy 完成后返回

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("[ocw] http server: %v", err)
		}
	}()
	log.Printf("[ocw] http server listening on %s", cfg.ListenAddr)

	<-ctx.Done()
	log.Printf("[ocw] shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("[ocw] http shutdown: %v", err)
	}
	log.Printf("[ocw] stopped")
	return nil
}

// bootstrapAdmin：AdminBootstrapToken 非空且当前无激活 admin 用户时创建 admin，
// 初始口令为该 token（bcrypt 哈希后经可选 store 能力落地，见 api.PasswordHashSetter）。
func bootstrapAdmin(st store.Store, token string) error {
	if token == "" {
		return nil
	}
	users, err := st.ListUsers()
	if err != nil {
		return err
	}
	for _, u := range users {
		if u.Role == model.RoleAdmin && u.Status == model.StatusActive {
			return nil // 已存在 admin
		}
	}
	now := time.Now().UTC()
	hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	admin := &model.User{
		Username:     "admin",
		DisplayName:  "Administrator",
		Role:         model.RoleAdmin,
		Status:       model.StatusActive,
		Provider:     "local",
		ProviderSub:  "admin", // users(provider, provider_sub) 唯一索引要求
		PasswordHash: string(hash),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := st.CreateUser(admin); err != nil {
		return err
	}
	log.Printf("[ocw] bootstrap admin user created (username=admin, password=OCW_ADMIN_BOOTSTRAP_TOKEN, login via POST /api/v1/auth/login)")
	return nil
}

// storeSchedulerAdapter 把 store.Store（操作 model.Instance）适配为
// scheduler.InstanceStore（操作 scheduler.Instance 的最小视图）。
type storeSchedulerAdapter struct {
	store store.Store
}

func (a *storeSchedulerAdapter) ListInstancesByStatuses(statuses []scheduler.InstanceStatus) ([]*scheduler.Instance, error) {
	modelStatuses := make([]model.InstanceStatus, 0, len(statuses))
	for _, s := range statuses {
		modelStatuses = append(modelStatuses, model.InstanceStatus(s))
	}
	insts, err := a.store.ListInstancesByStatuses(modelStatuses)
	if err != nil {
		return nil, err
	}
	out := make([]*scheduler.Instance, 0, len(insts))
	for _, i := range insts {
		out = append(out, &scheduler.Instance{
			ID:           i.ID,
			UserID:       i.UserID,
			Status:       scheduler.InstanceStatus(i.Status),
			TfWorkspace:  i.TfWorkspace,
			ErrorMessage: i.ErrorMessage,
			ExpiresAt:    i.ExpiresAt,
			CreatedAt:    i.CreatedAt,
			UpdatedAt:    i.UpdatedAt,
		})
	}
	return out, nil
}

// UpdateInstance 采用“读取-合并-写回”：scheduler.Instance 只携带其管辖的
// 字段（Status/ErrorMessage 等），而 store.UpdateInstance 是全列 UPDATE，
// 直接写回部分字段会清空 Name/ImageID/PublicIP 等。先取完整记录，
// 覆盖调度器拥有的字段后再落库。
func (a *storeSchedulerAdapter) UpdateInstance(i *scheduler.Instance) error {
	full, err := a.store.GetInstance(i.ID)
	if err != nil {
		return err
	}
	full.Status = model.InstanceStatus(i.Status)
	full.ErrorMessage = i.ErrorMessage
	if !i.ExpiresAt.IsZero() {
		full.ExpiresAt = i.ExpiresAt
	}
	if !i.UpdatedAt.IsZero() {
		full.UpdatedAt = i.UpdatedAt
	}
	return a.store.UpdateInstance(full)
}

// accountDestroyer 实现 scheduler.Destroyer：按 workspace 找到实例，
// 再凭实例记录的云账号恢复凭据构造 runner 执行销毁。
type accountDestroyer struct {
	store     *store.SQLiteStore
	secretKey string
	newRunner func(provider, ak, sk string) tofu.Runner
}

func (d *accountDestroyer) Destroy(ctx context.Context, workspace string) error {
	inst, err := d.store.GetInstanceByWorkspace(workspace)
	if err != nil {
		return fmt.Errorf("destroy %s: %w", workspace, err)
	}
	if inst.CloudAccountID <= 0 {
		return fmt.Errorf("destroy %s: instance has no cloud account", workspace)
	}
	acct, err := d.store.GetCloudAccount(inst.CloudAccountID)
	if err != nil {
		return fmt.Errorf("destroy %s: cloud account unavailable: %w", workspace, err)
	}
	sk, err := secrets.Decrypt(acct.SecretEnc, d.secretKey)
	if err != nil {
		return fmt.Errorf("destroy %s: %w", workspace, err)
	}
	return d.newRunner(acct.Provider, acct.AccessKey, sk).Destroy(ctx, workspace)
}

// seedSecurityGroups 首次启动预置常用端口集合（已存在则跳过）。
func seedSecurityGroups(st store.Store) {
	if existing, err := st.ListSecurityGroups(0); err == nil && len(existing) > 0 {
		return
	}
	presets := []struct {
		name     string
		ports    []int
		udpPorts []int
		remark   string
	}{
		{"仅 SSH", []int{22}, nil, "默认：仅开放 SSH"},
		{"Web 服务", []int{22, 80, 443}, nil, "SSH + HTTP/HTTPS"},
		{"MQTT 服务", []int{22, 1883, 8883}, nil, "SSH + MQTT/MQTTS"},
		{"常用测试", []int{22, 80, 443, 1883, 3306, 6379, 8080}, nil, "SSH/Web/MQTT/MySQL/Redis/8080"},
		{"MediaMTX 流媒体", []int{22, 1935, 8554, 8888, 8889}, []int{8189, 8890},
			"RTMP 1935 / RTSP 8554 / HLS 8888 / WebRTC 8889 + WebRTC ICE 8189(UDP) / SRT 8890(UDP)"},
	}
	existing, _ := st.ListSecurityGroups(0)
	have := map[string]bool{}
	for _, g := range existing {
		have[g.Name] = true
	}
	n := 0
	for _, p := range presets {
		if have[p.name] {
			continue
		}
		if err := st.CreateSecurityGroup(&model.SecurityGroup{
			UserID: 0, Name: p.name, Ports: p.ports, UDPPorts: p.udpPorts, Remark: p.remark,
		}); err != nil {
			log.Printf("[ocw] seed security group %q: %v", p.name, err)
			continue
		}
		n++
	}
	if n > 0 {
		log.Printf("[ocw] seeded %d preset security groups", n)
	}
}
