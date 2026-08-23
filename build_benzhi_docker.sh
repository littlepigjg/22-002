#!/usr/bin/env bash
#
# build_benzhi_docker.sh —— 为本项目构建评测专用镜像。
#
# 用法：
#   ./build_benzhi_docker.sh exam-system latest linux/amd64
#   ./build_benzhi_docker.sh exam-system latest linux/arm64
#   ./build_benzhi_docker.sh --tag my-repo/fu-benzhi:latest
#   ./build_benzhi_docker.sh --no-cache --proxy cn --load
#
# 参数：
#   位置参数（按顺序）：
#     $1  镜像名称（如 exam-system）
#     $2  镜像标签（如 latest）
#     $3  平台架构（如 linux/amd64 或 linux/arm64）
#   命名参数：
#     --tag, -t        目标镜像标签
#     --file, -f       Dockerfile 路径，默认：./benzhi.Dockerfile
#     --proxy, -p      使用国内加速：cn 或默认 direct
#     --no-cache       强制不使用构建缓存
#     --load           buildx build 完成后 load 到本地 docker images
#     --push           构建完成后 push 镜像（需 docker login）
#     --save           构建完成后导出为 tar.gz
#
# 产物：
#   - docker 镜像（本地）
#   - 可选 firmware-upgrade-benzhi.tar.gz（当前目录）

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
LOAD=0
PUSH=0
SAVE=0

# ---- 解析参数 -----------------------------------------------------------------
# 先处理命名参数
ARGS=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    -t|--tag)
      TAG="$2"; shift 2 ;;
    -f|--file)
      DOCKERFILE="$2"; shift 2 ;;
    -p|--proxy)
      PROXY="$2"; shift 2 ;;
    --no-cache)
      NO_CACHE=1; shift ;;
    --load)
      LOAD=1; shift ;;
    --push)
      PUSH=1; shift ;;
    --save)
      SAVE=1; shift ;;
    -h|--help)
      sed -n '2,35p' "$0"; exit 0 ;;
    *)
      ARGS+=("$1")
      shift ;;
  esac
done

# 再处理位置参数（最多3个：镜像名、标签、平台）
if [[ ${#ARGS[@]} -ge 1 ]]; then
  IMAGE_NAME="${ARGS[0]}"
fi
if [[ ${#ARGS[@]} -ge 2 ]]; then
  IMAGE_TAG="${ARGS[1]}"
fi
if [[ ${#ARGS[@]} -ge 3 ]]; then
  PLATFORM="${ARGS[2]}"
fi

# 如果通过 --tag 设置了 TAG，使用它
if [[ -n "${TAG:-}" ]]; then
  # 从 TAG 解析镜像名和标签（格式：name:tag 或 name）
  if [[ "$TAG" == *:* ]]; then
    IMAGE_NAME="${TAG%%:*}"
    IMAGE_TAG="${TAG##*:}"
  else
    IMAGE_NAME="$TAG"
    IMAGE_TAG="latest"
  fi
fi

# 设置默认值
if [[ -z "$IMAGE_NAME" ]]; then
  IMAGE_NAME="firmware-upgrade-benzhi"
fi
if [[ -z "$IMAGE_TAG" ]]; then
  IMAGE_TAG="$(date +%Y%m%d-%H%M%S)"
fi
if [[ -z "$PLATFORM" ]]; then
  PLATFORM="linux/amd64"
fi

FULL_TAG="${IMAGE_NAME}:${IMAGE_TAG}"

echo "[build_benzhi] IMAGE_NAME  = ${IMAGE_NAME}"
echo "[build_benzhi] IMAGE_TAG   = ${IMAGE_TAG}"
echo "[build_benzhi] PLATFORM    = ${PLATFORM}"
echo "[build_benzhi] FULL_TAG    = ${FULL_TAG}"
echo "[build_benzhi] DOCKERFILE  = ${DOCKERFILE}"
echo "[build_benzhi] PROXY       = ${PROXY}"
echo "[build_benzhi] NO_CACHE    = ${NO_CACHE}"

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

# ---- 检查 buildx builder ------------------------------------------------------
if ! docker buildx version >/dev/null 2>&1; then
  echo "[build_benzhi] 未检测到 docker buildx" >&2
  exit 7
fi

# 使用默认 builder（支持多架构的构建需要 QEMU/binfmt 支持）
# 先检查是否已注册跨架构支持
if ! docker buildx inspect default >/dev/null 2>&1; then
  echo "[build_benzhi] 初始化默认 builder"
  docker buildx create --name default --driver docker-container --use >/dev/null 2>&1 || \
  docker buildx use default >/dev/null 2>&1 || true
else
  docker buildx use default >/dev/null 2>&1 || true
fi

# 检查是否需要 binfmt 支持（跨架构构建）
HOST_ARCH=$(uname -m)
case "${PLATFORM}" in
  linux/arm64)
    if [[ "${HOST_ARCH}" != "aarch64" && "${HOST_ARCH}" != "arm64" ]]; then
      echo "[build_benzhi] 跨平台构建 arm64，检查 binfmt 支持..."
      # 尝试安装/注册 QEMU binfmt
      if ! docker run --rm --platform linux/arm64 alpine:latest echo "arm64 OK" >/dev/null 2>&1; then
        echo "[build_benzhi] 注册 binfmt_misc 以支持跨架构构建..."
        docker run --privileged --rm tonistiigi/binfmt --install all >/dev/null 2>&1 || \
        echo "[build_benzhi] 警告: 跨架构可能不可用，若构建失败请手动运行: docker run --privileged --rm tonistiigi/binfmt --install all"
      fi
    fi
    ;;
esac

# 确定 Go 架构参数
case "${PLATFORM}" in
  linux/amd64)  ARCH="amd64" ;;
  linux/arm64)  ARCH="arm64" ;;
  *)            ARCH="amd64" ;;
esac

# ---- 根据 PROXY 设置 build-arg -----------------------------------------------
if [[ "${PROXY}" == "cn" ]]; then
  BUILD_PROXY_ARGS=(
    --build-arg GOPROXY=https://goproxy.cn,direct
    --build-arg GOSUMDB=sum.golang.google.cn
  )
else
  BUILD_PROXY_ARGS=()
fi

# 添加架构 build-arg
BUILD_ARCH_ARGS=( --build-arg ARCH="${ARCH}" )

BUILDX_ARGS=()
if [[ "${NO_CACHE}" -eq 1 ]]; then
  BUILDX_ARGS+=( --no-cache )
fi
# 指定平台
BUILDX_ARGS+=( --platform "${PLATFORM}" )
# 如果没有指定 push 且是同平台构建，使用 --load
if [[ "${PUSH}" -ne 1 ]]; then
  HOST_ARCH=$(uname -m)
  if [[ "${HOST_ARCH}" == "x86_64" && "${ARCH}" == "amd64" ]] || \
     [[ "${HOST_ARCH}" == "aarch64" && "${ARCH}" == "arm64" ]] || \
     [[ "${HOST_ARCH}" == "arm64" && "${ARCH}" == "arm64" ]]; then
    BUILDX_ARGS+=( --load )
  fi
fi

# ---- 构建 --------------------------------------------------------------------
echo "[build_benzhi] 开始构建镜像 (${PLATFORM}, arch=${ARCH})..."

docker buildx build "${BUILDX_ARGS[@]}" "${BUILD_ARCH_ARGS[@]}" "${BUILD_PROXY_ARGS[@]}" \
  -t "${FULL_TAG}" \
  -f "${DOCKERFILE}" \
  .

echo "[build_benzhi] 构建完成：${FULL_TAG} (${PLATFORM})"

# ---- 校验镜像 ----------------------------------------------------------------
if [[ "${LOAD}" -eq 1 ]] || [[ "${PUSH}" -ne 1 ]]; then
  set +e
  INSPECT_ID=$(docker inspect -f '{{.Id}}' "${FULL_TAG}" 2>/dev/null || true)
  set -e
  if [[ -n "${INSPECT_ID}" ]]; then
    SIZE=$(docker inspect -f '{{.Size}}' "${FULL_TAG}" 2>/dev/null | awk '{printf "%.1f MiB", $1/1024/1024}')
    echo "[build_benzhi] 镜像 ID    = ${INSPECT_ID}"
    echo "[build_benzhi] 镜像大小  = ${SIZE}"
  fi
fi

# ---- 可选：save 为 tar.gz ---------------------------------------------------
if [[ "${SAVE}" -eq 1 ]]; then
  TAR="${SCRIPT_DIR}/${IMAGE_NAME}-${IMAGE_TAG}.tar.gz"
  echo "[build_benzhi] 导出镜像到：${TAR}"
  docker save "${FULL_TAG}" | gzip > "${TAR}"
  SIZE=$(du -h "${TAR}" | cut -f1)
  echo "[build_benzhi] 导出大小：${SIZE}"
fi

# ---- 运行速查 ----------------------------------------------------------------
cat <<EOF

=== 启动速查（本地运行）===

  # 一次性前台启动
  docker run --rm -p 8080:8080 -e SEED_DATA=1 ${FULL_TAG}

  # 后台运行 + 挂载数据目录
  mkdir -p ./data && \\
  docker run -d --name ${IMAGE_NAME}-run \\
    -p 8080:8080 \\
    -v "\$PWD/data":/app/data \\
    -e SEED_DATA=1 \\
    -e LOG_LEVEL=debug \\
    --health-cmd="curl -fsS http://127.0.0.1:8080/health/live || exit 1" \\
    ${FULL_TAG}

  # 健康检查
  curl http://127.0.0.1:8080/health/live
  curl http://127.0.0.1:8080/health/ready
  curl http://127.0.0.1:8080/api/v1/stats/overview

EOF

echo "[build_benzhi] 完成 ✓"
