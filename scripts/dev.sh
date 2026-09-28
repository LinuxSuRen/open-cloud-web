#!/bin/sh
# 一键启动前后端：后端 :8080 + Vite 前端 :5173。
# Ctrl+C（或 SIGTERM）可靠退出全部子进程；退出前清理本脚本派生的残留进程。
set -eu
cd "$(dirname "$0")/.."

# ---- 环境默认值（生产请覆盖） ----
OCW_JWT_SECRET="${OCW_JWT_SECRET:-dev-only-secret-change-me-0123456789}"
OCW_ADMIN_BOOTSTRAP_TOKEN="${OCW_ADMIN_BOOTSTRAP_TOKEN:-admin12345}"
export OCW_JWT_SECRET OCW_ADMIN_BOOTSTRAP_TOKEN

PIDS=""
cleanup() {
  trap - INT TERM EXIT
  if [ -n "$PIDS" ]; then
    kill $PIDS 2>/dev/null || true
    wait $PIDS 2>/dev/null || true
  fi
  # 兜底：清理仍挂在本次会话下的 ocw / vite
  pkill -P "$$" -f 'ocw|vite' 2>/dev/null || true
  echo "==> 已停止全部服务"
}
trap cleanup INT TERM EXIT

# ---- 端口预检：避免“以为启动了其实在跟旧进程说话” ----
port_busy() { lsof -nP -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1; }
if port_busy 8080; then
  echo "!! 8080 已被占用（可能是残留的旧后端）："; lsof -nP -iTCP:8080 -sTCP:LISTEN || true
  echo "   请先执行 make stop，或换端口 OCW_LISTEN_ADDR=:8081"
  exit 1
fi

# ---- 构建 ----
echo "==> 构建后端..."
go build -o ./ocw ./cmd/ocw
echo "==> 检查前端依赖..."
( cd webapp && [ -d node_modules ] || npm install --no-audit --no-fund )

# ---- 启动（exec 链保证 PID 即真实进程，kill 不会打偏） ----
mkdir -p data
echo "==> 后端 http://127.0.0.1:8080  前端(热更新) http://127.0.0.1:5173"
echo "==> Ctrl+C 停止全部"
( cd webapp && exec ./node_modules/.bin/vite ) & PIDS="$PIDS $!"
OCW_LISTEN_ADDR=0.0.0.0:8080 OCW_DATA_DIR=data OCW_DB_PATH=data/ocw.db \
  exec ./ocw & PIDS="$PIDS $!"

# 任一子进程退出即整体收尾（wait 在信号到达时中断并触发 trap）
wait
