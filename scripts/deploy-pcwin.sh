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

step "pc-win 辅助脚本与 PATH"
# 两个辅助脚本装到 mindfs 的 bin 目录（同一目录已进用户 PATH）：
#   codex-switch  —— 本地切换 Codex 共享 profile（走运行中服务的本地 API）
#   register-user-path.ps1 —— 幂等把 bin 目录写进用户 PATH
scp -o BatchMode=yes -o ConnectTimeout=10 \
  "$ROOT/scripts/pcwin/codex-switch.ps1" "$ROOT/scripts/pcwin/codex-switch.cmd" \
  "$PC_HOST:C:/Users/10178/AppData/Local/Programs/mindfs/bin/" \
  || die "辅助脚本传输失败（$PC_HOST）"
# 脚本不能带中文：Windows PowerShell 按 ANSI 代码页读 .ps1，非 ASCII 会吃掉换行。
# 用 -File 而不是 `-Command -`：后者在 Windows PowerShell 5.1 上对 stdin 静默不执行。
scp -o BatchMode=yes -o ConnectTimeout=10 \
  "$ROOT/scripts/pcwin/register-user-path.ps1" "$PC_HOST:C:/Users/10178/register-user-path.ps1" \
  || die "PATH 注册脚本传输失败（$PC_HOST）"
ssh -o BatchMode=yes -o ConnectTimeout=10 "$PC_HOST" \
  'powershell -NoProfile -ExecutionPolicy Bypass -File C:\Users\10178\register-user-path.ps1' \
  || die "注册用户 PATH 失败"
ssh -o BatchMode=yes -o ConnectTimeout=10 "$PC_HOST" \
  'powershell -NoProfile -Command "Remove-Item -Force C:\Users\10178\register-user-path.ps1 -ErrorAction SilentlyContinue"' \
  >/dev/null 2>&1 || true
ok "codex-switch 已安装，mindfs bin 目录在用户 PATH 中"

step "pc-win 启动并核对"
# 计划任务 \mindfs 的登录类型是 S4U（2026-11-07 改的，原先是 InteractiveToken），
# 所以它跑在 Session 0、不管用户是否登录都会起，`schtasks /run` 从 ssh 触发也真的生效。
# 但「发出了运行请求」不等于「进程在跑」——这里轮询取回 PID 与 health，取不到就明说。
ssh -o BatchMode=yes -o ConnectTimeout=10 "$PC_HOST" 'schtasks /run /tn mindfs' >/dev/null 2>&1 || true
pid=""
for _ in 1 2 3; do
  sleep 4
  pid="$(ssh -o BatchMode=yes -o ConnectTimeout=10 "$PC_HOST" "powershell -NoProfile -Command \"Get-Process mindfs -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Id\"" 2>/dev/null | tr -d '\r\n ' || true)"
  [[ -n "$pid" ]] && break
done
if [[ -n "$pid" ]]; then
  health="$(ssh -o BatchMode=yes -o ConnectTimeout=10 "$PC_HOST" "powershell -NoProfile -Command \"try { (Invoke-WebRequest -UseBasicParsing -Uri http://127.0.0.1:7332/mindfs/health -TimeoutSec 8).StatusCode } catch { 'unreachable' }\"" 2>/dev/null | tr -d '\r\n' || true)"
  ok "pc-win 已启动：PID ${pid}，health ${health:-取不到}（Session 0，由计划任务启动）"
else
  # 不 die：产物已经装对、版本已核对过，这里失败只代表进程没起来（PC 睡眠/任务被禁用等），
  # 但绝不能把这一步说成「已重启」。
  printf '    \033[33m! pc-win 查不到 mindfs 进程：产物已装但**未确认在跑**。手动核对：ssh %s "netstat -ano | findstr :7332"\033[0m\n' "$PC_HOST"
fi

echo "pc-win 部署完成 $VERSION"
