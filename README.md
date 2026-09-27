# OpenCloudLab 云主机快速创建平台

基于 **OpenTofu** 的云主机快速创建平台（功能测试用途）：分钟级申请、TTL 到期自动回收，支持**阿里云**与**火山引擎**。Go 后端，容器化运行。

## 架构亮点

- **OpenTofu 动态编排**：一实例一 workspace，声明式创建/销毁，state 可审计、可重放，杜绝资源泄漏
- **多云适配**：`Provider` 抽象统一封装阿里云/火山引擎的地域、镜像、规格查询，新增厂商零侵入
- **TTL 生命周期**：创建即定过期时间，调度器 30s 扫描到期自动销毁；支持**一次性续用**
- **飞书 OAuth + PAT**：飞书扫码登录签发 JWT；`pat_ocw_` 前缀个人令牌供 CLI/自动化使用（仅存哈希）
- **管理员配额**：全局 + 用户级单次时长上限，用户管理、审计日志齐备
- **极简依赖**：仅 Go 标准库 + JWT/bcrypt/纯 Go SQLite 三个依赖；SQLite 单文件存储；docker compose 一键启动

## 快速开始

前置：Docker 20+、docker compose v2、飞书自建应用、云厂商 AK/SK。

```bash
git clone https://github.com/linuxsuren/open-cloud-web.git
cd open-cloud-web

export OCW_JWT_SECRET="$(openssl rand -hex 32)"
export OCW_FEISHU_CLIENT_ID=cli_xxxx
export OCW_FEISHU_CLIENT_SECRET=xxxx
export OCW_ALICLOUD_ACCESS_KEY=LTAI...
export OCW_ALICLOUD_SECRET_KEY=xxxx

docker compose -f deploy/compose.yaml up -d
curl http://localhost:8080/healthz
```

详见 [docs/deployment.md](docs/deployment.md)。

## 环境变量

均支持 `OCW_` 前缀（`OCW_<NAME>` 优先于 `<NAME>`）。

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `LISTEN_ADDR` | `:8080` | HTTP 监听地址 |
| `DB_PATH` | `data/ocw.db` | SQLite 文件路径 |
| `JWT_SECRET` | （必填） | JWT 签名密钥 |
| `PUBLIC_BASE_URL` | `http://localhost:8080` | 对外基础 URL（OAuth 回调） |
| `DATA_DIR` | `data` | 数据目录（sqlite + tofu workspaces） |
| `TOFU_BINARY` | `tofu` | OpenTofu 可执行文件路径 |
| `TOFU_TEMPLATES_DIR` | `deploy/tofu-templates` | OpenTofu 模板目录 |
| `DEFAULT_DURATION_SEC` | `3600` | 默认申请时长（秒） |
| `MAX_DURATION_SEC` | `86400` | 全局单次时长上限（秒） |
| `CREATING_TIMEOUT_SEC` | `900` | Creating 状态超时（秒） |
| `SCHEDULER_INTERVAL_SEC` | `30` | 调度器扫描间隔（秒） |
| `FEISHU_CLIENT_ID` / `FEISHU_CLIENT_SECRET` | 空 | 飞书自建应用凭据 |
| `FEISHU_REDIRECT_URL` | 空 | 飞书 OAuth 回调地址 |
| `ADMIN_BOOTSTRAP_TOKEN` | 空 | 首次启动引导管理员的令牌 |
| `ALICLOUD_ACCESS_KEY` / `ALICLOUD_SECRET_KEY` | 空 | 阿里云 AK/SK |
| `VOLCENGINE_ACCESS_KEY` / `VOLCENGINE_SECRET_KEY` | 空 | 火山引擎 AK/SK |

## API 一览

前缀 `/api/v1`，认证方式 `Authorization: Bearer <jwt>` 或 `Bearer pat_ocw_...`。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/auth/feishu/login` | 获取飞书授权地址 |
| GET | `/auth/feishu/callback` | 飞书 OAuth 回调 |
| POST | `/auth/token` | PAT 换 JWT |
| POST/GET | `/auth/pats`、`DELETE /auth/pats/{id}` | PAT 管理 |
| GET | `/me` | 当前用户 |
| GET/POST | `/users`、`PATCH/DELETE /users/{id}` | 用户管理（管理员） |
| GET | `/providers/{p}/regions`、`/images?region=`、`/instance-types?region=` | 云资源查询 |
| POST/GET | `/instances`、`GET/DELETE /instances/{id}` | 实例管理 |
| POST | `/instances/{id}/renew` | 续用（每个生命周期一次） |
| GET | `/admin/audit-logs` | 审计日志（管理员） |

`{p}` ∈ `alicloud`、`volcengine`。完整请求/响应结构见 [docs/api.md](docs/api.md)。

## CLI 示例

```bash
BASE=http://localhost:8080/api/v1
TOKEN=pat_ocw_xxxx
AUTH="Authorization: Bearer $TOKEN"

# 我是谁
curl -H "$AUTH" $BASE/me

# 查询阿里云杭州地域镜像与规格
curl -H "$AUTH" "$BASE/providers/alicloud/images?region=cn-hangzhou"
curl -H "$AUTH" "$BASE/providers/alicloud/instance-types?region=cn-hangzhou"

# 创建一台 2 小时的云主机
curl -X POST -H "$AUTH" -H "Content-Type: application/json" $BASE/instances -d '{
  "provider": "alicloud", "region": "cn-hangzhou", "zone": "cn-hangzhou-i",
  "imageID": "aliyun_3_x64_20G_alibase_20240528.vhd",
  "instanceType": "ecs.e-c1m1.large", "durationSec": 7200, "name": "test-vm-01"
}'

# 查看实例（等待 creating → running，拿到 publicIP）
curl -H "$AUTH" $BASE/instances/42

# 到期前续用一次（再续会被拒绝）
curl -X POST -H "$AUTH" -H "Content-Type: application/json" $BASE/instances/42/renew -d '{"durationSec": 3600}'

# 提前销毁
curl -X DELETE -H "$AUTH" $BASE/instances/42
```

## 目录结构

```
cmd/ocw/                 # main：装配
internal/config/         # 环境变量配置
internal/model/          # 领域模型与枚举
internal/store/          # 存储接口 + SQLite 实现
internal/api/            # REST API + 中间件
internal/auth/           # JWT/PAT/飞书 OAuth
internal/cloud/          # 云提供商抽象
internal/tofu/           # OpenTofu 执行封装 + 模板
internal/scheduler/      # TTL 过期调度
deploy/                  # Dockerfile, compose, tofu 模板
docs/                    # API 与部署文档
copyright-docs/          # 软著申请材料
tools/                   # 辅助脚本
```

## License

[Apache-2.0](LICENSE)
