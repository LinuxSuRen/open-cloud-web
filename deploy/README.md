# 部署

一条命令启动（在仓库根目录执行）：

```bash
docker compose -f deploy/docker-compose.yml up -d --build
```

- 服务监听 `http://localhost:8080`，健康检查 `GET /healthz`。
- 数据持久化在 `deploy/data/`（SQLite、tofu workspaces、插件缓存）。
- 首次启动前修改 `deploy/docker-compose.yml` 中的
  `OCW_JWT_SECRET`、`OCW_ADMIN_BOOTSTRAP_TOKEN`（非空且无 admin 时自动引导创建
  admin 用户，初始口令即该 token）以及云凭据 / 飞书 OAuth 配置。
- 查看日志：`docker compose -f deploy/docker-compose.yml logs -f`。
- 停止：`docker compose -f deploy/docker-compose.yml down`。
