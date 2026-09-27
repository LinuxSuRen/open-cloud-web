# syntax=docker/dockerfile:1
# 多阶段构建：builder 编译纯 Go 二进制（无 CGO），runtime 只带运行时依赖。
# 构建上下文为仓库根目录：docker build -f deploy/Dockerfile .

# ---------- builder ----------
FROM golang:1.27-alpine AS builder

ARG VERSION=dev
WORKDIR /src

# 先拷贝模块文件利用层缓存（依赖不新增时命中缓存）。
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/ocw ./cmd/ocw

# ---------- runtime ----------
FROM alpine:3.20

# 固定版本安装 opentofu（从 GitHub release 下载并校验 sha256）、
# ca-certificates（HTTPS 云 API）、tzdata、wget（HEALTHCHECK 用）。
# musl 动态链接：pipewire 无关，opentofu 官方 musl 包即此用法。
ARG OPENTOFU_VERSION=1.8.8
ARG OPENTOFU_SHA256=2e3973f9e2eb9d3f4b03dbcb4f8a1e18a10259d756f95a9e6b3b7a9c57c8c1b3
RUN apk add --no-cache ca-certificates tzdata wget \
    && wget -q -O /tmp/opentofu.apk \
       "https://github.com/opentofu/opentofu/releases/download/v${OPENTOFU_VERSION}/opentofu_${OPENTOFU_VERSION}_linux_amd64.apk" \
    && echo "${OPENTOFU_SHA256}  /tmp/opentofu.apk" | sha256sum -c - \
    && apk add --no-cache --allow-untrusted /tmp/opentofu.apk \
    && rm -f /tmp/opentofu.apk \
    && tofu version

# 非 root 运行（uid/gid 10001）。
RUN addgroup -g 10001 -S ocw && adduser -u 10001 -S -G ocw ocw

COPY --from=builder /out/ocw /app/ocw
COPY deploy/entrypoint.sh /app/entrypoint.sh
# tofu 模板（按 provider 一份，运行时由 embed 兜底，此处同时落盘便于挂载覆盖）。
COPY internal/tofu/templates /app/templates

RUN chmod +x /app/entrypoint.sh \
    && mkdir -p /app/data/workspaces /app/data/plugin-cache \
    && chown -R ocw:ocw /app
WORKDIR /app
USER 10001:10001

ENV OCW_LISTEN_ADDR=":8080" \
    OCW_DB_PATH="/app/data/ocw.db" \
    OCW_DATA_DIR="/app/data" \
    OCW_TOFU_TEMPLATES_DIR="/app/templates"

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/app/entrypoint.sh"]
