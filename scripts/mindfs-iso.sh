#!/usr/bin/env bash
# 隔离测试实例 —— 给 E2E 用，**绝不碰线上**。
#
# 为什么需要它：E2E 要验的三类行为（跨模块接线、真实时序、只有真后端才有的回包形状）
# 都要求一个真服务端，而线上的 mindfs 是 system 单元、重启会杀掉所有托管的 claude 子进程
# （只能由用户执行）。所以另起一个实例，用**独立的配置目录与端口**。
#
# 隔离靠什么（已实测，不是推测）：
#   · `MindFSConfigDir()` = `os.UserConfigDir()`/mindfs（`server/internal/config/paths.go:10`）
#     ⇒ 一个 `XDG_CONFIG_HOME` 搬走账户表、项目注册表、偏好、看板模板、pending 旁的 meta。
#   · **不隔离 HOME**：agent 发现逻辑要读 `~/.local/bin`、`~/.claude`
#     （`server/internal/agent/discovery.go:61,168`），搬走 HOME 会让真实回合跑不起来。
#   · 静态资源用 `MINDFS_STATIC_DIR` 指向**工作区**的 `web/dist` ⇒ E2E 验的是我改的那版前端。
#
# 已知且接受的副作用（写在这里以免下次又踩）：
#   · 在隔离实例里**新建**会话会产生新的转录目录 `~/.claude/projects/-tmp-mindfs-iso-...`
#     （按 cwd 派生 slug）。它是新目录，不碰任何既有会话。
#   · **绝不要在隔离实例里「继续聊」线上导入进来的真实会话** —— 那会以真实 session id
#     续跑并**追加写入真实转录**。E2E 一律用隔离项目里新建的会话。
#
# 用法：
#   bash scripts/mindfs-iso.sh start      # 起实例 + 建账户 + 注册隔离项目 + 自证隔离
#   bash scripts/mindfs-iso.sh verify     # 只跑隔离断言（不启停）
#   bash scripts/mindfs-iso.sh stop
#   bash scripts/mindfs-iso.sh status
set -euo pipefail

WT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ISO_ROOT="${MINDFS_ISO_ROOT:-/tmp/mindfs-iso}"
ISO_PORT="${MINDFS_ISO_PORT:-7431}"
ONLINE_PORT="${MINDFS_ONLINE_PORT:-7331}"
ONLINE_CFG="${HOME}/.config/mindfs"
ISO_CFG="$ISO_ROOT/config"          # XDG_CONFIG_HOME
ISO_CFG_DIR="$ISO_CFG/mindfs"       # MindFSConfigDir()
ISO_PROJ="$ISO_ROOT/proj"           # 隔离项目根（会话只在这里建）
ISO_BIN="$ISO_ROOT/bin/mindfs"
ISO_LOG="$ISO_ROOT/server.log"
ISO_PID="$ISO_ROOT/server.pid"
ISO_BASE="/mindfs"                  # 与 Makefile 的 MIND_FS_BASE 默认值一致
ISO_URL="http://127.0.0.1:$ISO_PORT$ISO_BASE"
ISO_USER="iso-e2e"
ISO_PASS="iso-e2e-pass-$(date +%s)"

die() { printf '✗ %s\n' "$*" >&2; exit 1; }
ok()  { printf '✓ %s\n' "$*"; }
say() { printf '· %s\n' "$*"; }

# ── 线上现场指纹：文件集 + 大小 + mtime。隔离前后必须逐字节相同。───────────────
online_fingerprint() {
  [ -d "$ONLINE_CFG" ] || { echo "no-online-config"; return; }
  find "$ONLINE_CFG" -type f -exec stat -c '%n %s %Y' {} + 2>/dev/null | sort | sha256sum | cut -d' ' -f1
}
online_alive() {
  curl -sf -o /dev/null --max-time 3 "http://127.0.0.1:$ONLINE_PORT$ISO_BASE/health" \
    || curl -sf -o /dev/null --max-time 3 "http://127.0.0.1:$ONLINE_PORT/health"
}

# 注意：部署前缀 /mindfs 下健康检查是 **`/mindfs/health`**，裸 `/health` 是 404
# （实测：裸 404、带前缀 200 `{"ok":true,"role":"control"}`）。前缀由构建期
# `-X mindfs/internal/deploy.Prefix` 决定，所以这里必须跟着 $ISO_BASE 走。
iso_health() { curl -sf --max-time 3 "$ISO_URL/health"; }
iso_up() { iso_health >/dev/null 2>&1; }

# ── start ─────────────────────────────────────────────────────────────────────
cmd_start() {
  [ "$ISO_PORT" = "$ONLINE_PORT" ] && die "隔离端口不能等于线上端口（$ONLINE_PORT）"
  command -v go >/dev/null || die "找不到 go"
  command -v curl >/dev/null || die "找不到 curl"

  say "线上现场（隔离前）：config 指纹 $(online_fingerprint)"
  online_fingerprint > "$ISO_ROOT.online-fp-before" 2>/dev/null || true

  mkdir -p "$ISO_CFG" "$ISO_PROJ" "$(dirname "$ISO_BIN")"

  # 每次 start 从干净状态起（除非 MINDFS_ISO_KEEP=1）——
  # 这不是洁癖，是被一个真实的坑逼出来的：项目列表**按账户分**（CLAUDE.md 事实 12），
  # 而「不带 user= 注册项目」落进**主账户**。若沿用上次的 config 目录，就会出现
  # 「项目注册在主账户（最早那个账户）里，浏览器登录的却是后来新建的账户 → 页面显示
  # 『No projects yet』、连输入框都不渲染」，而 /api/dirs 明明 200。清空后只建一个账户，
  # 它即主账户，注册与登录指向同一个账户，问题不存在。
  if [ "${MINDFS_ISO_KEEP:-0}" != "1" ]; then
    say "清空隔离状态（MINDFS_ISO_KEEP=1 可保留）：$ISO_CFG 与 $ISO_PROJ/.mindfs"
    rm -rf "$ISO_CFG" "$ISO_PROJ/.mindfs" "$ISO_ROOT/account"
    mkdir -p "$ISO_CFG"
  fi

  say "编译隔离二进制（工作区源码）"
  ( cd "$WT_ROOT" && go build -ldflags "-X mindfs/internal/deploy.Prefix=$ISO_BASE" -o "$ISO_BIN" ./cli/cmd )

  [ -d "$WT_ROOT/web/dist/assets" ] || die "web/dist 不存在 —— 先跑 make build-web（E2E 必须验工作区那版前端）"

  if [ ! -f "$ISO_PROJ/notes.md" ]; then
    printf '# 隔离项目\n\nE2E 用的合成项目，与线上任何项目无关。\n' > "$ISO_PROJ/notes.md"
    printf 'hello\n' > "$ISO_PROJ/a.txt"
  fi

  if iso_up; then
    say "端口 $ISO_PORT 上已有实例在跑，先停掉"
    cmd_stop
  fi

  say "启动：addr=127.0.0.1:$ISO_PORT  XDG_CONFIG_HOME=$ISO_CFG  HOME=(真实，Agent 需要)"
  (
    cd "$ISO_ROOT"
    XDG_CONFIG_HOME="$ISO_CFG" \
    MINDFS_STATIC_DIR="$WT_ROOT/web/dist" \
    nohup "$ISO_BIN" -addr "127.0.0.1:$ISO_PORT" -no-relayer -web-push=false -foreground \
      -agent-config "$WT_ROOT/agents.json" >"$ISO_LOG" 2>&1 &
    echo $! > "$ISO_PID"
  )

  for _ in $(seq 1 60); do iso_up && break; sleep 0.5; done
  iso_up || { tail -20 "$ISO_LOG" >&2; die "实例未就绪，日志见 $ISO_LOG"; }
  ok "实例就绪：$ISO_URL（pid $(cat "$ISO_PID")）"

  # 账户：**密码以 $ISO_ROOT/account 为唯一真源**。上一版每次随机生成密码，
  # 第二次启动时账户已存在（POST /api/users 不会改密码）却拿新密码去登录 ⇒ 401。
  if [ -f "$ISO_ROOT/account" ]; then
    ISO_USER=$(sed -n 1p "$ISO_ROOT/account")
    ISO_PASS=$(sed -n 2p "$ISO_ROOT/account")
    say "复用已有隔离账户：$ISO_USER"
  fi
  local login_json
  login_json=$(curl -s -X POST "http://127.0.0.1:$ISO_PORT$ISO_BASE/api/auth/login" \
      -H 'Content-Type: application/json' \
      -d "{\"username\":\"$ISO_USER\",\"password\":\"$ISO_PASS\"}" || echo '')
  if printf '%s' "$login_json" | grep -q '"id"'; then
    ok "登录成功：$ISO_USER"
  else
    say "账户不存在或密码不匹配 → 新建"
    # 用户名带时间戳：隔离目录里可能残留上次建的同名账户（密码已不可知），
    # 而 POST /api/users 对已存在的用户名会拒绝。用唯一名绕开，而不是去删线上以外
    # 还要动的东西（清空 config 目录也没用 —— auth store 已经在内存里了）。
    ISO_USER="iso-e2e-$(date +%s)"
    ISO_PASS="iso-e2e-pass-$(date +%s)"
    curl -sf -X POST "http://127.0.0.1:$ISO_PORT$ISO_BASE/api/users" \
      -H 'Content-Type: application/json' \
      -d "{\"username\":\"$ISO_USER\",\"password\":\"$ISO_PASS\"}" >/dev/null \
      || die "创建账户失败"
    login_json=$(curl -s -X POST "http://127.0.0.1:$ISO_PORT$ISO_BASE/api/auth/login" \
      -H 'Content-Type: application/json' \
      -d "{\"username\":\"$ISO_USER\",\"password\":\"$ISO_PASS\"}" || echo '')
    printf '%s' "$login_json" | grep -q '"id"' || die "登录失败"
    printf '%s\n%s\n' "$ISO_USER" "$ISO_PASS" > "$ISO_ROOT/account"
    ok "账户已建：$ISO_USER"
  fi

  # 项目注册**按登录账户**（默认落到主账户，但别依赖这个偶然 —— 见上面那段坑）。
  ISO_UID=$(printf '%s' "$login_json" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("user",{}).get("id") or "")' 2>/dev/null || echo '')
  [ -n "$ISO_UID" ] && printf '%s\n' "$ISO_UID" >> "$ISO_ROOT/account"
  say "注册隔离项目 $ISO_PROJ（user=${ISO_UID:-<主账户>}）"
  curl -sf -X POST "http://127.0.0.1:$ISO_PORT$ISO_BASE/api/dirs${ISO_UID:+?user=$ISO_UID}" \
    -H 'Content-Type: application/json' -d "{\"path\":\"$ISO_PROJ\"}" >/dev/null \
    || say "（项目可能已注册，继续）"

  cmd_verify
}

# ── verify：隔离自证 ──────────────────────────────────────────────────────────
cmd_verify() {
  local fails=0
  printf '\n===== 隔离自证 =====\n'

  if [ "$ISO_PORT" != "$ONLINE_PORT" ]; then ok "端口独立：$ISO_PORT ≠ 线上 $ONLINE_PORT"
  else printf '✗ 端口与线上相同\n'; fails=$((fails+1)); fi

  if iso_up; then ok "隔离实例在线：$(iso_health)"
  else printf '✗ 隔离实例不可达\n'; fails=$((fails+1)); fi

  # 配置目录必须落在 ISO_ROOT 内
  local seen
  seen=$(ls -d "$ISO_CFG_DIR" 2>/dev/null || true)
  case "$seen" in
    "$ISO_ROOT"/*) ok "配置目录隔离：$seen" ;;
    *) printf '✗ 配置目录没落在隔离根：%s\n' "${seen:-（不存在）}"; fails=$((fails+1)) ;;
  esac

  # 项目列表必须只含隔离项目。
  # **必须带 user=**：项目列表按账户分（CLAUDE.md 事实 12），不带就等于查主账户，
  # 会得到空列表并误报「隔离项目不在」—— 实测踩过。
  local iso_uid dirs
  iso_uid=$(sed -n 3p "$ISO_ROOT/account" 2>/dev/null || echo '')
  dirs=$(curl -sf --max-time 5 "http://127.0.0.1:$ISO_PORT$ISO_BASE/api/dirs${iso_uid:+?user=$iso_uid}" || echo '')
  if printf '%s' "$dirs" | grep -q "$ISO_PROJ"; then ok "隔离项目已在列表：$ISO_PROJ"
  else printf '✗ 隔离项目不在 /api/dirs 里\n'; fails=$((fails+1)); fi
  if printf '%s' "$dirs" | grep -qE '/home/xiaokubao/projects'; then
    printf '✗ /api/dirs 里出现了线上项目路径 —— 隔离失败\n'; fails=$((fails+1))
  else ok "/api/dirs 不含线上项目路径"; fi

  # 线上配置目录指纹 —— **只作信息项，不作断言**（2026-10-06 更正）。
  # 这条判据会喊狼来了：线上服务**自己**一直在写自己的配置目录（会话、DB、mtime），
  # 而基线是 start 那一刻取的，几小时后必然不同 —— 与隔离实例无关。
  # 真正决定性的判据是下面那条**句柄检查**：进程只能写它打开过的文件。
  local after
  after=$(online_fingerprint)
  if [ -f "$ISO_ROOT.online-fp-before" ]; then
    local before; before=$(cat "$ISO_ROOT.online-fp-before")
    if [ "$before" = "$after" ]; then ok "线上配置目录指纹未变（$after）"
    else say "线上配置目录指纹变了（before=${before:0:8} after=${after:0:8}）—— 线上服务自己在写，"
         say "  本条不作断言；是否被**本实例**碰过以下面的句柄检查为准"; fi
  else say "（无基线指纹，本次不比对）"; fi

  if online_alive; then ok "线上服务仍在线（127.0.0.1:$ONLINE_PORT）"
  else printf '· 线上服务未响应（可能本来就没跑；只要指纹没变就与本次无关）\n'; fi

  # 最直接的一条：看隔离进程**自己打开了哪些文件**。指纹比对会被「线上服务自身在写自己的
  # 配置目录」干扰（它一直在跑），而这条不会 —— 有句柄才是真的会写。
  if [ -f "$ISO_PID" ] && kill -0 "$(cat "$ISO_PID")" 2>/dev/null; then
    local pid; pid=$(cat "$ISO_PID")
    local handles
    handles=$(ls -l "/proc/$pid/fd" 2>/dev/null | grep -oE '/[^ ]+' | sort -u || true)
    if printf '%s' "$handles" | grep -q "^$ONLINE_CFG"; then
      printf '✗ 隔离进程打开了线上配置目录的文件：\n%s\n' \
        "$(printf '%s' "$handles" | grep "^$ONLINE_CFG")"; fails=$((fails+1))
    else ok "隔离进程零句柄落在线上配置目录（/proc/$pid/fd 已核）"; fi
    local cwd; cwd=$(readlink "/proc/$pid/cwd" 2>/dev/null || echo '')
    case "$cwd" in
      "$ISO_ROOT"*) ok "进程 cwd 在隔离根：$cwd" ;;
      *) printf '· 进程 cwd=%s（不在隔离根，但不影响配置隔离）\n' "${cwd:-未知}" ;;
    esac
  fi

  printf '===== 结论：%s =====\n' "$([ "$fails" -eq 0 ] && echo '隔离成立' || echo "有 $fails 项不通过")"
  [ "$fails" -eq 0 ] || return 1
}

cmd_stop() {
  if [ -f "$ISO_PID" ]; then
    local pid; pid=$(cat "$ISO_PID")
    if kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
      for _ in $(seq 1 20); do kill -0 "$pid" 2>/dev/null || break; sleep 0.25; done
      kill -9 "$pid" 2>/dev/null || true
      ok "已停止 pid $pid"
    fi
    rm -f "$ISO_PID"
  fi
  # 兜底：按端口找（绝不动线上端口）
  [ "$ISO_PORT" = "$ONLINE_PORT" ] && return 0
  pkill -f "mindfs -addr 127.0.0.1:$ISO_PORT" 2>/dev/null || true
}

cmd_status() {
  printf '隔离根   : %s\n' "$ISO_ROOT"
  printf '隔离端口 : %s（线上 %s）\n' "$ISO_PORT" "$ONLINE_PORT"
  printf '配置目录 : %s\n' "$ISO_CFG_DIR"
  printf 'URL      : %s\n' "$ISO_URL"
  if iso_up; then printf '状态     : 在线 %s\n' "$(iso_health)"
  else printf '状态     : 未运行\n'; fi
  if [ -f "$ISO_LOG" ]; then printf '日志尾部 :\n'; tail -3 "$ISO_LOG"; fi
}

case "${1:-status}" in
  start)  cmd_start ;;
  verify) cmd_verify ;;
  stop)   cmd_stop ;;
  status) cmd_status ;;
  *) die "用法: $0 {start|verify|stop|status}" ;;
esac
