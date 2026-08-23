#!/usr/bin/env bash
#
# build_benzhi_docker.sh —— 为本项目构建评测专用镜像（支持 linux/amd64 / linux/arm64）。
#
# 架构实现：
#   linux/amd64 → 构建时传入 --build-arg TARGETARCH=amd64，由 Go 在 builder 阶段交叉编译
#   linux/arm64 → 构建时传入 --build-arg TARGETARCH=arm64，由 Go 在 builder 阶段交叉编译
# 这样不用依赖 docker-container driver / QEMU / dockerfile:1.6 syntax 镜像。
#
# 用法（两种，二者择一，位置参数优先）：
#
#   方式 A —— 位置参数（符合提示词 步骤1 要求）：
#     ./build_benzhi_docker.sh <image_name> <tag> <platform>
#     例如：
#       ./build_benzhi_docker.sh exam-system latest linux/amd64
#       ./build_benzhi_docker.sh exam-system latest linux/arm64
#
#   方式 B —— 命名参数：
#     ./build_benzhi_docker.sh --tag my-repo/fu-benzhi:latest [--platform linux/amd64] [--proxy cn]
#
# 参数：
#   --tag, -t        目标镜像标签，默认：firmware-upgrade-benzhi:<时间戳>
#   --platform, -P   目标平台，如 linux/amd64（默认） 或 linux/arm64
#   --file, -f       Dockerfile 路径，默认：./benzhi.Dockerfile
#   --proxy, -p      使用国内加速：cn 或默认 direct
#   --no-cache       强制不使用构建缓存
#   --push           构建完成后 push 镜像（需 docker login）
#   --save           构建完成后导出为 <image>-<tag>.tar.gz
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${SCRIPT_DIR}"

# ---- 默认参数 -----------------------------------------------------------------
IMAGE_NAME=""
IMAGE_TAG=""
PLATFORM=""
DOCKERFILE="./benzhi.Dockerfile"
PROXY="direct"
NO_CACHE=0
PUSH=0
SAVE=0

# ---- 解析参数 -----------------------------------------------------------------
# 位置参数优先级：如果 $1 不是以 "-" 开头，认为是方式 A
if [[ $# -ge 1 && "${1:-}" != -* ]]; then
  IMAGE_NAME="$1"; shift
  if [[ $# -ge 1 && "${1:-}" != -* ]]; then
    IMAGE_TAG="$1"; shift
  fi
  if [[ $# -ge 1 && "${1:-}" != -* ]]; then
    PLATFORM="$1"; shift
  fi
fi

while [[ $# -gt 0 ]]; do
  case "$1" in
    -t|--tag)
      RAW="$2"; shift 2
      if [[ "$RAW" == *":"* ]]; then
        IMAGE_NAME="${RAW%:*}"
        IMAGE_TAG="${RAW##*:}"
      else
        IMAGE_NAME="$RAW"
        IMAGE_TAG="latest"
      fi
      ;;
    -P|--platform)    PLATFORM="$2"; shift 2 ;;
    -f|--file)        DOCKERFILE="$2"; shift 2 ;;
    -p|--proxy)       PROXY="$2"; shift 2 ;;
    --no-cache)       NO_CACHE=1; shift ;;
    --push)           PUSH=1; shift ;;
    --save)           SAVE=1; shift ;;
    -h|--help)
      sed -n '2,40p' "$0"; exit 0 ;;
    *)
      echo "未知参数: $1" >&2; exit 2 ;;
  esac
done

# ---- 补全默认值 ---------------------------------------------------------------
if [[ -z "${IMAGE_NAME}" ]]; then
  IMAGE_NAME="firmware-upgrade-benzhi"
fi
if [[ -z "${IMAGE_TAG}" ]]; then
  IMAGE_TAG="$(date +%Y%m%d-%H%M%S)"
fi
if [[ -z "${PLATFORM}" ]]; then
  HOST_ARCH="$(uname -m)"
  case "${HOST_ARCH}" in
    aarch64|arm64) PLATFORM="linux/arm64" ;;
    *)             PLATFORM="linux/amd64" ;;
  esac
fi
TAG="${IMAGE_NAME}:${IMAGE_TAG}"

# PLATFORM → GOARCH 映射（传进 Dockerfile 的 --build-arg TARGETARCH）
case "${PLATFORM}" in
  linux/amd64|x86_64|amd64)   TARGETARCH="amd64" ;;
  linux/arm64|aarch64|arm64)  TARGETARCH="arm64" ;;
  *)
    echo "[build_benzhi] 不支持的平台：${PLATFORM}（仅支持 linux/amd64 / linux/arm64）" >&2
    exit 7
    ;;
esac

echo "[build_benzhi] IMAGE       = ${IMAGE_NAME}"
echo "[build_benzhi] TAG         = ${IMAGE_TAG}"
echo "[build_benzhi] REF         = ${TAG}"
echo "[build_benzhi] PLATFORM    = ${PLATFORM}"
echo "[build_benzhi] GOARCH      = ${TARGETARCH}"
echo "[build_benzhi] DOCKERFILE  = ${DOCKERFILE}"
echo "[build_benzhi] PROXY       = ${PROXY}"
echo "[build_benzhi] NO_CACHE    = ${NO_CACHE}"
echo "[build_benzhi] PUSH/SAVE   = ${PUSH}/${SAVE}"

# ---- 前置依赖检查 -------------------------------------------------------------
if ! command -v docker >/dev/null 2>&1; then
  echo "[build_benzhi] 未检测到 docker，请先安装并启动 docker" >&2
  exit 3
fi
if ! docker info >/dev/null 2>&1; then
  echo "[build_benzhi] docker daemon 不可用，请检查：systemctl status docker / docker info" >&2
  exit 4
fi
if [[ ! -f "${DOCKERFILE}" ]]; then
  echo "[build_benzhi] Dockerfile 不存在：${DOCKERFILE}" >&2
  exit 5
fi
if [[ ! -f go.mod ]]; then
  echo "[build_benzhi] 非项目根目录，缺少 go.mod" >&2
  exit 6
fi
# go.sum 若不存在则创建空文件，避免 COPY go.mod go.sum ./ 指令报错
if [[ ! -f go.sum ]]; then
  touch go.sum
fi
# web 目录若不存在则创建，避免 COPY web /app/web 报错
if [[ ! -d web ]]; then
  mkdir -p web
  echo '<!doctype html><title>placeholder</title>' > web/index.html
fi

# ---- 根据 PROXY 设置 build-arg -----------------------------------------------
BUILD_PROXY_ARGS=()
if [[ "${PROXY}" == "cn" ]]; then
  BUILD_PROXY_ARGS+=(
    --build-arg "GOPROXY=https://goproxy.cn,direct"
    --build-arg "GOSUMDB=sum.golang.google.cn"
  )
fi

BUILD_ARGS=(
  --build-arg "TARGETARCH=${TARGETARCH}"
)
if [[ ${#BUILD_PROXY_ARGS[@]} -gt 0 ]]; then
  BUILD_ARGS+=( "${BUILD_PROXY_ARGS[@]}" )
fi

if [[ "${NO_CACHE}" -eq 1 ]]; then
  BUILD_ARGS+=( --no-cache )
fi

# ---- 构建 --------------------------------------------------------------------
echo "[build_benzhi] 开始构建镜像（docker build）..."
set -x
docker build "${BUILD_ARGS[@]}" \
  -t "${TAG}" \
  -f "${DOCKERFILE}" \
  .
set +x

echo "[build_benzhi] 构建完成：${TAG}"

if [[ "${PUSH}" -eq 1 ]]; then
  echo "[build_benzhi] push 镜像 ${TAG}"
  docker push "${TAG}"
fi

# ---- 校验镜像 ----------------------------------------------------------------
set +e
INSPECT_ID=$(docker inspect -f '{{.Id}}' "${TAG}" 2>/dev/null || true)
set -e
if [[ -n "${INSPECT_ID}" ]]; then
  SIZE=$(docker inspect -f '{{.Size}}' "${TAG}" 2>/dev/null | awk '{printf "%.1f MiB", $1/1024/1024}')
  OS=$(docker inspect -f '{{.Os}}' "${TAG}" 2>/dev/null || echo "-")
  ARCH_FROM_IMAGE=$(docker inspect -f '{{.Architecture}}' "${TAG}" 2>/dev/null || echo "-")
  echo "[build_benzhi] 镜像 ID       = ${INSPECT_ID}"
  echo "[build_benzhi] 镜像大小     = ${SIZE}"
  echo "[build_benzhi] 镜像 os/arch = ${OS}/${ARCH_FROM_IMAGE}"
  # 验证内部二进制是否匹配架构
  BIN_ARCH_MSG=$(docker run --rm --entrypoint=sh "${TAG}" -c 'uname -m 2>/dev/null; (file /usr/local/bin/server 2>/dev/null | head -1) || true' 2>/dev/null | tr '\n' '|')
  echo "[build_benzhi] 容器内内核 & 二进制 = ${BIN_ARCH_MSG}"
else
  echo "[build_benzhi] ⚠️  镜像不存在于本地（检查 build 输出）"
fi

# ---- 可选：save 为 tar.gz ---------------------------------------------------
if [[ "${SAVE}" -eq 1 && -n "${INSPECT_ID}" ]]; then
  SAFE_NAME="${IMAGE_NAME//\//_}_${IMAGE_TAG//\//_}"
  TAR="${SCRIPT_DIR}/${SAFE_NAME}.tar.gz"
  echo "[build_benzhi] 导出镜像到：${TAR}"
  docker save "${TAG}" | gzip > "${TAR}"
  SIZE=$(du -h "${TAR}" | cut -f1)
  echo "[build_benzhi] 导出大小：${SIZE}"
fi

# ---- 运行速查 ----------------------------------------------------------------
cat <<EOF

=== 启动速查（本地运行）===

  # 一次性前台启动
  docker run --rm -p 8080:8080 -e SEED_DATA=1 ${TAG}

  # 后台运行 + 挂载源码（用于 go build / go vet / red_green_test 验证）
  docker rm -f test-verify 2>/dev/null || true
  docker run -d --name test-verify \\
    -p 8080:8080 \\
    -v "\$PWD":/app \\
    -e SEED_DATA=1 \\
    -e LOG_LEVEL=debug \\
    ${TAG}

  # 健康检查
  curl http://127.0.0.1:8080/health
  curl http://127.0.0.1:8080/ready

EOF

echo "[build_benzhi] 完成 ✓"
