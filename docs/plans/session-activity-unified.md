# 单字段化：activity_at 合并进 updated_at

状态：Implemented 2026-10-25 → Shipped（`777e4b5c`，Scope: G-KA；`git tag --contains` 覆盖 v0.5.5-1095 起的全部 tag）。

## 目标

只保留 updated_at 一个字段；流式期间前端乐观推进；后端 EndSessionTurn 触摸 updated_at。刷新后不再回落到更旧的时间。

## 实施结果

- 后端：`Manager.TouchUpdatedAt`（`server/internal/session/manager_meta.go`）负责落库推进；回合结束由 `EndSessionTurn` 触摸，`server/internal/api/appcontext_task_flags.go` 在纯工具调用回合也会调它，避免会话停在旧时间。
- 前端：`activity_at` 字段、覆盖层与继承函数（`inheritClientActivity`）全部删除；`realtimeWsSessionStream.ts` 把 stream 事件时间按 100ms 量化后直接写 `updated_at`；`sessionListMerge.ts` 只按 `updated_at` 排序，同毫秒交错时按 pending 状态分桶，正在回复的会话不会被打回服务端旧值。
- 测试：删除 `web/tests/cross-node-completion-and-activity.test.mjs`；`web/tests/session-list-merge.test.mjs` 收缩到单字段语义。

## 事实核查（2026-11-20）

全仓 `activity_at` 零引用、`inheritClientActivity` 零引用；实现提交在 HEAD 且被 v0.5.5-1095 … v0.5.5-1200 全部包含。`docs/plans/README.md` 原先把它记成「待批准（未实现）」属于索引错误，已按归档判据移入历史表。
