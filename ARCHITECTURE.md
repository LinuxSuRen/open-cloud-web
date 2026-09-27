# OpenCloudLab — Architecture Contract (v1)

基于 OpenTofu 的云主机快速创建平台（功能测试用途）。Go 后端，容器化运行。

- Go module: `github.com/linuxsuren/open-cloud-web`（Go 1.27, 最低 1.22+ 语法特性可用：`net/http` 方法+通配符路由）
- 主二进制: `cmd/ocw`
- 存储: SQLite（`modernc.org/sqlite`，纯 Go 无 CGO，便于容器交叉编译）
- 认证: JWT (`github.com/golang-jwt/jwt/v5`) + PAT CLI 令牌 + 飞书 OAuth
- 密码哈希: `golang.org/x/crypto/bcrypt`
- **禁止引入其它第三方依赖**；只用标准库 + 上述三个依赖。
- 配置一律从环境变量读取（支持 `OCW_` 前缀），见 `internal/config`。

## 目录与职责（每个 agent 只允许修改自己负责的目录）

```
cmd/ocw/                 # main：装配（集成 agent 负责）
internal/config/         # 环境变量配置
internal/model/          # 领域模型与枚举
internal/store/          # 存储接口 + SQLite 实现
internal/api/            # REST API + 中间件（auth 依赖注入）
internal/auth/           # JWT/PAT/飞书 OAuth
internal/cloud/          # 云提供商抽象（镜像/规格查询）
internal/tofu/           # OpenTofu 执行封装 + 模板
internal/scheduler/      # 过期调度：TTL、续用一次、超时销毁
deploy/                  # Dockerfile, compose, entrypoint
docs/                    # API 与部署文档
copyright-docs/          # 软著材料
```

## 核心模型（internal/model，类型名精确如下）

```go
type User struct {
    ID           int64
    Username     string // 唯一
    DisplayName  string
    Email        string
    Role         Role   // RoleAdmin | RoleUser
    Status       UserStatus // StatusActive | StatusDisabled
    Provider     string // "local" | "feishu"
    ProviderSub  string
    MaxDurationSec int64  // 管理员设置的单次最长使用时长，<=0 用全局默认
    CreatedAt, UpdatedAt time.Time
}

type Instance struct {
    ID            int64
    UserID        int64
    Name          string
    Provider      string // "alicloud" | "volcengine"
    Region        string
    Zone          string
    ImageID       string
    InstanceType  string
    Status        InstanceStatus
    // Status: StatusCreating | StatusRunning | StatusDestroying | StatusDestroyed | StatusFailed
    ExpiresAt     time.Time
    RenewedAt     *time.Time // 非 nil 表示已续用一次（每个生命周期最多一次）
    DurationSec   int64      // 本次申请时长
    PublicIP      string
    PrivateIP     string
    TfWorkspace   string     // OpenTofu state 工作目录名
    ErrorMessage  string
    CreatedAt, UpdatedAt time.Time
}

type Image  { ID, Name, Provider, Region, OSType, Description string }
type InstanceTypeSpec { ID, CPU int, MemoryMB int, Provider, Region string }
type AuditLog { ID, UserID int64, Action, Detail string, CreatedAt time.Time }
```

## 关键接口（跨包契约，签名必须精确一致）

```go
// internal/store
type Store interface {
    // user
    CreateUser(*model.User) error
    GetUser(id int64) (*model.User, error)
    GetUserByUsername(username string) (*model.User, error)
    GetUserByProvider(provider, sub string) (*model.User, error)
    ListUsers() ([]*model.User, error)
    UpdateUser(*model.User) error
    DeleteUser(id int64) error
    // PAT
    CreatePAT(userID int64, name, tokenHash string, expiresAt time.Time) (int64, error)
    GetPATByHash(tokenHash string) (*model.PAT, error)
    ListPATs(userID int64) ([]*model.PAT, error)
    DeletePAT(id int64) error
    // instance
    CreateInstance(*model.Instance) error
    GetInstance(id int64) (*model.Instance, error)
    ListInstancesByUser(userID int64) ([]*model.Instance, error)
    ListInstancesByStatuses(statuses []model.InstanceStatus) ([]*model.Instance, error)
    UpdateInstance(*model.Instance) error
    // audit
    CreateAuditLog(*model.AuditLog) error
    ListAuditLogs(limit int) ([]*model.AuditLog, error)
    Close() error
}

// internal/cloud
type Provider interface {
    Name() string  // "alicloud" | "volcengine"
    ListImages(ctx, region string) ([]model.Image, error)
    ListInstanceTypes(ctx, region string) ([]model.InstanceTypeSpec, error)
    ListRegions(ctx) ([]string, error)
}

// internal/tofu — Runner 管理每个实例的工作目录（deploy/tofu-templates/ 下按 provider 一份模板）
type Runner interface {
    Apply(ctx, workspace string, vars map[string]string) error
    Destroy(ctx, workspace string) error
    OutputIP(ctx, workspace string) (public, private string, err error)
}
```

## 生命周期规则（internal/scheduler 实现权威逻辑）

1. 创建时 `ExpiresAt = now + DurationSec`（默认 3600s，受 `min(user.MaxDurationSec, cfg.MaxDurationSec)` 上限约束，管理员可配全局上限，用户配额 0 表示用全局默认）。
2. `ExpiresAt` 之前可点击“续用”**最多一次**：`DurationSec` 再次受上限约束，`RenewedAt` 置为当前时间。
3. 调度器每 30s 扫描：`Status==Running && now>ExpiresAt` → 置 `Destroying`，异步调 tofu Destroy，成功→`Destroyed`，失败→保持 `Destroying` 并记 `ErrorMessage`，下轮重试（指数退避可选）。
4. `Creating` 卡死超时（默认 15min）→ 置 `Failed`。

## API 面（internal/api，前缀 /api/v1，JSON）

- `POST /auth/feishu/login` `{authorize_url}` → `GET /auth/feishu/callback`
- `POST /auth/token` (PAT exchange) / `POST /auth/pats` / `GET /auth/pats` / `DELETE /auth/pats/{id}`
- `GET /me`
- Admin: `GET/POST /users`, `PATCH /users/{id}`（含 maxDurationSec、role、status）, `DELETE /users/{id}`
- `GET /providers/{p}/regions|images?region=|instance-types?region=`
- `POST /instances` `{provider,region,zone,imageID,instanceType,durationSec?,name?}`
- `GET /instances`, `GET /instances/{id}`, `POST /instances/{id}/renew` `{durationSec?}`, `DELETE /instances/{id}`（提前销毁）
- `GET /admin/audit-logs`

认证中间件：`Authorization: Bearer <jwt>` 或 `Authorization: Bearer <pat_ocw_...>`；401/403 JSON 错误。

## 部署（deploy/）

多阶段 Dockerfile（builder: golang:1.27-alpine，runtime: alpine + opentofu 安装），非 root 用户，HEALTHCHECK `/healthz`。compose 挂载 `data/`（sqlite + tofu workspaces）与 `tofu-templates/`。

## Git

所有提交信息中文，符合 Conventional Commits。
