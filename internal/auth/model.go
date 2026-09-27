package auth

import (
	"errors"
	"time"
)

// ErrNotFound 由 store 层返回，表示记录不存在。
var ErrNotFound = errors.New("auth: not found")

// Role 用户角色。
type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

// UserStatus 用户状态。
type UserStatus string

const (
	StatusActive   UserStatus = "active"
	StatusDisabled UserStatus = "disabled"
)

// User 是 internal/model.User 的局部契约子集（跨 agent 并行开发期间的依赖倒置）。
type User struct {
	ID             int64      `json:"id"`
	Username       string     `json:"username"`
	DisplayName    string     `json:"displayName"`
	Email          string     `json:"email"`
	Role           Role       `json:"role"`
	Status         UserStatus `json:"status"`
	Provider       string     `json:"provider"`
	ProviderSub    string     `json:"providerSub"`
	MaxDurationSec int64      `json:"maxDurationSec"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

// PAT 是 personal access token 的存储视图（只含哈希）。
type PAT struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"userID"`
	Name      string    `json:"name"`
	TokenHash string    `json:"-"`
	ExpiresAt time.Time `json:"expiresAt"` // 零值表示永不过期
	CreatedAt time.Time `json:"createdAt"`
}

// Expired 报告 PAT 是否已过期。
func (p *PAT) Expired(now time.Time) bool {
	return !p.ExpiresAt.IsZero() && now.After(p.ExpiresAt)
}
