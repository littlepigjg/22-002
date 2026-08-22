#!/usr/bin/env bash
#
# build_benzhi_docker.sh —— 为本项目构建评测专用镜像。
#
# 用法（位置参数，支持提示词脚本调用）：
#   ./build_benzhi_docker.sh <image_name> <tag> <platform>
#   ./build_benzhi_docker.sh exam-system latest linux/amd64
#   ./build_benzhi_docker.sh exam-system latest linux/arm64
#
# 用法（传统 flags）：
#   ./build_benzhi_docker.sh
#   ./build_benzhi_docker.sh --tag my-repo/fu-benzhi:latest --platform linux/amd64
#   ./build_benzhi_docker.sh --no-cache --proxy cn --load
#
# 参数（位置形式优先，flags 次之）：
#   $1                 镜像名称，如 exam-system
#   $2                 镜像标签，如 latest
#   $3                 构建平台，如 linux/amd64 或 linux/arm64
#
# 参数（flags）：
#   --tag, -t          目标镜像标签（完整，如 exam-system:latest）
#   --platform         构建平台（如 linux/amd64, linux/arm64，逗号分隔可多平台）
#   --file, -f         Dockerfile 路径，默认：./benzhi.Dockerfile
#   --proxy, -p        使用国内加速：cn 或默认 direct
#   --no-cache         强制不使用构建缓存
#   --load             buildx build 完成后 load 到本地 docker images（单平台默认启用）
#   --push             构建完成后 push 镜像（需 docker login）
#   --save             构建完成后导出为 firmware-upgrade-benzhi.tar.gz
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
TAG="firmware-upgrade-benzhi:$(date +%Y%m%d-%H%M%S)"
PLATFORM=""
DOCKERFILE="./benzhi.Dockerfile"
PROXY="direct"
NO_CACHE=0
LOAD=""
PUSH=0
SAVE=0

# ---- 解析位置参数（形如：name tag platform）---------------------------------
POSITIONAL=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    -t|--tag)        TAG="$2";              shift 2 ;;
    --platform)      PLATFORM="$2";         shift 2 ;;
    -f|--file)       DOCKERFILE="$2";       shift 2 ;;
    -p|--proxy)      PROXY="$2";            shift 2 ;;
    --no-cache)      NO_CACHE=1;            shift ;;
    --load)          LOAD=1;                shift ;;
    --push)          PUSH=1;                shift ;;
    --save)          SAVE=1;                shift ;;
    -h|--help)
      sed -n '2,40p' "$0"; exit 0 ;;
    --)
      shift ;;
    -*)
      echo "未知参数: $1" >&2; exit 2 ;;
    *)
      POSITIONAL+=( "$1" ); shift ;;
  esac
done

# 处理位置参数：./build_benzhi_docker.sh exam-system latest linux/amd64
if [[ ${#POSITIONAL[@]} -ge 2 ]]; then
  IMAGE_NAME="${POSITIONAL[0]}"
  IMAGE_TAG="${POSITIONAL[1]}"
  TAG="${IMAGE_NAME}:${IMAGE_TAG}"
fi
if [[ ${#POSITIONAL[@]} -ge 3 ]]; then
  PLATFORM="${POSITIONAL[2]}"
fi

# 若指定了平台且没有显式 --push，则默认 --load 让镜像可在本地 docker images 中被使用
# 但多平台（含逗号）只能配合 --push 或导出，不能 --load 到本地 daemon
if [[ -z "${LOAD}" && -n "${PLATFORM}" && "${PUSH}" -eq 0 ]]; then
  if [[ "${PLATFORM}" == *,* ]]; then
    LOAD=0
  else
    LOAD=1
  fi
fi
if [[ -z "${LOAD}" ]]; then
  LOAD=0
fi

echo "[build_benzhi] TAG         = ${TAG}"
echo "[build_benzhi] PLATFORM    = ${PLATFORM:-(default)}"
echo "[build_benzhi] DOCKERFILE  = ${DOCKERFILE}"
echo "[build_benzhi] PROXY       = ${PROXY}"
echo "[build_benzhi] NO_CACHE    = ${NO_CACHE}"
echo "[build_benzhi] LOAD/PUSH/SAVE = ${LOAD}/${PUSH}/${SAVE}"

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

# 确保有 buildx builder 可用（使用当前默认 builder，继承 docker daemon 的 registry-mirror 配置）
if [[ -n "${PLATFORM}" ]]; then
  set +e
  docker buildx inspect --bootstrap >/dev/null 2>&1 || true
  set -e
fi

# ---- 根据 PROXY 设置 build-arg -----------------------------------------------
if [[ "${PROXY}" == "cn" ]]; then
  BUILD_PROXY_ARGS=(
    --build-arg GOPROXY=https://goproxy.cn,direct
    --build-arg GOSUMDB=sum.golang.google.cn
  )
else
  BUILD_PROXY_ARGS=()
fi

BUILDX_ARGS=()
if [[ "${NO_CACHE}" -eq 1 ]]; then
  BUILDX_ARGS+=( --no-cache )
fi
if [[ "${LOAD}" -eq 1 ]]; then
  BUILDX_ARGS+=( --load )
fi
if [[ "${PUSH}" -eq 1 ]]; then
  BUILDX_ARGS+=( --push )
fi
if [[ -n "${PLATFORM}" ]]; then
  BUILDX_ARGS+=( --platform "${PLATFORM}" )
fi

# ---- 构建 --------------------------------------------------------------------
echo "[build_benzhi] 开始构建镜像..."

docker buildx build "${BUILDX_ARGS[@]}" "${BUILD_PROXY_ARGS[@]}" \
  -t "${TAG}" \
  -f "${DOCKERFILE}" \
  .

echo "[build_benzhi] 构建完成：${TAG}"

# ---- 校验镜像 ----------------------------------------------------------------
if command -v docker >/dev/null && [[ "${LOAD}" -eq 1 || "${PUSH}" -ne 1 ]]; then
  set +e
  INSPECT_ID=$(docker inspect -f '{{.Id}}' "${TAG}" 2>/dev/null || true)
  set -e
  if [[ -n "${INSPECT_ID}" ]]; then
    SIZE=$(docker inspect -f '{{.Size}}' "${TAG}" 2>/dev/null | awk '{printf "%.1f MiB", $1/1024/1024}')
    ARCH=$(docker inspect -f '{{.Architecture}}' "${TAG}" 2>/dev/null || true)
    echo "[build_benzhi] 镜像 ID    = ${INSPECT_ID}"
    echo "[build_benzhi] 镜像架构  = ${ARCH}"
    echo "[build_benzhi] 镜像大小  = ${SIZE}"
  fi
fi

# ---- 可选：save 为 tar.gz ---------------------------------------------------
if [[ "${SAVE}" -eq 1 ]]; then
  TAR="${SCRIPT_DIR}/firmware-upgrade-benzhi.tar.gz"
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

  # 后台运行 + 挂载数据目录
  mkdir -p ./data && \
  docker run -d --name fu-benzhi \
    -p 8080:8080 \
    -v "\$PWD/data":/app/data \
    -e SEED_DATA=1 \
    -e LOG_LEVEL=debug \
    --health-cmd="curl -fsS http://127.0.0.1:8080/health || exit 1" \
    ${TAG}

  # 健康检查
  curl http://127.0.0.1:8080/health
  curl http://127.0.0.1:8080/ready
  curl http://127.0.0.1:8080/api/v1/stats/overview

EOF

echo "[build_benzhi] 完成 ✓"
