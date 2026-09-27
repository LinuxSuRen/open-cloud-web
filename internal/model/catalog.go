package model

import "time"

// Image 云提供商可用镜像（查询用，不落库）。
type Image struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Provider    string `json:"provider"` // "alicloud" | "volcengine"
	Region      string `json:"region"`
	OSType      string `json:"osType"`
	Description string `json:"description"`
}

// InstanceTypeSpec 云提供商可用实例规格（查询用，不落库）。
type InstanceTypeSpec struct {
	ID       string `json:"id"`
	CPU      int    `json:"cpu"`
	MemoryMB int    `json:"memoryMB"`
	Provider string `json:"provider"`
	Region   string `json:"region"`
}

// AuditLog 审计日志。
type AuditLog struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"userID"`
	Action    string    `json:"action"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"createdAt"`
}
