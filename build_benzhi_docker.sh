#!/usr/bin/env bash
#
# build_benzhi_docker.sh —— 为本项目构建评测专用镜像。
#
# 用法一（位置参数，推荐用于评测流程）：
#   ./build_benzhi_docker.sh <image_name> <tag> <platform>
#   示例：
#     ./build_benzhi_docker.sh exam-system latest linux/amd64
#     ./build_benzhi_docker.sh exam-system latest linux/arm64
#
# 用法二（命名参数，向后兼容）：
#   ./build_benzhi_docker.sh
#   ./build_benzhi_docker.sh --tag my-repo/fu-benzhi:latest --platform linux/amd64
#   ./build_benzhi_docker.sh --no-cache --proxy cn --load
#
# 参数：
#   --tag, -t        目标镜像标签，默认：firmware-upgrade-benzhi:$(date +%Y%m%d-%H%M)
#   --file, -f       Dockerfile 路径，默认：./benzhi.Dockerfile
#   --proxy, -p      使用国内加速：cn 或默认 direct
#   --platform       目标平台：linux/amd64 | linux/arm64（可多个逗号分隔），默认：linux/amd64
#   --builder        buildx builder 名称；默认 benzhi-builder（若不存在则用 default）
#   --no-cache       强制不使用构建缓存
#   --load           buildx build 完成后 load 到本地 docker images（默认开启，可 --load=false 关闭）
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
TAG="firmware-upgrade-benzhi:$(date +%Y%m%d-%H%M%S)"
DOCKERFILE="./benzhi.Dockerfile"
PROXY="direct"
PLATFORM=""
BUILDER=""
NO_CACHE=0
LOAD=1
PUSH=0
SAVE=0

# ---- 解析参数：位置参数优先 --------------------------------------------------
# 若第一个参数不以 `-` 开头：视为 <image_name> <tag> <platform>
if [[ $# -ge 1 && "${1:-}" != -* ]]; then
  IMG_NAME="${1:-exam-system}"
  IMG_TAG="${2:-latest}"
  PLATFORM_ARG="${3:-}"
  TAG="${IMG_NAME}:${IMG_TAG}"
  if [[ -n "${PLATFORM_ARG}" ]]; then
    PLATFORM="${PLATFORM_ARG}"
  fi
  shift $(( $# > 3 ? 3 : $# ))
fi

# ---- 解析命名参数 ------------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    -t|--tag)        TAG="$2";   shift 2 ;;
    -f|--file)       DOCKERFILE="$2"; shift 2 ;;
    -p|--proxy)      PROXY="$2"; shift 2 ;;
    --platform)      PLATFORM="$2"; shift 2 ;;
    --builder)       BUILDER="$2"; shift 2 ;;
    --no-cache)      NO_CACHE=1; shift ;;
    --load)
      case "${2:-}" in
        false|0) LOAD=0; shift 2 ;;
        *)       LOAD=1; shift 1 ;;
      esac
      ;;
    --push)          PUSH=1; shift ;;
    --save)          SAVE=1; shift ;;
    -h|--help)
      sed -n '2,40p' "$0"; exit 0 ;;
    *)
      echo "未知参数: $1" >&2; exit 2 ;;
  esac
done

# 未显式指定平台时，默认按主机架构
if [[ -z "${PLATFORM}" ]]; then
  case "$(uname -m)" in
    aarch64|arm64) PLATFORM="linux/arm64" ;;
    *)             PLATFORM="linux/amd64" ;;
  esac
fi

# 选择 builder：优先指定，其次 benzhi-builder（若已建且可用），否则 default
if [[ -z "${BUILDER}" ]]; then
  if docker buildx inspect benzhi-builder >/dev/null 2>&1; then
    BUILDER="benzhi-builder"
  else
    BUILDER="default"
  fi
fi

echo "[build_benzhi] TAG         = ${TAG}"
echo "[build_benzhi] DOCKERFILE  = ${DOCKERFILE}"
echo "[build_benzhi] PROXY       = ${PROXY}"
echo "[build_benzhi] PLATFORM    = ${PLATFORM}"
echo "[build_benzhi] BUILDER     = ${BUILDER}"
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
  # 显式国内加速：强制 Go 代理走 goproxy.cn
  BUILD_PROXY_ARGS=(
    --build-arg GOPROXY=https://goproxy.cn,direct
    --build-arg GOSUMDB=sum.golang.google.cn
    --build-arg REGISTRY=docker.m.daocloud.io/library
  )
else
  # direct 模式：让 Dockerfile 内部根据 REGISTRY 默认值（docker.m.daocloud.io/library）
  # 自动推断 GOPROXY。若 REGISTRY 被用户覆盖为 docker hub 直连（REGISTRY= 空串），
  # 则 GOPROXY 回退到 proxy.golang.org；否则按国内镜像选 goproxy.cn。
  BUILD_PROXY_ARGS=(
    --build-arg REGISTRY=docker.m.daocloud.io/library
  )
fi

# ---- 组装 buildx 参数 --------------------------------------------------------
BUILDX_ARGS=( --builder "${BUILDER}" --platform "${PLATFORM}" )
if [[ "${NO_CACHE}" -eq 1 ]]; then
  BUILDX_ARGS+=( --no-cache )
fi
if [[ "${LOAD}" -eq 1 && "${PUSH}" -eq 0 ]]; then
  # 多平台时 --load 不支持；若用户显式指定了多平台（含逗号），自动禁用 load
  if [[ "${PLATFORM}" == *,* ]]; then
    echo "[build_benzhi] 多平台构建 (${PLATFORM})：自动禁用 --load（仅支持单平台 --load）"
  else
    BUILDX_ARGS+=( --load )
  fi
fi
if [[ "${PUSH}" -eq 1 ]]; then
  BUILDX_ARGS+=( --push )
fi

# ---- 构建 --------------------------------------------------------------------
echo "[build_benzhi] 开始构建镜像（platform=${PLATFORM}, builder=${BUILDER}）..."

docker buildx build "${BUILDX_ARGS[@]}" "${BUILD_PROXY_ARGS[@]}" \
  -t "${TAG}" \
  -f "${DOCKERFILE}" \
  .

echo "[build_benzhi] 构建完成：${TAG} (platform=${PLATFORM})"

# ---- 校验镜像 ----------------------------------------------------------------
if [[ "${LOAD}" -eq 1 && "${PLATFORM}" != *,* ]]; then
  set +e
  INSPECT_ID=$(docker inspect -f '{{.Id}}' "${TAG}" 2>/dev/null || true)
  set -e
  if [[ -n "${INSPECT_ID}" ]]; then
    SIZE=$(docker inspect -f '{{.Size}}' "${TAG}" 2>/dev/null | awk '{printf "%.1f MiB", $1/1024/1024}')
    ARCH=$(docker inspect -f '{{.Architecture}}' "${TAG}" 2>/dev/null || echo "?")
    echo "[build_benzhi] 镜像 ID    = ${INSPECT_ID}"
    echo "[build_benzhi] 镜像架构    = ${ARCH}"
    echo "[build_benzhi] 镜像大小    = ${SIZE}"
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

  # 挂载源码（重建 server + 方便在容器内执行 go build/vet/test）
  docker rm -f test-verify 2>/dev/null || true
  docker run -d --name test-verify -p 8080:8080 \\
    -v "${SCRIPT_DIR}:/app" ${TAG}
  # 健康检查
  curl http://127.0.0.1:8080/health
  # 容器内编译 + 缺陷验证
  docker exec test-verify /bin/bash -c "cd /app && go build ./... && go vet ./..."
  docker exec test-verify /bin/bash -c "cd /app && go test . -race -count=1 -run '^TestRedGreen\$'"

EOF

echo "[build_benzhi] 完成 ✓"
