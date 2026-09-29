# 火山引擎模板入参。region/zone/image_id/instance_type/instance_name/
# public_bandwidth/security_group 由平台写入 terraform.tfvars.json；
# access_key/secret_key 仅经 TF_VAR_ 环境变量注入（sensitive，不落盘）。

variable "region" {
  description = "火山引擎地域 ID，例如 cn-beijing"
  type        = string
}

variable "zone" {
  description = "可用区 ID，例如 cn-beijing-a"
  type        = string
}

variable "image_id" {
  description = "ECS 镜像 ID"
  type        = string
}

variable "instance_type" {
  description = "ECS 实例规格 ID，例如 ecs.g1ie.large"
  type        = string
}

variable "instance_name" {
  description = "实例名称（同时用作主机名/VPC/子网前缀）"
  type        = string
}

variable "public_bandwidth" {
  description = "公网出方向带宽（Mbps）"
  type        = number
  default     = 5
}

variable "security_group" {
  description = "内联创建的安全组名称"
  type        = string
  default     = "ocw-sg"
}

variable "access_key" {
  description = "火山引擎 AccessKey ID（经 TF_VAR_ 注入）"
  type        = string
  sensitive   = true
  default     = ""
}

variable "secret_key" {
  description = "火山引擎 AccessKey Secret（经 TF_VAR_ 注入）"
  type        = string
  sensitive   = true
  default     = ""
}

variable "system_volume_type" {
  description = "系统盘类型：PTSSD / ESSD_PL0 / ESSD_PL1 / ESSD_PL2 / ESSD_FlexPL"
  type        = string
  default     = "ESSD_PL0"
}

variable "system_volume_size" {
  description = "系统盘容量（GiB）"
  type        = number
  default     = 40
}

variable "session_token" {
  description = "火山引擎临时访问密钥的 SessionToken（长期密钥留空）"
  type        = string
  sensitive   = true
  default     = ""
}

variable "password" {
  description = "ECS 实例登录密码（不填则由平台自动生成）"
  type        = string
  sensitive   = true
  default     = ""
}

variable "ingress_ports" {
  description = "安全组放行的 TCP 入方向端口（逗号分隔）"
  type        = string
  default     = "22,80,443"
}
