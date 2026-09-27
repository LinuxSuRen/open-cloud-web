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
	"log"
	"net/http"
	"os/signal"
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
	log.Printf("[ocw] starting: listen=%s db=%s dataDir=%s", cfg.ListenAddr, cfg.DBPath, cfg.DataDir)

	// 2. 存储（SQLite，契约见 ARCHITECTURE.md store.Store）。
	st, err := store.OpenSQLitePath(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	// 3. admin bootstrap：AdminBootstrapToken 非空且当前无 admin 用户时创建。
	if err := bootstrapAdmin(st, cfg.AdminBootstrapToken); err != nil {
		return err
	}

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
	newRunner := func(provider, ak, sk string) tofu.Runner {
		return tofu.NewRunner(tofu.Config{
			TofuPath: cfg.TofuBinary,
			DataDir:  cfg.DataDir,
			Credentials: tofu.Credentials{
				provider: {"access_key": ak, "secret_key": sk},
			},
		})
	}

	// 6. 生命周期调度器：storeSchedulerAdapter 把 store.Store（model.Instance）
	// 适配为 scheduler.InstanceStore；accountDestroyer 按 workspace 恢复账号凭据。
	sched := scheduler.New(
		&storeSchedulerAdapter{store: st},
		&accountDestroyer{store: st, secretKey: secretKey, newRunner: newRunner},
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
		func(provider, ak, sk string) api.Runner { return newRunner(provider, ak, sk) },
		api.Config{
			DefaultDurationSec: cfg.DefaultDurationSec,
			MaxDurationSec:     cfg.MaxDurationSec,
			CORSAllowedOrigin:  cfg.CORSAllowedOrigin,
			SecretKey:          secretKey,
		},
	)

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
