# 腾讯云模板入参；access_key/secret_key/session_token 仅经 TF_VAR_ 注入。

variable "region" {
  description = "腾讯云地域 ID，例如 ap-guangzhou"
  type        = string
}

variable "zone" {
  description = "可用区 ID，例如 ap-guangzhou-3"
  type        = string
}

variable "image_id" {
  description = "CVM 镜像 ID"
  type        = string
}

variable "instance_type" {
  description = "CVM 实例规格，例如 S5.MEDIUM4"
  type        = string
}

variable "instance_name" {
  description = "实例名称（同时用作 VPC/子网/主机名前缀）"
  type        = string
}

variable "public_bandwidth" {
  description = "公网出方向带宽（Mbps）"
  type        = number
  default     = 5
}

variable "security_group" {
  description = "安全组名称"
  type        = string
  default     = "ocw-sg"
}

variable "ingress_ports" {
  description = "安全组放行的 TCP 入方向端口（逗号分隔）"
  type        = string
  default     = "22,80,443"
}

variable "udp_ports" {
  description = "安全组放行的 UDP 入方向端口（逗号分隔，可为空）"
  type        = string
  default     = ""
}

variable "password" {
  description = "CVM 登录密码（不填则由平台自动生成）"
  type        = string
  sensitive   = true
  default     = ""
}

variable "access_key" {
  description = "腾讯云 SecretId（经 TF_VAR_ 注入）"
  type        = string
  sensitive   = true
  default     = ""
}

variable "secret_key" {
  description = "腾讯云 SecretKey（经 TF_VAR_ 注入）"
  type        = string
  sensitive   = true
  default     = ""
}

variable "session_token" {
  description = "腾讯云临时密钥 security_token（长期密钥留空）"
  type        = string
  sensitive   = true
  default     = ""
}
