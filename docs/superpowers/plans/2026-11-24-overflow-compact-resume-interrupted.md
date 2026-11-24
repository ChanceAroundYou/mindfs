# 溢出压缩被用户重试打断：自动重发闭环修复

> Scope IDs: G-QU（claimed 2026-11-24T08:40Z）

## 现场证据（2026-11-24，本机 v0.5.5-1400-25-g74ceff29）

用户重启后第一条消息 `我重启好了 请检查` 仍然失败，第二条很快又能用。journal 与会话块流逐项对齐：

- `16:25:13` 用户回合落盘（seq45）；`16:25:14` CLI 返回 `result:"Prompt is too long"`、
  `terminal_reason=blocking_limit`、`usage.input_tokens=0`、耗时 191 ms，代理没有任何
  HTTP 请求/usage 行（只有 `16:25:10 first request /api/hello`）。这说明失败发生在 **CLI 本地**：
  它在发请求前用本地 transcript 判窗口，200k 默认窗口下 `contextWindow=200000`；
  MindFS 的 live compact 没机会改这条请求体，用户看到的胶囊节省量不是 CLI 本地窗口的证据。
- `16:25:14` G-QT 正确登记 `turn.overflow.resume.armed`，`16:25:16` 排 `/compact`。
- `16:29:02` 用户按下停止（`session.cancel target_turn=2`），随后重发同一段话。
  `finishHygieneCompact` 因 `Request was aborted.` 记 `auto_failed`，并在 `16:29:02` 打出
  `turn.overflow.resume.drop reason=user_turn` —— G-QT 的待办被用户重试清掉。
- `16:30:24` harness 终于报 `compact_result:success`，但待办已经不存在；用户第三条消息才成功。

结论：不是「窗口没满，MindFS 误判」，而是 **CLI 本地 transcript 真撞 200k**；
G-QT 已经排上自动重发，但界面文案「完成后请重新发送」诱导用户手动重试，
用户重试先取消压缩、再按「用户新回合」语义丢弃待办，自动闭环因此被打断。
另外成功后 `compact_error` 仍残留「harness 没有报告压缩事件，这次压缩没有生效」，
进一步误导用户继续手动操作。

## 修复

| # | 内容 | 测试 |
|---|---|---|
| T1 | 溢出且已排压缩时的文案改为「完成后会自动重发，无需手动发送」 | 文案断言 + 非压缩分支保持原义 |
| T2 | 用户显式停止压缩回合时拒绝取消（只对 pinned target turn；内部取消不受影响） | compactPending 下 pinned cancel 返回错误；无 pending / 非 pinned 照常取消 |
| T3 | harness 压缩成功后清掉 `compact_error`，不再残留误导性失败说明 | FinishCompact(err) → MarkCompacted → Snapshot 的 compact_error 为空 |

## 边界

- 保持 200k 默认窗口，不用 `[1m]`；不新增按请求大小自动压缩策略。
- 只拒绝「用户点了停止且目标回合就是压缩回合」这一条路径；task/workflow 内部取消不动。
- 压缩真的失败（非用户取消）仍按既有语义丢弃待办，不无限重试。

## 验收

- 先写 RED 测试再改生产；每处至少一次变异，必须因断言失败变红。
- `go test -count=1 ./server/internal/api/usecase/ ./server/internal/api/`。
- 提交标 `Scope: G-QU`，登记 `docs/upstream-customizations.yaml`，跑 `make check-upstream`。
- 默认不 push / 不部署 / 不重启任何节点；需要上线由用户单独授权。

## 执行记录（2026-11-24）

三条 RED 先红：旧文案仍写「请重新发送」；pinned cancel 返回 `<nil>`；`MarkCompacted` 后 `compact_error` 仍在。实现后三条全绿。三处变异各自因断言失败打红后还原：M1 恢复旧文案 → 文案用例红；M2 取消闸门恒 false → pinned cancel 用例红；M3 删 `rec.compactError = ""` → 清理用例红。

门禁（本轮实跑）：`go test -count=1 ./server/internal/api/usecase/ ./server/internal/api/` 两包 ok；`go test -count=1 ./...` 全量 ok（`server/internal/kanban` 315.645 s）；`go build ./...` exit 0；`npx tsc -b --noEmit` 无输出；`npx vitest run` 274 文件通过 / 5 跳过、968 通过 / 6 跳过。`make check-upstream` 在提交后单独复核。

边界与未验证：本机运行进程已由用户重启到 `v0.5.5-1400-25-g74ceff29`（PID 2656365，exe 与安装文件同 inode，health 200），但本次修复尚未提交、未 push、未部署、未重启任何节点，所以线上真实长会话里的自动重发闭环仍需部署后重新取真实窗口；未做真机点验「压缩中按停止」的提示呈现。内部无 pinned 目标的取消仍照常生效；压缩真的失败仍按既有语义丢弃待办。
