# syntax=docker/dockerfile:1.6
#
# benzhi.Dockerfile —— 本 Zhi 评测专用镜像（保留 Go 工具链以便容器内执行 go build/vet/test -race）。
#
#   产物路径：/usr/local/bin/server
#   对外端口：EXPOSE 8080
#   健康检查：/health
#   工作目录：/app（方便挂载源码后执行 go 命令）
#
# 构建参数：
#   --platform linux/amd64  或  --platform linux/arm64
#   buildx 会自动注入 TARGETARCH=amd64 / arm64

ARG GO_VERSION=1.22
ARG ALPINE_VERSION=3.20

FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS runtime

ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ARG TARGETARCH

ENV GOPROXY=${GOPROXY} \
    GOSUMDB=${GOSUMDB} \
    GO111MODULE=on \
    GOOS=linux \
    TZ=Asia/Shanghai \
    LANG=C.UTF-8 \
    APP_ENV=production \
    APP_PORT=8080 \
    APP_ADDR=0.0.0.0:8080 \
    DATA_DIR=/app/data \
    FIRMWARE_DIR=/app/data/firmwares \
    MAX_UPLOAD_MB=100 \
    SEED_DATA=1 \
    LOG_LEVEL=info

# 运行时最小依赖：CA、时区、curl、bash、git、
# 以及 gcc/musl-dev：go test -race 需要 cgo 编译 race 运行时
# 注：CGO_ENABLED 不设全局 ENV，默认 go 1.22 在安装 gcc 的情况下 CGO_ENABLED=1；
#     构建 server 二进制时再显式传 CGO_ENABLED=0 得到纯静态二进制。
RUN apk add --no-cache \
        ca-certificates \
        tzdata \
        curl \
        bash \
        git \
        build-base \
        musl-dev \
    && mkdir -p /app/data/firmwares /app/data/uploads /app/web /src \
    && rm -rf /var/cache/apk/* /tmp/*

WORKDIR /src

# 依赖层缓存：先拷贝 go.mod / go.sum 再下载
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download || true

# 拷贝全部源码
COPY . .

# 构建 server 二进制（CGO_ENABLED=0 产出纯静态二进制，与是否有 gcc 无关）
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    echo "Building server with GOARCH=${TARGETARCH} GOOS=linux CGO_ENABLED=0" && \
    CGO_ENABLED=0 GOARCH=${TARGETARCH} go build \
      -trimpath \
      -ldflags="-s -w -X 'main.buildVersion=docker-benzhi' -X 'main.buildCommit=local' -X 'main.buildTime=$(date -u +%FT%TZ)'" \
      -o /usr/local/bin/server \
      ./cmd/server && \
    echo "built: $(ls -l /usr/local/bin/server)" && \
    file /usr/local/bin/server 2>/dev/null || true

# 验证：安装后 go test -race 能用 CGO 编译
RUN echo "Testing CGO toolchain: CGO_ENABLED=$(go env CGO_ENABLED) CC=$(go env CC)" && \
    gcc --version 2>/dev/null | head -1 || echo "gcc not installed properly"

WORKDIR /app

EXPOSE 8080/tcp

VOLUME [ "/app/data" ]

# 健康检查（路径 /health，符合路由注册）
HEALTHCHECK --start-period=5s --interval=10s --timeout=3s --retries=5 \
    CMD curl -fsS http://127.0.0.1:8080/health || exit 1

STOPSIGNAL SIGTERM

# 启动入口：/usr/local/bin/server
ENTRYPOINT [ "/usr/local/bin/server" ]
CMD []
