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
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/linuxsuren/open-cloud-web/internal/api"
	"github.com/linuxsuren/open-cloud-web/internal/auth"
	"github.com/linuxsuren/open-cloud-web/internal/cloud"
	"github.com/linuxsuren/open-cloud-web/internal/config"
	"github.com/linuxsuren/open-cloud-web/internal/model"
	"github.com/linuxsuren/open-cloud-web/internal/scheduler"
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

	// 5. 云提供商 registry（alicloud / volcengine 凭据来自环境变量）。
	// 具体_PROVIDER 构造函数由 internal/cloud agent 提供；就位后在此 Register。

	// 6. OpenTofu runner（tofu.Runner 契约：Apply/Destroy/OutputIP）。
	runner := tofu.NewRunner(tofu.Config{
		TofuPath: cfg.TofuBinary,
		DataDir:  cfg.DataDir,
		Credentials: tofu.Credentials{
			"alicloud": {
				"access_key": cfg.AlicloudAccessKey,
				"secret_key": cfg.AlicloudSecretKey,
			},
			"volcengine": {
				"access_key": cfg.VolcengineAccessKey,
				"secret_key": cfg.VolcengineSecretKey,
			},
		},
	})

	// 7. 生命周期调度器：storeSchedulerAdapter 把 store.Store（model.Instance）
	// 适配为 scheduler.InstanceStore（最小依赖视图），可独立单测。
	sched := scheduler.New(
		&storeSchedulerAdapter{store: st},
		runner, // tofu.Runner 的 Destroy 与 scheduler.Destroyer 签名一致
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

	// 8. HTTP API（/api/v1，含 GET /healthz 健康检查）。
	handler := api.NewHandler(
		st, mgr, feishu, states,
		cloudRegistryAdapter{},
		runner,
		api.Config{
			DefaultDurationSec: cfg.DefaultDurationSec,
			MaxDurationSec:     cfg.MaxDurationSec,
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
	admin := &model.User{
		Username:    "admin",
		DisplayName: "Administrator",
		Role:        model.RoleAdmin,
		Status:      model.StatusActive,
		Provider:    "local",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := st.CreateUser(admin); err != nil {
		return err
	}
	// 可选能力：store 支持 SetUserPasswordHash 时保存 bcrypt 哈希。
	hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if s, ok := st.(interface {
		SetUserPasswordHash(userID int64, hash string) error
	}); ok {
		if err := s.SetUserPasswordHash(admin.ID, string(hash)); err != nil {
			return err
		}
	}
	log.Printf("[ocw] bootstrap admin user created")
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

// cloudRegistryAdapter 把 internal/cloud 的全局注册表适配为 api.CloudRegistry。
type cloudRegistryAdapter struct{}

func (cloudRegistryAdapter) Provider(name string) (api.CloudProvider, bool) {
	p, err := cloud.Get(name)
	if err != nil {
		return nil, false
	}
	return cloudProviderAdapter{p}, true
}

// cloudProviderAdapter 将 cloud.Provider（model 类型）转换为 api.CloudProvider
// （api 局部类型）。
type cloudProviderAdapter struct{ p cloud.Provider }

func (a cloudProviderAdapter) Name() string { return a.p.Name() }

func (a cloudProviderAdapter) ListRegions(ctx context.Context) ([]string, error) {
	return a.p.ListRegions(ctx)
}

func (a cloudProviderAdapter) ListImages(ctx context.Context, region string) ([]api.Image, error) {
	imgs, err := a.p.ListImages(ctx, region)
	if err != nil {
		return nil, err
	}
	out := make([]api.Image, 0, len(imgs))
	for _, i := range imgs {
		out = append(out, api.Image{
			ID: i.ID, Name: i.Name, Provider: i.Provider,
			Region: i.Region, OSType: i.OSType, Description: i.Description,
		})
	}
	return out, nil
}

func (a cloudProviderAdapter) ListInstanceTypes(ctx context.Context, region string) ([]api.InstanceTypeSpec, error) {
	specs, err := a.p.ListInstanceTypes(ctx, region)
	if err != nil {
		return nil, err
	}
	out := make([]api.InstanceTypeSpec, 0, len(specs))
	for _, s := range specs {
		out = append(out, api.InstanceTypeSpec{
			ID: s.ID, CPU: s.CPU, MemoryMB: s.MemoryMB, Provider: s.Provider, Region: s.Region,
		})
	}
	return out, nil
}
