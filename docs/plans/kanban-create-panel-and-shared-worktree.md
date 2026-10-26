# 看板四项：模板菜单不弹窗 / 分支名输入 / 面板滚动 / worktree 复用

状态：Implemented 2026-10-26，待提交与 ship。

## 四个症状（用户 2026-10-26 报）

1. 「全部」模板页签下点「新建任务」再点 debug 等模板，**永远弹不出新建任务面板**。
2. 新建面板里的「新分支名称」是裸 `<input>`，与 MindFS 输入体系不一致。
3. 面板里任务文本一长，**滚不到底、点不到发送键**。
4. 希望**多个任务复用同一个 worktree**。

## 1 模板菜单弹不出来（G-KE）

`useTaskTemplates.ts` 造了 `taskTemplateActionMenuRef` / `taskCreateTemplateMenuRef`，
一路透传到 `TaskBoardToolbar`，但两个叶子菜单组件**不接受 ref**。于是
`taskTemplateCreateMenuRef.current` 恒为 `null`，点外关闭的 `mousedown` 监听把每次点击
都判成「点在外面」，菜单在 `click` 到达之前就被卸载。

修：两个菜单组件加 `ref?: React.Ref<HTMLDivElement>` 并挂到最外层定位 div；
工具栏把两个 ref 透传下去。同一个缺陷也静默打断了 ⋯ 菜单里的新建/编辑/删除模板。

## 2 分支名输入（G-KE）

原来是 `style={{margin:"8px 12px",padding:8}}` 的裸 input，还悬在正文容器外面。
改成 `composerInputStyle` + `height:"26px"`，搬进 worktree 控件行、紧挨
`WorktreeBranchSelector` —— 与任务名输入框同一套。

## 3 长文本滚不到发送键（G-KE）

`PromptEditor` 给 `TokenEditor` 传了 `fillHeight`，那条路径把 contentEditable 设成
`height:100% / maxHeight:"none"`：高度随内容无限长高；`composerContainerStyle` 只有
`minHeight` 没有 max-height；发送键是 `absolute + right:6/bottom:6` 挂在容器上 ——
于是跟着容器一起沉到视口外。

修：不传 `fillHeight`，走默认路径（`maxHeight: 240px` + `overflowY: auto`），
与对话输入框（`ActionBar`）同源。全仓只有这一处传 `fillHeight`。

## 4 共享 worktree（G-KF）

用户定的三条口径：入口沿用现有「已有分支」选择器、收尾按**引用计数**（非最后一个只解绑）、
并发**允许**（沿用现有按任务的 `beginRunAdmission` / `acquireTaskFinish`，不加跨任务锁）。

### 4a 建树时认领已检出该分支的 worktree

`ensureTaskWorktree` 在 `CreateTaskWorktree` **之前**插一步：`branchMode == "existing"`
时用 `worktreePathForBranch` 找「已检出这个分支、且**不是主 checkout**」的 linked worktree；
找到就把归属指向它，写库仍走唯一出口 `SetWorktreeRefsAndTask`。

不认领的话 git 必然报 `branch is already checked out`，而后端只能把这错原样抛给用户 ——
但那其实是合法请求。`!item.Current` 是关键：绝不把主 checkout 认成任务 worktree，
否则 agent 直接在主干上改。读不到列表就返回 false，落回原建树路径，由 git 报它自己的错。

副作用（刻意接受）：`RebuildTaskWorktree` 先清路径再调 `ensureTaskWorktree`，
于是共享场景下「重建」等价于**重新认领**那棵树 —— 正是想要的。

### 4b 收尾按引用计数分流

`TaskStore.WorktreeReferenceCount(ctx, path, excludeTaskID)`（新文件
`task_store_worktree_refs.go`，`SELECT COUNT(*) FROM tasks WHERE worktree_path = ? AND id <> ?`）。

`FinishTaskWorktree` 在 `assertWorktreeMatches` / `MergeBranch` **之前**分流：引用数 > 0 →
`ClearWorktreeRefs` 只解绑，`ReferenceReleased=true` / `RemainingReferences=n`，广播
`TaskUpdated` 后直接返回。位置不可换：再往后就是替兄弟任务合并、拆掉它正在用的目录。

查询失败不拦：落回完整收尾（宁可做多，不可假装解绑成功而让任务继续钉着一棵没人管的树）。

`FinishWorktreeAndRepoint` 在 `ReferenceReleased` 时**不搬会话**：搬移是为了救 cwd 已消失的
会话，而这里目录还活着，硬搬会把还在用的那棵树的上下文拆散。

### 4c 机械清场判定不看别人的脏

`PlanFinishWorktree` 拿到 `worktreePath` 后先查引用数，> 0 直接
`FinishPlan{Mechanical: true, Reason: "worktree 还有别的任务在用，这次只解绑"}`。
否则后面两条判据（未提交改动 / 合并可行性）看的都是**兄弟任务**的状态，据此退给本任务的
agent 等于让它去提交别人的半成品。

### 4d 明确不做

不加跨任务锁；不删「已共享」任务的目录/分支；分支选择器不标注「已被某 worktree 使用」。

**已知上限（刻意接受）**：引用计数只数**任务**，数不到会话。所以一个任务若认领了
`CreateSessionWorktree` 开出来的树、且是唯一持有者，收尾会照常合并并拆掉它 —— 会话那边
随后失去 cwd（可走 `repoint` 收尾）。这与「收尾只认任务归属」的既有语义一致，
用户要的正是「选了这个分支 = 在这棵树里干活」；真要保护会话树，得让收尾也数会话归属，
那是另一件事，本次不做。

## 兼容

- 无数据库迁移：`worktree_path` 无 UNIQUE 约束，新增查询只读。
- 无 API 形状变更：请求体不动；响应只加 `omitempty` 可选字段，TS 侧对应 `?`。
- 单任务 worktree 零行为变化：引用数为 1 时两条收尾路径都走原路；`ensureTaskWorktree`
  只在 `branchMode=="existing"` 且真的找到非主 checkout 的占用者时才改变行为。
- 旧前端不看 `reference_released`：解绑对它表现为「worktree 路径被清空」，与既有
  「收尾清归属」一致。

## 验证

- Go：`TestWorktreeReferenceCount`、`TestSharedWorktreeIsReusedByBranch`、
  `TestSharedWorktreeNeverClaimsTheMainCheckout`、`TestFinishSharedWorktreeOnlyUnbinds`、
  `TestPlanFinishWorktreeTreatsSharedWorktreeAsMechanical`（真 git）。
- Web：`task-template-menu-outside-click`、`task-create-panel-editor-bounds`、
  `worktree-shared-refs`。
- 门禁：`make test`、`make test-web`、`make check-upstream`、`make build-web && make build`。
