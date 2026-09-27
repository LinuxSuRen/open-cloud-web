// Package config 从环境变量读取 OpenCloudLab 配置。
// 支持 OCW_ 前缀：优先读取 OCW_<NAME>，回落到 <NAME>。
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config 全量运行配置。
type Config struct {
	ListenAddr           string // 监听地址
	DBPath               string // SQLite 文件路径
	JWTSecret            string // JWT 签名密钥
	PublicBaseURL        string // 对外基础 URL（OAuth 回调等）
	TofuBinary           string // tofu 可执行文件
	TofuTemplatesDir     string // tofu 模板目录
	DataDir              string // 数据目录（sqlite + tofu workspaces）
	DefaultDurationSec   int64  // 默认申请时长
	MaxDurationSec       int64  // 全局单次时长上限
	CreatingTimeoutSec   int64  // Creating 状态超时
	SchedulerIntervalSec int64  // 调度器扫描间隔
	FeishuClientID       string //
	FeishuClientSecret   string //
	FeishuRedirectURL    string //
	AdminBootstrapToken  string // 首次启动引导 admin 的 token（可为空）
	AlicloudAccessKey    string //
	AlicloudSecretKey    string //
	VolcengineAccessKey  string //
	VolcengineSecretKey  string //
	CORSAllowedOrigin    string // 允许的 CORS Origin（空表示不加 CORS 头）
}

// Defaults 返回带默认值的配置（不读环境变量）。
func Defaults() Config {
	return Config{
		ListenAddr:           ":8080",
		DBPath:               "data/ocw.db",
		JWTSecret:            "",
		PublicBaseURL:        "http://localhost:8080",
		TofuBinary:           "tofu",
		TofuTemplatesDir:     "deploy/tofu-templates",
		DataDir:              "data",
		DefaultDurationSec:   3600,
		MaxDurationSec:       86400,
		CreatingTimeoutSec:   900,
		SchedulerIntervalSec: 30,
	}
}

// Load 在 Defaults 基础上读取环境变量（OCW_ 前缀优先）并做校验。
func Load() (Config, error) {
	cfg := Defaults()
	cfg.ListenAddr = getEnv("LISTEN_ADDR", cfg.ListenAddr)
	cfg.DBPath = getEnv("DB_PATH", cfg.DBPath)
	cfg.JWTSecret = getEnv("JWT_SECRET", cfg.JWTSecret)
	cfg.PublicBaseURL = getEnv("PUBLIC_BASE_URL", cfg.PublicBaseURL)
	cfg.TofuBinary = getEnv("TOFU_BINARY", cfg.TofuBinary)
	cfg.TofuTemplatesDir = getEnv("TOFU_TEMPLATES_DIR", cfg.TofuTemplatesDir)
	cfg.DataDir = getEnv("DATA_DIR", cfg.DataDir)
	cfg.FeishuClientID = getEnv("FEISHU_CLIENT_ID", cfg.FeishuClientID)
	cfg.FeishuClientSecret = getEnv("FEISHU_CLIENT_SECRET", cfg.FeishuClientSecret)
	cfg.FeishuRedirectURL = getEnv("FEISHU_REDIRECT_URL", cfg.FeishuRedirectURL)
	cfg.AdminBootstrapToken = getEnv("ADMIN_BOOTSTRAP_TOKEN", cfg.AdminBootstrapToken)
	cfg.AlicloudAccessKey = getEnv("ALICLOUD_ACCESS_KEY", cfg.AlicloudAccessKey)
	cfg.AlicloudSecretKey = getEnv("ALICLOUD_SECRET_KEY", cfg.AlicloudSecretKey)
	cfg.VolcengineAccessKey = getEnv("VOLCENGINE_ACCESS_KEY", cfg.VolcengineAccessKey)
	cfg.VolcengineSecretKey = getEnv("VOLCENGINE_SECRET_KEY", cfg.VolcengineSecretKey)
	cfg.CORSAllowedOrigin = getEnv("CORS_ALLOWED_ORIGIN", cfg.CORSAllowedOrigin)

	var err error
	if cfg.DefaultDurationSec, err = getIntEnv("DEFAULT_DURATION_SEC", cfg.DefaultDurationSec); err != nil {
		return cfg, err
	}
	if cfg.MaxDurationSec, err = getIntEnv("MAX_DURATION_SEC", cfg.MaxDurationSec); err != nil {
		return cfg, err
	}
	if cfg.CreatingTimeoutSec, err = getIntEnv("CREATING_TIMEOUT_SEC", cfg.CreatingTimeoutSec); err != nil {
		return cfg, err
	}
	if cfg.SchedulerIntervalSec, err = getIntEnv("SCHEDULER_INTERVAL_SEC", cfg.SchedulerIntervalSec); err != nil {
		return cfg, err
	}

	if cfg.DefaultDurationSec <= 0 {
		return cfg, fmt.Errorf("config: DefaultDurationSec must be > 0, got %d", cfg.DefaultDurationSec)
	}
	if cfg.MaxDurationSec < cfg.DefaultDurationSec {
		return cfg, fmt.Errorf("config: MaxDurationSec (%d) must be >= DefaultDurationSec (%d)",
			cfg.MaxDurationSec, cfg.DefaultDurationSec)
	}
	if cfg.CreatingTimeoutSec <= 0 {
		return cfg, fmt.Errorf("config: CreatingTimeoutSec must be > 0, got %d", cfg.CreatingTimeoutSec)
	}
	if cfg.SchedulerIntervalSec <= 0 {
		return cfg, fmt.Errorf("config: SchedulerIntervalSec must be > 0, got %d", cfg.SchedulerIntervalSec)
	}
	// JWT secret 是认证体系的根：空/过短 secret 下任何人都能离线伪造
	// admin JWT（HS256），属于完全认证绕过，必须在启动时拒绝。
	if len(cfg.JWTSecret) < 16 {
		return cfg, fmt.Errorf("config: JWT_SECRET must be set and at least 16 bytes (got %d)", len(cfg.JWTSecret))
	}
	return cfg, nil
}

// getEnv 优先读取 OCW_<name>，否则读 <name>，均未设置时返回 def。
func getEnv(name, def string) string {
	if v, ok := os.LookupEnv("OCW_" + name); ok {
		return v
	}
	if v, ok := os.LookupEnv(name); ok {
		return v
	}
	return def
}

// getIntEnv 读取整型环境变量，非法值返回包裹错误。
func getIntEnv(name string, def int64) (int64, error) {
	raw, ok := lookupEnv(name)
	if !ok {
		return def, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return def, fmt.Errorf("config: invalid %s %q: %w", name, raw, err)
	}
	return v, nil
}

func lookupEnv(name string) (string, bool) {
	if v, ok := os.LookupEnv("OCW_" + name); ok {
		return v, true
	}
	return os.LookupEnv(name)
}

// CreatingTimeout 返回 Creating 超时时间。
func (c Config) CreatingTimeout() time.Duration {
	return time.Duration(c.CreatingTimeoutSec) * time.Second
}

// SchedulerInterval 返回调度器扫描间隔。
func (c Config) SchedulerInterval() time.Duration {
	return time.Duration(c.SchedulerIntervalSec) * time.Second
}
