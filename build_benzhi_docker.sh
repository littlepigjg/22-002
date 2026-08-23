#!/usr/bin/env bash
#
# build_benzhi_docker.sh —— 为本项目构建评测专用镜像。
#
# 用法：
#   ./build_benzhi_docker.sh                                          # 使用默认值
#   ./build_benzhi_docker.sh exam-system latest linux/amd64            # 按位置参数：APP_NAME TAG PLATFORM
#   ./build_benzhi_docker.sh exam-system latest linux/arm64
#   ./build_benzhi_docker.sh --tag my-repo/fu-benzhi:latest
#   ./build_benzhi_docker.sh --no-cache --proxy cn --load
#
# 位置参数（按顺序）：
#   APP_NAME        应用名称（如 exam-system），用于拼接默认 tag
#   TAG             镜像标签（如 latest）
#   PLATFORM        目标平台（如 linux/amd64, linux/arm64），指定后自动 --load
#
# 可选参数：
#   --tag, -t        目标镜像标签（覆盖位置参数 TAG）
#   --file, -f       Dockerfile 路径，默认：./benzhi.Dockerfile
#   --proxy, -p      使用国内加速：cn 或默认 direct
#   --no-cache       强制不使用构建缓存
#   --load           buildx build 完成后 load 到本地 docker images
#   --push           构建完成后 push 镜像（需 docker login）
#   --save           构建完成后导出为 tar.gz
#   --builder        指定 buildx builder 名称
#
# 产物：
#   - docker 镜像（本地）
#   - 可选 firmware-upgrade-benzhi.tar.gz（当前目录）

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${SCRIPT_DIR}"

# ---- 默认参数 -----------------------------------------------------------------
APP_NAME=""
TAG=""
PLATFORM=""
DOCKERFILE="./benzhi.Dockerfile"
PROXY="direct"
NO_CACHE=0
LOAD=0
PUSH=0
SAVE=0
BUILDER=""

# ---- 位置参数 -----------------------------------------------------------------
# 先收集所有非 flag 的位置参数
POSITIONAL=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    -t|--tag|-f|--file|-p|--proxy|--builder|--save)
      POSITIONAL+=("$1" "$2")
      shift 2
      ;;
    --no-cache|--load|--push|--save)
      POSITIONAL+=("$1")
      shift
      ;;
    -h|--help)
      sed -n '2,45p' "$0"; exit 0
      ;;
    -*)
      echo "未知参数: $1" >&2; exit 2
      ;;
    *)
      POSITIONAL+=("$1")
      shift
      ;;
  esac
done

# 现在将 POSITIONAL 拆分回参数
set -- "${POSITIONAL[@]}"

# 先处理位置参数（前 3 个非 flag 参数依次为 APP_NAME, TAG, PLATFORM）
POSITIONAL_INDEX=0
TEMP_ARGS=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    -t|--tag)
      TAG="$2"; shift 2 ;;
    -f|--file)
      DOCKERFILE="$2"; shift 2 ;;
    -p|--proxy)
      PROXY="$2"; shift 2 ;;
    --builder)
      BUILDER="$2"; shift 2 ;;
    --no-cache)
      NO_CACHE=1; shift ;;
    --load)
      LOAD=1; shift ;;
    --push)
      PUSH=1; shift ;;
    --save)
      SAVE=1; shift ;;
    -*)
      echo "未知参数: $1" >&2; exit 2 ;;
    *)
      # 位置参数
      case $POSITIONAL_INDEX in
        0) APP_NAME="$1" ;;
        1) TAG="$1" ;;
        2) PLATFORM="$1" ;;
        *) TEMP_ARGS+=("$1") ;;
      esac
      POSITIONAL_INDEX=$((POSITIONAL_INDEX + 1))
      shift ;;
  esac
done

# ---- 处理默认值 ----------------------------------------------------------------
if [[ -z "${TAG}" ]]; then
  if [[ -n "${APP_NAME}" ]]; then
    TAG="${APP_NAME}:$(date +%Y%m%d-%H%M%S)"
  else
    TAG="firmware-upgrade-benzhi:$(date +%Y%m%d-%H%M%S)"
  fi
fi

# 如果 APP_NAME 已提供但 TAG 未显式包含 APP_NAME，则拼接
if [[ -n "${APP_NAME}" && "${TAG}" != *"${APP_NAME}"* ]]; then
  # TAG 仅为 "latest" 这类短标签时，拼接 APP_NAME
  if [[ "${TAG}" != *":"* ]]; then
    TAG="${APP_NAME}:${TAG}"
  fi
fi

# 指定 PLATFORM 时默认启用 --load
if [[ -n "${PLATFORM}" && "${LOAD}" -eq 0 && "${PUSH}" -eq 0 ]]; then
  LOAD=1
fi

echo "[build_benzhi] APP_NAME   = ${APP_NAME:-'(默认)'}"
echo "[build_benzhi] TAG        = ${TAG}"
echo "[build_benzhi] PLATFORM   = ${PLATFORM:-'(默认)'}"
echo "[build_benzhi] DOCKERFILE = ${DOCKERFILE}"
echo "[build_benzhi] PROXY      = ${PROXY}"
echo "[build_benzhi] NO_CACHE   = ${NO_CACHE}"
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

# ---- 根据 PLATFORM 提取 TARGETARCH --------------------------------------------
BUILD_PLATFORM_ARGS=()
BUILD_ARCH_ARGS=()
if [[ -n "${PLATFORM}" ]]; then
  BUILD_PLATFORM_ARGS=( --platform "${PLATFORM}" )
  # 从 linux/amd64 中提取 amd64
  ARCH="${PLATFORM#linux/}"
  BUILD_ARCH_ARGS=( --build-arg TARGETARCH="${ARCH}" )
fi

BUILDX_ARGS=()
if [[ -n "${BUILDER}" ]]; then
  BUILDX_ARGS+=( --builder "${BUILDER}" )
fi
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

docker buildx build "${BUILDX_ARGS[@]}" "${BUILD_PLATFORM_ARGS[@]}" "${BUILD_ARCH_ARGS[@]}" "${BUILD_PROXY_ARGS[@]}" \
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
