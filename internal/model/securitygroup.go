package model

import "time"

// SecurityGroup 命名安全组（端口集合模板）：创建云主机时选择，
// 平台在云上按端口集合创建对应的安全组规则。
// UserID>0 为用户自建；UserID==0 为全局预置。
type SecurityGroup struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"userID"`
	Name      string    `json:"name"`
	Ports     []int     `json:"ports"` // 开放的 TCP 入方向端口
	Remark    string    `json:"remark,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
