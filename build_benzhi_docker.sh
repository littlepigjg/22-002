#!/usr/bin/env bash
#
# build_benzhi_docker.sh —— 为本项目构建评测专用镜像（支持多架构 buildx）。
#
# 用法（两种方式均可）：
#
#   ① 位置参数（与 提示词3 完全一致）：
#       ./build_benzhi_docker.sh <image_name> <tag> <platform>
#       示例：
#         ./build_benzhi_docker.sh exam-system latest linux/amd64
#         ./build_benzhi_docker.sh exam-system latest linux/arm64
#
#   ② 命名参数（旧版兼容，扩展能力更强）：
#       ./build_benzhi_docker.sh -t exam-system:latest --platform linux/amd64 [--proxy cn] [--load]
#
# 平台：
#   linux/amd64  或  linux/arm64   （目前仅要求这两个）
#
# 前置：
#   - docker buildx 可用；若缺少先尝试自动安装
#   - 构建 arm64 时需要 QEMU binfmt；脚本会尝试自动注册（需 root）
#
# 退出码：
#   0  成功
#   其他：见脚本内 exit N 处注释

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${SCRIPT_DIR}"

# ---------------- 默认参数 ---------------------------------------------------
IMAGE_NAME=""
TAG=""
PLATFORM=""
DOCKERFILE="./benzhi.Dockerfile"
PROXY="direct"          # direct | cn
NO_CACHE=0
LOAD=1                  # 默认 --load 到本地 docker images（单架构时生效）
PUSH=0
SAVE=0
OVERWRITE_TAG=""        # 命名参数 -t 传入的完整 tag（name:tag）
EXTRA_BUILD_ARGS=()

# ---------------- 解析参数 ---------------------------------------------------
# 先识别：位置参数模式（前三个参数都不以 '-' 开头，视为 <image_name> <tag> <platform>）
# 允许后续再紧跟其他命名参数，例如：
#   ./build_benzhi_docker.sh exam-system latest linux/amd64 --proxy cn --no-cache
if [[ $# -ge 3 && "${1:-}" != -* && "${2:-}" != -* && "${3:-}" != -* ]]; then
  IMAGE_NAME="$1"
  TAG="$2"
  PLATFORM="$3"
  shift 3
fi

# 再解析命名参数（可以与位置参数并存，后者覆盖前者）
while [[ $# -gt 0 ]]; do
  case "$1" in
    -t|--tag)
      OVERWRITE_TAG="$2"; shift 2 ;;
    --name)
      IMAGE_NAME="$2"; shift 2 ;;
    --tag-only)
      TAG="$2"; shift 2 ;;
    -f|--file)
      DOCKERFILE="$2"; shift 2 ;;
    -p|--proxy)
      PROXY="$2"; shift 2 ;;
    --platform)
      PLATFORM="$2"; shift 2 ;;
    --no-cache)
      NO_CACHE=1; shift ;;
    --load)
      LOAD=1; shift ;;
    --no-load)
      LOAD=0; shift ;;
    --push)
      PUSH=1; shift ;;
    --save)
      SAVE=1; shift ;;
    --build-arg)
      EXTRA_BUILD_ARGS+=( --build-arg "$2" ); shift 2 ;;
    -h|--help)
      sed -n '2,40p' "$0"; exit 0 ;;
    *)
      echo "[build_benzhi] 未知参数: $1" >&2
      echo "用法: $0 <image_name> <tag> <platform> | -t name:tag --platform linux/amd64|arm64" >&2
      exit 2 ;;
  esac
done

# 组装最终 tag
if [[ -n "${OVERWRITE_TAG}" ]]; then
  FINAL_TAG="${OVERWRITE_TAG}"
elif [[ -n "${IMAGE_NAME}" && -n "${TAG}" ]]; then
  FINAL_TAG="${IMAGE_NAME}:${TAG}"
else
  FINAL_TAG="firmware-upgrade-benzhi:$(date +%Y%m%d-%H%M%S)"
fi

# 平台默认：宿主机当前架构
if [[ -z "${PLATFORM}" ]]; then
  case "$(uname -m)" in
    x86_64) PLATFORM="linux/amd64" ;;
    aarch64|arm64) PLATFORM="linux/arm64" ;;
    *)
      echo "[build_benzhi] 无法识别宿主机架构 $(uname -m)，请显式通过位置参数或 --platform 指定" >&2
      exit 9
      ;;
  esac
fi

case "${PLATFORM}" in
  linux/amd64|linux/arm64) ;;
  *)
    echo "[build_benzhi] 不支持的平台: ${PLATFORM}（仅允许 linux/amd64 或 linux/arm64）" >&2
    exit 10
    ;;
esac

echo "[build_benzhi] =============================="
echo "[build_benzhi] TAG        = ${FINAL_TAG}"
echo "[build_benzhi] PLATFORM   = ${PLATFORM}"
echo "[build_benzhi] DOCKERFILE = ${DOCKERFILE}"
echo "[build_benzhi] PROXY      = ${PROXY}"
echo "[build_benzhi] NO_CACHE   = ${NO_CACHE}"
echo "[build_benzhi] LOAD/PUSH  = ${LOAD}/${PUSH}"
echo "[build_benzhi] =============================="

# ---------------- 前置依赖检查 ----------------------------------------------
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

# -------------- 确保 buildx 可用 --------------------------------------------
if ! docker buildx version >/dev/null 2>&1; then
  echo "[build_benzhi] 未找到 docker buildx，尝试启用 buildx 插件..."
  # 新版 docker 自带 buildx；旧版需安装
  if docker buildx install >/dev/null 2>&1; then
    echo "[build_benzhi] buildx 已启用"
  else
    echo "[build_benzhi] 无法启用 buildx，手动安装: https://docs.docker.com/buildx/working-with-buildx/" >&2
    exit 7
  fi
fi

# -------------- 确保有可用的 buildx builder --------------------------------
# 策略：
#   A) 目标平台 == 宿主机原生架构  → 使用 "default" builder（docker 驱动），
#      共享 docker daemon 的 registry mirror 与本地 image cache（避免 docker.io 超时）。
#   B) 跨架构（如 x86 宿主机构建 arm64）→ 创建/使用 docker-container builder，
#      注入 buildkitd 镜像源配置，并注册 QEMU binfmt。

HOST_ARCH=""
case "$(uname -m)" in
  x86_64)   HOST_ARCH="linux/amd64" ;;
  aarch64|arm64) HOST_ARCH="linux/arm64" ;;
  *) HOST_ARCH="unknown" ;;
esac

NATIVE_BUILD=0
if [[ "${HOST_ARCH}" == "${PLATFORM}" ]]; then
  NATIVE_BUILD=1
fi

# ---- A) 原生架构：切回 default builder ------------------------------------
if [[ "${NATIVE_BUILD}" -eq 1 ]]; then
  echo "[build_benzhi] 原生架构构建（${HOST_ARCH}），使用 default builder（共享 daemon 镜像源/缓存）"
  set +e
  docker buildx use default 2>/dev/null
  set -e
  # 确保 default builder 已初始化
  docker buildx inspect --bootstrap >/dev/null 2>&1 || true

# ---- B) 跨架构：创建 docker-container builder + QEMU + 镜像源注入 --------
else
  echo "[build_benzhi] 跨架构构建（host=${HOST_ARCH} target=${PLATFORM}）"

  # 注册 QEMU binfmt
  echo "[build_benzhi] 注册 QEMU binfmt 以支持跨架构构建..."
  set +e
  docker run --privileged --rm tonistiigi/binfmt --install all >/dev/null 2>&1
  BINFMT_RC=$?
  set -e
  if [[ "${BINFMT_RC}" -ne 0 ]]; then
    echo "[build_benzhi] 警告: QEMU binfmt 注册失败，跨架构构建可能失败" >&2
  fi

  # 从宿主机 daemon 提取 Registry Mirrors（如果有），写入 buildkitd.toml
  BUILDER_NAME="benzhi-multiarch-builder"
  BUILDKITD_CFG="/tmp/benzhi-buildkitd-$$.toml"
  cat >"${BUILDKITD_CFG}" <<'BUILDKITD_EOF'
[worker.oci]
  max-parallelism = 4
BUILDKITD_EOF

  set +e
  MIRRORS_LINE="$(docker info 2>/dev/null | sed -n '/Registry Mirrors:/,/^ [^ ]/ s/^  //p' | grep -E '^https?://' || true)"
  set -e
  if [[ -n "${MIRRORS_LINE}" ]]; then
    echo "[build_benzhi] 注入宿主机 Docker 镜像源配置到 buildkit builder:"
    while IFS= read -r MIRROR; do
      [[ -z "${MIRROR}" ]] && continue
      echo "    mirror: ${MIRROR}"
      cat >>"${BUILDKITD_CFG}" <<MIRROR_EOF

[registry."docker.io"]
  mirrors = ["${MIRROR%/}"]
MIRROR_EOF
    done <<< "${MIRRORS_LINE}"
  fi
  # 额外写入常见国内镜像源兜底
  cat >>"${BUILDKITD_CFG}" <<'FALLBACK_EOF'

[registry."docker.io"]
  mirrors = ["https://docker.m.daocloud.io", "https://docker.nju.edu.cn", "https://docker.mirrors.ustc.edu.cn"]
FALLBACK_EOF

  # （重新）创建 builder 并注入配置
  echo "[build_benzhi] 创建/重置 buildx builder: ${BUILDER_NAME}"
  set +e
  docker buildx rm "${BUILDER_NAME}" 2>/dev/null || true
  docker buildx create --name "${BUILDER_NAME}" \
    --driver docker-container \
    --config "${BUILDKITD_CFG}" \
    --use >/dev/null 2>&1
  CR_RC=$?
  set -e
  # 清理临时配置
  rm -f "${BUILDKITD_CFG}"
  if [[ "${CR_RC}" -ne 0 ]]; then
    echo "[build_benzhi] buildx create 失败，回退尝试使用已存在的 builder..."
    set +e
    docker buildx use "${BUILDER_NAME}" 2>/dev/null || docker buildx use default 2>/dev/null || true
    set -e
  fi
  echo "[build_benzhi] bootstrap builder: $(docker buildx inspect --bootstrap 2>&1 | head -n 1 || true)"
fi

# ---------------- 代理设置 ----------------------------------------------------
if [[ "${PROXY}" == "cn" ]]; then
  BUILD_PROXY_ARGS=(
    --build-arg GOPROXY=https://goproxy.cn,direct
    --build-arg GOSUMDB=sum.golang.google.cn
  )
else
  BUILD_PROXY_ARGS=()
fi

BUILDX_ARGS=( --platform "${PLATFORM}" )
if [[ "${NO_CACHE}" -eq 1 ]]; then
  BUILDX_ARGS+=( --no-cache )
fi
if [[ "${LOAD}" -eq 1 ]]; then
  # --load 只允许单平台（正好符合我们一次只构建一个平台的模式）
  BUILDX_ARGS+=( --load )
fi
if [[ "${PUSH}" -eq 1 ]]; then
  BUILDX_ARGS+=( --push )
fi

# ---------------- 执行构建 ----------------------------------------------------
echo "[build_benzhi] 开始构建（平台 ${PLATFORM}）..."

set -x
docker buildx build "${BUILDX_ARGS[@]}" "${BUILD_PROXY_ARGS[@]}" "${EXTRA_BUILD_ARGS[@]}" \
  -t "${FINAL_TAG}" \
  -f "${DOCKERFILE}" \
  .
{ set +x; } 2>/dev/null

echo "[build_benzhi] 构建完成：${FINAL_TAG} (${PLATFORM})"

# ---------------- 校验镜像 ----------------------------------------------------
if command -v docker >/dev/null && [[ "${LOAD}" -eq 1 ]]; then
  set +e
  INSPECT_ID=$(docker inspect -f '{{.Id}}' "${FINAL_TAG}" 2>/dev/null || true)
  set -e
  if [[ -n "${INSPECT_ID}" ]]; then
    SIZE=$(docker inspect -f '{{.Size}}' "${FINAL_TAG}" 2>/dev/null | awk '{printf "%.1f MiB", $1/1024/1024}')
    ARCH=$(docker inspect -f '{{.Architecture}}' "${FINAL_TAG}" 2>/dev/null || echo "unknown")
    OS=$(docker inspect -f '{{.Os}}' "${FINAL_TAG}" 2>/dev/null || echo "unknown")
    echo "[build_benzhi] 镜像 ID    = ${INSPECT_ID}"
    echo "[build_benzhi] 镜像大小  = ${SIZE}"
    echo "[build_benzhi] 镜像平台  = ${OS}/${ARCH}"
  fi
fi

# ---------------- 可选：save 为 tar.gz ---------------------------------------
if [[ "${SAVE}" -eq 1 ]]; then
  TAR="${SCRIPT_DIR}/firmware-upgrade-benzhi-$(echo "${PLATFORM}" | tr '/' '-').tar.gz"
  echo "[build_benzhi] 导出镜像到：${TAR}"
  docker save "${FINAL_TAG}" | gzip > "${TAR}"
  SIZE=$(du -h "${TAR}" | cut -f1)
  echo "[build_benzhi] 导出大小：${SIZE}"
fi

# ---------------- 运行速查 ---------------------------------------------------
cat <<EOF

=== 启动速查 ===

  # 按提示词3启动（挂载源码到 /app，后台运行，暴露 8080）
  docker rm -f test-verify 2>/dev/null || true
  docker run -d --name test-verify \\
    -p 8080:8080 \\
    -v "\$PWD":/app \\
    ${FINAL_TAG}

  # 容器内编译 & 静态检查
  docker exec test-verify /bin/bash -c "cd /app && go build ./... && echo BUILD OK && go vet ./... && echo VET OK"

  # 健康检查
  curl -s http://127.0.0.1:8080/health

  # 缺陷验证
  docker exec test-verify /bin/bash -c "cd /app && go test . -race -count=1 -run '^TestRedGreen\$'"

EOF

echo "[build_benzhi] 完成 ✓"
