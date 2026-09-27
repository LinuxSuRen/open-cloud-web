// Package store 定义 OpenCloudLab 的存储接口及其 SQLite 实现。
package store

import (
	"errors"
	"time"

	"github.com/linuxsuren/open-cloud-web/internal/model"
)

// ErrNotFound 目标记录不存在时返回的哨兵错误。
var ErrNotFound = errors.New("store: record not found")

// Store 是所有持久化操作的接口（跨包契约，签名与 ARCHITECTURE.md 精确一致）。
type Store interface {
	// user
	CreateUser(*model.User) error
	GetUser(id int64) (*model.User, error)
	GetUserByUsername(username string) (*model.User, error)
	GetUserByProvider(provider, sub string) (*model.User, error)
	ListUsers() ([]*model.User, error)
	UpdateUser(*model.User) error
	DeleteUser(id int64) error
	// PAT
	CreatePAT(userID int64, name, tokenHash string, expiresAt time.Time) (int64, error)
	GetPATByHash(tokenHash string) (*model.PAT, error)
	ListPATs(userID int64) ([]*model.PAT, error)
	DeletePAT(id int64) error
	// instance
	CreateInstance(*model.Instance) error
	GetInstance(id int64) (*model.Instance, error)
	ListInstancesByUser(userID int64) ([]*model.Instance, error)
	ListInstancesByStatuses(statuses []model.InstanceStatus) ([]*model.Instance, error)
	UpdateInstance(*model.Instance) error
	// cloud account（用户添加的云提供商认证信息）
	CreateCloudAccount(*model.CloudAccount) error
	GetCloudAccount(id int64) (*model.CloudAccount, error)
	ListCloudAccountsByUser(userID int64) ([]*model.CloudAccount, error)
	ListCloudAccounts() ([]*model.CloudAccount, error)
	UpdateCloudAccount(*model.CloudAccount) error
	DeleteCloudAccount(id int64) error
	// audit
	CreateAuditLog(*model.AuditLog) error
	ListAuditLogs(limit int) ([]*model.AuditLog, error)
	Close() error
}
