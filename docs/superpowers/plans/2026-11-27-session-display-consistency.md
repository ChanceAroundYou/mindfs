# 会话展示一致性：活跃子会话可见 · 状态不跳变 · 尾部统计与思考块去重

> Scope IDs: G-SI / G-SJ（claimed 2026-11-27T14:35Z）

任务 #132。用户在 pc-wsl / CMAI 的活跃 Claude Code 会话上观察到的三个通病，要求先定位根因再改，且修通用逻辑。以下证据来自线上只读取样 + 代码路径核对。

## 现场证据

- CMAI 父会话 `1795652359-56cf8c9bb730` 有 **80 条子会话**（`session-list.db`，0 条 closed）。侧栏折叠态只渲染前 3 条，且子会话只在「选中链路」上出现。
- `/api/replying-sessions` 曾同时出现两种错态：已完成的子会话 `1795740312`（最后一条产出 03:07Z，摘要写明已提交）持续 `replying` 约 1 小时；同一父会话下真正在跑的子会话反而不在表里。父回合结束后两者都消失 —— 说明泄漏生命周期绑定在父回合上。
- 父会话 blocks（25743 行）与 `.jsonl`（288 行）里都不存在重复的 598 字思考块：持久数据没有重复，重复只可能由前端窗口/overlay 两条来源叠加产生。

## 根因

1. **活跃子会话不可见**（`web/src/components/session/useSessionChildTree.ts`）
   - `activeParents` 只收「选中会话 + 祖先链」；非选中父会话的子会话一律不渲染。
   - 折叠态 `children.slice(0, 3)` 只按列表顺序取前 3，在跑的子会话既不被优先纳入，也不参与可见性判断；80 条子里它必然被埋掉。
   - `hiddenCount` 用展开时的基线 `collapsedBaseCount` 计算，折叠期间新到的子会话不计入隐藏数，toggle 会少报。

2. **状态在「执行中/已完成」之间跳变**（pending 真相表不完整且会残留）
   - `server/internal/api/usecase/session_subagent_stream.go` 的 receiver-thread 路径（Codex `collabToolCall`）创建子会话但**从不**调用 `OnSubAgentAttached`/`SetPendingReply`；服务端 `/api/replying-sessions` 永远不列它。前端 `markSessionPending` 在每个流事件上把它点亮，5 秒轮询又按服务端真相把它抹掉 —— 事件回合与轮询回合互相覆盖，灯以轮询周期闪。
   - `server/internal/api/usecase/session_subagents.go` 的合成子代理按 **ref key** 去重（`tool:`/`task:`）。同一个子会话若先后以不同 id 形状出现，会建出第二个 wrapper 并覆盖 pending 的回合身份；先关闭的那个 wrapper 因身份不匹配被 `PendingTurnIDMatches` 拒绝，另一个又收不到事件，于是已完成的子会话一直亮到父回合 `FinishAll`。
   - 看板的任务运行时判定（`sessionRunningProbe` = `IsSessionReplying`）读的是同一张表，残留条目会让任务头继续报执行中。

3. **尾部缺统计行 + 思考块重复**（窗口/overlay 与 turn footer 判据）
   - `SessionViewer` 的 `composedExchanges` 只按**对象同一性**去重；窗口与缓存里同一 `seq` 的两个不同实例会同时进入时间线。blocks 以 `seq` 为键，于是同一段 reasoning 被渲染两遍，且第二个副本排在尾部。
   - `isAssistantFlowEnd` 只把 `user_text`/`turn_meta` 当作回合边界；尾部跟着一条没有 `turnMeta` 的瞬时思考行时，上一条持久行的 footer 被抑制，统计行整体消失。

## 设计

| 位置 | 改动 |
|---|---|
| `useSessionChildTree.ts` | 「选中链路 + 所有在跑会话的祖先链」都可见；折叠态在首 N 条之外补入 pending/awaiting 子会话；隐藏数取 `max(基线, 实际条数) - N`，不再少报 |
| `SessionViewer.tsx` | `composedExchanges` 对持久行再按 `seq` 去重（窗口优先），瞬时行仍按对象同一性 |
| `sessionTimeline` / `sessionBlocks` | 丢弃与已渲染持久思考块内容相同的无 `turnMeta` 瞬时 thought；`isAssistantFlowEnd` 以 `turnMeta` 引用判定回合边界，无 meta 的后续条目视为新一轮 |
| `session_subagent_stream.go` | receiver-thread 子会话也走 `OnSubAgentAttached` / `OnSubAgentTurnFinished`，pending 与回合身份成对出现 |
| `session_subagents.go` | router 按**子会话 key** 去重 wrapper；终态摘要按 key 兜底找到已 attach 的子会话，避免漏关 |

## 验证

- 新增/更新 `web/tests/session-child-tree.test.mjs`（在跑子会话与祖先链可见）、`web/tests/session-window-overlay-dedup.test.mjs`（同 seq 去重、footer 边界）、`web/tests/session-tail-dedup.test.mjs`（重复思考块被丢弃）。
- Go：receiver-thread attach 置 pending、finish 清 pending；同 key 重复 attach 只有一个 wrapper 且关闭后 pending 清空。
- `make test`、`make build-web`、`go run ./scripts/check-upstream`。