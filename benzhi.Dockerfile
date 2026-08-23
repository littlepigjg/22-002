# syntax=docker/dockerfile:1.6
#
# benzhi.Dockerfile —— 本 Zhi 评测专用多阶段构建镜像（纯 Go，不暴露任何第三方依赖）。
#
#   阶段：
#     1. builder —— 拉取 Go 1.22 基础镜像，构建静态二进制。
#     2. runner  —— 基于 golang-alpine（内置 Go）+ 额外依赖运行。
#
#   产物路径：/app/server、/app/web、/app/data
#   对外端口：EXPOSE 8080
#   健康检查：/health + /ready

# 1) 构建镜像 -----------------------------------------------------------
ARG GO_VERSION=1.22
ARG ALPINE_VERSION=3.20

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

# 评测环境在国内时，可通过 --build-arg GOPROXY=https://goproxy.cn,direct 切换
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

# 依赖层缓存：先拷贝 go.mod 再下载（没有 go.sum 时跳过 verify）
COPY go.mod ./
RUN if [ -f go.sum ]; then echo "go.sum found"; fi
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# 拷贝全部源码
COPY . .

# 构建：关闭 CGO、移除调试符号，输出 /out/server
# 入口文件在 cmd/server/main.go（根目录有独立测试文件 red_green_test.go，不能放 package main）
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    mkdir -p /out && \
    go build \
      -trimpath \
      -ldflags="-s -w -X 'main.buildVersion=docker-benzhi' -X 'main.buildCommit=local' -X 'main.buildTime=$(date -u +%FT%TZ)'" \
      -o /out/server \
      ./cmd/server && \
    echo "built: $(ls -l /out/server)"

# 2) 运行镜像（使用 golang-alpine 直接作为 runner，内置 Go 工具链，避免慢 apk add go）
#    注意：golang:1.22-alpine3.20 大小约 300MB（含 Go），比 apk add go 更快
FROM --platform=$TARGETPLATFORM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS runner

ARG APP_UID=10001
ARG APP_GID=10001

# 运行时额外依赖：CA、时区、curl、bash（Go 已在基础镜像中预装）
RUN apk add --no-cache ca-certificates tzdata curl bash \
    && addgroup -g ${APP_GID} -S appgroup 2>/dev/null || true \
    && adduser  -u ${APP_UID} -S appuser -G appgroup -h /app -s /bin/bash 2>/dev/null || true \
    && mkdir -p /app/data/firmwares /app/data/uploads /app/web \
    && chown -R ${APP_UID}:${APP_GID} /app || true \
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

# 二进制：放在 /usr/local/bin 避免被 -v $(pwd):/app 挂载覆盖
COPY --from=builder /out/server /usr/local/bin/server

# 创建 web 目录兜底（如果不存在则创建空目录）
RUN mkdir -p /app/web

# 运行用户与暴露端口（使用 root 以便挂载 /app 后拥有写权限执行 go build / go test）
USER root
EXPOSE 8080/tcp

# 持久化数据目录
VOLUME [ "/app/data" ]

# 健康检查（5s 宽限、10s 间隔、3 次失败算不健康）
HEALTHCHECK --start-period=5s --interval=10s --timeout=3s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8080/health || exit 1

STOPSIGNAL SIGTERM

# 启动入口逻辑：
#   - 有自定义 CMD 参数（如 /bin/bash -c "..."）时，直接执行 CMD
#   - CMD 为空（默认）时，启动服务端：优先 /app/server，否则 /usr/local/bin/server
ENTRYPOINT [ "/bin/bash", "-c", "if [ \"$#\" -gt 0 ]; then exec \"$@\"; elif [ -x /app/server ]; then exec /app/server; else exec /usr/local/bin/server; fi", "--" ]
CMD []
