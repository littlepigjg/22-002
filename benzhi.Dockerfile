# benzhi.Dockerfile —— 评测专用多阶段构建镜像（多架构 + 内含 Go 工具链）。
#
#   阶段：
#     1. builder —— 拉取 Go 1.22 基础镜像，构建静态二进制（按目标架构编译）。
#     2. runner  —— 基于 golang:1.22-alpine，内含 Go 工具链、bash、curl、tzdata，
#                   支持容器内 `go build ./...` / `go vet ./...` / `go test`。
#
#   产物路径：/usr/local/bin/server（二进制，挂载源码不覆盖它）、/app/web、/app/data
#   对外端口：EXPOSE 8080
#   健康检查：/health
#

# 1) 构建镜像 -----------------------------------------------------------
ARG GO_VERSION=1.22
ARG ALPINE_VERSION=3.20

FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

# 评测环境在国内时，可通过 --build-arg GOPROXY=https://goproxy.cn,direct 切换
ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ARG CGO_ENABLED=0

# 由 buildx --platform 参数自动注入：TARGETOS=linux, TARGETARCH=amd64|arm64 等
ARG TARGETOS
ARG TARGETARCH

ENV GOPROXY=${GOPROXY} \
    GOSUMDB=${GOSUMDB} \
    CGO_ENABLED=${CGO_ENABLED} \
    GO111MODULE=on \
    GOOS=${TARGETOS:-linux} \
    GOARCH=${TARGETARCH:-amd64}

WORKDIR /src

# 依赖层缓存：先拷贝 go.mod / go.sum 再下载
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    if [ -s go.sum ]; then \
      go mod download && go mod verify; \
    else \
      go mod download; \
    fi

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
    echo "built for ${GOOS}/${GOARCH}: $(ls -l /out/server)"

# 2) 运行镜像（保留 Go 工具链，支持容器内 go build / go vet / go test） -----
FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS runner

# 运行时依赖：bash（脚本 exec）、curl（健康检查、触发 HTTP）、CA、时区
RUN apk add --no-cache ca-certificates tzdata curl bash \
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
    LOG_LEVEL=info \
    GOPATH=/go \
    GOCACHE=/go/.cache/go-build \
    PATH=/usr/local/go/bin:/go/bin:$PATH

WORKDIR /app

# 二进制放在 /usr/local/bin，避免挂载 /app 时被覆盖
COPY --from=builder /out/server /usr/local/bin/server
RUN chmod +x /usr/local/bin/server && mkdir -p /go/.cache/go-build

# 前端静态资源目录
COPY web /app/web

# 暴露端口
EXPOSE 8080/tcp

# 持久化数据目录
VOLUME [ "/app/data" ]

# 健康检查：/health （与 handler/router 中注册的端点一致）
HEALTHCHECK --start-period=5s --interval=10s --timeout=3s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8080/health || exit 1

STOPSIGNAL SIGTERM

# 启动入口：运行预编译二进制（挂载源码不会影响它）
ENTRYPOINT [ "/usr/local/bin/server" ]
CMD []
