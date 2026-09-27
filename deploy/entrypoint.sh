#!/bin/sh
# entrypoint.sh：初始化数据目录权限后 exec 主程序（信号直达 ocw）。
set -eu

DATA_DIR="${OCW_DATA_DIR:-/app/data}"

# 初始化 sqlite / workspaces / plugin-cache 目录（挂载卷首次为空）。
mkdir -p "$DATA_DIR/workspaces" "$DATA_DIR/plugin-cache"

# 挂载卷属主可能不是 10001，尽量修正（无权限时静默跳过，由挂载配置保证可写）。
chown -R "$(id -u):$(id -g)" "$DATA_DIR" 2>/dev/null || true
chmod -R u+rwX "$DATA_DIR" 2>/dev/null || true

echo "[entrypoint] data dir ready: $DATA_DIR"
exec /app/ocw
