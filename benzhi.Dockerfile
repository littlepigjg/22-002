# benzhi.Dockerfile —— 两阶段构建，BUILDPLATFORM 原生编译 + TARGETPLATFORM 运行层。
#
# 关键加速点（跨架构构建时）：
#   * builder 阶段使用 --platform=$BUILDPLATFORM，始终在宿主机原生架构
#     （如 x86_64）上运行，快速交叉编译出目标架构的 server 二进制。
#   * runner 阶段基于目标架构（linux/arm64 / amd64），仅做轻量 apk 安装，
#     不跑 Go 大编译，避免 QEMU 下长达数十分钟的卡顿。
#
# 最终镜像保留 Go 工具链，支持在容器内执行：
#     go build ./...
#     go vet ./...
#     go test -race . -run '^TestRedGreen$'
#
# 服务二进制位于 /usr/local/bin/server，不会被 -v $PWD:/app 挂载覆盖。
#
# 对外端口：EXPOSE 8080
# 健康检查：GET /health
# 工作目录：/app（挂载宿主源码即可验证）

ARG GO_VERSION=1.22
ARG ALPINE_VERSION=3.20

# =================== builder：原生架构交叉编译（$BUILDPLATFORM） ===================
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

# 评测 / 国内加速
ARG GOPROXY=https://goproxy.cn,direct
ARG GOSUMDB=sum.golang.google.cn
ARG APK_MIRROR=mirrors.aliyun.com

# 切换 APK 国内镜像
RUN if [ -n "${APK_MIRROR}" ]; then \
      sed -i "s/dl-cdn.alpinelinux.org/${APK_MIRROR}/g" /etc/apk/repositories; \
    fi

# 目标平台参数（buildx 自动注入）
ARG TARGETOS
ARG TARGETARCH

ENV GOPROXY=${GOPROXY} \
    GOSUMDB=${GOSUMDB} \
    GO111MODULE=on \
    CGO_ENABLED=0

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download 2>&1 || true

COPY . .
RUN mkdir -p /out \
    && GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} CGO_ENABLED=0 \
       go build \
        -trimpath \
        -ldflags="-s -w -X 'main.buildVersion=docker-benzhi' -X 'main.buildCommit=local' -X 'main.buildTime=$(date -u +%FT%TZ)'" \
        -o /out/server \
        ./cmd/server \
    && echo "built server for GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64}: $(ls -l /out/server)"

# =================== runner：目标架构运行层（保留 Go 工具链） =====================
FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS runner

ARG APK_MIRROR=mirrors.aliyun.com
ARG GOPROXY=https://goproxy.cn,direct
ARG GOSUMDB=sum.golang.google.cn

# 切换 APK 国内镜像
RUN if [ -n "${APK_MIRROR}" ]; then \
      sed -i "s/dl-cdn.alpinelinux.org/${APK_MIRROR}/g" /etc/apk/repositories; \
    fi

# 依赖说明：
#   gcc + musl-dev —— go test -race 需要 CGO；
#   curl            —— 健康检查 HTTP 探测；
#   bash            —— docker exec /bin/bash 验收脚本；
#   tzdata          —— 时区数据库。
RUN apk add --no-cache \
        curl \
        bash \
        tzdata \
        gcc \
        musl-dev \
    && rm -rf /var/cache/apk/* /tmp/*

ENV GOPROXY=${GOPROXY} \
    GOSUMDB=${GOSUMDB} \
    CGO_ENABLED=1 \
    GO111MODULE=on \
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

# server 二进制（来自 builder 交叉编译）
COPY --from=builder /out/server /usr/local/bin/server
RUN chmod +x /usr/local/bin/server

# 数据目录与静态资源
RUN mkdir -p /app/data/firmwares /app/data/uploads /app/web
COPY web /app/web

EXPOSE 8080/tcp
VOLUME [ "/app/data" ]

# 健康检查
HEALTHCHECK --start-period=5s --interval=10s --timeout=3s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8080/health || exit 1

STOPSIGNAL SIGTERM

ENTRYPOINT [ "/usr/local/bin/server" ]
CMD []
