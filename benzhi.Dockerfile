#
# benzhi.Dockerfile —— 本 Zhi 评测专用多阶段构建镜像。
#
#   阶段：
#     1. builder —— 使用 BUILDPLATFORM（宿主机原生架构）交叉编译目标架构静态二进制。
#     2. runner  —— 基于 golang:1.22-alpine3.20（保留 Go 工具链，支持在容器内 go build / go vet / go test -race）。
#                   二进制放在 /usr/local/bin/server，避免挂载 $(pwd):/app 时覆盖入口程序。
#
#   产物路径：/usr/local/bin/server
#   工作目录：/app（挂载源代码后可在容器内执行 go build ./...）
#   对外端口：EXPOSE 8080
#   健康检查：/health

# 1) 构建镜像 -----------------------------------------------------------
ARG GO_VERSION=1.22
ARG ALPINE_VERSION=3.20

# builder 阶段固定使用 BUILDPLATFORM（宿主机原生架构），通过 GOARCH 交叉编译目标架构二进制，
# 避免在 QEMU 仿真下运行 Go 编译器触发 segfault。
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

# 评测环境在国内时，可通过 --build-arg GOPROXY=https://goproxy.cn,direct 切换
ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ARG CGO_ENABLED=0
ARG TARGETARCH
ARG BUILDARCH

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
    go mod download && go mod verify

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
    echo "built: $(ls -l /out/server)"

# 2) 运行镜像（保留 Go 工具链，支持容器内 go build / go vet / go test -race） --------
FROM --platform=$TARGETPLATFORM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS runner

# 运行时依赖：CA、时区、curl、bash、git、以及 gcc+musl-dev（支持容器内 go test -race / CGO_ENABLED=1）
RUN apk add --no-cache ca-certificates tzdata curl bash git gcc musl-dev \
    && mkdir -p /app/data/firmwares /app/data/uploads /app/web \
    && rm -rf /var/cache/apk/* /tmp/*

# GOPROXY 设为国内加速，容器内 go test / go build 需要下载模块时更稳定
ENV GOPROXY=https://goproxy.cn,direct \
    GOSUMDB=off \
    GO111MODULE=on \
    CGO_ENABLED=0 \
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

WORKDIR /app

# 二进制放在 /usr/local/bin 避免挂载 /app 时被覆盖
COPY --from=builder /out/server /usr/local/bin/server
RUN chmod +x /usr/local/bin/server

# 前端静态资源目录（若构建时已内嵌 go:embed 则无需复制；这里也保留显式目录兜底）
COPY web /app/web

EXPOSE 8080/tcp

# 持久化数据目录
VOLUME [ "/app/data" ]

# 健康检查（5s 宽限、10s 间隔、3 次失败算不健康）
HEALTHCHECK --start-period=5s --interval=10s --timeout=3s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8080/health || exit 1

STOPSIGNAL SIGTERM

# 启动入口
ENTRYPOINT [ "/usr/local/bin/server" ]
CMD []
