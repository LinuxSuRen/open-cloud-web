package api

import (
	"context"
	"time"

	"github.com/linuxsuren/open-cloud-web/internal/auth"
	"github.com/linuxsuren/open-cloud-web/internal/model"
)

// Store 是 API 层需要的 store 子集（Go 隐式接口）。
// 由 internal/store 的实现满足；未列出的方法不属于 API 层职责。
type Store interface {
	// user
	CreateUser(*auth.User) error
	GetUser(id int64) (*auth.User, error)
	GetUserByUsername(username string) (*auth.User, error)
	GetUserByProvider(provider, sub string) (*auth.User, error)
	ListUsers() ([]*auth.User, error)
	UpdateUser(*auth.User) error
	DeleteUser(id int64) error
	// PAT
	CreatePAT(userID int64, name, tokenHash string, expiresAt time.Time) (int64, error)
	GetPATByHash(tokenHash string) (*auth.PAT, error)
	ListPATs(userID int64) ([]*auth.PAT, error)
	DeletePAT(id int64) error
	// instance
	CreateInstance(*Instance) error
	GetInstance(id int64) (*Instance, error)
	ListInstancesByUser(userID int64) ([]*Instance, error)
	UpdateInstance(*Instance) error
	DeleteInstance(id int64) error
	// cloud account（用户添加的云提供商认证信息）
	CreateCloudAccount(*model.CloudAccount) error
	GetCloudAccount(id int64) (*model.CloudAccount, error)
	ListCloudAccountsByUser(userID int64) ([]*model.CloudAccount, error)
	ListCloudAccounts() ([]*model.CloudAccount, error)
	UpdateCloudAccount(*model.CloudAccount) error
	DeleteCloudAccount(id int64) error
	// settings
	GetSetting(key string) (string, error)
	SetSetting(key, value string) error
	// audit
	CreateAuditLog(*AuditLog) error
	ListAuditLogs(limit, offset int) ([]*AuditLog, error)
	CountAuditLogs() (int64, error)
}

// Runner 是 internal/tofu.Runner 契约的局部视图（签名照抄）。
type Runner interface {
	Apply(ctx context.Context, workspace string, vars map[string]string) error
	Destroy(ctx context.Context, workspace string) error
	OutputIP(ctx context.Context, workspace string) (public, private string, err error)
}

// RunnerFactory 按云提供商与账号凭据构造 tofu Runner。
// 由装配层注入（包一层 internal/tofu.NewRunner），测试可注入 fake。
type RunnerFactory func(provider, accessKey, secretKey string) Runner
