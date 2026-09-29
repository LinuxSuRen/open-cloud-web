# 火山引擎 ECS 模板（OpenCloudLab 功能测试用）。
# 字段名依据 volcengine provider 0.0.196 的 schema（tofu providers schema 校对）。
# 文档：https://registry.terraform.io/providers/volcengine/volcengine/latest/docs
#
# 凭证说明：access_key / secret_key 通过 TF_VAR_ 环境变量注入
# （等价于 provider 默认的 VOLCENGINE_ACCESS_KEY / VOLCENGINE_SECRET_KEY
# 环境变量方式），不写入任何磁盘文件。

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    volcengine = {
      source  = "volcengine/volcengine"
      version = ">= 0.0.150"
    }
  }
}

provider "volcengine" {
  region        = var.region
  access_key    = var.access_key
  secret_key    = var.secret_key
  session_token = var.session_token # 临时密钥(STS)时必填，长期密钥为空
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
  protocol          = "tcp"
  port_start        = each.value
  port_end          = each.value
  cidr_ip           = "0.0.0.0/0"
  direction         = "ingress"
}

resource "volcengine_ecs_instance" "this" {
  instance_name        = var.instance_name
  host_name            = var.instance_name
  image_id             = var.image_id
  instance_type        = var.instance_type
  subnet_id            = volcengine_subnet.this.id
  security_group_ids   = [volcengine_security_group.this.id]
  instance_charge_type = "PostPaid"
  system_volume_type   = var.system_volume_type
  system_volume_size   = var.system_volume_size
  password             = var.password # SSH 登录密码（平台自动生成或用户指定）
}

# 公网 IP：按流量计费的 EIP 并绑定到 ECS 实例。
resource "volcengine_eip_address" "this" {
  billing_type = "PostPaidByTraffic"
  bandwidth    = var.public_bandwidth
}

resource "volcengine_eip_associate" "this" {
  allocation_id = volcengine_eip_address.this.id
  instance_id   = volcengine_ecs_instance.this.id
  instance_type = "EcsInstance"
}

output "public_ip" {
  value = volcengine_eip_address.this.eip_address
}

output "private_ip" {
  value = try(volcengine_ecs_instance.this.primary_ip_address, "")
}

output "password" {
  value     = var.password
  sensitive = true
}
