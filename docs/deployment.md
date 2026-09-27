# OpenCloudLab 生产部署指南

本文描述 OpenCloudLab（Go + OpenTofu，SQLite 存储）在生产环境的部署、凭据注入、备份与升级。所有配置经环境变量读取，统一支持 `OCW_` 前缀（`OCW_<NAME>` 优先于 `<NAME>`）。

## 1. 前置条件

- Docker 20+ 与 docker compose v2
- 一个飞书企业自建应用（获取 App ID / App Secret，配置回调地址）
- 阿里云 / 火山引擎 AccessKey（建议最小权限：仅 ECS/云主机实例与镜像、规格的查询权限）
- 对外域名与 TLS 终结（建议 Nginx/Caddy 反向代理 + HTTPS；OAuth 回调要求回调地址与飞书后台配置一致）

## 2. compose 参数

部署文件位于 `deploy/compose.yaml`，核心要点：

- 镜像由仓库多阶段 Dockerfile 构建（builder: `golang:1.27-alpine`；runtime: alpine + opentofu，非 root 用户，内置 `HEALTHCHECK /healthz`）
- 挂载 `./data` → 容器 `data/`：SQLite 数据库与每个实例的 OpenTofu workspace 都在这里，**必须持久化**
- 挂载 tofu 模板目录（`deploy/tofu-templates`）
- 建议显式声明健康检查与重启策略：

```yaml
services:
  ocw:
    build: ..
    restart: unless-stopped
    environment:
      OCW_JWT_SECRET: ${OCW_JWT_SECRET}
      OCW_PUBLIC_BASE_URL: https://ocw.example.com
      # 其余变量见下表
    volumes:
      - ./data:/app/data
      - ../deploy/tofu-templates:/app/deploy/tofu-templates:ro
    ports:
      - "8080:8080"
```

推荐在 compose 同目录放置 `.env` 文件管理变量（勿提交到 Git）。

## 3. 凭据注入

三类敏感凭据全部经环境变量进入进程，不落数据库、不进前端：

| 类别 | 变量 | 获取方式 |
| --- | --- | --- |
| JWT 密钥 | `OCW_JWT_SECRET` | `openssl rand -hex 32` 自行生成；更换会使所有会话失效 |
| 飞书应用 | `OCW_FEISHU_CLIENT_ID`、`OCW_FEISHU_CLIENT_SECRET` | 飞书开放平台 → 企业自建应用 |
| 飞书回调 | `OCW_FEISHU_REDIRECT_URL` | 与飞书后台「重定向 URL」完全一致，指向 `{PUBLIC_BASE_URL}/api/v1/auth/feishu/callback` |
| 阿里云 | `OCW_ALICLOUD_ACCESS_KEY`、`OCW_ALICLOUD_SECRET_KEY` | RAM 子账号 AK/SK |
| 火山引擎 | `OCW_VOLCENGINE_ACCESS_KEY`、`OCW_VOLCENGINE_SECRET_KEY` | IAM 子账号 AK/SK |
| 引导管理员 | `OCW_ADMIN_BOOTSTRAP_TOKEN` | 可选，首次启动引导 admin |

建议：凭据通过部署平台的 Secret 机制（如 compose 的 `.env` + 文件权限 600，或 K8s Secret）注入；云 AK 定期轮换，轮换后仅需重启容器。

其余常用参数：`OCW_DEFAULT_DURATION_SEC`（默认时长，3600）、`OCW_MAX_DURATION_SEC`（全局上限，86400，必须 ≥ 默认时长）、`OCW_CREATING_TIMEOUT_SEC`（900）、`OCW_SCHEDULER_INTERVAL_SEC`（30）、`OCW_LISTEN_ADDR`（`:8080`）、`OCW_DB_PATH`（`data/ocw.db`）。

## 4. 数据持久化与备份

数据全部集中在挂载卷 `data/`：

```
data/
├── ocw.db            # SQLite 数据库（用户、PAT 哈希、实例元数据、审计日志）
└── workspaces/       # 每个实例一个 OpenTofu 目录（.tf 变量 + terraform.tfstate）
```

备份策略：

1. **SQLite**：使用 SQLite 在线备份（而非直接 cp 正在写的文件）：

   ```bash
   docker compose exec ocw sh -c \
     'sqlite3 data/ocw.db ".backup /app/data/backup-$(date +%F).db"'
   ```

   每日定时执行，保留 7～30 份。
2. **tofu workspaces**：`data/workspaces/` 直接 rsync/tar 备份。**丢失 state 意味着平台无法再销毁对应云资源**，只能去云控制台手工清理，务必随库一起备份。
3. 恢复：停容器 → 还原 `data/` → 起容器。建议每月演练一次恢复。

## 5. 升级流程

1. 备份 `data/`（见上节）。
2. 拉取新版本代码/镜像：

   ```bash
   git pull
   docker compose -f deploy/compose.yaml build
   ```
3. 滚动替换：

   ```bash
   docker compose -f deploy/compose.yaml up -d
   ```
4. 验证 `curl https://ocw.example.com/healthz`、登录、实例列表正常。
5. 出问题回滚：切回旧镜像 tag 重新 `up -d`，并确认数据库未执行过不兼容变更（升级说明会注明是否需要迁移）。

注意：升级窗口内调度器停摆，已过期实例会在服务恢复后的下一轮扫描中补销毁，不会遗漏。

## 6. 监控与健康检查

- 健康检查端点：`GET /healthz`（容器 `HEALTHCHECK` 已内置，compose/LB 可直接引用）。
- 建议外部拨测：每分钟探测 `/healthz`，连续失败告警。
- 关键业务指标可通过审计日志间接观测（实例创建/销毁成功率）；`Destroying` 长期堆积说明云侧销毁异常，需关注。
- 容器内存/CPU 常规阈值告警即可（正常负载很低，OpenTofu apply 期间有小峰值）。

## 7. 日志

- 标准输出 JSON/文本日志：`docker compose logs -f ocw`。
- OpenTofu 执行输出随实例记录在错误信息/日志中；排查创建或销毁失败时优先看实例的 `errorMessage` 与容器日志里的 tofu 段。
- 建议将容器日志接入集中式收集（loki/ELK），审计日志表用于业务侧追溯。

## 8. 常见问题（FAQ）

| 问题 | 处理 |
| --- | --- |
| 启动报 `JWT_SECRET` 相关错误 | 设置 `OCW_JWT_SECRET` 为强随机值 |
| 启动报 MaxDurationSec 校验失败 | 保证 `OCW_MAX_DURATION_SEC ≥ OCW_DEFAULT_DURATION_SEC` 且两者 > 0 |
| 飞书登录回调失败 | 核对 `PUBLIC_BASE_URL`、`FEISHU_REDIRECT_URL` 与飞书后台回调地址三方一致 |
| 实例一直 creating 后 failed | 查看实例 `errorMessage`：AK 权限、镜像/规格在该可用区不存在、账号欠费 |
| 实例长期 destroying | tofu destroy 失败，看容器日志 tofu 输出；必要时在云控制台核对资源残留 |
| 想回收所有资源后重建平台 | 逐台 `DELETE /instances/{id}` 销毁，再停服备份/迁移；不要直接删 workspace 目录（会失联云资源） |
| 磁盘增长 | `data/` 下已销毁实例的 workspace 可按需归档清理；SQLite 定期 VACUUM |
| 如何换云 AK | 更新环境变量 → `docker compose up -d` 重启即可，存量 state 不受影响 |
