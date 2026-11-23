#!/usr/bin/env bash
# Build the Windows edge-controller one-click installer (self-extracting exe).
#
# Usage: bash scripts/build-windows-setup.sh <VERSION> [DIST_DIR]
#
# Inputs : dist/mindfs_<VERSION>_windows_amd64/  (from scripts/build-all.sh)
# Outputs: dist/mindfs_<VERSION>_windows_amd64-setup.exe
#
# The exe is [stub][payload zip][length+magic] (see internal/deploy/setup.go).
# The stub is cross-compiled here; the payload is assembled by
# scripts/windows-setup (a Go generator, see its header for why not bash).
set -euo pipefail

VERSION="${1:-}"
DIST_DIR="${2:-dist}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

if [[ -z "${VERSION}" ]]; then
  echo "usage: bash scripts/build-windows-setup.sh <VERSION> [DIST_DIR]" >&2
  exit 1
fi

PKG_DIR="${DIST_DIR}/mindfs_${VERSION}_windows_amd64"
if [[ ! -d "${PKG_DIR}" ]]; then
  echo "missing ${PKG_DIR}: run 'bash scripts/build-all.sh ${VERSION}' first" >&2
  exit 1
fi
# The installer would otherwise embed a payload that cannot open a page -- the
# binary works, GET / is a 404, and nothing in the log says why. Fail here.
for f in mindfs.exe agents.json web/index.html web/favicon.svg; do
  if [[ ! -f "${PKG_DIR}/${f}" ]]; then
    echo "missing ${PKG_DIR}/${f}: the Windows package is incomplete" >&2
    exit 1
  fi
done

STUB="${DIST_DIR}/mindfs-setup-stub-windows-amd64.exe"
echo "==> Building setup stub -> ${STUB}"
# windowsgui: double-click must not flash a console window. Errors surface
# through a MessageBoxW dialog (server/cmd/mindfs-setup-stub/main.go).
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
  go build -trimpath -ldflags "-s -w -H=windowsgui" \
  -o "${STUB}" ./server/cmd/mindfs-setup-stub

echo "==> Assembling self-extracting installer"
go run ./scripts/windows-setup -version "${VERSION}" -dist "${DIST_DIR}" -stub "${STUB}"

OUT="${DIST_DIR}/mindfs_${VERSION}_windows_amd64-setup.exe"
if [[ ! -f "${OUT}" ]]; then
  echo "installer was not produced: ${OUT}" >&2
  exit 1
fi
ls -l "${OUT}"
