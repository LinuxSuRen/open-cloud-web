package api

import (
	"context"
	"time"

	"github.com/linuxsuren/open-cloud-web/internal/auth"
)

// Store 是 API 层需要的 store 子集（Go 隐式接口，签名照抄 ARCHITECTURE.md 契约）。
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
	// audit
	CreateAuditLog(*AuditLog) error
	ListAuditLogs(limit int) ([]*AuditLog, error)
}

// CloudProvider 是 internal/cloud.Provider 契约的局部视图。
type CloudProvider interface {
	Name() string
	ListRegions(ctx context.Context) ([]string, error)
	ListImages(ctx context.Context, region string) ([]Image, error)
	ListInstanceTypes(ctx context.Context, region string) ([]InstanceTypeSpec, error)
}

// CloudRegistry 按 provider 名（alicloud | volcengine）查找 CloudProvider。
type CloudRegistry interface {
	Provider(name string) (CloudProvider, bool)
}

// Runner 是 internal/tofu.Runner 契约的局部视图（签名照抄）。
type Runner interface {
	Apply(ctx context.Context, workspace string, vars map[string]string) error
	Destroy(ctx context.Context, workspace string) error
	OutputIP(ctx context.Context, workspace string) (public, private string, err error)
}
