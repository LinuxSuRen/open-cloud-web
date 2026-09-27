// Package auth 的模型视图。
//
// 并行开发期间曾存在局部镜像类型；集成阶段统一为 internal/model 的
// 类型别名，消除漂移风险：签名与 JSON 序列化均以 model 为准。
package auth

import (
	"github.com/linuxsuren/open-cloud-web/internal/model"
	"github.com/linuxsuren/open-cloud-web/internal/store"
)

// ErrNotFound 与 store 层哨兵错误同一实例，供 errors.Is 判定。
var ErrNotFound = store.ErrNotFound

// Role 用户角色（model.Role 别名）。
type Role = model.Role

// UserStatus 用户状态（model.UserStatus 别名）。
type UserStatus = model.UserStatus

const (
	RoleAdmin      = model.RoleAdmin
	RoleUser       = model.RoleUser
	StatusActive   = model.StatusActive
	StatusDisabled = model.StatusDisabled
)

// User 平台用户（model.User 别名）。
type User = model.User

// PAT 个人访问令牌的存储视图（model.PAT 别名，只含哈希；Expired 方法可用）。
type PAT = model.PAT
