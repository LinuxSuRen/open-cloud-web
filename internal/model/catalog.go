package model

import "time"

// Image 云提供商可用镜像（查询用，不落库）。
type Image struct {
	ID          string // 镜像 ID
	Name        string //
	Provider    string // "alicloud" | "volcengine"
	Region      string //
	OSType      string //
	Description string //
}

// InstanceTypeSpec 云提供商可用实例规格（查询用，不落库）。
type InstanceTypeSpec struct {
	ID       string //
	CPU      int    //
	MemoryMB int    //
	Provider string //
	Region   string //
}

// AuditLog 审计日志。
type AuditLog struct {
	ID        int64     //
	UserID    int64     //
	Action    string    //
	Detail    string    //
	CreatedAt time.Time //
}
