# benzhi.Dockerfile —— 本 Zhi 评测专用多阶段构建镜像（纯 Go，不暴露任何第三方依赖）。
#
#   阶段：
#     1. builder —— 拉取 Go 1.22 基础镜像，构建静态二进制。
#     2. runner  —— 基于 slim + ca-certificates/tzdata 运行。
#
#   产物路径：/app/server、/app/web、/app/data
#   对外端口：EXPOSE 8080
#   健康检查：/health/live + /health/ready

# 1) 构建镜像 -----------------------------------------------------------
ARG GO_VERSION=1.22
ARG ALPINE_VERSION=3.20

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

# 评测环境在国内时，可通过 --build-arg GOPROXY=https://goproxy.cn,direct 切换
ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ARG CGO_ENABLED=0
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

# 2) 运行镜像 -----------------------------------------------------------
# 注：runner 内置 Go 工具链，用于在挂载源码的容器内执行 go build / go vet / go test -race 等验证步骤；
#     同时内置 bash，兼容提示词脚本中 “/bin/bash -c” 的调用方式。
FROM --platform=$TARGETPLATFORM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS runner

ARG APP_UID=10001
ARG APP_GID=10001

# 运行时依赖：CA、时区、curl（健康检查）、bash、go 工具链；
# build-base(gcc/musl-dev) 用于 go test -race（必须 CGO_ENABLED=1）；
# 注意：仅在 BUILDPLATFORM == TARGETPLATFORM（原生构建）时安装 build-base，
#       跨架构（QEMU 模拟）构建时跳过以避免 apk add 在 qemu-user 下长时间/卡死安装 build-base。
#       运行测试按提示词仅在宿主机原生架构容器内执行，因此跨架构镜像只需要可成功构建即可。
ARG BUILDPLATFORM
ARG TARGETPLATFORM
ARG TARGETARCH
ARG BUILDARCH
RUN echo "BUILDPLATFORM=${BUILDPLATFORM} TARGETPLATFORM=${TARGETPLATFORM}" \
    && if [ "${BUILDPLATFORM}" = "${TARGETPLATFORM}" ]; then \
         echo "Native build: installing full runtime + build-base for race tests"; \
         apk add --no-cache ca-certificates tzdata curl bash git build-base; \
       else \
         echo "Cross-arch build (QEMU emulated): installing minimal runtime (skipping build-base)"; \
         apk add --no-cache ca-certificates tzdata curl bash git; \
       fi \
    && addgroup -g ${APP_GID} -S appgroup 2>/dev/null || true \
    && adduser  -u ${APP_UID} -S appuser -G appgroup -h /app -s /bin/bash 2>/dev/null || true \
    && mkdir -p /app/data/firmwares /app/data/uploads /app/web \
    && rm -rf /var/cache/apk/* /tmp/*

# 继承 golang 镜像自带的 Go env
# 注意：CGO_ENABLED 默认开启，保证 go test -race 可用；仅在需要纯静态二进制时可临时覆盖为 0
ENV GOPROXY=https://goproxy.cn,direct \
    GOSUMDB=sum.golang.google.cn \
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

# 预置二进制（未挂载源码时可直接运行；挂载源码后将被覆盖，但容器仍可存活）
COPY --from=builder /out/server /usr/local/bin/server

# 前端静态资源目录
COPY web /app/web

# 暴露端口
EXPOSE 8080/tcp

# 持久化数据目录
VOLUME [ "/app/data" ]

# 健康检查（5s 宽限、10s 间隔、3 次失败算不健康）
HEALTHCHECK --start-period=5s --interval=10s --timeout=3s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8080/health || exit 1

STOPSIGNAL SIGTERM

# 启动入口：
#   1) 优先使用容器内预置二进制（未挂载源码场景）
#   2) 当 /app 被源码挂载覆盖、/app/server 不存在时，使用 sleep infinity 保持容器存活，
#      便于通过 docker exec 进入容器手工执行 go build / go vet / go run / go test。
ENTRYPOINT [ "/bin/sh", "-c", "if [ -x /app/server ]; then exec /app/server \"$@\"; elif [ -x /usr/local/bin/server ] && [ ! -f /app/go.mod ]; then exec /usr/local/bin/server \"$@\"; else exec sleep infinity; fi", "--" ]
CMD []
