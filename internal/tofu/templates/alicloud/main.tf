# 阿里云 ECS 模板（OpenCloudLab 功能测试用）。
# 文档：
#   - alicloud provider: https://registry.terraform.io/providers/aliyun/alicloud/latest/docs
#   - ECS 资源: https://registry.terraform.io/providers/aliyun/alicloud/latest/docs/resources/instance
#
# 凭证说明：access_key / secret_key 通过 TF_VAR_ 环境变量注入
# （等价于 provider 默认的 ALICLOUD_ACCESS_KEY / ALICLOUD_SECRET_KEY /
# ALICLOUD_REGION 环境变量方式），不写入任何磁盘文件。

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    alicloud = {
      source = "aliyun/alicloud"
      # 锁定已知可用的 1.24x 系列（>= 约束，允许补丁升级）。
      version = ">= 1.249.0, < 2.0.0"
    }
  }
}

provider "alicloud" {
  region         = var.region
  access_key     = var.access_key
  secret_key     = var.secret_key
  security_token = var.session_token # STS 临时密钥时必填，长期密钥为空
}

resource "alicloud_vpc" "this" {
  vpc_name   = "${var.instance_name}-vpc"
  cidr_block = "172.16.0.0/16"
}

resource "alicloud_vswitch" "this" {
  vswitch_name = "${var.instance_name}-vsw"
  vpc_id       = alicloud_vpc.this.id
  cidr_block   = "172.16.0.0/24"
  zone_id      = var.zone
}

# 安全组：内联创建并放行 22/80/443 入方向 TCP。
resource "alicloud_security_group" "this" {
  security_group_name = var.security_group
  vpc_id              = alicloud_vpc.this.id
}

resource "alicloud_security_group_rule" "ingress" {
  # 端口集合由平台注入（逗号分隔字符串），如 "22,80,443,1883"。
  for_each          = toset(split(",", var.ingress_ports))
  type              = "ingress"
  ip_protocol       = "tcp"
  port_range        = "${each.value}/${each.value}"
  security_group_id = alicloud_security_group.this.id
  cidr_ip           = "0.0.0.0/0"
}

resource "alicloud_instance" "this" {
  instance_name        = var.instance_name
  host_name            = var.instance_name
  image_id             = var.image_id
  instance_type        = var.instance_type
  security_groups      = [alicloud_security_group.this.id]
  vswitch_id           = alicloud_vswitch.this.id
  internet_charge_type = "PayByTraffic"
  # 公网带宽（Mbps）；>0 时阿里云自动分配公网 IP。
  internet_max_bandwidth_out = var.public_bandwidth
  password                   = var.password # SSH 登录密码（平台自动生成或用户指定）
}

output "public_ip" {
  value = alicloud_instance.this.public_ip
}

output "private_ip" {
  value = alicloud_instance.this.private_ip
}

output "password" {
  value     = var.password
  sensitive = true
}
