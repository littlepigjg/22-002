# benzhi.Dockerfile —— 本 Zhi 评测专用多阶段构建镜像（Go 工具链内置，支持容器内 go build/test）。
#（构建使用 buildx/BuildKit 即可，无需显式 syntax= 前端镜像拉取，避免外网超时。）
#
#   阶段：
#     1. builder —— 运行在 BUILDPLATFORM（主机 arch）上，交叉编译静态二进制至 TARGETARCH。
#     2. runner  —— 基于 golang-alpine（完整 Go 工具链 + curl + tzdata），支持：
#                   - 挂载源码后自动重建 server 并启动；
#                   - 在容器内直接执行 go build / go vet / go test -race。
#
#   对外端口：EXPOSE 8080
#   健康检查：/health
#
# 可选 --build-arg：
#   REGISTRY        镜像仓库前缀；默认 docker.m.daocloud.io/library（国内可达），
#                   Docker Hub 直连时可传空字符串：
#                     --build-arg REGISTRY=
#   GOPROXY         Go 模块代理，默认根据 REGISTRY 自动推断。

# ---- 全局 ARG（必须位于首个 FROM 之前）-----------------------------
ARG REGISTRY=docker.m.daocloud.io/library
ARG GO_VERSION=1.22
ARG ALPINE_VERSION=3.20

# ---- 构建阶段（主机架构上交叉编译，避免 QEMU 下慢速 go build）-------------------
FROM --platform=$BUILDPLATFORM ${REGISTRY:+$REGISTRY/}golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

ARG GOPROXY=
ARG GOSUMDB=sum.golang.org
ARG CGO_ENABLED=0
ARG TARGETARCH
ARG REGISTRY

# GOPROXY 默认值：若 REGISTRY 使用国内镜像，则走 goproxy.cn
RUN : \
 && if [ -z "$GOPROXY" ]; then \
      case "$REGISTRY" in \
        *daocloud*|*nju.edu*|*aliyun*|*tencentyun*) \
          echo "using CN GOPROXY (registry=$REGISTRY)"; \
          GOPROXY=https://goproxy.cn,direct; GOSUMDB=sum.golang.google.cn ;; \
        *) GOPROXY=https://proxy.golang.org,direct ;; \
      esac; \
    fi; \
    echo "GOPROXY=$GOPROXY" > /etc/gomod.env; \
    echo "GOSUMDB=$GOSUMDB" >> /etc/gomod.env

ENV GOPROXY=${GOPROXY} \
    GOSUMDB=${GOSUMDB} \
    CGO_ENABLED=${CGO_ENABLED} \
    GO111MODULE=on \
    GOOS=linux \
    GOARCH=${TARGETARCH}

WORKDIR /src

COPY go.mod ./
# go.sum 可选：当无第三方依赖时可以不存在；此处兜底创建空文件，保证 go mod verify 一致性。
COPY go.sum* ./
RUN if [ ! -f go.sum ]; then touch go.sum; fi

RUN --mount=type=cache,target=/go/pkg/mod \
    set -a; . /etc/gomod.env; set +a; \
    go mod download; \
    go mod verify 2>/dev/null || true

COPY . .

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    mkdir -p /out && \
    go build \
      -trimpath \
      -ldflags="-s -w -X 'main.buildVersion=docker-benzhi' -X 'main.buildCommit=local' -X 'main.buildTime=$(date -u +%FT%TZ)'" \
      -o /out/server \
      ./cmd/server && \
    echo "built (GOARCH=$GOARCH): $(ls -l /out/server)"

# ---- 运行阶段（包含完整 Go 工具链，支持容器内 build/test）-------------------------
FROM --platform=$TARGETPLATFORM ${REGISTRY:+$REGISTRY/}golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS runner

ARG APP_UID=10001
ARG APP_GID=10001
ARG GOPROXY=
ARG GOSUMDB=sum.golang.org
ARG REGISTRY

# GOPROXY 默认值（与 builder 保持一致）
RUN : \
 && if [ -z "$GOPROXY" ]; then \
      case "$REGISTRY" in \
        *daocloud*|*nju.edu*|*aliyun*|*tencentyun*) \
          GOPROXY=https://goproxy.cn,direct; GOSUMDB=sum.golang.google.cn ;; \
        *) GOPROXY=https://proxy.golang.org,direct ;; \
      esac; \
    fi; \
    echo "GOPROXY=$GOPROXY" > /etc/gomod.env; \
    echo "GOSUMDB=$GOSUMDB" >> /etc/gomod.env; \
    cat /etc/gomod.env

# 运行时依赖：bash（便于 exec 交互）、curl（健康检查）、ca-certificates、tzdata、
#                  git（go mod 间接依赖）、gcc + musl-dev（Go 运行时 -race 需要 CGO）
# 若检测到 REGISTRY 使用了国内镜像，则 APK 源也切到国内镜像加速安装。
RUN : \
 && case "$REGISTRY" in \
      *daocloud*|*nju.edu*|*aliyun*|*tencentyun*) \
        echo "using CN Alpine mirror" ; \
        sed -i 's#https\?://dl-cdn.alpinelinux.org/alpine#https://mirrors.tuna.tsinghua.edu.cn/alpine#g' /etc/apk/repositories ;; \
    esac \
 && cat /etc/apk/repositories \
 && apk add --no-cache bash ca-certificates tzdata curl git gcc musl-dev \
 && addgroup -g ${APP_GID} -S appgroup \
 && adduser  -u ${APP_UID} -S appuser -G appgroup -h /app -s /bin/bash \
 && mkdir -p /app/data/firmwares /app/data/uploads /app/web \
 && chown -R ${APP_UID}:${APP_GID} /app \
 && rm -rf /var/cache/apk/* /tmp/*

# 从 /etc/gomod.env 载入 Go 代理设置（同时写入 ENV，以便子进程与 go 命令使用）
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
    CGO_ENABLED=0 \
    GO111MODULE=on

# entrypoint.sh 在启动时 source /etc/gomod.env 载入 GOPROXY/GOSUMDB
WORKDIR /app

# 预构建的 server 二进制（兜底，挂载 /app 后会被覆盖；entrypoint 会重新 build）
COPY --from=builder /out/server /usr/local/bin/server.prebuilt

# 静态资源目录：保持空目录（源码里若有 web/，挂载后会自然提供；无需内嵌复制）
RUN mkdir -p /app/web /app/data && chown -R ${APP_UID}:${APP_GID} /app/web /app/data

# entrypoint.sh（放在 /，不被 /app 挂载覆盖）：先 build 源码里的 server，再启动。
RUN printf '%s\n' \
  '#!/bin/bash' \
  'set +e' \
  'cd /app' \
  '[ -f /etc/gomod.env ] && { set -a; . /etc/gomod.env; set +a; }' \
  'export GOPROXY GOSUMDB CGO_ENABLED GO111MODULE' \
  'SERVER_BIN=""' \
  'if [ -f go.mod ]; then' \
  '  echo "[entrypoint] building server from mounted source (GOPROXY=$GOPROXY)..."' \
  '  BUILD_OUT="$(go build -o /tmp/server ./cmd/server 2>&1)"' \
  '  BUILD_RC=$?' \
  '  if [ $BUILD_RC -ne 0 ]; then' \
  '    echo "[entrypoint] go build failed (rc=$BUILD_RC):"' \
  '    echo "$BUILD_OUT" | tail -n 30' \
  '  fi' \
  '  if [ -x /tmp/server ]; then' \
  '    echo "[entrypoint] using freshly built /tmp/server"' \
  '    SERVER_BIN=/tmp/server' \
  '  fi' \
  'fi' \
  'if [ -z "$SERVER_BIN" ] && [ -x ./server ]; then' \
  '  echo "[entrypoint] using ./server"' \
  '  SERVER_BIN=./server' \
  'fi' \
  'if [ -z "$SERVER_BIN" ]; then' \
  '  echo "[entrypoint] fallback to /usr/local/bin/server.prebuilt"' \
  '  SERVER_BIN=/usr/local/bin/server.prebuilt' \
  'fi' \
  'echo "[entrypoint] exec: $SERVER_BIN $*"' \
  'exec "$SERVER_BIN" "$@"' \
  > /entrypoint.sh && chmod +x /entrypoint.sh

EXPOSE 8080/tcp

VOLUME [ "/app/data" ]

# 健康检查：/health（与 handler/router.go 里注册的路径一致）
HEALTHCHECK --start-period=5s --interval=10s --timeout=3s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8080/health || exit 1

STOPSIGNAL SIGTERM

ENTRYPOINT [ "/entrypoint.sh" ]
CMD []
