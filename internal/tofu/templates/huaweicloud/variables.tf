# 华为云模板入参；access_key/secret_key/security_token 仅经 TF_VAR_ 注入。

variable "region" {
  description = "华为云区域 ID，例如 cn-north-4"
  type        = string
}

variable "zone" {
  description = "可用区名称，例如 cn-north-4a"
  type        = string
}

variable "image_id" {
  description = "IMS 镜像 ID"
  type        = string
}

variable "instance_type" {
  description = "ECS 规格 ID，例如 c7.large.2"
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
  description = "ECS 管理密码（不填则由平台自动生成）"
  type        = string
  sensitive   = true
  default     = ""
}

variable "access_key" {
  description = "华为云 AccessKey ID（经 TF_VAR_ 注入）"
  type        = string
  sensitive   = true
  default     = ""
}

variable "secret_key" {
  description = "华为云 AccessKey Secret（经 TF_VAR_ 注入）"
  type        = string
  sensitive   = true
  default     = ""
}

variable "security_token" {
  description = "华为云临时密钥 security_token（长期密钥留空）"
  type        = string
  sensitive   = true
  default     = ""
}
