// Package model 定义 OpenCloudLab 的领域模型与枚举。
// 类型名与字段为跨 agent 契约（见 ARCHITECTURE.md），不得随意变更。
package model

import "time"

// Role 用户角色。
type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

// Valid 报告角色取值是否合法。
func (r Role) Valid() bool {
	return r == RoleAdmin || r == RoleUser
}

// UserStatus 用户状态。
type UserStatus string

const (
	StatusActive   UserStatus = "active"
	StatusDisabled UserStatus = "disabled"
)

// Valid 报告用户状态取值是否合法。
func (s UserStatus) Valid() bool {
	return s == StatusActive || s == StatusDisabled
}

// User 平台用户（local 或飞书 OAuth 来源）。
type User struct {
	ID             int64      `json:"id"`
	Username       string     `json:"username"`
	DisplayName    string     `json:"displayName"`
	Email          string     `json:"email"`
	Role           Role       `json:"role"`     // RoleAdmin | RoleUser
	Status         UserStatus `json:"status"`   // StatusActive | StatusDisabled
	Provider       string     `json:"provider"` // "local" | "feishu"
	ProviderSub    string     `json:"providerSub"`
	MaxDurationSec int64      `json:"maxDurationSec"` // 管理员设置的单次最长使用时长，<=0 用全局默认
	PasswordHash   string     `json:"-"`              // 本地用户 bcrypt 哈希，绝不序列化外泄
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

// PAT 个人访问令牌（CLI 认证用），仅存储 SHA-256 哈希。
type PAT struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"userID"`
	Name       string     `json:"name"`
	TokenHash  string     `json:"-"`         // SHA-256 hex，绝不外泄
	ExpiresAt  time.Time  `json:"expiresAt"` // 零值表示永不过期
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"` // 可为 nil
}

// Expired 报告 PAT 是否已过期（ExpiresAt 零值表示永不过期）。
func (p *PAT) Expired(now time.Time) bool {
	return p != nil && !p.ExpiresAt.IsZero() && now.After(p.ExpiresAt)
}
