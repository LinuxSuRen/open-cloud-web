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
	ID             int64      // 唯一
	Username       string     // 唯一
	DisplayName    string     //
	Email          string     //
	Role           Role       // RoleAdmin | RoleUser
	Status         UserStatus // StatusActive | StatusDisabled
	Provider       string     // "local" | "feishu"
	ProviderSub    string     // provider 侧唯一标识
	MaxDurationSec int64      // 管理员设置的单次最长使用时长，<=0 用全局默认
	CreatedAt      time.Time  //
	UpdatedAt      time.Time  //
}

// PAT 个人访问令牌（CLI 认证用），仅存储 SHA-256 哈希。
type PAT struct {
	ID         int64
	UserID     int64
	Name       string
	TokenHash  string // SHA-256 hex
	ExpiresAt  time.Time
	CreatedAt  time.Time
	LastUsedAt *time.Time // 可为 nil
}
