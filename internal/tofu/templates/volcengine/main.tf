# 火山引擎 ECS 模板（OpenCloudLab 功能测试用）。
# 文档：
#   - volcengine provider: https://registry.terraform.io/providers/volcengine/volcengine/latest/docs
#   - ECS 实例: https://registry.terraform.io/providers/volcengine/volcengine/latest/docs/resources/ecs_instance
#   - EIP: https://registry.terraform.io/providers/volcengine/volcengine/latest/docs/resources/eip_address
#
# 凭证说明：access_key / secret_key 通过 TF_VAR_ 环境变量注入
# （等价于 provider 默认的 VOLCENGINE_ACCESS_KEY / VOLCENGINE_SECRET_KEY
# 环境变量方式），不写入任何磁盘文件。

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    volcengine = {
      source = "volcengine/volcengine"
      # 锁定 1.x 大版本（>= 约束，允许补丁升级）。
      version = ">= 0.0.150"
    }
  }
}

provider "volcengine" {
  region     = var.region
  access_key = var.access_key
  secret_key = var.secret_key
}

resource "volcengine_vpc" "this" {
  vpc_name   = "${var.instance_name}-vpc"
  cidr_block = "172.16.0.0/16"
}

resource "volcengine_subnet" "this" {
  subnet_name = "${var.instance_name}-subnet"
  vpc_id      = volcengine_vpc.this.id
  cidr_block  = "172.16.0.0/24"
  zone_id     = var.zone
}

# 安全组：内联创建并放行 22/80/443 入方向 TCP。
resource "volcengine_security_group" "this" {
  security_group_name = var.security_group
  vpc_id              = volcengine_vpc.this.id
}

resource "volcengine_security_group_rule" "ingress" {
  for_each          = toset(["22", "80", "443"])
  security_group_id = volcengine_security_group.this.id
  protocol          = "TCP"
  port_start        = each.value
  port_end          = each.value
  source_cidr_ip    = "0.0.0.0/0"
  direction         = "ingress"
}

resource "volcengine_ecs_instance" "this" {
  instance_name        = var.instance_name
  host_name            = var.instance_name
  image_id             = var.image_id
  instance_type_id     = var.instance_type
  subnet_id            = volcengine_subnet.this.id
  security_group_ids   = [volcengine_security_group.this.id]
  instance_charge_type = "PostPaid"
}

# 公网 IP：按流量计费的 EIP 并绑定到 ECS 实例。
resource "volcengine_eip_address" "this" {
  billing_type = "PostPaidByTraffic"
  bandwidth    = var.public_bandwidth
}

resource "volcengine_eip_association" "this" {
  allocation_id = volcengine_eip_address.this.id
  instance_id   = volcengine_ecs_instance.this.id
  instance_type = "EcsInstance"
}

output "public_ip" {
  value = volcengine_eip_address.this.eip_address
}

output "private_ip" {
  value = try(
    volcengine_ecs_instance.this.primary_ip,
    try(volcengine_ecs_instance.this.network_interfaces[0].primary_ip, "")
  )
}
