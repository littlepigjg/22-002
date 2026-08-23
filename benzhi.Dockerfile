# benzhi.Dockerfile —— 评测专用多阶段构建镜像（Go 工具链保留在最终镜像中，便于容器内 go build/vet/test）。
#
#   阶段：
#     1. builder —— 拉取 Go 1.22 基础镜像，按目标架构编译静态二进制 /usr/local/bin/server
#     2. runner  —— 基于 golang:1.22-alpine（保留 Go 工具链）+ ca-certificates/tzdata/curl/bash
#
#   注意：server 二进制放到 /usr/local/bin/server，避免 -v $(pwd):/app 挂载源码时覆盖 server。
#   健康检查：/health （对应 router 中 mux.HandleFunc("/health", health.Health)）
#   对外端口：EXPOSE 8080

ARG GO_VERSION=1.22
ARG ALPINE_VERSION=3.20

# ============ 1) 构建阶段：按目标架构编译二进制 ============
FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

# 通过 buildx --platform 自动注入 TARGETARCH（amd64 / arm64）
ARG TARGETARCH
ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ARG CGO_ENABLED=0

ENV GOPROXY=${GOPROXY} \
    GOSUMDB=${GOSUMDB} \
    CGO_ENABLED=${CGO_ENABLED} \
    GO111MODULE=on \
    GOOS=linux \
    GOARCH=${TARGETARCH}

WORKDIR /src

# 依赖层缓存：先拷贝 go.mod / go.sum 再下载
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download && go mod verify || true

# 拷贝全部源码
COPY . .

# 构建：关闭 CGO、移除调试符号，输出 /out/server
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    mkdir -p /out && \
    go build \
      -trimpath \
      -ldflags="-s -w -X 'main.buildVersion=docker-benzhi' -X 'main.buildCommit=local' -X 'main.buildTime=$(date -u +%FT%TZ)'" \
      -o /out/server \
      ./cmd/server && \
    echo "built: $(ls -l /out/server)" && \
    file /out/server || ls -la /out/server

# ============ 2) 运行阶段：保留 Go 工具链以便容器内 go build/vet/test ============
FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS runner

ARG APP_UID=10001
ARG APP_GID=10001

# 运行时依赖：CA、时区、curl（健康检查）、bash（评测脚本使用 /bin/bash）、file（可选）
RUN apk add --no-cache ca-certificates tzdata curl bash file \
    && mkdir -p /app/data/firmwares /app/data/uploads /app/web \
    && rm -rf /var/cache/apk/* /tmp/*

ENV TZ=Asia/Shanghai \
    LANG=C.UTF-8 \
    APP_ENV=production \
    APP_PORT=8080 \
    APP_ADDR=0.0.0.0:8080 \
    DATA_DIR=/app/data \
    FIRMWARE_DIR=/app/data/firmwares \
    MAX_UPLOAD_MB=100 \
    SEED_DATA=1 \
    LOG_LEVEL=info

WORKDIR /app

# 把预编译的 server 放到 /usr/local/bin，防止 -v $(pwd):/app 挂载源码时被覆盖
COPY --from=builder /out/server /usr/local/bin/server
RUN chmod +x /usr/local/bin/server

# 前端静态资源目录（占位）
COPY web /app/web

# 暴露端口
EXPOSE 8080/tcp

# 持久化数据目录
VOLUME [ "/app/data" ]

# 健康检查：使用路由中实际存在的 /health 路径
HEALTHCHECK --start-period=5s --interval=10s --timeout=3s --retries=5 \
    CMD curl -fsS http://127.0.0.1:8080/health || exit 1

STOPSIGNAL SIGTERM

# 入口：使用 /usr/local/bin/server（不在 /app 下，不会被挂载覆盖）
ENTRYPOINT [ "/usr/local/bin/server" ]
CMD []
