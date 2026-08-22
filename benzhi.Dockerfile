#
# benzhi.Dockerfile —— 本 Zhi 评测专用多阶段构建镜像（纯 Go）。
#
#   阶段：
#     1. builder —— 拉取 Go 1.22 基础镜像，构建静态二进制（输出到 /out/server）。
#     2. runner  —— 基于 golang:1.22-alpine（内置 Go 工具链），方便挂载源码后执行
#                   go build / go vet / go test -race 等容器内编译与缺陷验证。
#
#   注意：server 二进制放置在 /usr/local/bin/server，不在 /app 下，
#        避免 `docker run -v $(pwd):/app` 挂载源码覆盖后入口失效。
#
#   对外端口：EXPOSE 8080
#   健康检查：GET /health

# -----------------------------------------------------------------------------
# 全局 ARG
# -----------------------------------------------------------------------------
ARG GO_VERSION=1.22
ARG ALPINE_VERSION=3.20

# -----------------------------------------------------------------------------
# 1) 构建阶段：在 BUILDPLATFORM（宿主机原生架构）上交叉编译 -> TARGETARCH 产物
# -----------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ARG CGO_ENABLED=0
ARG TARGETARCH
ARG TARGETOS

ENV GOPROXY=${GOPROXY} \
    GOSUMDB=${GOSUMDB} \
    CGO_ENABLED=${CGO_ENABLED} \
    GO111MODULE=on \
    GOOS=${TARGETOS:-linux} \
    GOARCH=${TARGETARCH:-amd64}

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
    echo "built $(go env GOOS)/$(go env GOARCH): $(ls -l /out/server)"

# -----------------------------------------------------------------------------
# 2) 运行阶段：内置 Go 工具链，支持挂载源码后在容器内编译 / 测试 / 验证缺陷
# -----------------------------------------------------------------------------
FROM --platform=$TARGETPLATFORM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS runner

ARG APP_UID=10001
ARG APP_GID=10001

# bash: docker exec 要求 /bin/bash; curl: 健康检查; ca-certificates/tzdata: TLS 与时区
# gcc / musl-dev / binutils: go test -race 所需 CGO 工具链
RUN apk add --no-cache ca-certificates tzdata curl bash git gcc musl-dev binutils \
    && addgroup -g ${APP_GID} -S appgroup \
    && adduser  -u ${APP_UID} -S appuser -G appgroup -h /app -s /bin/bash \
    && mkdir -p /app/data/firmwares /app/data/uploads /app/web \
    && chown -R ${APP_UID}:${APP_GID} /app \
    && rm -rf /var/cache/apk/* /tmp/*

# 时区 / 语言 / 服务配置
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
    GOSUMDB=sum.golang.org \
    GO111MODULE=on \
    CGO_ENABLED=1

WORKDIR /app

# server 二进制放在 /usr/local/bin，避免 -v $(pwd):/app 挂载源码时被覆盖
COPY --from=builder /out/server /usr/local/bin/server
RUN chmod +x /usr/local/bin/server

# 静态资源兜底（若后续挂载源码覆盖 /app，需主机也提供 web 目录；这里保留镜像内拷贝）
COPY web /app/web

# 暴露端口
EXPOSE 8080/tcp

# 持久化数据目录
VOLUME [ "/app/data" ]

# 健康检查（5s 宽限、10s 间隔、3 次失败算不健康）
HEALTHCHECK --start-period=5s --interval=10s --timeout=3s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8080/health || exit 1

STOPSIGNAL SIGTERM

# 使用 root 用户运行容器，便于挂载源码后在 /app 下执行 go build / go test 写缓存
# （也可改为 appuser，但需要确保 /go 和 /app 写权限，评测场景 root 更简单可靠）
USER root

# 启动入口：直接执行 /usr/local/bin/server（源码挂载覆盖不影响）
ENTRYPOINT [ "/usr/local/bin/server" ]
CMD []
