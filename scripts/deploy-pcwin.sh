#!/usr/bin/env bash
# 单独部署 pc-win（Windows worker，监听 0.0.0.0:7332）。
# 用法: bash scripts/deploy-pcwin.sh [VERSION]
#   VERSION 省略时从 git derive。
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.."; pwd)"
cd "$ROOT"

PC_HOST="${MINDFS_PC_HOST:-pc}"
VERSION="${1:-${VERSION:-}}"
if [[ -z "$VERSION" ]]; then
  # 从 git describe 推导，与 make build 一致
  VERSION="$(git describe --tags --always 2>/dev/null || git rev-parse --short HEAD)"
fi

step() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
ok()   { printf '    \033[32m✓\033[0m %s\n' "$*"; }
die()  { printf '    \033[31m✗ %s\033[0m\n' "$*" >&2; exit 1; }

step "交叉编译 Windows 二进制（pc-win）"
GOOS=windows GOARCH=amd64 go build \
  -ldflags "-H windowsgui -X main.version=$VERSION -X mindfs/internal/deploy.Prefix=/mindfs" \
  -o /tmp/mindfs-pcwin.exe ./cli/cmd \
  || die "Windows 交叉编译失败"
ok "Windows 二进制已构建"

step "推送产物到 pc-win"
scp -o BatchMode=yes -o ConnectTimeout=10 /tmp/mindfs-pcwin.exe "$PC_HOST:C:/Users/10178/mindfs.exe.new" \
  || die "产物传输失败（$PC_HOST）"
ok "产物已推送"

step "pc-win 安装"
ssh -o BatchMode=yes -o ConnectTimeout=10 "$PC_HOST" 'schtasks /end /tn mindfs' >/dev/null 2>&1 || true
sleep 2
ssh -o BatchMode=yes -o ConnectTimeout=10 "$PC_HOST" "powershell -Command \"if (Test-Path 'C:/Users/10178/AppData/Local/Programs/mindfs/bin/mindfs.exe') { Remove-Item -Force 'C:/Users/10178/AppData/Local/Programs/mindfs/bin/mindfs.exe' }\"" >/dev/null 2>&1 || true
ssh -o BatchMode=yes -o ConnectTimeout=10 "$PC_HOST" "powershell -Command \"Move-Item -Force 'C:/Users/10178/mindfs.exe.new' 'C:/Users/10178/AppData/Local/Programs/mindfs/bin/mindfs.exe'\"" \
  || die "移动二进制失败"
# 版本号必须在服务启动前读
remote_version="$(ssh -o BatchMode=yes -o ConnectTimeout=10 "$PC_HOST" "powershell -Command \"& (Join-Path \$env:USERPROFILE 'AppData/Local/Programs/mindfs/bin/mindfs.exe') --version\"" 2>&1 | sed -nE 's/^mindfs version: (.*)$/\1/p' | tail -1)"
[[ -n "$remote_version" ]] || die "读不出 pc-win 的版本号"
[[ "$remote_version" == "$VERSION" ]] || die "版本不一致：pc-win=$remote_version 本机=$VERSION"
ok "pc-win 已安装 $remote_version"

step "pc-win 重启"
ssh -o BatchMode=yes -o ConnectTimeout=10 "$PC_HOST" 'schtasks /run /tn mindfs' >/dev/null 2>&1 || true
sleep 3
ssh -o BatchMode=yes -o ConnectTimeout=10 "$PC_HOST" 'powershell -Command "Get-Process mindfs -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Id"' >/dev/null 2>&1 || true
ok "pc-win 已重启"

echo "pc-win 部署完成 $VERSION"
