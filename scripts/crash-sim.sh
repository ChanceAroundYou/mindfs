#!/usr/bin/env bash
# 崩溃注入（Plan 2026-11-20 Task 10）
#
# 为什么需要它：自主收尾的每条证据都建立在「进程会在任意时刻消失」之上 ——
# 回复活动、Run 投影、Router 事件账本、启动对账，四者只要有一处没落盘或没对账，
# 重启后就会留下「任务还在跑但其实什么都没在跑」的死局。单元测试只能模拟，
# 这里真的 kill -9 一个正在跑真实 Agent 回合的实例，再让它自己恢复。
#
# 每轮：起隔离实例（独立 root + 端口，绝不碰线上 7331）→ 建任务并立刻开跑 →
# 等回复真的进入 running → 随机延迟后 kill -9 → 原库重启 → 四条断言：
#   ① 没有仍停在 pending/running 的 Agent 回复（孤儿回复必须被对账结算）
#   ② 没有「任务还在 running，但 Run 投影全终态且没有活着的回复」的悬挂
#   ③ 第二次重启不新增任何事件（task_events / router_events 条数不变）
#   ④ PRAGMA integrity_check = ok
#
# 用法：
#   bash scripts/crash-sim.sh                    # 默认 3 轮
#   CRASH_SIM_ROUNDS=1 bash scripts/crash-sim.sh # 自检 1 轮
#   CRASH_SIM_ROOT=/tmp/x CRASH_SIM_PORT=7437 bash scripts/crash-sim.sh
#
# 退出码：0 = 每轮四条断言全过；1 = 有失败（明细见输出）。
set -uo pipefail

WT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ROOT="${CRASH_SIM_ROOT:-/tmp/mindfs-crash-sim}"
PORT="${CRASH_SIM_PORT:-7436}"
ROUNDS="${CRASH_SIM_ROUNDS:-3}"
BASE="http://127.0.0.1:$PORT/mindfs"
LOGDIR="$ROOT/logs"
mkdir -p "$LOGDIR"

fails=0
say() { printf '%s\n' "$*"; }
ok() { printf '  ✓ %s\n' "$*"; }
bad() { printf '  ✗ %s\n' "$*"; fails=$((fails + 1)); }

# 从 HEAD 切一份干净源码树来编译：共享工作区里随时有别的会话在飞改动，
# 拿工作区编译会让这个脚本时而编译不过、时而在验别人的半成品。
SRC="$(mktemp -d)/src"
cleanup() {
  MINDFS_ISO_ROOT="$ROOT" MINDFS_ISO_PORT="$PORT" bash "$WT_ROOT/scripts/mindfs-iso.sh" stop >/dev/null 2>&1 || true
  git -C "$WT_ROOT" worktree remove --force "$SRC" >/dev/null 2>&1 || true
  rm -rf "$(dirname "$SRC")"
}
trap cleanup EXIT

if ! git -C "$WT_ROOT" worktree add --detach "$SRC" HEAD >"$LOGDIR/worktree.log" 2>&1; then
  echo "crash-sim: 无法从 HEAD 切出临时源码树（见 $LOGDIR/worktree.log）" >&2
  exit 1
fi
# 前端产物与设备本地配置不进 git：软链回工作区，源码本身仍取 HEAD。
ln -sfn "$WT_ROOT/web/dist" "$SRC/web/dist"
ln -sfn "$WT_ROOT/agents.json" "$SRC/agents.json"

iso() { MINDFS_ISO_ROOT="$ROOT" MINDFS_ISO_PORT="$PORT" MINDFS_ISO_SRC="$SRC" bash "$WT_ROOT/scripts/mindfs-iso.sh" "$@"; }

wait_health() {
  for _ in $(seq 1 120); do
    curl -sf --max-time 2 "$BASE/health" >/dev/null 2>&1 && return 0
    sleep 0.5
  done
  return 1
}

# jget <json> <python-expression over d> —— 只读 JSON，不做别的。
jget() {
  python3 -c 'import json,sys
try: d=json.loads(sys.argv[1])
except Exception: d={}
print(eval(sys.argv[2], {"d": d, "json": json}))' "$1" "$2" 2>/dev/null
}

# db_file 找出任务库（每个项目根一份；隔离实例清空过，只会有一个）。
db_file() { find "$ROOT" -name task-kanban.db -print -quit 2>/dev/null; }

# report <db> 打印四条断言需要的实测值（只读）。
report() {
  python3 - "$1" <<'PY'
import json, sqlite3, sys

path = sys.argv[1]
db = sqlite3.connect(f"file:{path}?mode=ro", uri=True)

def rows(sql, args=()):
    return db.execute(sql, args).fetchall()

terminal_nodes = {"success", "fail", "cancelled", "skipped", "unknown"}

stuck = rows("SELECT id, task_id, status FROM task_activities "
             "WHERE role='agent' AND status IN ('pending','running')")

hangs = []
for task_id, status in rows("SELECT id, status FROM tasks WHERE status='running'"):
    live = rows("SELECT count(*) FROM task_activities WHERE task_id=? AND role='agent' "
                "AND status IN ('pending','running')", (task_id,))[0][0]
    if live:
        continue
    run = rows("SELECT node_states_json FROM workflow_runs WHERE task_id=? "
               "ORDER BY created_at DESC LIMIT 1", (task_id,))
    if not run:
        continue
    try:
        states = json.loads(run[0][0] or "{}")
    except Exception:
        states = {}
    if not states:
        continue
    open_nodes = []
    for node_id, value in states.items():
        state = value.get("status") if isinstance(value, dict) else value
        if str(state or "") not in terminal_nodes:
            open_nodes.append(node_id)
    if not open_nodes:
        hangs.append({"task_id": task_id, "status": status})

counts = {
    "task_events": rows("SELECT count(*) FROM task_events")[0][0],
    "router_events": rows("SELECT count(*) FROM router_events")[0][0],
    "task_activities": rows("SELECT count(*) FROM task_activities")[0][0],
}
integrity = rows("PRAGMA integrity_check")[0][0]
print(json.dumps({
    "stuck_replies": [[r[0], r[1], r[2]] for r in stuck],
    "hangs": hangs,
    "counts": counts,
    "integrity": integrity,
}))
PY
}

# wait_reply_running <task_id> <user> —— 等这一轮真的跑起来（回复行进入 pending/running）。
wait_reply_running() {
  local task_id="$1" user="$2" root_id="$3" body
  for _ in $(seq 1 180); do
    body=$(curl -s --max-time 5 "$BASE/api/tasks/$task_id/activities?user=$user&root_id=$root_id" || true)
    if [[ -n "$body" ]] && printf '%s' "$body" | python3 -c 'import json,sys
d=json.load(sys.stdin)
items=(d.get("items") or d.get("activities")) if isinstance(d,dict) else d
items=items or []
print("yes" if any((i.get("role")=="agent" and i.get("status") in ("pending","running")) for i in items) else "no")' | grep -q yes; then
      return 0
    fi
    sleep 1
  done
  return 1
}

run_round() {
  local n="$1"
  say "第 $n/$ROUNDS 轮"
  iso start >"$LOGDIR/start-$n.log" 2>&1 || { bad "隔离实例起不来（见 $LOGDIR/start-$n.log）"; return 1; }

  local user root_id task_json task_id delay
  user=$(sed -n 3p "$ROOT/account")
  root_id=$(jget "$(curl -s --max-time 5 -X POST "$BASE/api/dirs?user=$user" \
    -H 'Content-Type: application/json' -d "{\"path\":\"$ROOT/proj\"}")" 'd.get("id","")')
  if [[ -z "$root_id" ]]; then bad "注册隔离项目失败"; return 1; fi

  task_json=$(curl -s --max-time 15 -X POST "$BASE/api/tasks?user=$user" \
    -H 'Content-Type: application/json' \
    -d "{\"root_id\":\"$root_id\",\"name\":\"crash-sim-$n\",\"input\":\"只回一句话：CRASH-SIM 在线。不要调用任何工具。\",\"create_worktree\":false,\"start_immediately\":true}")
  task_id=$(jget "$task_json" '(d.get("task") or d).get("id","")')
  if [[ -z "$task_id" ]]; then bad "建任务失败：$task_json"; return 1; fi
  say "  任务 $task_id，等回复进入 running"

  if ! wait_reply_running "$task_id" "$user" "$root_id"; then
    bad "回合没有跑起来（90s 内没等到 running 的回复）"
    return 1
  fi

  delay=$(python3 -c 'import random; print(round(random.uniform(0.5, 3.0), 2))')
  sleep "$delay"
  local pid
  pid=$(cat "$ROOT/server.pid" 2>/dev/null || true)
  if [[ -z "$pid" ]] || ! kill -0 "$pid" 2>/dev/null; then bad "拿不到实例 pid"; return 1; fi
  say "  回合跑到一半，kill -9 pid=$pid（延迟 ${delay}s）"
  kill -9 "$pid" 2>/dev/null || true
  for _ in $(seq 1 40); do kill -0 "$pid" 2>/dev/null || break; sleep 0.25; done

  # 先证明这一刀确实砍在回合中间：进程已经死了，库里那条回复还挂着 running。
  # 没有这一条，后面三条断言在「回合早跑完了」的空场上也会全绿。
  local crash_db crash_state
  crash_db=$(db_file)
  if [[ -n "$crash_db" ]]; then
    crash_state=$(report "$crash_db")
    if [[ "$(jget "$crash_state" 'len(d.get("stuck_replies") or [])')" != "0" ]]; then
      ok "崩溃快照：$(jget "$crash_state" 'len(d.get("stuck_replies") or [])') 条回复停在 pending/running"
    else
      bad "崩溃时没有活着的回复 —— 这一刀没砍在回合中间，本轮不算有效崩溃"
      return 1
    fi
  fi

  say "  原库重启（MINDFS_ISO_KEEP=1）"
  MINDFS_ISO_KEEP=1 iso start >"$LOGDIR/restart-$n.log" 2>&1 || { bad "重启失败"; return 1; }
  wait_health || { bad "重启后 health 不通过"; return 1; }
  sleep 8

  local db; db=$(db_file)
  if [[ -z "$db" ]]; then bad "找不到任务库"; return 1; fi

  local first; first=$(report "$db")
  if [[ "$(jget "$first" 'd.get("integrity","")')" == "ok" ]]; then
    ok "④ PRAGMA integrity_check = ok"
  else
    bad "④ integrity_check = $(jget "$first" 'd.get("integrity","")')"
  fi
  if [[ "$(jget "$first" 'len(d.get("stuck_replies") or [])')" == "0" ]]; then
    ok "① 没有残留的 pending/running Agent 回复"
  else
    bad "① 仍有残留回复：$(jget "$first" 'd.get("stuck_replies")')"
  fi
  if [[ "$(jget "$first" 'len(d.get("hangs") or [])')" == "0" ]]; then
    ok "② 没有「任务 running 但投影全终态」的悬挂"
  else
    bad "② 悬挂任务：$(jget "$first" 'd.get("hangs")')"
  fi

  say "  再重启一次，检查事件账本是否稳定"
  MINDFS_ISO_KEEP=1 iso stop >"$LOGDIR/stop2-$n.log" 2>&1 || true
  MINDFS_ISO_KEEP=1 iso start >"$LOGDIR/restart2-$n.log" 2>&1 || { bad "第二次重启失败"; return 1; }
  wait_health || { bad "第二次重启后 health 不通过"; return 1; }
  sleep 3
  local second; second=$(report "$db")
  if [[ "$(jget "$first" 'json.dumps(d["counts"],sort_keys=True)')" == \
        "$(jget "$second" 'json.dumps(d["counts"],sort_keys=True)')" ]]; then
    ok "③ 第二次重启不新增事件（$(jget "$second" 'json.dumps(d["counts"],sort_keys=True)')）"
  else
    bad "③ 第二次重启改动了账本：$(jget "$first" 'json.dumps(d["counts"],sort_keys=True)') → $(jget "$second" 'json.dumps(d["counts"],sort_keys=True)')"
  fi

  iso stop >"$LOGDIR/stop-$n.log" 2>&1 || true
}

for round in $(seq 1 "$ROUNDS"); do
  run_round "$round" || true
done

echo "crash-sim: rounds=$ROUNDS fails=$fails"
[[ $fails -eq 0 ]]
