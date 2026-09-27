// Package api 的模型视图。
//
// 并行开发期间曾存在局部镜像类型；集成阶段统一为 internal/model 的
// 类型别名，消除漂移风险：签名与 JSON 序列化均以 model 为准。
package api

import "github.com/linuxsuren/open-cloud-web/internal/model"

// InstanceStatus 实例状态机（model.InstanceStatus 别名）。
type InstanceStatus = model.InstanceStatus

const (
	StatusCreating   = model.StatusCreating
	StatusRunning    = model.StatusRunning
	StatusDestroying = model.StatusDestroying
	StatusDestroyed  = model.StatusDestroyed
	StatusFailed     = model.StatusFailed
)

// Instance 云主机实例（model.Instance 别名，CanRenew 等方法随类型可用）。
type Instance = model.Instance

// Image 云镜像（model.Image 别名）。
type Image = model.Image

// InstanceTypeSpec 云规格（model.InstanceTypeSpec 别名）。
type InstanceTypeSpec = model.InstanceTypeSpec

// AuditLog 审计日志（model.AuditLog 别名）。
type AuditLog = model.AuditLog
