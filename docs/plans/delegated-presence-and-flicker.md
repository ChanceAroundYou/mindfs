# 委托态空心灯 + 切换闪烁（已确认结论，未开工）

> 状态：根因已定位、短设计已确认，**未动手改代码**。等 B/C 根因一并批准后按 TDD 开工。
> 日期：2026-10-15。约定的验收：隔离实例复现 + 落盘为准（刷新/切换一致）+ 全门禁 + fork 三件套（登记 + 针对性测试 + `Scope: G-X`）。

## 一、委托态（父闲 + 子在跑）—— 缺一态，不是闪烁

- 现象：主会话把任务交给子代理后，主会话可正常交流（不卡），但主会话灯灭、子会话常亮 → 误以为完成。
- 根因（实证）：父 `session.done → EndSessionTurn/ClearSessionPending(parent)` 后父条目灭，但子 `SetPendingReply(child)` 仍 `Active`。前端只有"亮/灭"两态可表达，父只能灭。
  - 服务端：`server/internal/api/stream_hub.go:701 SetPendingReply` / `:867 ListReplyingSessions` / `:959 ClearSessionPending`；`server/internal/api/usecase/session.go:796 ensureSubagentSession` / `:827 startSubagentSubscription`；`server/internal/api/ws.go:930 OnSubSessionCreated` / `:941 OnSubSessionUpdate`。
- 设计（已确认）：新增第三态 `delegating = !selfPending && 任一后代 pending`，空心灯表达。
  - 优先级：自跑（实心）> 委托中（空心）> 全闲（灭）；子灯保持现状。
  - 后代收集复用 `web/src/shared/sessionTree.ts:38 collectSessionSubtreeKeys`（已排除 fork），与 pending 真相表用 `scopeSessionKey(nodeId,…)` 求交；Claude 子代理与 Codex 子会话都算（都走 `parent_session_key`，不区分 provider）。
  - 纯函数拟放 `web/src/shared/sessionPresence.ts`：`getSessionPresence(selfPending, descendantPending) → running|delegating|idle`。
  - 三处接线（与现小圆灯同范围）：列表 `SessionCard.tsx:391` 实心分支旁加空心分支（同位同尺寸、透明底+彩边、新 aria-label）；viewer 等待指示器 `SessionViewerWaitingIndicator.tsx:16-22` 加常驻分支（文案"子代理运行中"，新 key `session.subagentRunning` / `sessionList.delegating`，中英同步：`zh-CN-sessions.ts:38,109-110` / `en-US-sessions.ts` 对应行）；`SessionViewerHeader.tsx` 当前无状态位，需新增 prop 加同样式小点。
  - 委托态下底部文案常驻"子代理运行中"，不闪。
  - 后端 child 路由/持久化明确 OUT（前端表达层 only）。

## 二、切换闪（"亮一会又灭" + "正在思考闪现"）—— 陈旧快照首帧

- 现象：切会话后灯亮一会又灭；底部"正在思考/已发送等待"出现一会再消失。
- 根因（已读完触发链）：唯一写入点 `web/src/app/useSessionSelection.ts:97-122`。
  ```ts
  const preservePending = currentDrawer?.key === key
    ? !!currentDrawer?.pending
    : !!session?.pending;   // ← 陈旧快照，未经真相表校验
  setSelectedSession(toSessionItem(targetRoot, { ...session, pending: preservePending }));
  // 之后才 async 经 cache/persisted/restoreActiveSession 逐级纠正
  ```
  - `pending` 唯一真相是 `multiProjectPendingByKey`（WS 乐观更新 + `/api/replying-sessions` 5s 轮询 `useReplyingSessionPoll.ts:82-126`，增量合并 `appSession.ts:140 mergeReplyingStateByNode`，投影 `appSession.ts:157 applyPendingToMultiProjectGroups` 经 `scopeSessionKey(group._nodeId…)`）。
  - `drawer/session/cache` 上的 `pending` 都是快照。首帧用快照即时渲染 → `isAwaiting=true`（`SessionViewer.tsx:128`）→ `SessionViewerWaitingIndicator` 直接渲染 `sentWaiting`；随后 `restoreActiveSession`（`useSessionLifecycle.ts:97-295`，经 `resolvePendingForSession` 查真相表 `useSessionPending.ts:93-115`）纠正 → 灯灭/文案消失。中间那一帧就是闪。
  - 次要放大器：`useSessionStream.ts:51-106` 的 `subscribe` 会重放 `dispatcher.pendingStreams` 队列（`sessionDispatcher.ts:94-106` 无人订阅时缓存 200 条），切过去瞬间 `onStream` 置 `isStreaming=true`；导出值虽有 `&& sessionPending` 闸门（`:123`），但 `isAwaiting` 那路无闸，照样闪。dispatcher 自身状态机是对的（`message_done` 非终止 `:157-159`，清状态只在 done/error）。
  - `handleSelectSession` 后续 `applySession`（`:127-175`）已用 `resolvePendingForSession` 纠正，说明"先快照后纠正"是故意的即时渲染策略——修的是首帧，不是纠正链。
- 设计（已确认）：首帧 `preservePending` 改走 `resolvePendingForSession(targetRoot, key, false)` 校验真相表（ref 读，不进依赖数组，无身份抖动）；`onStream` 置 `isStreaming` 前加 `sessionPendingRef` 闸，队列重放不再点亮。覆盖所有切换场景（选会话/切项目 bound 恢复/`tryShowBoundSessionForRoot`/跨节点）。

## 三、开工顺序（批准后）

1. RED：纯函数单测 + 切会话无闪回归 + 隔离 E2E（父空心常驻、子实心、刷新/切换一致、无闪现），先失败。
2. 最小实现（`useSessionSelection` 首帧 + `sessionPresence` + 三处接线 + i18n 双语）。
3. 全门禁：前端 typecheck/test/build + `make test` + `make check-upstream`；定制登记 + 提交 `Scope: G-X`。
4. 部署/重启另行批准（本机重启只由用户执行）。

## 四、新增问题（B/C，只读调查结论，未开工）

### B：子会话 "Independent whole-branch review" 卡死、停止按钮失灵 —— CONFIRMED

现场（只读，未碰服务）：
- 会话行：`1792065145-baadd0e7dbd9`，parent=`1792047067-e7f430da1a1b`（即".claude同步优化"），jsonl **0 字节**（无 aux/blocks），从未落过一笔。
- binding：`agent=claude, agent_session_id=claude-subagent:call_01a13f679bd777efa64c44ecdb5a0a13, ctx_seq=0` —— Claude 合成子代理路径。
- 转录：`subagents/agent-a39a0c2aa86a8a2b8.jsonl`（meta 对上 description/toolUseId），123 行，最后一行 12:09:42Z（之后再无输出，CLI 侧已死）。
- `/api/replying-sessions` 仍列出该子会话 `status=replying, UpdatedAt=12:09:42Z` —— pending 条目泄漏，灯常亮。

根因链（"我认为取消链是断的，因为三处都是 no-op"）：
1. Claude 合成子代理**从不注册 activeTurn**：`session_subagents.go:177-189 attach()` 只调 `attachBackgroundSessionUpdates(context.Background(),…)`，没有 `registerActiveTurn`（对比 Codex 协作路径 `session.go:835` 有）。`CancelSessionTurn`（`session.go:1403-1449`）按 `active==nil → return nil` 直接返回 —— 静默成功，实际什么都没掐。
2. 即使注册了也掐不动：`syntheticAgentSession.CancelCurrentTurn() { return nil }`（`session.go:763`），无条件空操作。
3. 取消路径**不碰 pending 表**：pending 只由 `EndSessionTurn→ClearSessionPending` 清（`stream_hub.go:979`，且 turnID 必须匹配，`PendingTurnIDMatches` 失配则整条丢弃 `:949`）。死子代理永远等不到 `MessageDone`（转录已停）→ `FinishAll`（`session_send.go:359`）又是**按 turn 新建 router**（`:178`），跨 turn 的旧孩子没人收 → 灯永久常亮、按钮永远"失灵"。

修向（待批）：取消必须能按 key 杀死合成子会话并清其 pending（含 turnID 失配/无 activeTurn 的兜底）；`FinishAll` 或等价收尾必须覆盖跨 turn 残留孩子。注意只杀该 child，不碰父 turn。

### C：父会话"更新一句→完成→几秒后又输出"，灯灭又亮 —— 主假设（证据支撑，待你确认一条行为）

服务端日志时间线（`journalctl -u mindfs`，只读）：
- 21:20:08 父新输入 → 21:20:20 `session.ask_user` webpush（4 个问题卡片已展示）→ tool_use 增量 → 21:22:40 `result success` + `[ws] session.done`（request=msg-c361…）→ 灯灭；
- 21:22:57 新输入 "ok" → tool_use 增量持续（21:23:01…21:24:22）→ 灯亮。

主假设："看到的'完成→又跑'是**真 done，不是闪烁** —— AskUserQuestion 把一个逻辑任务切成了多个 turn：提问轮结束（done、灯灭）→ 你回答后起新轮（灯亮）。灯忠实反映了 turn 状态，但你的心理模型是任务级连续，所以觉得它'明明在跑却灭了'。"
- 佐证：`AnswerQuestion`（`session_stream_events.go:13-55`）只做 `MarkPendingAskUserAnswered` + 转发答案给 agent，**不置 pending、不开新 turn**；若经卡片回答后 agent 续输出，那段输出是没有 turn 身份的"黑户输出"（灯不亮却有字，另一种迷惑）。而你这次是经 composer 回的 "ok"（`[agent/claude] input content="ok"`），走的是新 turn，所以是"灭→亮"形态。
- 与 A 的区别：A 修的是"同 turn 内的假闪"，C 是"跨 turn 的真 done 但不符合任务直觉"。修向二选一（待你拍板）：(a) 提问等待期视为仍在跑（done 延后/pending 保持到答案落地）；(b) 保持 turn 语义，用委托态空心灯覆盖"等你回答"（前端已能表达"非跑非闲"）。

请确认：C 你当时是**点了卡片回答、还是在输入框 typed ok**？这决定修 (a) 还是 (b)。B 可独立先修。
