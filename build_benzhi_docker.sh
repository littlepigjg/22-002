#!/usr/bin/env bash
#
# build_benzhi_docker.sh —— 为本项目构建评测专用镜像（支持多架构）。
#
# 用法 A（位置参数，与提示词 3 对齐）：
#   ./build_benzhi_docker.sh <IMAGE_NAME> <TAG> <PLATFORM>
#   ./build_benzhi_docker.sh exam-system latest linux/amd64
#   ./build_benzhi_docker.sh exam-system latest linux/arm64
#
# 用法 B（命名参数）：
#   ./build_benzhi_docker.sh --tag exam-system:latest --platform linux/amd64 --proxy cn
#
# 参数：
#   <IMAGE_NAME>    位置 #1：镜像仓库名（如 exam-system）
#   <TAG>           位置 #2：镜像标签（如 latest）
#   <PLATFORM>      位置 #3：目标平台，如 linux/amd64、linux/arm64
#   -t|--tag        完整镜像标签（如 exam-system:latest）
#   -f|--file       Dockerfile 路径，默认 ./benzhi.Dockerfile
#   -p|--proxy      使用国内加速：cn 或默认 direct
#   --platform      目标平台（等价于位置 #3）
#   --builder       指定 buildx builder，默认自动选择支持多架构的 builder
#   --no-cache      强制不使用构建缓存
#   --load          buildx 构建完成后 load 到本地 docker images（默认开启，除非指定 --push）
#   --push          构建完成后 push 镜像（需 docker login）
#   --save          构建完成后导出为 .tar.gz
#
# 产物：
#   - docker 镜像（本地）
#   - 可选 tar.gz（当前目录）

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${SCRIPT_DIR}"

# ---- 默认参数 -----------------------------------------------------------------
NAME_ARG=""
TAG_ARG=""
PLATFORM_ARG=""
DOCKERFILE="./benzhi.Dockerfile"
PROXY="direct"
NO_CACHE=0
LOAD=0
PUSH=0
SAVE=0
BUILDER=""

# ---- 解析位置参数：NAME TAG PLATFORM -----------------------------------------
if [[ $# -ge 1 ]] && [[ "$1" != -* ]]; then
  NAME_ARG="$1"
  shift
fi
if [[ $# -ge 1 ]] && [[ "$1" != -* ]]; then
  TAG_ARG="$1"
  shift
fi
if [[ $# -ge 1 ]] && [[ "$1" != -* ]]; then
  PLATFORM_ARG="$1"
  shift
fi

# 组合完整 TAG
if [[ -n "${NAME_ARG}" ]] && [[ -n "${TAG_ARG}" ]]; then
  TAG="${NAME_ARG}:${TAG_ARG}"
elif [[ -n "${NAME_ARG}" ]]; then
  TAG="${NAME_ARG}:latest"
else
  TAG="firmware-upgrade-benzhi:$(date +%Y%m%d-%H%M%S)"
fi

# ---- 解析命名参数（可覆盖位置参数）------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    -t|--tag)        TAG="$2";   shift 2 ;;
    -f|--file)       DOCKERFILE="$2"; shift 2 ;;
    -p|--proxy)      PROXY="$2"; shift 2 ;;
    --platform)      PLATFORM_ARG="$2"; shift 2 ;;
    --builder)       BUILDER="$2"; shift 2 ;;
    --no-cache)      NO_CACHE=1; shift ;;
    --load)          LOAD=1;     shift ;;
    --push)          PUSH=1;     shift ;;
    --save)          SAVE=1;     shift ;;
    -h|--help)
      sed -n '2,36p' "$0"; exit 0 ;;
    *)
      echo "未知参数: $1" >&2; exit 2 ;;
  esac
done

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

# ---- 自动选择 buildx builder ------------------------------------------------
# 优先使用 default（docker driver，直接复用宿主机 daemon 的镜像缓存，无需额外拉 buildkit）
if [[ -z "${BUILDER}" ]]; then
  BUILDER="default"
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

# ---- 组装 buildx 参数 --------------------------------------------------------
BUILDX_ARGS=( --builder "${BUILDER}" )
if [[ "${NO_CACHE}" -eq 1 ]]; then
  BUILDX_ARGS+=( --no-cache )
fi
if [[ -n "${PLATFORM_ARG}" ]]; then
  BUILDX_ARGS+=( --platform "${PLATFORM_ARG}" )
fi
if [[ "${PUSH}" -eq 1 ]]; then
  BUILDX_ARGS+=( --push )
else
  # 单平台场景：默认 --load 到本地，便于 docker run
  if [[ -z "${PLATFORM_ARG}" ]] || [[ "${PLATFORM_ARG}" != *,* ]]; then
    BUILDX_ARGS+=( --load )
    LOAD=1
  fi
fi

echo "[build_benzhi] TAG         = ${TAG}"
echo "[build_benzhi] DOCKERFILE  = ${DOCKERFILE}"
echo "[build_benzhi] PROXY       = ${PROXY}"
echo "[build_benzhi] PLATFORM    = ${PLATFORM_ARG:-auto/native}"
echo "[build_benzhi] BUILDER     = ${BUILDER}"
echo "[build_benzhi] NO_CACHE    = ${NO_CACHE}"
echo "[build_benzhi] LOAD/PUSH/SAVE = ${LOAD}/${PUSH}/${SAVE}"

# ---- 构建 --------------------------------------------------------------------
echo "[build_benzhi] 开始构建镜像..."
echo "[build_benzhi] cmd: docker buildx build ${BUILDX_ARGS[*]} ${BUILD_PROXY_ARGS[*]:-} -t ${TAG} -f ${DOCKERFILE} ."

docker buildx build "${BUILDX_ARGS[@]}" "${BUILD_PROXY_ARGS[@]}" \
  -t "${TAG}" \
  -f "${DOCKERFILE}" \
  .

echo "[build_benzhi] 构建完成：${TAG}"

# ---- 校验镜像 ----------------------------------------------------------------
if command -v docker >/dev/null && [[ "${LOAD}" -eq 1 ]]; then
  set +e
  INSPECT_ID=$(docker inspect -f '{{.Id}}' "${TAG}" 2>/dev/null || true)
  set -e
  if [[ -n "${INSPECT_ID}" ]]; then
    SIZE=$(docker inspect -f '{{.Size}}' "${TAG}" 2>/dev/null | awk '{printf "%.1f MiB", $1/1024/1024}')
    ARCH=$(docker inspect -f '{{.Architecture}}' "${TAG}" 2>/dev/null || echo "?")
    OS=$(docker inspect -f '{{.Os}}' "${TAG}" 2>/dev/null || echo "?")
    echo "[build_benzhi] 镜像 ID     = ${INSPECT_ID}"
    echo "[build_benzhi] 镜像大小    = ${SIZE}"
    echo "[build_benzhi] 镜像 OS/Arch = ${OS}/${ARCH}"
  fi
fi

# ---- 可选：save 为 tar.gz ----------------------------------------------------
if [[ "${SAVE}" -eq 1 ]]; then
  TAR="${SCRIPT_DIR}/$(echo "${TAG}" | tr '/:' '__').tar.gz"
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

  # 后台运行 + 挂载源码（用于容器内 go build/go vet/go test）
  docker rm -f test-verify 2>/dev/null || true
  docker run -d --name test-verify \\
    -p 8080:8080 \\
    -v "\$PWD":/app \\
    -e SEED_DATA=1 -e LOG_LEVEL=debug \\
    ${TAG}

  # 容器内验证编译与静态检查
  docker exec test-verify /bin/bash -c "cd /app && go build ./... && echo BUILD OK && go vet ./... && echo VET OK"

  # 健康检查
  curl http://127.0.0.1:8080/health
  curl http://127.0.0.1:8080/ready
  curl http://127.0.0.1:8080/api/v1/stats/overview

EOF

echo "[build_benzhi] 完成 ✓"
