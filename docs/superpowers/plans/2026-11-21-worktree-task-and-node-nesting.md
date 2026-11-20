# 任务树 + 节点树：真建、真合、w2 → w1 → main

Plan 2026-11-21，用户直接下令：「按你上一轮分析里『需要做的改动』的建议，把 worktree 这套问题真正修好」，
并要求 TDD（先写失败测试/复现，再实现，最后重跑复现路径确认红→绿），三条期望：

1. 任务级 worktree：建卡勾选后要真正建树；共享节点默认在任务树里工作；Run 进终态后自动合并回 main
   （只在服务端执行层做，不改 Router 的高优先级规则/提示词）。
2. 节点级 worktree：保持「仅该节点在独立树、结束自动合并并清理」，补齐冲突处理。
3. 嵌套：修掉「合并在主 checkout 执行、目标分支被任务树占用 ⇒ 节点直接 fail」（缺陷 A）与
   「节点分支从主 checkout HEAD 切」（缺陷 B），实现 w2 → w1 → main。

## 根因（代码取证）

- `Service.CreateTask` 从不建树；建树只剩显式 `rebuild-worktree` 接口（UI 走不到）。活库 #97 正是
  「勾了标记、`worktree_path` 为空」。
- `gitview.MergeBranch` 始终在 `MainDir` 里合并并先 `git checkout Target`；`validateTarget` 把
  「目标分支被其它 linked worktree 检出」直接判错 → 节点树合回任务树必失败。
- `gitview.AddWorktree` 在 rootPath 里 `worktree add -b <branch>`，新分支基线是主 checkout HEAD，
  不是任务树 tip。
- 一轮结束没有任何自动合并；节点分支清理用 `branch -d`，嵌套时按 main 的可达性判断，永远拒绝删除。

## 落地

1. `CreateTask` 勾了就 `ensureTaskWorktree` 建树，失败删任务并返回错误；用 `store.CreateTask`
   返回值继续（任务号是建树命名的依据）。
2. `snapshotRunIntegrationBranch` / `PrepareWorkflowNodeWorktree` 兜底 `ensureTaskWorktreeReady`，
   失败上抛（不回落主 checkout）。
3. `MergeOptions.TargetDir` + `gitview.MergeTargetDir` + `validateMergeTargetDir`：目标分支被某棵
   worktree 检出就在那棵树里合并；`TargetDir` 为空保持老的「占用即拒绝」。
4. `gitview.AddWorktreeFrom` + `WorktreeBaseRunner`：节点独立树从 `run.IntegrationBranch` tip 切。
5. `autoMergeTaskWorktreeAfterRun` 挂到 `projectWorkflowTaskStatus` 的成功分支：任务树并回主分支，
   registry 主行保持 active，冲突转 needs_user + 可见错误并保留现场。
6. `DeleteBranchMergedAt`：按「已并进 merged_commit」判定后删分支，兼容 oneline 形状。

## 验证

- 新增 `gitview/worktree_merge_target_test.go` 8 条（占用目标树里合并、老行为不回退、分支不符拒绝、
  MergeTargetDir 三态、基线建树、按 merged_commit 删分支正/反例、oneline 形状）。
- 新增 `kanban/workflow_task_worktree_test.go` 6 条（建卡即建树、失败不留孤儿卡、成功轮自动合并 +
  幂等、失败轮不合并、冲突转 needs_user、嵌套 w2→w1→main 且共享活可见、节点分支被清理）。
- 既有契约按新语义更新：`service_worktree_test.go`（建卡选项、任务号命名、重建失败留错误且不留陈旧路径）、
  `service_worktree_state_test.go`（不可用产出 ⇒ 建卡失败且无残留）、`service_worktree_multi_test.go`
  （主树 + 两棵节点树 = 3 条 active）。
- 变异验证（每处打红后还原）：建卡不建树、失败不删任务、`TargetDir` 置空、节点基线置空、自动合并
  钩子置空、合并条件改成恒真、`DeleteBranchMergedAt` 退回 `-d`、`AddWorktreeFrom` 忽略 base、
  目标目录分支校验去掉 —— 对应用例均按预期变红。
- 门禁：`go build ./...` 通过；`go test ./server/... ./cli/...` 全绿（kanban 全包复跑确认唯一红项
  `TestCoordinatorCrashReplayCompletesWithoutModelCall` 是既有负载 flake，单跑通过）。并行会话在飞的
  4 个未完成测试文件用 `go test -overlay` 排除，未改动它们。

## 未做 / 边界

- 前端建卡面板未加新入口（勾选本身已生效）；「重建 worktree」仍是显式修复入口。
- Router 仍不参与合并；冲突停在主 checkout 等人处理。
- 重启不会补合并历史轮次（没有恰好一次成功投影就不合）。
- 未部署、未重启任何节点。
