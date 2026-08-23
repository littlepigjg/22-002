#!/usr/bin/env bash
#
# build_benzhi_docker.sh —— 为本项目构建评测专用镜像。
#
# 用法（两种模式）：
#   模式 A：位置参数（与验收脚本对齐）——
#     ./build_benzhi_docker.sh <image_name> <image_tag> <platform>
#     例如：
#       ./build_benzhi_docker.sh exam-system latest linux/amd64
#       ./build_benzhi_docker.sh exam-system latest linux/arm64
#
#   模式 B：flag 参数（原有用法，向后兼容）——
#     ./build_benzhi_docker.sh
#     ./build_benzhi_docker.sh -t my-repo/fu-benzhi:latest --platform linux/amd64 --load
#     ./build_benzhi_docker.sh --no-cache --proxy cn --load
#
# flag 参数：
#   -t, --tag        目标镜像标签（默认：firmware-upgrade-benzhi:<时间戳>）
#   -f, --file       Dockerfile 路径（默认：./benzhi.Dockerfile）
#   --platform       目标平台（如 linux/amd64 | linux/arm64；默认自动选择宿主机架构）
#   -p, --proxy      使用国内加速：cn 或默认 direct
#   --no-cache       强制不使用构建缓存
#   --load           buildx build 完成后 load 到本地 docker images（默认开启，除非 --push）
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
PLATFORM=""
PROXY="direct"
NO_CACHE=0
LOAD=0
PUSH=0
SAVE=0

# ---- 支持模式 A：位置参数 <name> <tag> <platform> ----------------------------
if [[ $# -ge 3 ]] && [[ "$1" != -* ]] && [[ "$2" != -* ]] && [[ "$3" != -* ]]; then
  IMAGE_NAME="$1"
  IMAGE_TAG="$2"
  PLATFORM="$3"
  TAG="${IMAGE_NAME}:${IMAGE_TAG}"
  shift 3
  # 模式 A 下默认 load 到本地
  LOAD=1
fi

# ---- 解析 flag 参数 ----------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    -t|--tag)        TAG="$2";   shift 2 ;;
    -f|--file)       DOCKERFILE="$2"; shift 2 ;;
    --platform)      PLATFORM="$2"; shift 2 ;;
    -p|--proxy)      PROXY="$2"; shift 2 ;;
    --no-cache)      NO_CACHE=1; shift ;;
    --load)          LOAD=1;     shift ;;
    --push)          PUSH=1;     shift ;;
    --save)          SAVE=1;     shift ;;
    -h|--help)
      sed -n '2,40p' "$0"; exit 0 ;;
    *)
      echo "未知参数: $1" >&2; exit 2 ;;
  esac
done

echo "[build_benzhi] TAG         = ${TAG}"
echo "[build_benzhi] DOCKERFILE  = ${DOCKERFILE}"
echo "[build_benzhi] PLATFORM    = ${PLATFORM:-<host default>}"
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

# ---- 确保 buildx builder 支持跨架构（可选安装 QEMU） --------------------------
if ! docker buildx version >/dev/null 2>&1; then
  echo "[build_benzhi] 未检测到 buildx 插件，请先安装 docker-buildx-plugin" >&2
  exit 7
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
BUILDX_ARGS=( --progress=plain )
if [[ "${NO_CACHE}" -eq 1 ]]; then
  BUILDX_ARGS+=( --no-cache )
fi
if [[ -n "${PLATFORM}" ]]; then
  BUILDX_ARGS+=( --platform "${PLATFORM}" )
fi
if [[ "${PUSH}" -eq 1 ]]; then
  BUILDX_ARGS+=( --push )
elif [[ "${LOAD}" -eq 1 ]]; then
  # --load 与 --push 互斥；且 buildx --load 一次只能处理一个 platform
  BUILDX_ARGS+=( --load )
fi

# ---- 构建 --------------------------------------------------------------------
echo "[build_benzhi] 开始构建镜像 (docker buildx build ${BUILDX_ARGS[*]} ${BUILD_PROXY_ARGS[*]:-} -t ${TAG} -f ${DOCKERFILE} .)"

docker buildx build "${BUILDX_ARGS[@]}" ${BUILD_PROXY_ARGS[@]+"${BUILD_PROXY_ARGS[@]}"} \
  -t "${TAG}" \
  -f "${DOCKERFILE}" \
  .

echo "[build_benzhi] 构建完成：${TAG}"

# ---- 校验镜像 ----------------------------------------------------------------
if command -v docker >/dev/null 2>&1; then
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

=== 启动速查（本地运行 + 挂载源码到 /app 便于容器内 go build/vet/test）===

  # 清理旧容器
  docker rm -f test-verify 2>/dev/null || true

  # 后台运行（挂载源码，便于进入容器跑 go build/vet/test）
  docker run -d --name test-verify \\
    -p 8080:8080 \\
    -v "${SCRIPT_DIR}":/app \\
    ${TAG}

  # 健康检查
  curl http://127.0.0.1:8080/health

  # 容器内跑编译/静态检查
  docker exec test-verify /bin/bash -c "cd /app && go build ./... && echo BUILD OK && go vet ./... && echo VET OK"

  # 容器内跑缺陷验证用例
  docker exec test-verify /bin/bash -c "cd /app && go test . -count=1 -run '^TestRedGreen$'"

EOF

echo "[build_benzhi] 完成 ✓"
