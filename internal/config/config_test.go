package config

import (
	"os"
	"testing"
)

var knownEnvKeys = []string{"OCW_LISTEN_ADDR", "LISTEN_ADDR",
	"OCW_DB_PATH", "DB_PATH",
	"OCW_JWT_SECRET", "JWT_SECRET",
	"OCW_PUBLIC_BASE_URL", "PUBLIC_BASE_URL",
	"OCW_TOFU_BINARY", "TOFU_BINARY",
	"OCW_TOFU_TEMPLATES_DIR", "TOFU_TEMPLATES_DIR",
	"OCW_DATA_DIR", "DATA_DIR",
	"OCW_DEFAULT_DURATION_SEC", "DEFAULT_DURATION_SEC",
	"OCW_MAX_DURATION_SEC", "MAX_DURATION_SEC",
	"OCW_CREATING_TIMEOUT_SEC", "CREATING_TIMEOUT_SEC",
	"OCW_SCHEDULER_INTERVAL_SEC", "SCHEDULER_INTERVAL_SEC",
	"OCW_FEISHU_CLIENT_ID", "FEISHU_CLIENT_ID",
	"OCW_ADMIN_BOOTSTRAP_TOKEN", "ADMIN_BOOTSTRAP_TOKEN",
	"OCW_ALICLOUD_ACCESS_KEY", "ALICLOUD_ACCESS_KEY",
	"OCW_VOLCENGINE_SECRET_KEY", "VOLCENGINE_SECRET_KEY",
}

// setEnv 清空所有相关环境变量后，批量设置给定键值。
// 测试内不并发，直接 unset 安全。
func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for _, k := range knownEnvKeys {
		os.Unsetenv(k)
	}
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestDefaults(t *testing.T) {
	cfg := Defaults()
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want :8080", cfg.ListenAddr)
	}
	if cfg.TofuBinary != "tofu" {
		t.Errorf("TofuBinary = %q, want tofu", cfg.TofuBinary)
	}
	if cfg.DefaultDurationSec != 3600 {
		t.Errorf("DefaultDurationSec = %d, want 3600", cfg.DefaultDurationSec)
	}
	if cfg.MaxDurationSec != 86400 {
		t.Errorf("MaxDurationSec = %d, want 86400", cfg.MaxDurationSec)
	}
	if cfg.CreatingTimeoutSec != 900 {
		t.Errorf("CreatingTimeoutSec = %d, want 900", cfg.CreatingTimeoutSec)
	}
	if cfg.SchedulerIntervalSec != 30 {
		t.Errorf("SchedulerIntervalSec = %d, want 30", cfg.SchedulerIntervalSec)
	}
}

func TestLoadDefaults(t *testing.T) {
	setEnv(t, nil)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ListenAddr != ":8080" || cfg.DefaultDurationSec != 3600 {
		t.Errorf("unexpected config: %+v", cfg)
	}
}

func TestLoadPrefixOverridesPlain(t *testing.T) {
	setEnv(t, map[string]string{
		"OCW_LISTEN_ADDR": ":9090",
		"LISTEN_ADDR":     ":7070", // OCW_ 前缀优先
		"DB_PATH":         "/tmp/plain.db",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ListenAddr != ":9090" {
		t.Errorf("ListenAddr = %q, want :9090 (OCW_ prefix should win)", cfg.ListenAddr)
	}
	if cfg.DBPath != "/tmp/plain.db" {
		t.Errorf("DBPath = %q, want /tmp/plain.db (plain fallback)", cfg.DBPath)
	}
}

func TestLoadInts(t *testing.T) {
	setEnv(t, map[string]string{
		"OCW_DEFAULT_DURATION_SEC":   "1800",
		"OCW_MAX_DURATION_SEC":       "7200",
		"OCW_CREATING_TIMEOUT_SEC":   "300",
		"OCW_SCHEDULER_INTERVAL_SEC": "15",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DefaultDurationSec != 1800 || cfg.MaxDurationSec != 7200 ||
		cfg.CreatingTimeoutSec != 300 || cfg.SchedulerIntervalSec != 15 {
		t.Errorf("unexpected durations: %+v", cfg)
	}
	if cfg.CreatingTimeout().Seconds() != 300 {
		t.Errorf("CreatingTimeout() = %v, want 300s", cfg.CreatingTimeout())
	}
}

func TestLoadInvalidInt(t *testing.T) {
	setEnv(t, map[string]string{"OCW_DEFAULT_DURATION_SEC": "abc"})
	if _, err := Load(); err == nil {
		t.Fatal("Load() should fail on invalid integer")
	}
}

func TestLoadValidation(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"default must be positive", map[string]string{"OCW_DEFAULT_DURATION_SEC": "0"}},
		{"max >= default", map[string]string{"OCW_MAX_DURATION_SEC": "10"}},
		{"creating timeout positive", map[string]string{"OCW_CREATING_TIMEOUT_SEC": "-1"}},
		{"scheduler interval positive", map[string]string{"OCW_SCHEDULER_INTERVAL_SEC": "0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, tt.env)
			if _, err := Load(); err == nil {
				t.Errorf("Load() should fail for %v", tt.env)
			}
		})
	}
}
