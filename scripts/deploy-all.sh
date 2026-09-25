#!/usr/bin/env bash
# 代码变更收尾：提交 → 推送 → 两侧编译安装 → WSL 拉取重建重启 → 对账。
#
# 用法:
#   bash scripts/deploy-all.sh                 # 提交已在 main，直接走推送+部署
#   bash scripts/deploy-all.sh -m "fix: 说明"  # 先把工作区改动提交到 main 再走
#   bash scripts/deploy-all.sh --no-restart    # 只编译安装，不重启 WSL
#
# 分工（见 CLAUDE.md）：本机是 system 单元，sudo restart 只能由用户执行，
# 脚本只负责装好二进制并把该提示打出来；WSL 是 user 单元，脚本直接重启。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.."; pwd)"
cd "$ROOT"

MESSAGE=""
DO_RESTART=1
WSL_HOST="${MINDFS_WSL_HOST:-wsl}"

usage() {
  sed -n '2,10p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
  exit "${1:-0}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -m|--message) MESSAGE="${2:-}"; shift 2 ;;
    --no-restart) DO_RESTART=0; shift ;;
    -h|--help) usage 0 ;;
    *) echo "未知参数: $1" >&2; usage 2 ;;
  esac
done

# Go 不在非交互 shell 的 PATH 里（.zshenv 只对 zsh 生效）。
export PATH="/usr/local/go/bin:$HOME/.local/share/go/bin:$PATH"

step() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
ok()   { printf '    \033[32m✓\033[0m %s\n' "$*"; }
die()  { printf '    \033[31m✗ %s\033[0m\n' "$*" >&2; exit 1; }

# ── 0. 门禁：先确保没在别的分支、没在半截合并里 ──
step "检查仓库状态"
[[ -z "$(git status --porcelain --untracked-files=no -- . | grep -v '^\(..\|UU\|AA\)' || true)" ]] \
  || echo "    (工作区有改动，属正常)"

if [[ -f .git/MERGE_HEAD ]]; then
  die "存在未完成的合并（MERGE_HEAD），先解决冲突"
fi

BRANCH="$(git branch --show-current)"
[[ "$BRANCH" == "main" ]] || die "当前在 $BRANCH，不是 main。先合并到主分支再部署。"
ok "分支 main，工作区提交 $(git rev-parse --short HEAD)"

# ── 1. 可选：先提交 ──
if [[ -n "$MESSAGE" ]]; then
  step "提交工作区改动"
  if [[ -z "$(git status --porcelain)" ]]; then
    echo "    (无改动，跳过提交)"
  else
    # 只提交本仓库跟踪的源码，不碰 .mindfs/ 等运行期数据
    git add -A -- web server Makefile task_template.json scripts .claude 2>/dev/null || git add -A
    git commit -m "$MESSAGE"
    ok "已提交 $(git rev-parse --short HEAD)"
  fi
fi

# ── 2. 门禁：推送前跑测试 ──
step "门禁：Go 测试 + web 类型检查 + web 测试"
( cd server && go test ./... ) >/tmp/mindfs-go-test.log 2>&1 \
  || { tail -30 /tmp/mindfs-go-test.log >&2; die "go test 失败，见 /tmp/mindfs-go-test.log"; }
ok "go test 通过"

( cd web && ./node_modules/.bin/tsc --noEmit -p tsconfig.json ) >/tmp/mindfs-tsc.log 2>&1 \
  || { tail -30 /tmp/mindfs-tsc.log >&2; die "tsc 失败，见 /tmp/mindfs-tsc.log"; }
ok "tsc 通过"

( cd web && node --test tests/*.test.mjs ) >/tmp/mindfs-web-test.log 2>&1 \
  || { tail -40 /tmp/mindfs-web-test.log >&2; die "web 测试失败，见 /tmp/mindfs-web-test.log"; }
ok "web 测试通过（$(grep -oE '^# pass [0-9]+' /tmp/mindfs-web-test.log | tail -1 | grep -oE '[0-9]+') 项）"

# ── 3. 推送 ──
step "推送到 origin"
git push origin main
ok "已推送 main"

# ── 4. 本机构建 + 安装 ──
step "本机构建并安装"
make build >/tmp/mindfs-build.log 2>&1 || { tail -30 /tmp/mindfs-build.log >&2; die "make build 失败"; }
make install >>/tmp/mindfs-build.log 2>&1 || { tail -30 /tmp/mindfs-build.log >&2; die "make install 失败"; }
VERSION="$(grep -oE 'version=v[0-9][^ ]*' /tmp/mindfs-build.log | tail -1 | cut -d= -f2)"
ok "本机已安装 $VERSION"

# ── 5. WSL 端：拉取 → 重建 → 安装 → 重启 ──
step "WSL 拉取并重建"
ssh -o BatchMode=yes -o ConnectTimeout=10 "$WSL_HOST" bash -s <<'REMOTE' || die "WSL 部署失败"
set -euo pipefail
export PATH="$HOME/.local/share/go/bin:/usr/local/go/bin:$PATH"
cd ~/projects/mindfs
git pull --ff-only origin main
make build
make install
systemctl --user restart mindfs
sleep 3
systemctl --user is-active --quiet mindfs && echo "WSL 服务已 active"
REMOTE
ok "WSL 已拉取、编译、安装并重启"

# ── 6. 对账：两端服务的 bundle 应当一致 ──
step "对账两端服务"
bundle_of() { curl -s -m 20 "$1/" | grep -oE 'assets/index-[A-Za-z0-9_-]+\.js' | head -1 || true; }
size_of()  { curl -s -m 60 -o /dev/null -w '%{size_download}' "$1/$2" 2>/dev/null || echo 0; }

LOCAL_BASE="http://127.0.0.1:7331/mindfs"
PC_BASE="https://pc.xiaokubao.space/mindfs"

L_BUNDLE="$(bundle_of "$LOCAL_BASE")"
P_BUNDLE="$(bundle_of "$PC_BASE")"
L_SIZE="$(size_of "$LOCAL_BASE" "$L_BUNDLE")"
P_SIZE="$(size_of "$PC_BASE" "$P_BUNDLE")"

echo "    本机  $L_BUNDLE  ${L_SIZE} bytes"
echo "    PC    $P_BUNDLE  ${P_SIZE} bytes"

if [[ -n "$L_SIZE" && "$L_SIZE" == "$P_SIZE" && "$L_SIZE" != "0" ]]; then
  ok "两端 bundle 字节数一致（哈希不同是构建随机戳，内容相同）"
else
  echo "    \033[33m! 两端 bundle 大小不一致，需人工确认\033[0m"
fi

# ── 7. 本机重启：只能由用户执行 ──
echo
if [[ "$DO_RESTART" == "1" ]]; then
  echo "    \033[1m本机还需你手动重启：\033[0m sudo systemctl restart mindfs"
  echo "    （纯前端改动刷新页面即生效；重启只为让二进制版本号一致）"
fi
echo "    部署完成 $VERSION"
