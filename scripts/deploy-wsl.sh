#!/usr/bin/env bash
# 单独部署 WSL worker（监听 127.0.0.1:7331）。
# 用法: bash scripts/deploy-wsl.sh [VERSION]
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.."; pwd)"
cd "$ROOT"

WSL_HOST="${MINDFS_WSL_HOST:-wsl}"
VERSION="${1:-${VERSION:-}}"
[[ -n "$VERSION" ]] || VERSION="$(git describe --tags --always 2>/dev/null || git rev-parse --short HEAD)"
STAGE=".mindfs-deploy"


step() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
ok()   { printf '    \033[32m✓\033[0m %s\n' "$*"; }
die()  { printf '    \033[31m✗ %s\033[0m\n' "$*" >&2; exit 1; }

step "本机构建 mindfs 二进制"
go build -ldflags "-X main.version=$VERSION -X mindfs/internal/deploy.Prefix=/mindfs" -o "$ROOT/mindfs" ./cli/cmd \
  || die "go build 失败"
ok "已构建 $VERSION"

step "推送产物到 WSL"
tar -C "$ROOT" -cf - mindfs agents.json \
  | ssh -o BatchMode=yes -o ConnectTimeout=10 "$WSL_HOST" \
      "set -euo pipefail; rm -rf ~/$STAGE; mkdir -p ~/$STAGE; tar -xf - -C ~/$STAGE" \
  || die "产物传输失败（$WSL_HOST）"

step "WSL 安装并重启"
remote_out="$(ssh -o BatchMode=yes -o ConnectTimeout=10 "$WSL_HOST" bash -s 2>&1 <<REMOTE
set -euo pipefail
install -m 0755 ~/$STAGE/mindfs             ~/.local/bin/mindfs
install -m 0644 ~/$STAGE/agents.json        ~/.local/share/mindfs/agents.json
rm -rf ~/$STAGE
systemctl --user restart mindfs
sleep 3
systemctl --user is-active --quiet mindfs || { echo 'WSL 服务未 active'; exit 1; }
~/.local/bin/mindfs --version
REMOTE
)" || die "WSL 安装失败：$remote_out"

remote_version="$(printf '%s\n' "$remote_out" | sed -nE 's/^mindfs version: (.*)$/\1/p' | tail -1)"
[[ -n "$remote_version" ]] || die "读不出 WSL 的版本号：$remote_out"
[[ "$remote_version" == "$VERSION" ]] || die "版本不一致：WSL=$remote_version 本机=$VERSION"
ok "WSL 已安装 $remote_version"
echo "WSL 部署完成 $VERSION"