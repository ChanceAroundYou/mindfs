---
name: upstream-merge
description: worktree 先真合出冲突→逐 hunk 定解的上游合并 SOP；分叉点前移、零漏嫁接、门禁接回。触发词：合并上游/upstream merge/升级合并/分叉点前移
---

# Upstream Merge — worktree 先真合 → 逐 hunk 定解

> 沉淀自 v0.4.7(18b10ca)→v0.4.9(38833f6) 合并（main@a86bb16，2de84fe 双 parent，19 冲突，38 agents 对账 0 missing）。以后重文件改走逐 hunk，不再 `checkout --theirs`。

## 触发词

`合并上游` `upstream merge` `升级合并` `分叉点前移` `worktree merge`

## 铁律（违反=丢 hunk）

1. **禁 `--theirs` 整文件取舍**：`App.tsx / FileTree.tsx / SessionViewer.tsx` 等重文件必须逐 `<<<<<<<` 段并集，`checkout --theirs/--ours` 仅用于新增/删除文件。
2. **分叉点可验证**：`git merge --no-ff` 产生双 parent，`merge-base --is-ancestor v0.4.X HEAD` 必须为真。
3. **main 只读到接回**：全部 `M+F` 在 `../mindfs-merge-X` 内完成，`main` 不动、可随时 `worktree remove` 回滚。
4. **门禁不过不接回**：`go vet / go test / tsc --noEmit / web 18+ / make build` 全绿 + `upstream/main...HEAD` 仅本地定制。

## 工作流

### 0. 准备（10 min，产出重叠表）

- 冻结 `docs/upstream-customizations.md` 按 hunk 互斥分组（同 commit 跨组，App 占多组）。
- `git fetch upstream --tags && git diff --name-only v0.4.7..v0.4.X | sort > /tmp/A && git diff --name-only v0.4.7..HEAD | sort > /tmp/B && comm -12 /tmp/A /tmp/B` 得交集（~45 文件），标红 `App/FileTree/SessionViewer/http/ws/fs`。
- 列 `v0.4.7..v0.4.X N commits / 76 files` vs `v0.4.7..HEAD 118 files`。

### 1. 真合（worktree，事实触发范围）

```bash
git worktree add ../mindfs-merge-X v0.4.X   # 如 v0.4.9=38833f6
cd ../mindfs-merge-X && pwd                 # Bash 每次调用后 cwd 重置回主仓，必须显式 cd 并 pwd 自证
ln -sfn "$PWD/../mindfs/web/node_modules" web/node_modules   # worktree 冷启动缺 node_modules
# ⚠️ `git merge --no-ff ../mindfs` 不可用（报 "not something we can merge"）：
#    git merge 不接受本地仓库路径。必须先 fetch 成分支再合：
git fetch /home/xiaokubao/projects/mindfs main:refs/heads/local-main
git merge --no-ff local-main -m "merge: upstream v0.4.X (N commits, M files)

Merge v0.4.X to advance merge-base from v0.4.7 to v0.4.X.
Conflicts resolved hunk-by-hunk (no --theirs).
Scope: merge"
# 收集两张清单
git diff --name-only --diff-filter=U              # U：真冲突（本次 19）
git diff --name-only v0.4.7..v0.4.X | sort > /tmp/A; git diff --name-only v0.4.7..9334d5a | sort > /tmp/B; comm -12 /tmp/A /tmp/B  # 重叠（含已自动合）
git status --short --branch                        # 冷启动：node_modules / 新增/删除文件
```

### 2. 分拣（决定“逐冲突”还是“逐文件复核”）

| 桶 | 做法 |
|---|---|
| `U` 真冲突 | 逐 `<<<<<<< HEAD / ======= / >>>>>>> main` 段，`git show <sha>:<file>` 对照，手合 |
| `重叠\U` 已自动合 | 逐文件 `git show <sha>:<file>` 复核语义并存（如 `file.ts rawFileFailures TTL+Promise`、`App onRefresh(tab)` + `handleTabRefresh`） |
| `v0.4.X 新增` | `present` 校验（`cli/cmd/autostart`/`meta_location.go`/`codebuddy.svg` 等） |
| `本地新增` | `present` 校验（`deploy/prefix`/`nodeRegistry` 等） |

### 3. 逐 hunk 定解（一次 F，不 cherry-pick）

- 每段 `HEAD vs MERGE_HEAD vs base` 对照，原则：**功能以上游为主/本地为辅 + 界面以本地为主/上游为辅**。
- 重文件按域并集而非二选一：
  - `FileTree onRefresh:(tab:ProjectTreeTab)` + `handleTabRefresh=()=>onRefresh?.(projectTreeTab)` + 顶部 `gap 0 / 56px+6px` + 按钮 `22px外框+18px内层`
  - `App refreshAfterNewest` 带 `nodeId: getNodeIdForRoot(root)` + `scopedRootKey(root)` + `options.waitForIncremental`
  - `BottomSheet flex:1 display:flex overflow:hidden` + `--panel-bg` 不透（`9334d5a`）共存
- 禁 `cherry-pick` 捷径；异常如 `stripMindfsPrefix→StripDeployPrefix` 吸收等需在 commit message `Scope: fixup` 分节写清。
- 产出：`git add <resolved> && git commit -m "fixup: resolve merge conflicts with upstream v0.4.X

- functional: ...
- ui: ...
Scope: fixup"`

### 4. 门禁（worktree 内，不过不接回）

```bash
export PATH="/home/xiaokubao/.local/share/go/bin:$PATH"
go vet ./... && go test ./...  # 含 server/internal/session -run TestManager
./web/node_modules/.bin/tsc --noEmit -p web/tsconfig.json
node --test web/tests/*.test.mjs  # 18+，含 upstream-restore.test.mjs
git merge-base --is-ancestor v0.4.X HEAD && echo "base moved: $(git merge-base HEAD v0.4.X | xargs git rev-parse --short)"
git diff upstream/main...HEAD --shortstat  # 仅本地定制
make build  # 35s 级
```

轻量对账（替代 38 agents）：`git diff HEAD..v0.4.X -- <file>` 反向查，空=完整（`release-notes.md` 位置移动除外）。

### 5. 接回（保留双 parent，不 cp -r）

```bash
cd /home/xiaokubao/projects/mindfs && pwd   # ⚠️ 必须绝对路径 + pwd 自证；cwd 会被重置，
                                            #    否则 fetch/merge 落在 worktree 里，主仓纹丝不动（"Already up to date"）
git status --short --branch  # 确认 main 干净
git fetch ../mindfs-merge-X HEAD:merge-X
git merge --ff-only merge-X
git branch -f merge-X main  # 对齐
rm -f ../mindfs-merge-X/web/node_modules   # 先摘软链，避免 remove 时误伤
git worktree remove ../mindfs-merge-X
git branch -D local-main merge-X           # 清理临时分支（回滚锚点是 backup/main-pre-X）
make install  # 覆盖 ~/.local/bin/mindfs + ~/.local/share/mindfs/web
# 你侧
systemctl --user restart mindfs.service
# 手测 5 项：⋮→空闲释放 72h / 星徽幻影冷抽屉 3 灰条 / 新项目 ~/.mindfs 切换 / 关联文件 +lines / 重连不丢 seq
git push origin main  # 普通 push，双 parent 可 revert -m 1 <M>
```

回滚：`worktree` 阶段 `git worktree remove --force`；接回后未 push `git reset --hard HEAD~2`；已 push `git revert -m 1 <M> && git revert <F>` 或 `backup/main-pre-*`。

## 产出物

- `M: merge + F: fixup` 双提交，`git log --graph --decorate` 可审计
- 门禁全绿 + `merge-base` + `upstream...HEAD` 范围证据
- 备份：`git branch backup/main-pre-X + tag + ~/.local/share/mindfs-backup-*`

## 反面案例

`--theirs` 覆盖 App/FileTree/SessionViewer 导致 6 段漏嫁接（3951aa4/466b21e/c0a4398/5941a36/d36cc53/8e7d857），需 `b2bbf27..b0ccc55` 6 次补丁 + 38 agents 对账才追回。本 Skill 以此为戒。
