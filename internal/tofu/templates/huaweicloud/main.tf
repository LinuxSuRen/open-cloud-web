# 华为云 ECS 模板（OpenCloudLab 功能测试用）。
# 字段依据 huaweicloud/huaweicloud provider 官方文档：
#   compute_instance / vpc / vpc_subnet / networking_secgroup(_rule) /
#   vpc_eip / compute_eip_associate。
#
# 凭证说明：access_key/secret_key/security_token 经 TF_VAR_ 环境变量注入，
# 不写入任何磁盘文件。

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    huaweicloud = {
      source  = "huaweicloud/huaweicloud"
      version = ">= 1.40.0"
    }
  }
}

provider "huaweicloud" {
  region         = var.region
  access_key     = var.access_key
  secret_key     = var.secret_key
  security_token = var.security_token # 临时密钥必填，长期密钥为空
}

resource "huaweicloud_vpc" "this" {
  name = "${var.instance_name}-vpc"
  cidr = "172.16.0.0/16"
}

resource "huaweicloud_vpc_subnet" "this" {
  name              = "${var.instance_name}-subnet"
  cidr              = "172.16.0.0/24"
  gateway_ip        = "172.16.0.1"
  vpc_id            = huaweicloud_vpc.this.id
  availability_zone = var.zone
}

resource "huaweicloud_networking_secgroup" "this" {
  name        = var.security_group
  description = "Managed by OpenCloudLab"
}

resource "huaweicloud_networking_secgroup_rule" "ingress_tcp" {
  for_each = toset(split(",", var.ingress_ports))

  security_group_id = huaweicloud_networking_secgroup.this.id
  direction         = "ingress"
  ethertype         = "IPv4"
  protocol          = "tcp"
  port_range_min    = tonumber(each.value)
  port_range_max    = tonumber(each.value)
  remote_ip_prefix  = "0.0.0.0/0"
}

resource "huaweicloud_networking_secgroup_rule" "ingress_udp" {
  for_each = toset(compact(split(",", var.udp_ports)))

  security_group_id = huaweicloud_networking_secgroup.this.id
  direction         = "ingress"
  ethertype         = "IPv4"
  protocol          = "udp"
  port_range_min    = tonumber(each.value)
  port_range_max    = tonumber(each.value)
  remote_ip_prefix  = "0.0.0.0/0"
}

resource "huaweicloud_compute_instance" "this" {
  name               = var.instance_name
  image_id           = var.image_id
  flavor_id          = var.instance_type
  admin_pass         = var.password # SSH 登录密码（平台自动生成或用户指定）
  security_group_ids = [huaweicloud_networking_secgroup.this.id]
  availability_zone  = var.zone

  network {
    uuid = huaweicloud_vpc_subnet.this.id
  }
}

# 公网 IP：按流量计费的 EIP 并绑定到 ECS 实例。
resource "huaweicloud_vpc_eip" "this" {
  publicip {
    type = "5_bgp"
  }
  bandwidth {
    name        = "${var.instance_name}-bw"
    size        = var.public_bandwidth
    share_type  = "PER"
    charge_mode = "traffic"
  }
}

resource "huaweicloud_compute_eip_associate" "this" {
  public_ip   = huaweicloud_vpc_eip.this.address
  instance_id = huaweicloud_compute_instance.this.id
}

output "public_ip" {
  value = huaweicloud_vpc_eip.this.address
}

output "private_ip" {
  value = try(huaweicloud_compute_instance.this.access_ip_v4, "")
}

output "password" {
  value     = var.password
  sensitive = true
}
