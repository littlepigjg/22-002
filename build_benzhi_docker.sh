#!/usr/bin/env bash
#
# build_benzhi_docker.sh —— 为本项目构建评测专用镜像。
#
# 用法一：位置参数（与提示词3对齐）
#   ./build_benzhi_docker.sh <repo_name> <tag> <platform>
#   例：./build_benzhi_docker.sh exam-system latest linux/amd64
#   例：./build_benzhi_docker.sh exam-system latest linux/arm64
#
# 用法二：命名参数（原有兼容）
#   ./build_benzhi_docker.sh
#   ./build_benzhi_docker.sh --tag my-repo/fu-benzhi:latest
#   ./build_benzhi_docker.sh --no-cache --proxy cn --load
#
# 参数：
#   --tag, -t        目标镜像标签
#   --file, -f       Dockerfile 路径，默认：./benzhi.Dockerfile
#   --proxy, -p      使用国内加速：cn 或默认 direct
#   --no-cache       强制不使用构建缓存
#   --load           buildx build 完成后 load 到本地 docker images
#   --push           构建完成后 push 镜像（需 docker login）
#   --platform       目标平台（如 linux/amd64、linux/arm64），默认自动
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${SCRIPT_DIR}"

# ---- 处理位置参数模式（用法一） ----------------------------------------------
# 形如：./build_benzhi_docker.sh exam-system latest linux/amd64
POSITIONAL=()
if [[ $# -ge 2 ]] && [[ "$1" != --* ]] && [[ "$1" != -* ]]; then
  # 位置参数模式：$1=repo_name $2=tag [$3=platform]
  REPO_NAME="$1"
  TAG_NAME="$2"
  PLATFORM="${3:-}"
  TAG="${REPO_NAME}:${TAG_NAME}"
  shift
  shift
  if [[ -n "${PLATFORM}" ]]; then
    shift || true
  fi
  # 剩余参数交给命名参数解析覆盖
  LOAD=1
  PROXY="direct"
  NO_CACHE=0
  PUSH=0
  SAVE=0
  DOCKERFILE="./benzhi.Dockerfile"
else
  # 命名参数默认值
  TAG="firmware-upgrade-benzhi:$(date +%Y%m%d-%H%M%S)"
  DOCKERFILE="./benzhi.Dockerfile"
  PROXY="direct"
  NO_CACHE=0
  LOAD=0
  PUSH=0
  SAVE=0
  PLATFORM=""
fi

# ---- 解析命名参数 -----------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    -t|--tag)        TAG="$2";   shift 2 ;;
    -f|--file)       DOCKERFILE="$2"; shift 2 ;;
    -p|--proxy)      PROXY="$2"; shift 2 ;;
    --no-cache)      NO_CACHE=1; shift ;;
    --load)          LOAD=1;     shift ;;
    --push)          PUSH=1;     shift ;;
    --save)          SAVE=1;     shift ;;
    --platform)      PLATFORM="$2"; shift 2 ;;
    -h|--help)
      sed -n '2,40p' "$0"; exit 0 ;;
    *)
      echo "未知参数: $1" >&2; exit 2 ;;
  esac
done

echo "[build_benzhi] TAG         = ${TAG}"
echo "[build_benzhi] DOCKERFILE  = ${DOCKERFILE}"
echo "[build_benzhi] PROXY       = ${PROXY}"
echo "[build_benzhi] PLATFORM    = ${PLATFORM:-<auto>}"
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

# 确保存在 buildx builder（支持多架构）
CURRENT_BUILDER=$(docker buildx ls 2>/dev/null | grep -E '^\*|current' | head -1 | awk '{print $1}')
echo "[build_benzhi] buildx current builder: ${CURRENT_BUILDER:-<default>}"

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

# 对于单平台 --load：buildx 要求 --load 只能单平台
# 对于多平台同时构建（不 --load）：可以 --platform linux/amd64,linux/arm64
docker buildx build "${BUILDX_ARGS[@]}" "${BUILD_PROXY_ARGS[@]}" \
  -t "${TAG}" \
  -f "${DOCKERFILE}" \
  .

echo "[build_benzhi] 构建完成：${TAG}"

# ---- 校验镜像 ----------------------------------------------------------------
set +e
INSPECT_ID=$(docker inspect -f '{{.Id}}' "${TAG}" 2>/dev/null || true)
set -e
if [[ -n "${INSPECT_ID}" ]]; then
  SIZE=$(docker inspect -f '{{.Size}}' "${TAG}" 2>/dev/null | awk '{printf "%.1f MiB", $1/1024/1024}')
  ARCH=$(docker inspect -f '{{.Architecture}}' "${TAG}" 2>/dev/null || echo "N/A")
  echo "[build_benzhi] 镜像 ID    = ${INSPECT_ID}"
  echo "[build_benzhi] 镜像架构    = ${ARCH}"
  echo "[build_benzhi] 镜像大小    = ${SIZE}"
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

  # 后台运行 + 挂载源码目录（用于在容器内执行 go build/vet/test）
  mkdir -p ./data && \
  docker run -d --name test-verify \
    -p 8080:8080 \
    -v "\$PWD":/app \
    -e SEED_DATA=1 \
    -e LOG_LEVEL=info \
    ${TAG}

  # 健康检查
  curl http://127.0.0.1:8080/health
  curl http://127.0.0.1:8080/api/v1/stats/overview

  # 容器内执行编译测试
  docker exec test-verify bash -c "cd /app && go build ./... && echo BUILD OK"

EOF

echo "[build_benzhi] 完成 ✓"
