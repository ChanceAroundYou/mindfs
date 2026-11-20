#!/usr/bin/env bash
# 变异账本（Plan 2026-11-20 Task 9）
#
# 每条记录是一次「已知的错误改法」：注入到一份临时的 git worktree（从 HEAD 切出，
# 不带工作区里其他会话的未提交改动），跑指定的 Go 测试，测试必须变红。
#   变红 = killed   —— 确实有测试守着这行
#   仍绿 = survived —— 这行可以随便改而无人发现，账本上那条证据是假的
#
# 用法：
#   bash scripts/mutate.sh                    # 跑全部
#   MUTATION=<name> bash scripts/mutate.sh    # 只跑一条
#   MUTATIONS_FILE=/tmp/x.txt bash scripts/mutate.sh
#
# 退出码：0 = 全部 killed；1 = 有 survived / invalid / missing / ambiguous。
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MUTATIONS_FILE="${MUTATIONS_FILE:-$REPO_ROOT/scripts/mutations.txt}"
FILTER="${MUTATION:-}"

if [[ ! -f "$MUTATIONS_FILE" ]]; then
  echo "mutate: $MUTATIONS_FILE not found" >&2
  exit 1
fi

WORK="$(mktemp -d)"
WT="$WORK/wt"
cleanup() {
  git -C "$REPO_ROOT" worktree remove --force "$WT" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

if ! git -C "$REPO_ROOT" worktree add --detach "$WT" HEAD >"$WORK/worktree.log" 2>&1; then
  echo "mutate: cannot create a temporary worktree:" >&2
  cat "$WORK/worktree.log" >&2
  exit 1
fi

# inject 把 before 换成 after；before 必须恰好出现一次，否则这条记录本身是坏的。
inject() { # file before after -> ok|equal|missing|ambiguous
  python3 - "$1" "$2" "$3" <<'PY'
import sys

def decode(value):
    out, i = [], 0
    while i < len(value):
        ch = value[i]
        if ch == "\x01":
            # 账本里的 \p：字面竖线，进解码阶段才还原，免得和字段分隔符打架。
            out.append("|"); i += 1; continue
        if ch == "\\" and i + 1 < len(value):
            nxt = value[i + 1]
            if nxt == "t":
                out.append("\t"); i += 2; continue
            if nxt == "n":
                out.append("\n"); i += 2; continue
            if nxt == "\\":
                out.append("\\"); i += 2; continue
        out.append(ch)
        i += 1
    return "".join(out)

path, before, after = sys.argv[1], decode(sys.argv[2]), decode(sys.argv[3])
if before == after:
    print("equal")
    raise SystemExit(0)
with open(path, encoding="utf-8") as fh:
    text = fh.read()
hits = text.count(before)
if hits == 0:
    print("missing")
    raise SystemExit(0)
if hits > 1:
    print("ambiguous")
    raise SystemExit(0)
with open(path, "w", encoding="utf-8") as fh:
    fh.write(text.replace(before, after, 1))
print("ok")
PY
}

killed=0
survived=0
invalid=0

while IFS= read -r line || [[ -n "$line" ]]; do
  # \p 先换成占位符再切字段：before/after 里合法地会带 |（Go 的 ||），
  # 不转义就会被当成分隔符切碎。
  line="${line//\\p/$'\x01'}"
  IFS='|' read -r name file before after pkg run <<<"$line"
  name="$(printf '%s' "$name" | tr -d '\r')"
  case "$name" in
    ''|'#'*) continue ;;
  esac
  file="$(printf '%s' "$file" | tr -d '\r')"
  pkg="$(printf '%s' "$pkg" | tr -d '\r')"
  run="$(printf '%s' "$run" | tr -d '\r')"
  if [[ -n "$FILTER" && "$name" != "$FILTER" ]]; then
    continue
  fi

  target="$WT/$file"
  if [[ ! -f "$target" ]]; then
    echo "[invalid] $name: no such file $file"
    invalid=$((invalid + 1))
    continue
  fi

  verdict="$(inject "$target" "$before" "$after")"
  case "$verdict" in
    ok) ;;
    equal)
      echo "[invalid] $name: before and after are identical"
      invalid=$((invalid + 1))
      continue
      ;;
    missing)
      echo "[invalid] $name: before not found in $file"
      invalid=$((invalid + 1))
      continue
      ;;
    ambiguous)
      echo "[invalid] $name: before matches more than once in $file"
      invalid=$((invalid + 1))
      continue
      ;;
    *)
      echo "[invalid] $name: injection failed ($verdict)"
      invalid=$((invalid + 1))
      continue
      ;;
  esac

  log="$WORK/$name.log"
  ( cd "$WT" && go test "./$pkg" -run "$run" -count=1 ) >"$log" 2>&1
  status=$?
  git -C "$WT" checkout -- "$file" >/dev/null 2>&1

  if [[ $status -ne 0 ]]; then
    echo "[killed] $name"
    killed=$((killed + 1))
  else
    echo "[survived] $name: the tests below still pass — nothing guards this line"
    tail -n 15 "$log" | sed 's/^/    /'
    survived=$((survived + 1))
  fi
done <"$MUTATIONS_FILE"

echo "mutate: killed=$killed survived=$survived invalid=$invalid"
if [[ $survived -ne 0 || $invalid -ne 0 ]]; then
  exit 1
fi
