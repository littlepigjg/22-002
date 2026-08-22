# syntax=docker/dockerfile:1.6
#
# benzhi.Dockerfile —— 本 Zhi 评测专用多阶段构建镜像（纯 Go，同时容器内保留完整 Go 工具链用于验证）。
#
#   阶段：
#     1. builder —— 拉取 Go 1.22 基础镜像，构建静态二进制。
#     2. runner  —— 基于 golang:1.22-alpine（带完整 go 工具链）运行，便于容器内 go build / go test 验证。
#
#   对外端口：EXPOSE 8080
#   健康检查：/health

# 1) 构建镜像 -----------------------------------------------------------
ARG GO_VERSION=1.22
ARG ALPINE_VERSION=3.20

FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

# 评测环境在国内时，可通过 --build-arg GOPROXY=https://goproxy.cn,direct 切换
ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ARG CGO_ENABLED=0
ARG TARGETOS=linux
ARG TARGETARCH=amd64

ENV GOPROXY=${GOPROXY} \
    GOSUMDB=${GOSUMDB} \
    CGO_ENABLED=${CGO_ENABLED} \
    GO111MODULE=on \
    GOOS=${TARGETOS} \
    GOARCH=${TARGETARCH}

WORKDIR /src

# 依赖层缓存：先拷贝 go.mod / go.sum 再下载
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download 2>/dev/null || true

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
    echo "built: $(ls -l /out/server) GOOS=${TARGETOS} GOARCH=${TARGETARCH}"

# 2) 运行镜像（带完整 Go 工具链，便于容器内 go build / go vet / go test 验证） ---------
FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS runner

ARG APP_UID=10001
ARG APP_GID=10001

# 运行时最小依赖：CA、时区、bash、curl、用户创建
RUN apk add --no-cache ca-certificates tzdata curl bash \
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

# 二进制
COPY --from=builder /out/server /app/server

# 前端静态资源目录
COPY web /app/web

# 不切换用户，挂载源代码后需要 root 写权限（go build / go test 需要写缓存）
# USER ${APP_UID}:${APP_GID}
EXPOSE 8080/tcp

# 持久化数据目录
VOLUME [ "/app/data" ]

# 健康检查（5s 宽限、10s 间隔、3 次失败算不健康）—— /health 对应 router 中的路径
HEALTHCHECK --start-period=5s --interval=10s --timeout=3s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8080/health || exit 1

STOPSIGNAL SIGTERM

# 启动入口：默认跑 server，但因为是挂载源码验证，允许入口被覆盖（如 sleep infinity）
ENTRYPOINT [ "/app/server" ]
CMD []
