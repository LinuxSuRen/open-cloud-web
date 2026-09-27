package model

import "time"

// InstanceStatus 实例生命周期状态。
type InstanceStatus string

const (
	StatusCreating   InstanceStatus = "creating"
	StatusRunning    InstanceStatus = "running"
	StatusDestroying InstanceStatus = "destroying"
	StatusDestroyed  InstanceStatus = "destroyed"
	StatusFailed     InstanceStatus = "failed"
)

// Valid 报告实例状态取值是否合法。
func (s InstanceStatus) Valid() bool {
	switch s {
	case StatusCreating, StatusRunning, StatusDestroying, StatusDestroyed, StatusFailed:
		return true
	}
	return false
}

// legalTransitions 合法状态转换表（from -> 允许的 to 集合）。
var legalTransitions = map[InstanceStatus]map[InstanceStatus]bool{
	StatusCreating: {
		StatusRunning:    true, // tofu apply 成功
		StatusFailed:     true, // apply 失败或超时
		StatusDestroying: true, // 创建中提前销毁
	},
	StatusRunning: {
		StatusDestroying: true, // 过期/手动销毁
		StatusFailed:     true, // 销毁前置检查失败等异常
	},
	StatusDestroying: {
		StatusDestroyed:  true, // tofu destroy 成功
		StatusDestroying: true, // 重试自身（幂等，允许保持）
		StatusFailed:     true, // 多次重试仍失败
	},
	StatusDestroyed: {}, // 终态
	StatusFailed: {
		StatusDestroying: true, // 失败后仍需回收资源
	},
}

// CanTransition 报告 from -> to 是否为合法状态转换。
func CanTransition(from, to InstanceStatus) bool {
	if to == from && to == StatusDestroying {
		return true // Destroying 重试
	}
	allowed, ok := legalTransitions[from]
	if !ok {
		return false
	}
	return allowed[to]
}

// CanRenew 报告实例当前是否允许续用：
// 必须 Running、未续用过（RenewedAt == nil）且尚未过期。
func (i *Instance) CanRenew(now time.Time) bool {
	return i != nil &&
		i.Status == StatusRunning &&
		i.RenewedAt == nil &&
		now.Before(i.ExpiresAt)
}

// Instance 云主机实例。
type Instance struct {
	ID           int64          //
	UserID       int64          //
	Name         string         //
	Provider     string         // "alicloud" | "volcengine"
	Region       string         //
	Zone         string         //
	ImageID      string         //
	InstanceType string         //
	Status       InstanceStatus // StatusCreating | StatusRunning | StatusDestroying | StatusDestroyed | StatusFailed
	ExpiresAt    time.Time      //
	RenewedAt    *time.Time     // 非 nil 表示已续用一次（每个生命周期最多一次）
	DurationSec  int64          // 本次申请时长
	PublicIP     string         //
	PrivateIP    string         //
	TfWorkspace  string         // OpenTofu state 工作目录名
	ErrorMessage string         //
	CreatedAt    time.Time      //
	UpdatedAt    time.Time      //
}
