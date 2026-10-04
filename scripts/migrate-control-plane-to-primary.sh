#!/usr/bin/env bash
# 把运行节点（WSL）独有的控制面数据并进主节点（本机）。
#
# 背景见 docs/multi-node-control-plane.md：控制面只有一份真相，在主节点上。
# 模板库/提示词库今天每台机器各一份且已经漂移（模板 3 vs 5、阶段模板只有
# PC 有），本脚本把 PC 独有的条目按 id 并集进本机。
#
# 幂等：按 id 判重，第二次跑检测到已存在即跳过，diff 为空。
# 只做加法，冲突一律**本机胜**（本机是权威，且用户可能已经改过）。
# 写前备份 .bak-<ts>；**不删 PC 上的任何文件** —— 冻结而非销毁。
#
# 用法:
#   bash scripts/migrate-control-plane-to-primary.sh              # 预览，不写
#   bash scripts/migrate-control-plane-to-primary.sh --apply      # 真写
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

APPLY=0
REMOTE_HOST="${MINDFS_WSL_HOST:-wsl}"
LOCAL_CFG="${MINDFS_CONFIG_DIR:-$HOME/.config/mindfs}"
# 远端配置目录必须在**远端**展开。这里的 $HOME 是本机的（/home/xiaokubao），
# 直接拿它拼路径会去读本机目录 —— 脚本会安静地「迁移」了 0 条，看起来还成功了。
# 让远端自己展开：REMOTE_CFG 传的是相对路径或空串，由下面的 ${REMOTE_CFG:-$HOME} 在远端解析。
REMOTE_CFG="${MINDFS_WSL_CONFIG_DIR-}"

for arg in "$@"; do
  case "$arg" in
    --apply) APPLY=1 ;;
    -h|--help) sed -n '2,15p' "$0"; exit 0 ;;
    *) echo "未知参数：$arg" >&2; exit 2 ;;
  esac
done

command -v jq >/dev/null || { echo "需要 jq" >&2; exit 1; }

[[ "$APPLY" == "1" ]] && echo "模式：写入" || echo "模式：预览（加 --apply 真写）"

TS="$(date +%Y%m%d-%H%M%S)"

# 拉远端文件。远端没有就返回空 —— 「PC 没有 prompts.json」是实测事实，
# 不是异常。jq 校验失败也当空：宁可少迁，也不要把一个坏文件写进本机配置。
# 刻意不吞 stderr：ssh 连不上要看得见。
fetch_remote() {
  local name="$1" raw
  raw="$(ssh "$REMOTE_HOST" "cat \"\${REMOTE_CFG:-\$HOME}/.config/mindfs/$name\" 2>/dev/null" || true)"
  if [[ -z "$(printf '%s' "$raw" | tr -d '[:space:]')" ]]; then
    printf '[]'
    return
  fi
  printf '%s' "$raw" | jq '.' 2>/dev/null || printf '[]'
}

# 读本机文件；没有就是空数组。
fetch_local() {
  local name="$1"
  if [[ -f "$LOCAL_CFG/$name" ]]; then
    jq '.' "$LOCAL_CFG/$name" 2>/dev/null || printf '[]'
  else
    printf '[]'
  fi
}

backup_and_write() {
  local name="$1" content="$2"
  local dest="$LOCAL_CFG/$name"
  if [[ "$APPLY" != "1" ]]; then
    echo "    （预览）将写入 $dest"
    return
  fi
  mkdir -p "$LOCAL_CFG"
  if [[ -f "$dest" ]]; then
    cp -p "$dest" "$dest.bak-$TS"
    echo "    已备份 $dest.bak-$TS"
  fi
  printf '%s\n' "$content" > "$dest"
  echo "    已写入 $dest"
}

# merge_by_id <远端json数组> <本机json数组>
# 并集，id 相同取**本机**那条（用户可能已经改过，本机是权威）。
merge_by_id() {
  jq -n --argjson remote "$1" --argjson local "$2" '
    ($local | map(.id)) as $localIDs
    | (($remote | map(select(.id as $id | ($localIDs | index($id)) == null))) + $local)
    | sort_by(.name)
  '
}

# merge_strings <远端字符串数组> <本机字符串数组>
# prompts.json 不是对象数组而是一串纯文本（实测：["push吧", "..."]），
# 没有 id 可判重，只能按值去重。
merge_strings() {
  jq -n --argjson remote "$1" --argjson local "$2" '
    ($remote + $local) | unique
  '
}

report_counts() {
  printf '    %s: %s → %s\n' "$1" \
    "$(printf '%s' "$2" | jq 'length' 2>/dev/null || echo '?')" \
    "$(printf '%s' "$3" | jq 'length' 2>/dev/null || echo '?')"
}

# ── 1. task_template.json ──
echo "1/4 任务模板 task_template.json"
REMOTE_T="$(fetch_remote task_template.json)"
LOCAL_T="$(fetch_local task_template.json)"
MERGED_T="$(merge_by_id "$REMOTE_T" "$LOCAL_T")"
report_counts "task_template" "$LOCAL_T" "$MERGED_T"
backup_and_write task_template.json "$MERGED_T"

# ── 2. stage_template.json ──
echo "2/4 阶段模板 stage_template.json"
REMOTE_S="$(fetch_remote stage_template.json)"
LOCAL_S="$(fetch_local stage_template.json)"
MERGED_S="$(merge_by_id "$REMOTE_S" "$LOCAL_S")"
report_counts "stage_template" "$LOCAL_S" "$MERGED_S"
backup_and_write stage_template.json "$MERGED_S"

# ── 3. prompts.json ──
echo "3/4 提示词库 prompts.json"
REMOTE_P="$(fetch_remote prompts.json)"
LOCAL_P="$(fetch_local prompts.json)"
MERGED_P="$(merge_strings "$REMOTE_P" "$LOCAL_P")"
report_counts "prompts" "$LOCAL_P" "$MERGED_P"
backup_and_write prompts.json "$MERGED_P"

# ── 4. 不迁移的东西 ──
cat <<'NOTES'
4/4 刻意不迁移：
  nodes.json            以本机为准；PC 条目加 "remote": true 留在本机表里
  web-push 订阅         VAPID 私钥在 PC，密文绑定该密钥，搬过来解不开
                        → 需要你在本机页面重新点一次订阅
  session_naming        PC 自己的命名偏好，改它会让既有会话命名行为变化
  session_project_pins  键里含 node id，迁移要重写键，收益不抵风险
                        （键空间碎裂由节点 id 分裂造成，改 role 后新写入即自愈）
  会话库/任务库/registry  数据面本就该留在 PC
NOTES

if [[ "$APPLY" == "1" ]]; then
  echo
  echo "完成。下一步：给 $REMOTE_HOST 写 ~/.config/mindfs/config.json：{\"role\":\"worker\"}，然后重启。"
  echo "别忘了在本机页面重新订阅 Web Push。"
fi