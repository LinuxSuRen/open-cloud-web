# OpenCloudLab REST API

Base URL：`http://<host>:<port>`，所有业务端点在 `/api/v1` 前缀下，请求/响应均为 JSON（除 `204 No Content`）。

- 健康检查：`GET /healthz`（无需认证）
- 认证：`Authorization: Bearer <jwt 或 pat_ocw_...>`
- 错误统一为 `{"error": "..."}`；每个响应带 `X-Request-ID` 头（8 字节随机 hex），并记录访问日志
- CORS：`Access-Control-Allow-Origin` 由服务端配置（环境变量），预检 `OPTIONS` 直接返回 `204`

## 目录

- [认证方式](#认证方式)
- [飞书 OAuth 登录](#飞书-oauth-登录)
- [PAT 令牌](#pat-令牌)
- [当前用户](#当前用户)
- [用户管理（Admin）](#用户管理admin)
- [云目录查询](#云目录查询)
- [实例管理](#实例管理)
- [审计日志（Admin）](#审计日志admin)

---

## 认证方式

支持两种 Bearer 令牌：

1. **JWT**（HS256，2 小时过期，claims：`uid`/`role`/`exp`）——浏览器会话使用，由飞书 OAuth 回调或 PAT 交换获得。
2. **PAT**（`pat_ocw_` + 43 位 base64url）——CLI/脚本长期使用。服务端只存 SHA-256 哈希；明文仅在创建时返回一次，可设过期天数，可随时吊销。

令牌识别规则：`Bearer pat_ocw_...` 按 PAT 哈希查库（含过期检查），其余按 JWT 校验。失败返回 `401`；被禁用用户返回 `401`；非管理员访问 admin 端点返回 `403`。

```bash
# 任一方式：
curl -H "Authorization: Bearer <jwt>" http://localhost:8080/api/v1/me
curl -H "Authorization: Bearer pat_ocw_xxx" http://localhost:8080/api/v1/me
```

## 密码登录（本地用户）

```bash
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"<密码>"}'
# {"token":"<jwt>","user":{...}}
```

- 仅 `provider=local` 且已有密码哈希的用户可用（管理员创建的本地用户、bootstrap admin）。
- 首次启动设置 `OCW_ADMIN_BOOTSTRAP_TOKEN` 会创建 `admin` 用户，密码即该 token，用本端点登录。
- 同一用户名连续 5 次失败锁定 5 分钟（`429`）；凭据错误统一返回 `401`，不区分用户名是否存在。

## 飞书 OAuth 登录

### 获取授权地址

```bash
curl -X POST http://localhost:8080/api/v1/auth/feishu/login
# {"authorize_url":"https://open.feishu.cn/open-apis/authen/v1/index?app_id=...&redirect_uri=...&state=..."}
```

`state` 为随机数 + HMAC-SHA256 签名（常量时间校验），防 CSRF。

### OAuth 回调

```bash
curl "http://localhost:8080/api/v1/auth/feishu/callback?code=<授权码>&state=<上一步的state>"
# {"token":"<jwt>","user":{...}}
```

- 服务端用 `code` 换 `user_access_token`（`POST /open-apis/authen/v2/oauth/token`），再取 `GET /open-apis/authen/v1/user_info` 得 `open_id/name/email`。
- 首次登录自动建用户（`provider=feishu`，`role=user`）。
- state 缺失/校验失败返回 `400`；飞书侧错误返回 `502`（带错误码信息）。

## PAT 令牌

### 创建 PAT（需认证）

```bash
curl -X POST http://localhost:8080/api/v1/auth/pats \
  -H "Authorization: Bearer <jwt>" \
  -H "Content-Type: application/json" \
  -d '{"name":"my-cli","expiresInDays":30}'
# 201 {"id":1,"name":"my-cli","token":"pat_ocw_<43字符>","expiresAt":"...","note":"token 明文仅此一次返回，请妥善保存"}
```

### 列出我的 PAT

```bash
curl -H "Authorization: Bearer <jwt>" http://localhost:8080/api/v1/auth/pats
# [{"id":1,"userID":7,"name":"my-cli","expiresAt":"...","createdAt":"..."}]   （不含哈希）
```

### 删除 PAT（吊销）

```bash
curl -X DELETE -H "Authorization: Bearer <jwt>" http://localhost:8080/api/v1/auth/pats/1
# 204；被删 PAT 立即失效
```

### PAT 换 JWT

```bash
curl -X POST http://localhost:8080/api/v1/auth/token \
  -H "Content-Type: application/json" \
  -d '{"token":"pat_ocw_<...>"}'
# {"token":"<jwt>","expiresIn":7200,"user":{...}}
```

## 当前用户

```bash
curl -H "Authorization: Bearer pat_ocw_<...>" http://localhost:8080/api/v1/me
```

## 用户管理（Admin）

需 `role=admin`，否则 `403`。本地用户仅管理员创建；密码仅以 bcrypt 哈希存储，任何响应都不回显。

### 列出用户 / 创建用户

```bash
curl -H "Authorization: Bearer <admin-jwt>" http://localhost:8080/api/v1/users

curl -X POST http://localhost:8080/api/v1/users \
  -H "Authorization: Bearer <admin-jwt>" -H "Content-Type: application/json" \
  -d '{"username":"alice","password":"s3cret-pass","role":"user","maxDurationSec":7200}'
```

- `username` 必须匹配 `^[a-zA-Z0-9_-]{2,32}$`；`password` 8–72 字符；重名返回 `409`。

### 更新用户（PATCH，部分字段）

```bash
curl -X PATCH http://localhost:8080/api/v1/users/12 \
  -H "Authorization: Bearer <admin-jwt>" -H "Content-Type: application/json" \
  -d '{"role":"admin"}'
```

可更新字段：`displayName`、`email`、`role`（admin|user）、`status`（active|disabled）、`maxDurationSec`（>=0，0 表示用全局默认）。

### 删除用户

```bash
curl -X DELETE -H "Authorization: Bearer <admin-jwt>" http://localhost:8080/api/v1/users/12
# 204；不能删除自己（400）
```

## 云目录查询

```bash
curl -H "Authorization: Bearer <jwt>" http://localhost:8080/api/v1/providers/alicloud/regions
curl -H "Authorization: Bearer <jwt>" "http://localhost:8080/api/v1/providers/alicloud/images?region=cn-beijing"
curl -H "Authorization: Bearer <jwt>" "http://localhost:8080/api/v1/providers/alicloud/instance-types?region=cn-beijing"
```

provider 取值 `alicloud` | `volcengine`；未知 provider 返回 `404`；`region` 缺失返回 `400`。

## 实例管理

### 创建实例

```bash
curl -X POST http://localhost:8080/api/v1/instances \
  -H "Authorization: Bearer pat_ocw_<...>" -H "Content-Type: application/json" \
  -d '{
    "provider":"alicloud",
    "region":"cn-beijing",
    "zone":"cn-beijing-a",
    "imageID":"ubuntu-2204",
    "instanceType":"ecs.e-c1m1.large",
    "durationSec":3600,
    "name":"my-lab"
  }'
# 202 {"id":101,"status":"creating",...}
```

规则：

- `durationSec` 缺省用服务端默认；上限 = `min(用户 maxDurationSec(>0 时), 全局 maxDurationSec)`，超限返回 `400`。
- 创建即返回 `202`（`status=creating`）；服务端异步执行 OpenTofu Apply（vars：region/zone/image_id/instance_type/instance_name/bandwidth），成功后写入公/私 IP、`status=running`、`expiresAt = apply 完成时刻 + duration`；失败置 `failed` + `errorMessage`。
- 同一用户并发 `creating` 实例最多 5 个，超出返回 `429`。

### 查询实例

```bash
curl -H "Authorization: Bearer <jwt>" http://localhost:8080/api/v1/instances        # 我的（admin 为全部）
curl -H "Authorization: Bearer <jwt>" http://localhost:8080/api/v1/instances/101    # 单个（非所有者 403）
```

状态机：`creating → running → destroying → destroyed`；任一步失败进入 `failed`。

### 续用（最多一次）

```bash
curl -X POST http://localhost:8080/api/v1/instances/101/renew \
  -H "Authorization: Bearer <jwt>" -H "Content-Type: application/json" \
  -d '{"durationSec":3600}'
```

- 仅 `running`、未过期、且本生命周期未续用过（`renewedAt` 为空）时可续用，否则 `400`。
- `expiresAt = min(now + duration, now + 上限)`；成功后 `renewedAt` 置为当前时间。

### 提前销毁

```bash
curl -X DELETE -H "Authorization: Bearer <jwt>" http://localhost:8080/api/v1/instances/101
# 202；所有者或 admin；置 destroying 并异步 tofu destroy，成功后 destroyed
```

## 审计日志（Admin）

```bash
curl -H "Authorization: Bearer <admin-jwt>" http://localhost:8080/api/v1/admin/audit-logs
# [{"id":1,"userID":7,"action":"instance.create","detail":"...","createdAt":"..."}]
```

记录的动作包括：`user.create/update/delete`、`user.register`、`pat.create/delete`、`instance.create/renew/destroy`、`instance.apply_failed` 等。
