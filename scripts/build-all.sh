#!/usr/bin/env bash
# Cross-compile mindfs for all target platforms.
# Usage: bash scripts/build-all.sh [VERSION] [DIST_DIR]
set -euo pipefail

VERSION="${1:-dev}"
DIST_DIR="${2:-dist}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.."; pwd)"

PLATFORMS=(
  "darwin/amd64"
  "darwin/arm64"
  "linux/amd64"
  "linux/arm64"
  "linux/arm"
  "windows/amd64"
  "windows/arm64"
)

# 前端是发布包的一部分，不是可选装饰：control 角色的页面就靠它，装完打不开首页
# 等于整台机器不可用。缺了就在打包前失败（`make build-all` 会先跑 build-web）。
if [[ ! -f "${ROOT}/web/dist/index.html" ]]; then
  echo "missing web/dist/index.html: run 'make build-web' first" >&2
  exit 1
fi

echo "==> Building mindfs ${VERSION}"
echo "    Output: ${DIST_DIR}/"
echo

LDFLAGS="-s -w -X main.version=${VERSION}"
if [[ -n "${MINDFS_RELEASE_PUBLIC_KEY:-}" ]]; then
  LDFLAGS="${LDFLAGS} -X mindfs/server/internal/update.releaseManifestPublicKey=${MINDFS_RELEASE_PUBLIC_KEY}"
fi
# 与前端 VITE_MIND_FS_BASE 保持一致的后端部署前缀
MIND_FS_BASE="${MIND_FS_BASE:-/mindfs}"
LDFLAGS="${LDFLAGS} -X mindfs/internal/deploy.Prefix=${MIND_FS_BASE}"

built_dirs=()
for PLATFORM in "${PLATFORMS[@]}"; do
  GOOS="${PLATFORM%%/*}"
  GOARCH="${PLATFORM##*/}"

  BIN_NAME="mindfs"
  [[ "$GOOS" == "windows" ]] && BIN_NAME="mindfs.exe"

  OUT_DIR="${DIST_DIR}/mindfs_${VERSION}_${GOOS}_${GOARCH}"
  rm -rf "${ROOT}/${OUT_DIR}"
  mkdir -p "${ROOT}/${OUT_DIR}"
  built_dirs+=("$(basename "${OUT_DIR}")")

  TARGET="${OUT_DIR}/${BIN_NAME}"
  printf "  %-20s -> %s\n" "${GOOS}/${GOARCH}" "${TARGET}"

  GOARM=""
  if [[ "$GOARCH" == "arm" ]]; then
    GOARM=7
  fi

  # Windows 编成 GUI 子系统：控制台子系统程序被「本来就没有控制台」的启动方拉起时
  # （任务计划、资源管理器双击、Run 键），Windows 会新建一个控制台窗口并一直显示 ——
  # 用户看到的就是那个关不掉的黑框。这个窗口在进程内部藏不掉：Windows Terminal 是默认
  # 终端时控制台被交接给它，GetConsoleWindow 拿到的是隐藏的假句柄。只有不分配控制台才
  # 彻底没有黑框；CLI 输出由启动时的 AttachConsole 找回（cli/cmd/console_windows.go）。
  PLATFORM_LDFLAGS="${LDFLAGS}"
  if [[ "$GOOS" == "windows" ]]; then
    PLATFORM_LDFLAGS="${LDFLAGS} -H=windowsgui"
  fi

  CGO_ENABLED=0 \
    GOOS="$GOOS" \
    GOARCH="$GOARCH" \
    GOARM="$GOARM" \
    go build \
      -trimpath \
      -ldflags "${PLATFORM_LDFLAGS}" \
      -o "${ROOT}/${TARGET}" \
      ./cli/cmd

  # Bundle web assets (checked above) and install scripts
  cp -r "${ROOT}/web/dist" "${ROOT}/${OUT_DIR}/web"
  # 页面的构建身份写进包里：边缘对账要知道「装进去的这一版是谁」，
  # 而不是靠文件名或首页内容去猜（T20 的版本切换判据读它）。
  printf '%s\n' "${VERSION}" > "${ROOT}/${OUT_DIR}/web/BUILD_VERSION"
  if [[ -f "${ROOT}/agents.json" ]]; then
    cp "${ROOT}/agents.json" "${ROOT}/${OUT_DIR}/agents.json"
  fi
done

# Produce archives
cd "${ROOT}/${DIST_DIR}"
export COPYFILE_DISABLE=1
for DIR in "${built_dirs[@]}"; do
  GOOS="$(echo "$DIR" | cut -d_ -f3)"
  if [[ "$GOOS" == "windows" ]]; then
    rm -f "${DIR}.zip"
    zip -Xqr "${DIR}.zip" "$DIR"
    echo "  Archived: ${DIST_DIR}/${DIR}.zip"
  else
    rm -f "${DIR}.tar.gz"
    tar --no-xattrs -czf "${DIR}.tar.gz" "$DIR"
    echo "  Archived: ${DIST_DIR}/${DIR}.tar.gz"
  fi
done

echo
echo "==> Done. Artifacts in ${DIST_DIR}/"
