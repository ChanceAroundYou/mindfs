#!/usr/bin/env bash
# Build the Windows edge installer (NSIS, standard wizard + uninstaller).
#
# Usage: bash scripts/build-windows-installer.sh <VERSION> [DIST_DIR]
#
# Inputs : dist/mindfs_<VERSION>_windows_amd64/  (from scripts/build-all.sh)
# Outputs: dist/mindfs_<VERSION>_windows_amd64-setup.exe
#
# Requires makensis. On Linux it is not packaged for every distro; the
# documented fallback (no root) is:
#
#   apt-get download nsis nsis-common
#   for f in nsis*.deb; do dpkg -x "$f" ~/.local/nsis; done
#   export NSISDIR=~/.local/nsis/usr/share/nsis
#   export PATH=~/.local/nsis/usr/bin:$PATH
#
# The WebView2 Evergreen Bootstrapper is embedded so the installer works
# offline; set MINDFS_WEBVIEW2_BOOTSTRAPPER to reuse a local copy, otherwise it
# is downloaded once into <DIST_DIR>/cache/.
set -euo pipefail

VERSION="${1:-}"
DIST_DIR="${2:-dist}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

if [[ -z "${VERSION}" ]]; then
  echo "usage: bash scripts/build-windows-installer.sh <VERSION> [DIST_DIR]" >&2
  exit 1
fi

PKG_DIR="${DIST_DIR}/mindfs_${VERSION}_windows_amd64"
if [[ ! -d "${PKG_DIR}" ]]; then
  echo "missing ${PKG_DIR}: run 'bash scripts/build-all.sh ${VERSION}' first" >&2
  exit 1
fi
# An installer without the web assets produces a machine whose service runs but
# GET / is a 404 — nothing in the log points at the installer.
for f in mindfs.exe agents.json web/index.html web/favicon.svg; do
  if [[ ! -f "${PKG_DIR}/${f}" ]]; then
    echo "missing ${PKG_DIR}/${f}: the Windows package is incomplete" >&2
    exit 1
  fi
done

# VIProductVersion needs four numeric fields: v0.5.5-1700 -> 0.5.5.1700
BASE="${VERSION#v}"
BUILD="${BASE#*-}"
if [[ "${BUILD}" == "${BASE}" ]]; then
  BUILD=0
fi
BASE="${BASE%%-*}"
IFS=. read -r V_MAJOR V_MINOR V_PATCH <<<"${BASE}"
for n in "${V_MAJOR}" "${V_MINOR}" "${V_PATCH}" "${BUILD}"; do
  if [[ ! "${n}" =~ ^[0-9]+$ ]]; then
    echo "cannot derive VIProductVersion from '${VERSION}'" >&2
    exit 1
  fi
done
VERSION4="${V_MAJOR}.${V_MINOR}.${V_PATCH}.${BUILD}"

MAKENSIS="${MAKENSIS:-}"
if [[ -z "${MAKENSIS}" ]]; then
  if command -v makensis >/dev/null 2>&1; then
    MAKENSIS="$(command -v makensis)"
  elif [[ -x "${HOME}/.local/nsis/usr/bin/makensis" ]]; then
    MAKENSIS="${HOME}/.local/nsis/usr/bin/makensis"
    export NSISDIR="${NSISDIR:-${HOME}/.local/nsis/usr/share/nsis}"
  else
    echo "makensis not found; see the header of this script for the rootless install" >&2
    exit 1
  fi
fi
if [[ -z "${NSISDIR:-}" && -d "$(dirname "${MAKENSIS}")/../share/nsis" ]]; then
  export NSISDIR="$(cd "$(dirname "${MAKENSIS}")/../share/nsis" && pwd)"
fi

# Payload layout matches the installed tree: <prefix>/bin/mindfs.exe,
# <prefix>/share/mindfs/{agents.json,web}. Keeping it identical to deploy's
# SetupPathsFor is what lets the scheduled task and the desktop shell agree on
# where the web assets live.
# makensis 在 Linux 上解析相对路径（无论是 `\*.*` 通配还是 File 的单个文件）会得到空
# 匹配，实测两种相对路径都报 "no files found"，绝对路径则正常，所以传给 .nsi 的
# PAYLOAD / bootstrapper 都先转成绝对路径。
PAYLOAD="$(cd "${DIST_DIR}" && pwd)/nsis-payload"
rm -rf "${PAYLOAD}"
mkdir -p "${PAYLOAD}/bin" "${PAYLOAD}/share/mindfs"
cp "${PKG_DIR}/mindfs.exe" "${PAYLOAD}/bin/mindfs.exe"
cp "${PKG_DIR}/agents.json" "${PAYLOAD}/share/mindfs/agents.json"
cp -r "${PKG_DIR}/web" "${PAYLOAD}/share/mindfs/web"

BOOTSTRAPPER="${MINDFS_WEBVIEW2_BOOTSTRAPPER:-}"
if [[ -z "${BOOTSTRAPPER}" ]]; then
  CACHE_DIR="${DIST_DIR}/cache"
  BOOTSTRAPPER="${CACHE_DIR}/MicrosoftEdgeWebView2Setup.exe"
  if [[ ! -s "${BOOTSTRAPPER}" ]]; then
    mkdir -p "${CACHE_DIR}"
    echo "==> Downloading WebView2 Evergreen Bootstrapper"
    curl -fsSL --retry 3 -o "${BOOTSTRAPPER}.tmp" \
      "https://go.microsoft.com/fwlink/p/?LinkId=2124703"
    mv "${BOOTSTRAPPER}.tmp" "${BOOTSTRAPPER}"
  fi
fi
if [[ ! -s "${BOOTSTRAPPER}" ]]; then
  echo "WebView2 bootstrapper missing: ${BOOTSTRAPPER}" >&2
  exit 1
fi
if [[ "${BOOTSTRAPPER}" != /* ]]; then
  BOOTSTRAPPER="${ROOT}/${BOOTSTRAPPER}"
fi

OUT="$(cd "${DIST_DIR}" && pwd)/mindfs_${VERSION}_windows_amd64-setup.exe"
APP_NAME="$(python3 -c 'import json; print(json.load(open("internal/brand/brand.json"))["name"])')"
echo "==> Building NSIS installer -> ${OUT}"
"${MAKENSIS}" -V2 \
  -DVERSION="${VERSION}" \
  -DVERSION4="${VERSION4}" \
  -DAPP_NAME="${APP_NAME}" \
  -DOUTFILE="${OUT}" \
  -DPAYLOAD="${PAYLOAD}" \
  -DWEBVIEW2_BOOTSTRAPPER="${BOOTSTRAPPER}" \
  -DAPP_ICON="${ROOT}/cli/cmd/assets/app.ico" \
  scripts/windows-installer/mindfs.nsi

if [[ ! -f "${OUT}" ]]; then
  echo "installer was not produced: ${OUT}" >&2
  exit 1
fi
ls -l "${OUT}"
