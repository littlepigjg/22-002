# benzhi.Dockerfile —— 本 Zhi 评测专用多阶段构建镜像（纯 Go，支持 linux/amd64 + linux/arm64）。
#
#   阶段：
#     1. builder —— 拉取 Go 1.22 基础镜像，构建静态二进制（按 --build-arg TARGETARCH 交叉编译）。
#     2. runner  —— 基于 golang:1.22-alpine，保留 Go 工具链以便容器内 go build / go vet。
#
#   服务器二进制：/usr/local/bin/server（避免 -v $(pwd):/app 覆盖）
#   挂载工作区：/app（挂载宿主源码，供容器内 go build / go vet / red_green_test.go 运行）
#   对外端口：EXPOSE 8080
#   健康检查：GET /health
#
# 构建参数：
#   --build-arg TARGETARCH=amd64 | arm64   目标架构（默认 amd64）
#   --build-arg GOPROXY=...                国内可用 https://goproxy.cn,direct

# 1) 构建镜像 -----------------------------------------------------------
ARG GO_VERSION=1.22
ARG ALPINE_VERSION=3.20

FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

# 评测环境在国内时，可通过 --build-arg GOPROXY=https://goproxy.cn,direct 切换
ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ARG CGO_ENABLED=0
ARG TARGETARCH=amd64

ENV GOPROXY=${GOPROXY} \
    GOSUMDB=${GOSUMDB} \
    CGO_ENABLED=${CGO_ENABLED} \
    GO111MODULE=on \
    GOOS=linux \
    GOARCH=${TARGETARCH}

RUN echo ">>> BUILD: GOARCH=${GOARCH}"

WORKDIR /src

# 依赖层缓存：先拷贝 go.mod / go.sum 再下载（空 go.sum 是合法的）
COPY go.mod go.sum ./
RUN set -eux; \
  if [ -s go.sum ]; then \
    go mod download && go mod verify; \
  else \
    go mod download || true; \
  fi

# 拷贝全部源码
COPY . .

# 构建：关闭 CGO、移除调试符号，输出 /out/server
RUN set -eux; \
  mkdir -p /out; \
  go build \
    -trimpath \
    -ldflags="-s -w -X 'main.buildVersion=docker-benzhi' -X 'main.buildCommit=local' -X 'main.buildTime=$(date -u +%FT%TZ)'" \
    -o /out/server \
    ./cmd/server; \
  ls -l /out/server; \
  (file /out/server 2>/dev/null || true)

# 2) 运行镜像（保留 Go 工具链 + 运行时最小依赖） --------------------
#    使用 golang:alpine 作为 runner 基础，确保容器内可执行 go build ./... / go vet ./...
FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS runner

ARG APP_UID=10001
ARG APP_GID=10001

# 运行时最小依赖：CA、时区、curl、bash（提示词脚本用 /bin/bash）
RUN set -eux; \
  apk add --no-cache ca-certificates tzdata curl bash; \
  (addgroup -g ${APP_GID} -S appgroup 2>/dev/null || true); \
  (adduser -u ${APP_UID} -S appuser -G appgroup -h /app -s /bin/bash 2>/dev/null || true); \
  mkdir -p /app/data/firmwares /app/data/uploads /app/web; \
  (chown -R ${APP_UID}:${APP_GID} /app 2>/dev/null || true); \
  rm -rf /var/cache/apk/* /tmp/*

ENV TZ=Asia/Shanghai \
    LANG=C.UTF-8 \
    APP_ENV=production \
    APP_PORT=8080 \
    APP_ADDR=0.0.0.0:8080 \
    DATA_DIR=/app/data \
    FIRMWARE_DIR=/app/data/firmwares \
    MAX_UPLOAD_MB=100 \
    SEED_DATA=1 \
    LOG_LEVEL=info \
    GOPROXY=https://proxy.golang.org,direct \
    GO111MODULE=on

WORKDIR /app

# 服务器二进制放到 /usr/local/bin，避免 -v $(pwd):/app 把镜像里的 server 覆盖掉
COPY --from=builder /out/server /usr/local/bin/server
RUN chmod +x /usr/local/bin/server && ls -l /usr/local/bin/server

# 前端静态资源目录（若构建时已内嵌 go:embed 则无需复制；这里也保留显式目录兜底）
COPY web /app/web

# 暴露端口
EXPOSE 8080/tcp

# 持久化数据目录
VOLUME [ "/app/data" ]

# 健康检查：路由注册中是 GET /health
HEALTHCHECK --start-period=5s --interval=10s --timeout=3s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8080/health >/dev/null || exit 1

STOPSIGNAL SIGTERM

# 入口：先确保 app 内目录可读可写，再启动 server（exec 让 PID=1 收 SIGTERM）
ENTRYPOINT [ "sh", "-c", "mkdir -p /app/data/firmwares /app/data/uploads && exec /usr/local/bin/server" ]
CMD []
