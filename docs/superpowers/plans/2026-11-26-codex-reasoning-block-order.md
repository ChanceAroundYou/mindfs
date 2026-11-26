# Codex 思考块按真实发生时间定位

> Scope IDs: G-RZ（claimed 2026-11-26T07:18Z）

Plan 2026-11-26。用户报「Codex 回合末尾经常出现一个异常大的思考块，难道不应该在开头吗？」只读取证已定位为落库/排序链路问题，不是模型行为：`response_item/reasoning` 在 `06:23:02.374Z` 先于正文 `06:23:02.380Z` 完成，思考内容开头是「我需要回答用户…」、结尾是「Let's final in Chinese.」，确实是作答前的规划。

## 取证结论

- 会话：`/home/nnb/.codex/sessions/2026/11/26/rollout-2026-11-26T12-40-44-01a21627-*.jsonl`；MindFS 落库：`/home/nnb/projects/HBSN/latex/.mindfs/sessions/1793614627-3fb661ae7266.{aux,blocks}.jsonl`。
- 同一轮（seq 667）：正文 2586 字符 / 93 行，思考 summary 6192 字符。`aux.jsonl` 记录 `line:93`（恰好等于正文末行），`blocks.jsonl` 落成 `text pos=0`、`reasoning pos=1`，前端按 `(seq,pos)` 升序渲染，思考块因此显示在答案之后。
- 根因一：Codex 的 reasoning summary 只在 `item.completed` 时完整。SDK 把流式 `item/reasoning/summaryTextDelta` 累积进 item 的 `text` 字段（`codex-go-sdk` `app_server_exec.go` 的 `applyTextDelta`），而 typed `ReasoningItem` 只解析 `summary`，`item.updated` 到手时内容为空；且 `item.started` 的 reasoning 分支被适配器直接忽略（`server/internal/agent/codex/session_events.go:160`）。
- 根因二：`flushThought()` 用 flush 时刻的正文行数当锚点（`server/internal/api/usecase/session_send.go:188-199`）。思考文本在正文流完之后才到，于是被锚到 `line=93`。

## 目标 / 非目标

**目标：**

- 思考块的位置由它**真实开始**的时间/事件顺序决定，而不是 flush 时刻的正文行数。
- Codex 的 `item.started` / `item.updated`（reasoning）作为「推理已开始」的实时信号，把锚点定在当时的正文行数；summary 稍后（`item.completed`）到达时沿用该锚点。
- 确实发生在正文之后的思考（没有任何提前信号、首个片段就在正文末行之后）仍然排在后面——不做「reasoning 永远排第一」的特判。

**非目标：**

- 不改 Codex 协议、不 fork/patch SDK、不恢复流式 summary 正文（SDK 的 typed item 已丢掉 `text`，本次只取时序信号）。
- 不改 Claude/ACP 的行为；它们逐块流式到达，锚点仍在首个片段处，语义不变。
- 不改 provisional 块、不迁移历史数据、不改前端渲染顺序。

## 设计

1. `agenttypes.ThoughtChunk` 增加 `Start bool`（仅内部时序信号，不进入 WS）。
2. Codex 适配器在 reasoning 的 `item.started` / `item.updated` 上发出 `ThoughtChunk{ID, Start:true}`；`item.completed` 仍发正文 summary。
3. `session_send.go` 用一个小型 `thoughtAnchor` 累积器：`Start` 到达时记录当前正文行数；内容到达时若尚无锚点则就地补锚；flush 时使用记录的行号。上一个思考未收口时不被新信号改写锚点，空缓冲时允许新 item 重新锚定。
4. `updateToEvent` 丢弃 `Start` 标记，避免 WS/前端看到空思考块。
5. 补 `thoughtAnchor` 单测（复刻 line=93 场景与「思考确实在正文后」场景）和 Codex 适配器的事件时序单测。

## 验收

- 复刻上一轮：`Start` 在正文 0 行时到达、summary 在 93 行后到达 → aux `line=0`，`BuildTurnBlocks` 输出 `reasoning pos=0`、`text pos=1`。
- 对照组：没有 `Start`、首个片段在 93 行到达 → `line=93`，思考仍在正文之后。
- 定向 Go 测试 + `make check-upstream` 全绿；不 push、不部署、不重启任何节点。