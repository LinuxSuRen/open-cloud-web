# OpenCloudLab Makefile
# 一条命令启动前后端：make dev（后端 :8080 + Vite 前端 :5173，Ctrl+C 一键停止）

SHELL := /bin/sh
BIN   := ocw
GO    ?= go

# 默认开发配置（生产部署请通过环境变量覆盖，勿提交真实密钥）
OCW_JWT_SECRET          ?= dev-only-secret-change-me-0123456789
OCW_ADMIN_BOOTSTRAP_TOKEN ??= admin12345
export OCW_JWT_SECRET OCW_ADMIN_BOOTSTRAP_TOKEN

.PHONY: dev backend frontend build test fmt vet ci clean up down logs docker-build help

## dev: 同时启动后端(:8080)与前端开发服务器(:5173)，Ctrl+C 一并停止
dev: backend-build web-build
	@echo "==> 后端 http://127.0.0.1:8080  前端(热更新) http://127.0.0.1:5173"
	@echo "==> Ctrl+C 停止全部"
	@mkdir -p data
	@OCW_LISTEN_ADDR=0.0.0.0:8080 OCW_DATA_DIR=data OCW_DB_PATH=data/ocw.db \
		./$(BIN) & \
		back_pid=$$!; \
		cd webapp && npm run dev & \
		front_pid=$$!; \
		trap 'kill $$back_pid $$front_pid 2>/dev/null; exit 0' INT TERM; \
		wait

## backend: 只启动后端（Web 控制台已内嵌，浏览器开 :8080 即可用）
backend: backend-build
	@mkdir -p data
	@OCW_LISTEN_ADDR=0.0.0.0:8080 OCW_DATA_DIR=data OCW_DB_PATH=data/ocw.db ./$(BIN)

backend-build:
	$(GO) build -o ./$(BIN) ./cmd/ocw

## frontend: 只启动前端开发服务器（API 代理到 :8080，需后端在跑）
frontend:
	cd webapp && npm run dev

web-build:
	cd webapp && npm install --no-audit --no-fund && npm run build

build: backend-build ## 构建后端二进制（前端 dist 已内嵌）

test:
	$(GO) test -race -count=1 ./...

fmt:
	$(GO) fmt ./...
	tofu fmt -recursive internal/tofu/templates

vet:
	$(GO) vet ./...

ci: fmt-check vet test web-fresh ## 本地跑与 GitHub Actions 等价的检查

fmt-check:
	@test -z "$$($(GO) fmt ./... | tee /dev/stderr)" || echo "存在未格式化文件"

web-fresh: web-build
	@git diff --exit-code -- internal/web/dist || { \
		echo "internal/web/dist 与 webapp 源码不一致：请 make web-build 后提交"; exit 1; }

## up/down/logs: Docker Compose 生产形态
up:
	docker compose -f deploy/docker-compose.yml up -d --build

down:
	docker compose -f deploy/docker-compose.yml down

logs:
	docker compose -f deploy/docker-compose.yml logs -f

clean:
	rm -f $(BIN)
	$(GO) clean -testcache

help: ## 显示帮助
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## //'

.DEFAULT_GOAL := help
