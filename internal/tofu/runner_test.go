package tofu

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// writeFakeTofu 生成一个模拟 tofu 二进制的可执行 shell 脚本。
// 通过环境变量 FAKE_TOFU 控制行为：
//   - fail-apply  apply 输出 error diagnostic 并退出码 1
//   - hang-apply  apply 长时间睡眠（用于 context 取消路径）
func writeFakeTofu(t *testing.T, dir string) string {
	t.Helper()
	script := `#!/bin/sh
# 记录调用参数，便于断言 init/apply/destroy/output 的调用序列。
echo "$@" >> "` + filepath.Join(dir, "calls.log") + `"
case "$1" in
  init)
    echo "init ok"
    exit 0
    ;;
  apply)
    case "$FAKE_TOFU" in
      fail-apply)
        echo '{"@level":"error","type":"diagnostic","diagnostic":{"severity":"error","summary":"invalid instance_type","detail":"ecs.x.large not found"}}'
        exit 1
        ;;
      hang-apply)
        sleep 30
        exit 1
        ;;
    esac
    echo '{"@level":"info","type":"change_summary","changes":{"create":3,"update":0,"delete":0,"no-op":0}}'
    echo '{"@level":"info","type":"apply_progress","resource":"alicloud_instance.this"}'
    echo '{}' > terraform.tfstate
    exit 0
    ;;
  destroy)
    echo "destroy ok"
    exit 0
    ;;
  output)
    echo '{"public_ip":{"sensitive":false,"type":"string","value":"203.0.113.10"},"private_ip":{"sensitive":false,"type":"string","value":"10.0.0.8"}}'
    exit 0
    ;;
esac
exit 2
`
	path := filepath.Join(dir, "fake-tofu")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func newTestRunner(t *testing.T, tofuPath string) Runner {
	return NewRunner(Config{
		TofuPath: tofuPath,
		DataDir:  t.TempDir(),
		Credentials: Credentials{
			"alicloud": {"access_key": "AKTEST", "secret_key": "SKTEST"},
		},
	})
}

func TestApplySuccessAndOutput(t *testing.T) {
	dir := t.TempDir()
	r := newTestRunner(t, writeFakeTofu(t, dir))
	var progress []ChangeSummary
	r.(*runner).cfg.OnProgress = func(s ChangeSummary) { progress = append(progress, s) }

	vars := map[string]string{
		"provider": "alicloud", "region": "cn-hangzhou", "zone": "cn-hangzhou-i",
		"image_id": "img-123", "instance_type": "ecs.e-c1m1.large",
		"instance_name": "ocw-1", "public_bandwidth": "5",
	}
	if err := r.Apply(context.Background(), "ws1", vars); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(progress) != 1 || progress[0].Create != 3 {
		t.Fatalf("change_summary 回调异常: %+v", progress)
	}

	// workspace 目录应有模板与 tfvars，且凭证未写盘。
	ws := filepath.Join(r.(*runner).cfg.DataDir, "workspaces", "ws1")
	for _, f := range []string{"main.tf", "variables.tf", "terraform.tfvars.json"} {
		if _, err := os.Stat(filepath.Join(ws, f)); err != nil {
			t.Fatalf("缺少 %s: %v", f, err)
		}
	}
	tfvars, _ := os.ReadFile(filepath.Join(ws, "terraform.tfvars.json"))
	var got map[string]string
	if err := json.Unmarshal(tfvars, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["provider"]; ok {
		t.Fatal("provider 不应写入 tfvars")
	}
	if got["region"] != "cn-hangzhou" {
		t.Fatalf("tfvars 内容异常: %v", got)
	}
	if strings.Contains(string(tfvars), "AKTEST") || strings.Contains(string(tfvars), "SKTEST") {
		t.Fatal("凭证泄漏到磁盘")
	}

	pub, priv, err := r.OutputIP(context.Background(), "ws1")
	if err != nil || pub != "203.0.113.10" || priv != "10.0.0.8" {
		t.Fatalf("OutputIP = %q,%q,%v", pub, priv, err)
	}

	// 调用序列断言：init -> apply -> output。
	logs, _ := os.ReadFile(filepath.Join(dir, "calls.log"))
	calls := strings.FieldsFunc(string(logs), func(r rune) bool { return r == '\n' })
	want := []string{"init -input=false -no-color", "apply -auto-approve -no-color -json", "output -json"}
	if len(calls) != 3 {
		t.Fatalf("调用序列: %q", calls)
	}
	for i, w := range want {
		if !strings.HasPrefix(calls[i], w) {
			t.Fatalf("第 %d 次调用 = %q, want 前缀 %q", i, calls[i], w)
		}
	}

	if err := r.Destroy(context.Background(), "ws1"); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
}

func TestApplyFailureDiagnostic(t *testing.T) {
	dir := t.TempDir()
	r := newTestRunner(t, writeFakeTofu(t, dir))
	t.Setenv("FAKE_TOFU", "fail-apply")
	err := r.Apply(context.Background(), "ws2", map[string]string{"provider": "alicloud"})
	if err == nil {
		t.Fatal("期望失败")
	}
	for _, want := range []string{"invalid instance_type", "ecs.x.large not found"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("错误信息应包含 %q: %v", want, err)
		}
	}
}

func TestApplyContextCancel(t *testing.T) {
	dir := t.TempDir()
	r := newTestRunner(t, writeFakeTofu(t, dir))
	t.Setenv("FAKE_TOFU", "hang-apply")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := r.Apply(ctx, "ws3", map[string]string{"provider": "alicloud"})
	if err == nil {
		t.Fatal("超时取消应返回错误")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("错误应为 context 取消: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("取消未及时生效: %v", time.Since(start))
	}
}

func TestApplyInvalidWorkspaceAndProvider(t *testing.T) {
	dir := t.TempDir()
	r := newTestRunner(t, writeFakeTofu(t, dir))
	if err := r.Apply(context.Background(), "../escape", nil); err == nil {
		t.Fatal("非法 workspace 名应报错")
	}
	if err := r.Apply(context.Background(), "ws4", map[string]string{}); err == nil {
		t.Fatal("缺少 provider 应报错")
	}
}

func TestEnsureWorkspaceMissingTemplate(t *testing.T) {
	mem := fstest.MapFS{} // 无模板
	r := NewRunner(Config{TofuPath: "tofu", DataDir: t.TempDir(), Templates: mem})
	err := r.Apply(context.Background(), "ws5", map[string]string{"provider": "alicloud"})
	if err == nil || !strings.Contains(err.Error(), "模板") {
		t.Fatalf("缺少模板应报错: %v", err)
	}
}

func TestDestroyWithoutStateIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	r := newTestRunner(t, writeFakeTofu(t, dir))
	if err := r.Destroy(context.Background(), "never-applied"); err != nil {
		t.Fatalf("未 apply 的 workspace 销毁应幂等成功: %v", err)
	}
}

func TestEnvVars(t *testing.T) {
	r := NewRunner(Config{
		DataDir:     t.TempDir(),
		Credentials: Credentials{"volcengine": {"secret_key": "S", "access_key": "A"}},
	}).(*runner)
	got := r.EnvVars("volcengine")
	want := []string{"TF_VAR_access_key=A", "TF_VAR_secret_key=S"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("EnvVars = %v, want %v", got, want)
	}
	if len(r.EnvVars("unknown")) != 0 {
		t.Fatal("未知 provider 应无凭证变量")
	}
}

func TestOutputValueListForm(t *testing.T) {
	raw := map[string]struct {
		Value json.RawMessage `json:"value"`
	}{"public_ip": {Value: json.RawMessage(`["198.51.100.7"]`)},
		"private_ip": {Value: json.RawMessage(`"10.1.2.3"`)}}
	if got := outputValue(raw, "public_ip"); got != "198.51.100.7" {
		t.Fatalf("list 形式输出解析失败: %q", got)
	}
	if got := outputValue(raw, "private_ip"); got != "10.1.2.3" {
		t.Fatalf("string 形式输出解析失败: %q", got)
	}
	if got := outputValue(raw, "missing"); got != "" {
		t.Fatalf("缺失输出应为空: %q", got)
	}
}
