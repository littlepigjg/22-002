#!/usr/bin/env bash
#
# build_benzhi_docker.sh —— 为本项目构建评测专用镜像。
#
# 用法：
#   ./build_benzhi_docker.sh [tag_name] [tag_version] [platform]
#   ./build_benzhi_docker.sh                    # 默认参数
#   ./build_benzhi_docker.sh exam-system latest linux/amd64
#   ./build_benzhi_docker.sh --tag my-repo/fu-benzhi:latest
#   ./build_benzhi_docker.sh --no-cache --proxy cn --load
#
# 参数（位置参数）：
#   tag_name       镜像名称，如 exam-system
#   tag_version    镜像版本，如 latest
#   platform       目标平台，如 linux/amd64 或 linux/arm64
#
# 参数（命名参数）：
#   --tag, -t        目标镜像标签，默认：firmware-upgrade-benzhi:$(date +%Y%m%d-%H%M)
#   --file, -f       Dockerfile 路径，默认：./benzhi.Dockerfile
#   --proxy, -p      使用国内加速：cn 或默认 direct
#   --platform       目标平台（用于 buildx），如 linux/amd64
#   --no-cache       强制不使用构建缓存
#   --load           buildx build 完成后 load 到本地 docker images
#   --push           构建完成后 push 镜像（需 docker login）
#   --save           构建完成后导出为 firmware-upgrade-benzhi.tar.gz
#
# 产物：
#   - docker 镜像（本地）
#   - 可选 firmware-upgrade-benzhi.tar.gz（当前目录）

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${SCRIPT_DIR}"

# ---- 默认参数 -----------------------------------------------------------------
TAG=""
TAG_NAME=""
TAG_VERSION=""
PLATFORM=""
DOCKERFILE="./benzhi.Dockerfile"
PROXY="direct"
NO_CACHE=0
LOAD=0
PUSH=0
SAVE=0

# ---- 解析参数（支持位置参数和命名参数） -------------------------------------------
POSITIONAL_ARGS=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    -t|--tag)        TAG="$2";   shift 2 ;;
    -f|--file)       DOCKERFILE="$2"; shift 2 ;;
    -p|--proxy)      PROXY="$2"; shift 2 ;;
    --platform)      PLATFORM="$2"; shift 2 ;;
    --no-cache)      NO_CACHE=1; shift ;;
    --load)          LOAD=1;     shift ;;
    --push)          PUSH=1;     shift ;;
    --save)          SAVE=1;     shift ;;
    -h|--help)
      sed -n '2,35p' "$0"; exit 0 ;;
    -*)
      echo "未知参数: $1" >&2; exit 2 ;;
    *)
      POSITIONAL_ARGS+=("$1"); shift ;;
  esac
done

# 处理位置参数（镜像名、版本、平台）
if [[ ${#POSITIONAL_ARGS[@]} -ge 1 ]]; then
  TAG_NAME="${POSITIONAL_ARGS[0]}"
fi
if [[ ${#POSITIONAL_ARGS[@]} -ge 2 ]]; then
  TAG_VERSION="${POSITIONAL_ARGS[1]}"
fi
if [[ ${#POSITIONAL_ARGS[@]} -ge 3 ]]; then
  PLATFORM="${POSITIONAL_ARGS[2]}"
fi

# 如果没有显式设置 TAG，则根据位置参数或默认值生成
if [[ -z "${TAG}" ]]; then
  if [[ -n "${TAG_NAME}" && -n "${TAG_VERSION}" ]]; then
    TAG="${TAG_NAME}:${TAG_VERSION}"
  elif [[ -n "${TAG_NAME}" ]]; then
    TAG="${TAG_NAME}:latest"
  else
    TAG="firmware-upgrade-benzhi:$(date +%Y%m%d-%H%M%S)"
  fi
fi

# 自动开启 --load 以便能在本机使用镜像
if [[ "${LOAD}" -eq 0 && "${PUSH}" -eq 0 ]]; then
  LOAD=1
fi

echo "[build_benzhi] TAG         = ${TAG}"
echo "[build_benzhi] PLATFORM    = ${PLATFORM:-auto}"
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

# ---- 根据 PROXY 设置 build-arg -----------------------------------------------
if [[ "${PROXY}" == "cn" ]]; then
  BUILD_PROXY_ARGS=(
    --build-arg GOPROXY=https://goproxy.cn,direct
    --build-arg GOSUMDB=sum.golang.google.cn
  )
else
  BUILD_PROXY_ARGS=()
fi

# ---- 根据 PLATFORM 设置 build-arg --------------------------------------------
BUILD_PLATFORM_ARGS=()
if [[ -n "${PLATFORM}" ]]; then
  BUILD_PLATFORM_ARGS+=( --platform "${PLATFORM}" )
  # 从 linux/amd64 提取 GOARCH
  GOARCH_VALUE="${PLATFORM#linux/}"
  BUILD_PLATFORM_ARGS+=( --build-arg GOARCH="${GOARCH_VALUE}" )
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

# ---- 构建 --------------------------------------------------------------------
echo "[build_benzhi] 开始构建镜像..."
echo "[build_benzhi] buildx args: ${BUILDX_ARGS[*]:-none}"
echo "[build_benzhi] platform args: ${BUILD_PLATFORM_ARGS[*]:-none}"

docker buildx build "${BUILDX_ARGS[@]}" "${BUILD_PLATFORM_ARGS[@]}" "${BUILD_PROXY_ARGS[@]}" \
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
    echo "[build_benzhi] 镜像 ID    = ${INSPECT_ID}"
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
    --health-cmd="curl -fsS http://127.0.0.1:8080/health/live || exit 1" \
    ${TAG}

  # 健康检查
  curl http://127.0.0.1:8080/health/live
  curl http://127.0.0.1:8080/health/ready
  curl http://127.0.0.1:8080/api/v1/stats/overview

EOF

echo "[build_benzhi] 完成 ✓"
