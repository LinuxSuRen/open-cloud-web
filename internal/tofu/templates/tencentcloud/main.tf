# 腾讯云 CVM 模板（OpenCloudLab 功能测试用）。
# 字段依据 tencentcloudstack/tencentcloud provider 官方文档（master 分支）。
#
# 凭证说明：access_key/secret_key/session_token 经 TF_VAR_ 环境变量注入，
# 不写入任何磁盘文件。

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    tencentcloud = {
      source = "tencentcloudstack/tencentcloud"
      # security_group_rule_set 自 1.81.90 起提供（lite_rule 已弃用）。
      version = ">= 1.81.90"
    }
  }
}

provider "tencentcloud" {
  region         = var.region
  secret_id      = var.access_key
  secret_key     = var.secret_key
  security_token = var.session_token # 临时密钥必填，长期密钥为空
}

resource "tencentcloud_vpc" "this" {
  name       = "${var.instance_name}-vpc"
  cidr_block = "172.16.0.0/16"
}

resource "tencentcloud_subnet" "this" {
  availability_zone = var.zone
  name              = "${var.instance_name}-subnet"
  cidr_block        = "172.16.0.0/24"
  vpc_id            = tencentcloud_vpc.this.id
  is_multicast      = false
}

resource "tencentcloud_security_group" "this" {
  name        = var.security_group
  description = "Managed by OpenCloudLab"
}

# 端口集合由平台注入（逗号分隔字符串）；空集合不生成规则。
resource "tencentcloud_security_group_rule_set" "this" {
  security_group_id = tencentcloud_security_group.this.id

  dynamic "ingress" {
    for_each = toset(split(",", var.ingress_ports))
    content {
      action     = "ACCEPT"
      cidr_block = "0.0.0.0/0"
      protocol   = "TCP"
      port       = ingress.value
    }
  }

  dynamic "ingress" {
    for_each = toset(compact(split(",", var.udp_ports)))
    content {
      action     = "ACCEPT"
      cidr_block = "0.0.0.0/0"
      protocol   = "UDP"
      port       = ingress.value
    }
  }
}

resource "tencentcloud_instance" "this" {
  instance_name              = var.instance_name
  hostname                   = var.instance_name
  availability_zone          = var.zone
  image_id                   = var.image_id
  instance_type              = var.instance_type
  vpc_id                     = tencentcloud_vpc.this.id
  subnet_id                  = tencentcloud_subnet.this.id
  orderly_security_groups    = [tencentcloud_security_group.this.id]
  instance_charge_type       = "POSTPAID_BY_HOUR"
  allocate_public_ip         = true
  internet_charge_type       = "TRAFFIC_POSTPAID_BY_HOUR"
  internet_max_bandwidth_out = var.public_bandwidth
  password                   = var.password # SSH 登录密码（平台自动生成或用户指定）

  depends_on = [tencentcloud_security_group_rule_set.this]
}

output "public_ip" {
  value = tencentcloud_instance.this.public_ip
}

output "private_ip" {
  value = tencentcloud_instance.this.private_ip
}

output "password" {
  value     = var.password
  sensitive = true
}
