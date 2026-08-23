#!/usr/bin/env bash
#
# build_benzhi_docker.sh —— 为评测构建 Docker 镜像。
#
# 用法（位置参数模式，提示词指定）：
#   ./build_benzhi_docker.sh <image_name> <tag> <platform>
#   # 例如：
#   ./build_benzhi_docker.sh exam-system latest linux/amd64
#   ./build_benzhi_docker.sh exam-system latest linux/arm64
#
# 用法（传统 flag 模式，保留向后兼容）：
#   ./build_benzhi_docker.sh --tag my-repo/fu-benzhi:latest --platform linux/amd64 --load
#
# 参数：
#   位置参数：
#     $1  镜像名（name，不带 tag）
#     $2  tag（版本，默认 latest）
#     $3  platform（如 linux/amd64 / linux/arm64，默认不指定）
#   flags:
#     -t/--tag        完整镜像名:tag（若指定，覆盖位置参数）
#     -f/--file       Dockerfile 路径
#     -p/--proxy      GOPROXY: cn | direct
#     --platform      目标平台，如 linux/amd64,linux/arm64
#     --no-cache      禁用缓存
#     --load          buildx 完成后 load 到本地（单架构默认启用）
#     --push          buildx 完成后 push 到仓库
#     --save          导出为 .tar.gz

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${SCRIPT_DIR}"

# ---- 位置参数优先 -----------------------------------------------------------
IMAGE_NAME=""
IMAGE_VERSION=""
PLATFORM_ARG=""

if [[ $# -ge 1 && "${1:0:1}" != "-" ]]; then
  IMAGE_NAME="${1}"
  IMAGE_VERSION="${2:-latest}"
  PLATFORM_ARG="${3:-}"
  # 消耗已解析的位置参数
  _consumed=1
  [[ -n "${2:-}" ]] && _consumed=2
  [[ -n "${3:-}" ]] && _consumed=3
  shift "${_consumed}"
fi

# ---- 默认值 -----------------------------------------------------------------
TAG=""
if [[ -n "${IMAGE_NAME}" ]]; then
  TAG="${IMAGE_NAME}:${IMAGE_VERSION}"
else
  TAG="firmware-upgrade-benzhi:$(date +%Y%m%d-%H%M%S)"
fi
DOCKERFILE="./benzhi.Dockerfile"
PROXY="direct"
NO_CACHE=0
LOAD=""
PUSH=0
SAVE=0

# ---- 解析 flags（位置参数处理完后再解析 flags） -----------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    -t|--tag)        TAG="$2";                  shift 2 ;;
    -f|--file)       DOCKERFILE="$2";           shift 2 ;;
    -p|--proxy)      PROXY="$2";                shift 2 ;;
    --platform)      PLATFORM_ARG="$2";         shift 2 ;;
    --no-cache)      NO_CACHE=1;                shift   ;;
    --load)          LOAD=1;                    shift   ;;
    --push)          PUSH=1;                    shift   ;;
    --save)          SAVE=1;                    shift   ;;
    -h|--help)
      sed -n '2,40p' "$0"; exit 0 ;;
    *)
      echo "未知参数: $1" >&2; exit 2 ;;
  esac
done

# ---- 单架构时默认 --load 到本地 --------------------------------------------
if [[ "${PUSH}" -ne 1 ]]; then
  # 如果只指定了单一架构且没有 --push，默认 --load
  if [[ -z "${LOAD}" && ( "${PLATFORM_ARG}" == "linux/amd64" || "${PLATFORM_ARG}" == "linux/arm64" ) ]]; then
    LOAD=1
  fi
fi

echo "[build_benzhi] TAG         = ${TAG}"
echo "[build_benzhi] DOCKERFILE  = ${DOCKERFILE}"
echo "[build_benzhi] PROXY       = ${PROXY}"
echo "[build_benzhi] PLATFORM    = ${PLATFORM_ARG:-<host default>}"
echo "[build_benzhi] NO_CACHE    = ${NO_CACHE}"
echo "[build_benzhi] LOAD/PUSH/SAVE = ${LOAD}/${PUSH}/${SAVE}"

# ---- 前置依赖检查 -----------------------------------------------------------
if ! command -v docker >/dev/null 2>&1; then
  echo "[build_benzhi] 未检测到 docker，请先安装并启动 docker" >&2
  exit 3
fi
if ! docker info >/dev/null 2>&1; then
  echo "[build_benzhi] docker daemon 不可用" >&2
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

# ---- 切换到 default builder（用宿主机 buildkit，避免拉取 docker-container buildkit 镜像）
BUILDER_NAME="default"
docker buildx use "${BUILDER_NAME}" >/dev/null 2>&1 || true
echo "[build_benzhi] 使用 buildx builder: ${BUILDER_NAME}"

# ---- GOPROXY / BASE_IMAGE 设置 --------------------------------------------
if [[ "${PROXY}" == "cn" ]]; then
  BUILD_PROXY_ARGS=(
    --build-arg GOPROXY=https://goproxy.cn,direct
    --build-arg GOSUMDB=sum.golang.google.cn
    --build-arg BASE_IMAGE=docker.m.daocloud.io/library/golang
  )
else
  BUILD_PROXY_ARGS=(
    --build-arg BASE_IMAGE=docker.m.daocloud.io/library/golang
  )
fi

# ---- 组装 buildx 参数 ------------------------------------------------------
BUILDX_ARGS=()
if [[ "${NO_CACHE}" -eq 1 ]]; then
  BUILDX_ARGS+=( --no-cache )
fi
if [[ -n "${PLATFORM_ARG}" ]]; then
  BUILDX_ARGS+=( --platform "${PLATFORM_ARG}" )
fi
if [[ "${LOAD}" -eq 1 ]]; then
  BUILDX_ARGS+=( --load )
fi
if [[ "${PUSH}" -eq 1 ]]; then
  BUILDX_ARGS+=( --push )
fi

# ---- 构建 ------------------------------------------------------------------
echo "[build_benzhi] 开始构建镜像（docker buildx build）..."
set -x
docker buildx build "${BUILDX_ARGS[@]}" "${BUILD_PROXY_ARGS[@]}" \
  -t "${TAG}" \
  -f "${DOCKERFILE}" \
  .
set +x

echo "[build_benzhi] 构建完成：${TAG}"

# ---- 校验镜像本地是否存在 --------------------------------------------------
if [[ "${LOAD}" -eq 1 || "${PUSH}" -ne 1 ]]; then
  set +e
  INSPECT_ID=$(docker inspect -f '{{.Id}}' "${TAG}" 2>/dev/null || true)
  set -e
  if [[ -n "${INSPECT_ID}" ]]; then
    SIZE=$(docker inspect -f '{{.Size}}' "${TAG}" 2>/dev/null | awk '{printf "%.1f MiB", $1/1024/1024}')
    ARCH=$(docker inspect -f '{{.Architecture}}' "${TAG}" 2>/dev/null || echo "n/a")
    echo "[build_benzhi] 镜像 ID    = ${INSPECT_ID}"
    echo "[build_benzhi] 镜像大小  = ${SIZE}"
    echo "[build_benzhi] 镜像架构  = ${ARCH}"
  fi
fi

# ---- 可选：save tar.gz -----------------------------------------------------
if [[ "${SAVE}" -eq 1 ]]; then
  TAR="${SCRIPT_DIR}/$(echo "${TAG}" | tr '/:' '__').tar.gz"
  echo "[build_benzhi] 导出镜像到：${TAR}"
  docker save "${TAG}" | gzip > "${TAR}"
  SIZE=$(du -h "${TAR}" | cut -f1)
  echo "[build_benzhi] 导出大小：${SIZE}"
fi

echo "[build_benzhi] 完成 ✓"
