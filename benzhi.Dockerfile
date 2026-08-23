#
# benzhi.Dockerfile —— 验证用镜像（包含 Go 编译器，可在容器内编译和测试）
#
#   阶段：
#     1. builder —— 拉取 Go 1.22 基础镜像，构建静态二进制。
#     2. runner  —— 基于 golang-alpine，包含 Go 编译器用于容器内验证。
#
#   对外端口：EXPOSE 8080
#   健康检查：/health/live + /health/ready

# 1) 构建镜像 -----------------------------------------------------------
ARG GO_VERSION=1.22
ARG ALPINE_VERSION=3.20

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

ARG GOPROXY=https://goproxy.cn,direct
ARG GOSUMDB=sum.golang.google.cn
ARG CGO_ENABLED=0
ARG TARGETARCH

ENV GOPROXY=${GOPROXY} \
    GOSUMDB=${GOSUMDB} \
    CGO_ENABLED=${CGO_ENABLED} \
    GO111MODULE=on \
    GOOS=linux \
    GOARCH=${TARGETARCH}

WORKDIR /src

# 依赖层缓存
COPY go.mod ./
RUN go mod tidy

# 拷贝全部源码
COPY . .

# 构建二进制
RUN mkdir -p /out && \
    go build \
      -trimpath \
      -ldflags="-s -w -X 'main.buildVersion=docker-benzhi' -X 'main.buildCommit=local' -X 'main.buildTime=$(date -u +%FT%TZ)'" \
      -o /out/server \
      ./cmd/server && \
    echo "built: $(ls -l /out/server)"

# 2) 运行镜像 -----------------------------------------------------------
# 注意：使用 golang 镜像作为 runner，保持 Go 编译器可用，便于容器内 go build/vet/test
FROM --platform=$TARGETPLATFORM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS runner

ARG TARGETARCH
ARG APP_UID=10001
ARG APP_GID=10001

ARG GOPROXY=https://goproxy.cn,direct
ARG GOSUMDB=sum.golang.google.cn

ENV GOPROXY=${GOPROXY} \
    GOSUMDB=${GOSUMDB} \
    CGO_ENABLED=0 \
    GO111MODULE=on \
    GOOS=linux \
    GOARCH=${TARGETARCH} \
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

# 安装 curl 用于健康检查
RUN apk add --no-cache ca-certificates tzdata curl && \
    mkdir -p /app/data/firmwares /app/data/uploads /app/web

WORKDIR /app

# 预构建的二进制（作为后备）
COPY --from=builder /out/server /app/server

EXPOSE 8080/tcp

# 健康检查
HEALTHCHECK --start-period=5s --interval=10s --timeout=3s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8080/health || exit 1

STOPSIGNAL SIGTERM

# 启动入口：使用 go run 编译并启动服务（支持挂载源码后动态编译）
# 若 /app/server 可执行则优先使用，否则编译运行
ENTRYPOINT ["/bin/sh", "-c", "if [ -x /app/server ]; then /app/server; else go run ./cmd/server; fi"]
