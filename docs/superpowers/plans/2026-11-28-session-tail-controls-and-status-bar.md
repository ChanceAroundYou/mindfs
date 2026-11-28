# 会话尾部重构：轮次控件行只在完整结束后出现 · 模型/上下文/缓存常驻状态条

> Scope IDs: G-SK（claimed 2026-11-28T03:42Z）

任务 #137。用户反馈会话底部的「控件 + 状态」仍然时有时无、重复、运行中闪现/消失；此前已针对同一区域改过多次（G-SJ 等），要求先定位根因、去掉层层叠叠的补丁再改。本计划把这一块拆成两个互不依赖的部分：

1. **控件行**（复制 / fork / 回复完成时间）：只在一个**完全结束**的 assistant 轮次末尾出现一次。
2. **状态条**（模型 / 上下文 / 缓存命中）：会话底部常驻，运行中也显示、实时更新、不消失；按各 harness 实际上报的字段逐项显示。

## 现场与复现

用真实函数构造最小复现（临时脚本，已删），三个确定性的坏法：

| 复现 | 结果 |
|---|---|
| `applySessionContextWindow` 给最后一个条目补会话级窗口 | 同一轮的 `turnMeta` 被拆成两个对象 → 两个收尾边界 → **两条控件行** |
| `groupConsecutiveToolCalls` 折叠 ≥5 张同类工具卡 | 组卡不携带 `turnMeta` → 以工具卡收尾的轮次 **没有控件行**；且可跨轮合并 |
| 渲染判据 `!isStreaming || idx !== last` | pending 轮询翻转时，**已结束轮次**的控件跟着闪现/消失 |
| 另外 | 空轮次合成 `turn_meta` 假行（同步后底部刷出一堆控件）、瞬时/持久两份 meta 叠加 |

## 根因

1. **轮次身份不是一等公民。** `TurnMeta` 只被当作「页脚数据包」：补丁会复制/替换它，工具分组会丢掉它，谁也不保证「一轮恰好一份、且所有条目共用」。所有边界判据都靠相邻条目的形状反推，必然漏。
2. **完成判据错位。** 控件可见性依赖客户端 `isStreaming`（受 5 秒 pending 轮询影响），而不是服务端是否已经持久化这一轮。轮询翻转 → UI 翻转。
3. **状态与轮次混在一个组件里。** 模型/上下文/缓存在每个轮次页脚重复渲染；每轮的数据来源、缺失情况都不同，于是「有的轮有、有的轮没有」。

## 设计

| 位置 | 改动 |
|---|---|
| `sessionBlocks.ts` | `TurnMeta` 新增权威身份：`turnKey`（持久 `s<seq>` / 瞬时 `t<idToken>`）与 `completed`（**seq > 0 = 服务端已持久化 = 完全结束**）。`attachTurnFooter` 不再合成 `turn_meta` 假行；每轮所有条目共用同一个 meta 对象。 |
| `sessionTimeline.ts` | 工具分组只在 `turnMeta` 身份相同时合并，并把身份带到组卡上；删除 `applySessionContextWindow`（它只会拆散身份）。 |
| `sessionViewerFormat.ts` | 新增纯函数 `collectCompletedTurnEndIndices`：`turnKey → 该轮最后一行`。删除 `isAssistantFlowEnd`；控件位置只查这张表。 |
| `SessionViewerTimeline.tsx` | 删除 `isStreaming` 参与控件判据；每个已完成轮次只在表中位置渲染一次 `SessionViewerTurnActions`。 |
| `SessionViewerTurnActions.tsx`（原 `SessionViewerTurnMeta`） | 只保留复制 / fork / agent 图标 / 完成时间；不再展示模型、用量、上下文。复制反馈按 `turnKey` 记，跨条目/分组稳定。 |
| `shared/sessionStatus.ts` + `SessionViewerStatusBar.tsx` | 状态条数据：**每个字段各取最近一次有值的 exchange**，会话级缓存兜底；`context_window` / `token_usage` 缺失时沿用上一轮的值，新数据到了再替换（不闪空）。缺字段隐藏，不伪造 0。 |
| `SessionViewer.tsx` | 状态条固定在消息列表尾部、等待指示器之后、关联文件之前（随内容滚动，不吸底）。 |
| `useSessionStream.ts` | 去掉 `sessionContextWindow` 入参（不再需要窗口补丁）。 |

## 验证

- 新增/改写行为测试：恰好一条控件行/轮、工具分组保留且不跨轮、运行中瞬时轮无控件、控件与全局流式状态无关、空轮不造假行、状态条字段回退/缺失隐藏/容器常驻。
- 全量 Web 门禁：`npm run typecheck`，Vitest 全量（301 文件 / 1075 条，5 文件跳过）。
- `make build-web`；`make check-upstream`。
- 本机服务重启仍由用户执行；本轮不 push / 不部署 / 不重启任何节点。

## 排除

- 不改服务端持久化格式（`seq` 仍是权威身份；崩溃遗留 `partial` 行属于导入恢复场景，不在本次范围）。
- 不恢复「空轮次也要有一行统计」：状态条已经常驻承担模型/上下文，空轮次不再造可见假行。
- 不调整滚动/虚拟化策略；状态条按用户要求随内容滚动。