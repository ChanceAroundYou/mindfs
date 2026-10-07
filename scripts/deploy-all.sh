#!/usr/bin/env bash
# 代码变更收尾：提交 → 推送 → 本机编译安装 → 推送产物到 WSL → 对账。
#
# 用法:
#   bash scripts/deploy-all.sh                 # 提交已在 main，直接走推送+部署
#   bash scripts/deploy-all.sh -m "fix: 说明"  # 先把工作区改动提交到 main 再走
#   bash scripts/deploy-all.sh --no-restart    # 只推产物+安装，不重启 WSL
#
# 分工（见 CLAUDE.md）：本机 system 单元，sudo restart 只能由用户执行，脚本只装好并提示；
# WSL 是 user 单元、**没有源码库**，只接收本机构建好的产物，脚本直接重启它。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.."; pwd)"
cd "$ROOT"

MESSAGE=""
DO_RESTART=1
WSL_HOST="${MINDFS_WSL_HOST:-wsl}"
STAGE=".mindfs-deploy"   # WSL 上的暂存目录（相对 $HOME），装完即删

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

# Go 通过 ~/.local/bin/{go,gofmt} 符号链接可见（.zshenv 首位，非交互 bash 也继承该 PATH），无需再补 PATH。

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
    # agents.json 是仓库根的源码文件（agent 目录与安装命令都在这），漏了它会让
    # 只改它的提交变成空提交，git commit 非零退出被 set -e 当场打死。
    git add -A -- web server Makefile task_template.json scripts .claude agents.json docs 2>/dev/null || git add -A
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

( cd web && node --test --import ./tests/source-map-hook.mjs --import ./tests/ts-module-preload.mjs tests/*.test.mjs ) >/tmp/mindfs-web-test.log 2>&1 \
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
# 版本号不能只认 v[0-9]…：Makefile 的 VERSION 来自 `git describe --tags --always`，
# 最近的一个 tag 要是叫 backup-pre-v0.5.5（备份 tag），describe 就返回
# backup-pre-v0.5.5-41-gd1d0df2。老的 `version=v[0-9]` 匹配不上，grep 退出码非 0，
# 在 set -euo pipefail 下直接把脚本打死在本机装完之后 —— 表现是「推到 origin 了、
# 本机也装上了，然后没声没响地没了」，WSL 根本没部署。这里匹配 -X main.version= 后面
# 的整个词，再把尾部引号剥掉。
VERSION="$(sed -nE 's/.*-X main\.version=([^ "]+).*/\1/p' /tmp/mindfs-build.log | tail -1)"
[[ -n "$VERSION" ]] || die "从构建日志里读不出版本号（/tmp/mindfs-build.log）"
ok "本机已安装 $VERSION"

# ── 5. WSL 端：只接收产物，不在那边编译 ──
# WSL 上自 2026-10-06 起**没有源码库**（~/projects/mindfs 只剩 .mindfs/ 数据目录）：
# 那边不再 git pull、不再 make build。只接收本机构建好的产物 —— 谁编译谁负责版本号，
# 两边永远跑的同一份二进制，不会再出现「那边拉到一半的源码」或「go 版本对不上」。
step "推送产物到 WSL"
tar -C "$ROOT" -cf - mindfs agents.json task_template.json -C "$ROOT/web" dist \
  | ssh -o BatchMode=yes -o ConnectTimeout=10 "$WSL_HOST" \
      "set -euo pipefail; rm -rf ~/$STAGE; mkdir -p ~/$STAGE; tar -xf - -C ~/$STAGE" \
  || die "产物传输失败（$WSL_HOST）"

if [[ "$DO_RESTART" == "1" ]]; then
  RESTART_LINE="systemctl --user restart mindfs
sleep 3
systemctl --user is-active --quiet mindfs || { echo 'WSL 服务未 active'; exit 1; }"
else
  RESTART_LINE="echo '(按 --no-restart 跳过重启，产物已装好)'"
fi

REMOTE_SCRIPT="$(mktemp)"
trap 'rm -f "$REMOTE_SCRIPT"' EXIT
cat >"$REMOTE_SCRIPT" <<REMOTE
set -euo pipefail
install -m 0755 ~/$STAGE/mindfs             ~/.local/bin/mindfs
install -d                                  ~/.local/share/mindfs/web
install -m 0644 ~/$STAGE/agents.json        ~/.local/share/mindfs/agents.json
install -m 0644 ~/$STAGE/task_template.json ~/.local/share/mindfs/task_template.json
# 覆盖复制而不是 rm -rf：哈希资源对外是 immutable，删掉会让部署前就打开的页面懒加载 404。
# worker 按设计不服务静态资源，这份 web/ 是留给「角色翻成 control」的 —— 与其那天才发现缺东西，
# 不如每次多推几 MB。
cp -R ~/$STAGE/dist/. ~/.local/share/mindfs/web/
find ~/.local/share/mindfs/web/assets -type f -mtime +14 -delete
rm -rf ~/$STAGE
$RESTART_LINE
~/.local/bin/mindfs --version
REMOTE

step "WSL 安装并重启"
REMOTE_OUT="$(ssh -o BatchMode=yes -o ConnectTimeout=10 "$WSL_HOST" bash -s <"$REMOTE_SCRIPT" 2>&1)" \
  || die "WSL 安装失败：$REMOTE_OUT"
# 版本号对账：推的是本机刚构建好的**同一个二进制文件**，两边版本号必须逐字相同。
# 不同就是产物没落到位，当场停 —— 比对 bundle 字节数更硬（worker 压根没有 bundle）。
REMOTE_VERSION="$(printf '%s\n' "$REMOTE_OUT" | sed -nE 's/^mindfs version: (.*)$/\1/p' | tail -1)"
[[ -n "$REMOTE_VERSION" ]] || die "读不出 WSL 的版本号：$REMOTE_OUT"
[[ "$REMOTE_VERSION" == "$VERSION" ]] \
  || die "版本不一致：WSL=$REMOTE_VERSION 本机=$VERSION（产物没落到位）"
ok "WSL 已安装 $REMOTE_VERSION"

# ── 6. 对账：两端服务的 bundle 应当一致 ──
# worker 节点按设计不提供前端（GET / 是 403，StaticDir 为空），所以它压根
# 取不到 bundle —— 那不是部署坏了，跳过对账即可，别报「不一致，需人工确认」。
step "对账两端服务"
bundle_of() { curl -s -m 20 "$1/" | grep -oE 'assets/index-[A-Za-z0-9_-]+\.js' | head -1 || true; }
size_of()  { curl -s -m 60 -o /dev/null -w '%{size_download}' "$1/$2" 2>/dev/null || echo 0; }
role_of()  { curl -s -m 20 "$1/health" | grep -oE '"role"\s*:\s*"[a-z]+"' | head -1 | grep -oE '[a-z]+"$' | tr -d '"' || true; }

LOCAL_BASE="http://127.0.0.1:7331/mindfs"
PC_BASE="https://pc.xiaokubao.space/mindfs"

L_BUNDLE="$(bundle_of "$LOCAL_BASE")"
L_SIZE="$(size_of "$LOCAL_BASE" "$L_BUNDLE")"
echo "    本机  $L_BUNDLE  ${L_SIZE} bytes"

P_ROLE="$(role_of "$PC_BASE")"
if [[ "$P_ROLE" == "worker" ]]; then
  echo "    PC    role=worker，不提供前端，跳过 bundle 对账"
else
  P_BUNDLE="$(bundle_of "$PC_BASE")"
  P_SIZE="$(size_of "$PC_BASE" "$P_BUNDLE")"
  echo "    PC    $P_BUNDLE  ${P_SIZE} bytes"

  if [[ -n "$L_SIZE" && "$L_SIZE" == "$P_SIZE" && "$L_SIZE" != "0" ]]; then
    ok "两端 bundle 字节数一致（哈希不同是构建随机戳，内容相同）"
  else
    echo "    \033[33m! 两端 bundle 大小不一致，需人工确认\033[0m"
  fi
fi

# ── 7. 本机重启：只能由用户执行 ──
echo
printf '    \033[1m本机还需你手动重启：\033[0m sudo systemctl restart mindfs\n'
echo    '    （纯前端改动刷新页面即生效；重启只为让二进制版本号一致）'
echo "    部署完成 $VERSION"
