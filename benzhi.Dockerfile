# benzhi.Dockerfile —— 评测专用镜像（多架构构建 + 保留 Go 工具链便于容器内 build/vet/test）。
#
# 特性：
#   1. 基于 TARGETARCH 自动适配 amd64 / arm64 等架构；
#   2. 构建阶段产出静态 server 二进制，runner 阶段放置到 /usr/local/bin/server
#      （避免 /app 被 -v 挂载后覆盖）；
#   3. runner 使用 golang:1.22-alpine，带完整 Go 工具链，可在容器内执行
#      go build / go vet / go test 等命令；
#   4. 默认入口 sleep infinity，容器启动后通过 docker exec 进入操作与手动启动服务；
#   5. 健康检查路径 /health（与 handler HealthHandler 对应）。

ARG GO_VERSION=1.22
ARG ALPINE_VERSION=3.20
ARG BASE_IMAGE=golang

# ---------- builder：交叉构建静态二进制 ----------
FROM ${BASE_IMAGE}:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ARG CGO_ENABLED=0
ARG TARGETARCH

ENV GOPROXY=${GOPROXY} \
    GOSUMDB=${GOSUMDB} \
    CGO_ENABLED=${CGO_ENABLED} \
    GO111MODULE=on \
    GOOS=linux \
    GOARCH=${TARGETARCH}

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download && go mod verify

COPY . .

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    mkdir -p /out && \
    go build \
      -trimpath \
      -ldflags="-s -w -X 'main.buildVersion=docker-benzhi' -X 'main.buildCommit=local' -X 'main.buildTime=$(date -u +%FT%TZ)'" \
      -o /out/server \
      ./cmd/server && \
    echo "built: $(ls -l /out/server), arch=$(go env GOARCH)"

# ---------- runner：保留 Go 工具链的运行镜像 ----------
FROM ${BASE_IMAGE}:${GO_VERSION}-alpine${ALPINE_VERSION} AS runner

ARG APP_UID=10001
ARG APP_GID=10001

# 运行时依赖：CA、时区、curl、bash、git
RUN apk add --no-cache ca-certificates tzdata curl bash git \
    && addgroup -g ${APP_GID} -S appgroup \
    && adduser  -u ${APP_UID} -S appuser -G appgroup -h /app -s /bin/bash \
    && mkdir -p /app/data/firmwares /app/data/uploads /app/web \
    && chown -R ${APP_UID}:${APP_GID} /app \
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
    LOG_LEVEL=info \
    GOPATH=/go \
    PATH=/usr/local/go/bin:/go/bin:$PATH

WORKDIR /app

# 预构建的 server 放在 /usr/local/bin，挂载 /app 时不会被覆盖
COPY --from=builder /out/server /usr/local/bin/server
RUN chmod +x /usr/local/bin/server

# 前端静态资源目录（空目录占位，保证 COPY 不会失败）
COPY web /app/web

EXPOSE 8080/tcp
VOLUME [ "/app/data" ]

# 健康检查：/health 端点
HEALTHCHECK --start-period=5s --interval=10s --timeout=3s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8080/health || exit 1

STOPSIGNAL SIGTERM

# 默认入口：sleep infinity，保持容器运行由 docker exec 驱动后续测试
ENTRYPOINT [ "sleep", "infinity" ]
CMD []
