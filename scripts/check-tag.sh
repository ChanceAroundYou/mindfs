#!/usr/bin/env bash
# 打 tag 之前的前置校验。由 `make tag TAG=v1.2.3` 调用，也可单独跑。
#
# 为什么要有它：tag 是一次发布的锚点 —— 四端版本号、APK versionCode、更新清单
# 全从它来。打错了改不掉（改 tag 等于让已装的机器认不出新旧），所以四件事必须在
# 打之前校验，而不是留到 `make release` 才炸（那时产物已经构建完了）。
#
# 用法：
#   bash scripts/check-tag.sh v1.2.3
#   MINDFS_ROOT=/path/to/repo bash scripts/check-tag.sh v1.2.3
set -euo pipefail

TAG="${1:-}"
if [[ -z "$TAG" ]]; then
  echo "Usage: bash scripts/check-tag.sh v1.2.3" >&2
  exit 1
fi

ROOT="${MINDFS_ROOT:-$(git rev-parse --show-toplevel 2>/dev/null || true)}"
if [[ -z "$ROOT" ]]; then
  echo "Error: 不在 git 仓库里（可设 MINDFS_ROOT 指定）。" >&2
  exit 1
fi
git_c() { git -C "$ROOT" "$@"; }

# 1 工作区干净：tag 会指向一份没人能复现的树。
if [[ -n "$(git_c status --porcelain)" ]]; then
  echo "Error: 工作区不干净，先提交或 stash —— tag 会指向一份没人能复现的树。" >&2
  git_c status --short >&2
  exit 1
fi

# 2 不在半截合并里：tag 一个带 MERGE_HEAD 的状态，推送后历史不可用。
merge_head="$(git_c rev-parse --git-path MERGE_HEAD)"
[[ "$merge_head" == /* ]] || merge_head="$ROOT/$merge_head"
if [[ -f "$merge_head" ]]; then
  echo "Error: 仓库处于半截合并中（MERGE_HEAD 存在），先完成或中止合并。" >&2
  exit 1
fi

# 3 TAG 形状：v<主>.<次>.<修>[-<构建号>]。与 install.ps1 / gradle 的解析一致 ——
#   形状不对的话，四端各自用不同方式读错版本号，比直接拒绝难查得多。
if ! printf '%s' "$TAG" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$'; then
  echo "Error: TAG 形状应为 v<主>.<次>.<修>[-<构建号>]，实际 '$TAG'。" >&2
  exit 1
fi

# 4 tag 尚不存在：重复 tag 的 git 失败最容易被当成「已经打过了」。
if git_c rev-parse -q --verify "refs/tags/$TAG" >/dev/null; then
  echo "Error: tag $TAG 已存在。发布过的 tag 不改；要重发请用新版本号。" >&2
  exit 1
fi

# 5 发布说明首行版本与 TAG 一致（make release 也查，但那时产物已构建）。
NOTES="${RELEASE_NOTES_FILE:-release-notes.md}"
[[ "$NOTES" == /* ]] || NOTES="$ROOT/$NOTES"
if [[ ! -f "$NOTES" ]]; then
  echo "Error: 发布说明不存在: $NOTES" >&2
  exit 1
fi
first="$(sed -nE '1s/^#[[:space:]]+MindFS[[:space:]]+([^[:space:]]+).*$/\1/p' "$NOTES")"
if [[ "$first" != "$TAG" ]]; then
  echo "Error: $NOTES 首行版本 '$first' 与 TAG '$TAG' 不一致。" >&2
  exit 1
fi

echo "✓ tag 前置校验通过：$TAG"
