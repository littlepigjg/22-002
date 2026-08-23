# syntax=docker/dockerfile:1.6
#
# benzhi.Dockerfile —— 本 Zhi 评测专用多阶段构建镜像。
#   阶段：
#     1. builder —— 拉取 Go 1.22 基础镜像，构建静态二进制。
#     2. runner  —— 基于 golang 镜像，同时包含 Go 工具链（用于容器内验证）。
#
#   产物路径：/app/server、/app/data
#   对外端口：EXPOSE 8080
#   健康检查：/health

# 1) 构建镜像 -----------------------------------------------------------
ARG GO_VERSION=1.22
ARG ALPINE_VERSION=3.20
ARG TARGETARCH=amd64

FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

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

COPY go.mod ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod tidy && go mod download && go mod verify

COPY . .

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    mkdir -p /out && \
    go build \
      -trimpath \
      -ldflags="-s -w -X 'main.buildVersion=docker-benzhi' -X 'main.buildCommit=local' -X 'main.buildTime=$(date -u +%FT%TZ)'" \
      -o /out/server \
      ./cmd/server && \
    echo "built: $(ls -l /out/server)"

# 2) 运行镜像 -----------------------------------------------------------
# 使用 golang 基础镜像以便容器内可执行 go build / go vet / go test
FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS runner

ARG TARGETARCH=amd64
ARG APP_UID=10001
ARG APP_GID=10001

RUN apk add --no-cache ca-certificates tzdata curl \
    && addgroup -g ${APP_GID} -S appgroup \
    && adduser  -u ${APP_UID} -S appuser -G appgroup -h /app -s /sbin/nologin \
    && mkdir -p /app/data/firmwares /app/data/uploads \
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
    LOG_LEVEL=info

WORKDIR /app

# 二进制
COPY --from=builder /out/server /app/server

EXPOSE 8080/tcp

# 持久化数据目录
VOLUME [ "/app/data" ]

# 健康检查
HEALTHCHECK --start-period=5s --interval=10s --timeout=3s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8080/health || exit 1

STOPSIGNAL SIGTERM

# 启动入口
ENTRYPOINT [ "/app/server" ]
CMD []
