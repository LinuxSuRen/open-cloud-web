package api

import "time"

// InstanceStatus 实例状态机。
type InstanceStatus string

const (
	StatusCreating   InstanceStatus = "creating"
	StatusRunning    InstanceStatus = "running"
	StatusDestroying InstanceStatus = "destroying"
	StatusDestroyed  InstanceStatus = "destroyed"
	StatusFailed     InstanceStatus = "failed"
)

// Instance 是 internal/model.Instance 的局部契约子集。
type Instance struct {
	ID           int64          `json:"id"`
	UserID       int64          `json:"userID"`
	Name         string         `json:"name"`
	Provider     string         `json:"provider"`
	Region       string         `json:"region"`
	Zone         string         `json:"zone"`
	ImageID      string         `json:"imageID"`
	InstanceType string         `json:"instanceType"`
	Status       InstanceStatus `json:"status"`
	ExpiresAt    time.Time      `json:"expiresAt"`
	RenewedAt    *time.Time     `json:"renewedAt,omitempty"`
	DurationSec  int64          `json:"durationSec"`
	PublicIP     string         `json:"publicIP"`
	PrivateIP    string         `json:"privateIP"`
	TfWorkspace  string         `json:"tfWorkspace"`
	ErrorMessage string         `json:"errorMessage,omitempty"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
}

// CanRenew 报告实例当前是否允许续用：Running、未续用过、未过期。
func (i *Instance) CanRenew(now time.Time) bool {
	return i.Status == StatusRunning && i.RenewedAt == nil && now.Before(i.ExpiresAt)
}

// Image 是云镜像的契约子集。
type Image struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	Region      string `json:"region"`
	OSType      string `json:"osType"`
	Description string `json:"description"`
}

// InstanceTypeSpec 是云规格的契约子集。
type InstanceTypeSpec struct {
	ID       string `json:"id"`
	CPU      int    `json:"cpu"`
	MemoryMB int    `json:"memoryMB"`
	Provider string `json:"provider"`
	Region   string `json:"region"`
}

// AuditLog 是审计日志的契约子集。
type AuditLog struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"userID"`
	Action    string    `json:"action"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"createdAt"`
}
