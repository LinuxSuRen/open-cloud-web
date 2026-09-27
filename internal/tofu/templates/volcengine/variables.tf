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
