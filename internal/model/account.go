package model

import "time"

// CloudAccount 用户添加的云提供商账号（认证信息）。
// secret 加密存储（AES-GCM，见 internal/api/crypto.go），绝不明文回显。
type CloudAccount struct {
	ID              int64     `json:"id"`
	UserID          int64     `json:"userID"`          // 所有者
	Name            string    `json:"name"`            // 用户自定义名称，如 "我的阿里云主账号"
	Provider        string    `json:"provider"`        // "alicloud" | "volcengine"
	AccessKey       string    `json:"accessKey"`       // 明文仅在创建/更新时接收
	SecretEnc       string    `json:"-"`               // AES-GCM 加密后的 secret
	SessionEnc      string    `json:"-"`               // AES-GCM 加密后的 STS SessionToken（长期密钥留空）
	HasSessionToken bool      `json:"hasSessionToken"` // 是否设置了 SessionToken（仅状态展示）
	Region          string    `json:"region"`          // 默认地域（可选）
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}
