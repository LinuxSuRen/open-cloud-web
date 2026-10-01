# OpenCloudLab 云主机快速创建平台

基于 **OpenTofu** 的云主机快速创建平台（功能测试用途）：分钟级申请、TTL 到期自动回收，支持**阿里云、火山引擎、腾讯云、华为云**。Go 后端 + 内嵌 Vue 3 控制台，容器化运行。

## 架构亮点

- **OpenTofu 动态编排**：一实例一 workspace，声明式创建/销毁，state 可审计、可重放，杜绝资源泄漏；provider 插件缓存 + 离线安装（`-plugin-dir`），弱网不再重复下载
- **多云适配**：`Provider` 抽象统一封装阿里云/火山引擎/腾讯云/华为云的地域、可用区、镜像、规格查询（实时调云 API，不写死），新增厂商零侵入（纯标准库实现各家签名）
- **云账号自助管理**：用户自行添加云账号（AK/SK + STS SessionToken，AES-256-GCM 加密落库），一键「测试」验证凭据
- **安全组模板**：命名端口集合（预置 SSH/Web/MQTT/**MediaMTX 流媒体**等，支持 TCP + UDP），创建时选择
- **TTL 生命周期**：创建即定过期时间，调度器 30s 扫描到期自动销毁；**多次续期**（admin 不限次数，普通用户按配额），创建中可取消
- **过程可观测**：实例创建过程日志（tofu apply 事件流）、provider 下载任务日志实时查看；WebSocket 推送列表刷新
- **飞书 OAuth + PAT + 密码登录**：飞书扫码、`pat_ocw_` 前缀 CLI 令牌（仅存哈希）、本地用户名密码（失败锁定）
- **管理员配额**：全局 + 用户级单次时长上限、**续期次数配额**，用户管理、审计日志（分页）齐备
- **网络友好**：provider 下载代理与云 API 代理两条独立链路，控制台随时配置即时生效
- **极简依赖**：仅 Go 标准库 + JWT/bcrypt/纯 Go SQLite 三个依赖；SQLite 单文件存储；docker compose 一键启动；**零配置 `go run` 即可跑通**（开发模式）

## 快速开始

### 零配置本地跑（开发）

```bash
go run cmd/ocw/main.go
# 浏览器打开 http://127.0.0.1:8080，admin / admin12345
# （未显式设置 JWT_SECRET 时为开发模式，重启后登录会话失效）
```

或前后端一起（后端 :8080 + Vite 热更新 :5173，Ctrl+C 一并停止）：

```bash
make dev    # make stop 清理残留进程
```

### 生产部署

前置：Docker 20+、docker compose v2。

```bash
git clone https://github.com/LinuxSuRen/open-cloud-web.git
cd open-cloud-web

export OCW_JWT_SECRET="$(openssl rand -hex 32)"
export OCW_ADMIN_BOOTSTRAP_TOKEN="强口令"
export OCW_SECRET_KEY="$(openssl rand -hex 32)"   # 云账号 Secret 加密密钥
# 可选：飞书登录
export OCW_FEISHU_CLIENT_ID=cli_xxxx
export OCW_FEISHU_CLIENT_SECRET=xxxx

docker compose -f deploy/docker-compose.yml up -d --build
curl http://localhost:8080/healthz
```

云厂商凭据**不需要**配置环境变量——启动后在控制台「云提供商」页签用你自己的 AK/SK 添加账号即可。

详见 [docs/deployment.md](docs/deployment.md)。

## 环境变量

均支持 `OCW_` 前缀（`OCW_<NAME>` 优先于 `<NAME>`）。

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `LISTEN_ADDR` | `:8080` | HTTP 监听地址 |
| `DB_PATH` | `data/ocw.db` | SQLite 文件路径（父目录自动创建） |
| `JWT_SECRET` | 自动生成 | JWT 签名密钥；显式设置须 ≥16 字节，未设置时开发模式随机生成 |
| `SECRET_KEY` | 回落 JWT_SECRET | 云账号 Secret/SessionToken/实例密码的 AES 加密密钥 |
| `DATA_DIR` | `data` | 数据目录（sqlite + tofu workspaces + 插件缓存） |
| `TOFU_BINARY` | `tofu` | OpenTofu 可执行文件路径 |
| `DEFAULT_DURATION_SEC` | `3600` | 默认申请时长（秒） |
| `MAX_DURATION_SEC` | `86400` | 全局单次时长上限（秒） |
| `DEFAULT_RENEW_TIMES` | `3` | 普通用户单实例默认可续期次数（admin 不受限） |
| `CREATING_TIMEOUT_SEC` | `900` | Creating 状态超时（秒） |
| `SCHEDULER_INTERVAL_SEC` | `30` | 调度器扫描间隔（秒） |
| `FEISHU_CLIENT_ID` / `FEISHU_CLIENT_SECRET` | 空 | 飞书自建应用凭据 |
| `FEISHU_REDIRECT_URL` | 空 | 飞书 OAuth 回调地址 |
| `ADMIN_BOOTSTRAP_TOKEN` | 开发模式默认 | 首启引导管理员的口令（即为 admin 登录密码） |
| `CORS_ALLOWED_ORIGIN` | 空 | 允许的 CORS origin |

## API 一览

前缀 `/api/v1`，认证方式 `Authorization: Bearer <jwt>` 或 `Bearer pat_ocw_...`。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/auth/login` | 用户名/密码登录（本地用户） |
| POST | `/auth/feishu/login`、GET `/auth/feishu/callback` | 飞书 OAuth |
| POST | `/auth/token` | PAT 换 JWT |
| POST/GET | `/auth/pats`、`DELETE /auth/pats/{id}` | PAT 管理 |
| GET | `/me` | 当前用户 |
| GET/POST | `/users`、`PATCH/DELETE /users/{id}` | 用户管理（admin，含时长/续期配额） |
| GET/POST | `/cloud-accounts`、`PATCH/DELETE /cloud-accounts/{id}` | 云账号管理（AK/SK/SessionToken） |
| GET | `/cloud-accounts/{id}/regions|zones|images|instance-types` | 云资源实时查询（用该账号凭据） |
| POST | `/cloud-accounts/{id}/test` | 测试云账号可用性 |
| GET/POST | `/security-groups`、`DELETE /security-groups/{id}` | 安全组（端口集合，TCP+UDP） |
| POST | `/instances`、GET `/instances?status=&page=` | 实例管理（含状态过滤、分页） |
| GET | `/instances/{id}/logs` | 创建过程日志 |
| POST | `/instances/{id}/renew` | 续期（按配额，admin 不限） |
| DELETE | `/instances/{id}` | 销毁 / 取消创建 / 删除已销毁记录 |
| GET | `/admin/audit-logs?page=&limit=` | 审计日志（admin，分页） |
| GET/PUT | `/admin/settings` | 代理设置（下载/云 API 两条链路） |
| GET | `/admin/providers`、POST `/admin/providers/preload` | provider 缓存状态 / 预下载（带日志） |
| GET | `/ws?token=` | WebSocket 实例变更推送 |

完整请求/响应结构见 [docs/api.md](docs/api.md)。

## CLI 示例

```bash
BASE=http://localhost:8080/api/v1
TOKEN=pat_ocw_xxxx
AUTH="Authorization: Bearer $TOKEN"

# 我是谁
curl -H "$AUTH" $BASE/me

# 查询火山账号可用区/镜像/规格（42 为云账号 ID）
curl -H "$AUTH" "$BASE/cloud-accounts/42/zones?region=cn-beijing"
curl -H "$AUTH" "$BASE/cloud-accounts/42/images?region=cn-beijing"

# 创建一台 2 小时的云主机（自动生成 SSH 密码）
curl -X POST -H "$AUTH" -H "Content-Type: application/json" $BASE/instances -d '{
  "cloudAccountID": 42, "securityGroupID": 10,
  "region": "cn-beijing", "zone": "cn-beijing-a",
  "imageID": "image-xxxx", "instanceType": "ecs.r4il.xlarge", "durationSec": 7200
}'

# 查看实例（等待 creating → running；详情含自动生成的 SSH 密码）
curl -H "$AUTH" $BASE/instances/7

# 续期（配额内可多次；admin 不限）
curl -X POST -H "$AUTH" -H "Content-Type: application/json" $BASE/instances/7/renew -d '{"durationSec": 3600}'

# 提前销毁（创建中调用则为取消）
curl -X DELETE -H "$AUTH" $BASE/instances/7
```

## 开发

```bash
make help     # 全部目标
make dev      # 前后端一起跑
make test     # go test -race
make ci       # 与 GitHub Actions 等价的本地检查（含 tofu validate、前端 dist 一致性）
make up/down  # docker compose
```

前端源码在 `webapp/`（Vue 3 + Vite），构建产物内嵌进 Go 二进制（`internal/web/dist`）。

## 目录结构

```
cmd/ocw/                 # main：装配
internal/config/         # 环境变量配置
internal/model/          # 领域模型与枚举
internal/store/          # 存储接口 + SQLite 实现（含列迁移）
internal/api/            # REST API + 中间件 + WebSocket
internal/auth/           # JWT/PAT/密码/飞书 OAuth
internal/cloud/          # 云提供商抽象（阿里云/火山引擎签名实现）
internal/tofu/           # OpenTofu 执行封装 + 模板（go:embed）
internal/scheduler/      # TTL 过期调度
internal/secrets/        # AES-GCM 加解密
internal/web/            # 内嵌控制台（Vue 构建产物）
webapp/                  # Vue 3 前端源码
deploy/                  # Dockerfile, compose
scripts/                 # dev.sh 等辅助脚本
docs/                    # API 与部署文档
tools/                   # 软著源程序生成脚本
```

## License

[Apache-2.0](LICENSE)
