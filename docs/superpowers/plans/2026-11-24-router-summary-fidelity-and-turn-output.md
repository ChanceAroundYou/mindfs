# Router 总结可信化与回合输出收口

> Scope IDs: G-RI / G-RJ / G-RK（claimed 2026-11-24T18:54Z；合回 main 前撞号避让：原 G-RE / G-RF / G-RG 已被 Edge Relay 占用，按「后来者让号」整体改号）

任务 #115（`task_87a576cf87d91597`）。起点是只读诊断报告
[`router-summary-problem-analysis.md`](../../router-summary-problem-analysis.md)：
近期任务的 worker 完成汇报与 Router 总结对不上，卡片上留下的是被机械截断的半句话、
重复的「没有变化」，以及整轮被拒后用户侧零可见变化。

用户在 2026-11-24 12:19 裁定**整套采纳报告的 S1–S8**，不做缩水、不顺手改其它方向。

## 现场证据（只读，本机任务库）

- **整轮拒绝**：`router_decisions` 自 11-22 起 5 条 `rejected`，全部是同一句
  `ask_user must be the only action`（`task_79ce7b7a` 三条、`task_7280e0df` 两条），
  trigger 都是 `turn_settled`。这些回合没有来源留言，旧的 `fallbackRoute` 无处落脚，
  用户侧零可见变化 —— 而模型写的那段正文也一起丢了。
- **截断断在词中间**：`cutAtSentence` 把 ASCII `.` 当句末，`` `192.168.1.15:2222` ``
  这种内容会在点号处被切开。
- **重复的「没有变化」**：`task_44d332a5` 17:48 → 17:52 两轮巡检、`task_d404e3fb`
  02:16 → 02:29 等多次结算，正文内容高度重合（`turn_settled` 事件摘录 1001 字符，
  尾部一段是上一轮原文的复制）。
- **卡片读的是 Markdown 原文**：`task.result_summary` 里有 `` `反引号` ``、`**加粗**`、
  `- 列表`，看板卡 `whiteSpace: pre-wrap` 直出，用户看到的是符号本身。

## 设计

| 编号 | 位置 | 改动 |
|---|---|---|
| S1 | `router_tools.go` 校验 + 两个 finish 路径 | `ask_user` 可与 `say` 同轮：唯一约束从「只能一个动作」放宽成「同轮至多一个 `ask_user`，且其余动作只能是 `say`」；顺序归一到 `say` 在前、`ask_user` 在后（占位回复被第一条回复类动作占用），`Seq` 按新顺序重排 |
| S2 | `router_service.go` / `router_coordinate_turn.go` 拒绝分支 | 校验失败时先把决策里的纯文本 `say` 按 summary 应用（用户留言回合仍走 `fallbackRoute`，运行期事件回合没有旧路径可落），再写一条 `route-rejected:<decision>:<seq>` 可见说明；正文不再整轮丢弃 |
| S3 | `turn_summary.go` `cutAtSentence` + 存储上限 | 只在空白/换行/句末标点处截断，标点后必须跟空白或结尾（`192.168.1.15` 不再被切）；存储上限 600 → 1000，优先在段落边界切 |
| S4 | `activity_route_apply.go` `applyRouteSay` | 结算回合的总结若满足「本轮零工具调用」且「与卡片上已有摘要的三元组 Jaccard > 0.6」且「本轮没有作答提问卡」，降级为 `progress`，不覆盖卡片 |
| S5 | 新增 `inspection_dedupe.go` + `applyRouteSay` | 巡检回合里同一任务、同一触发类、同一状态指纹、归一化正文 hash 相同的汇报，30 分钟内只发一次（后到的标 hidden，不再写摘要）；prompt 规则 12 补「没有变化就 `silent`」 |
| S6 | `service_turn_settled.go` | 事件摘录从「只取尾部 1000」改为「头 500 + 尾 500」，中间省略处写明省掉多少字 |
| S7 | `router_prompt.md` 规则 18 | 四段改成固定标签行（`结论` / `证据` / `没做或未验证` / `下一步`），允许列表与行内代码、禁止表格，硬上限 800 字 |
| S8 | `web/src/shared/summaryText.ts` + `TaskCardRows.tsx` | 卡片渲染前剥掉 Markdown（与后端 `notify.stripMarkdown` 同语义），`title` 与正文一致 |

### S1 的顺序归一

`action.Seq = len(c.actions)` 是收集顺序，应用层按 Seq 单调返回。允许 `[ask_user, say]`
之后必须把它归一到 `[say, ask_user]` 并重排 Seq，否则 `applyRouteAskUser` 会先占走
占位回复，正文只能另起一条，用户看到「问题在前、解释在后」。归一放在校验前，
校验和落库看到的是同一份顺序，`ClaimAppliedSeq` 单调性不变。

### S2 的边界

判据是**触发类**而不是留言 ID：`TriggerKind == RouterTriggerUserMessage` 的回合走
`fallbackRoute`（按那条留言重新路由），其余（`turn_settled` / `run_settled` /
`turn_interrupted` 等运行期事件）走救回。

不能用 `decision.UserActivityID == ""` 判定 —— 运行期事件决策同样带
`user_activity_id`（线上 20 条 rejected 全部如此，`routerTriggerForEvent` 原样转发
`ev.UserActivityID`），按 ID 判空会把生产主路径整条漏掉；而用户留言回合若也救回，
同一段结论会被 `fallbackRoute` 与救回各写一遍。

生产上真正生效的是 `finishCoordinateEventTurn`（所有带 `Event` 的触发都从
`runRouterTurn` 走这里，`finishRouteTurn` 只剩用户留言），`finishRouteTurn` 的分支
只是同一判据的防御性一致化。

### S4 的口径与实测

按批准口径实现：`tool_calls == 0` **且** 与上一条摘要的字符三元组 Jaccard > 0.6
**且** 本轮没有新作答（`AnsweredQuestion` 为假）。实测量级记在这里，供以后调参：

- `#111` 连续摘要 Jaccard：0.051 / 0.041 / 0.131 / 0.085
- `#104`：0.067 / 0.006
- `#112`：多数 0.0–0.21，只有两对逐字相同（1.0）

也就是说这条闸门主要挡「原样重念」，挡不住「换句话再说一遍同一件事」；后者由 S5
的巡检去重与 S7 的固定四段共同压制。**不据此改口径**，因为用户明确要求按报告实施。

### S5 的指纹

`task_id | trigger_kind | task.Status`。同一指纹下正文归一（去空白、去标点、小写）后
hash 相同且距上次不足 30 分钟 → 抑制。进程重启后内存表丢失，代价是重启后可能多发
一条，可以接受（不做落库，避免为一条去重记录改动 schema）。

## 验证

- 定向：`go test ./server/internal/kanban/ -run 'Router|Summary|Excerpt|Inspection'`
- 全量：`go test ./...`、`npx tsc -b --noEmit`、`npx vitest run`、`make check-upstream`
- 变异：每条新断言都要证明「改坏就红」
- 复现路径重跑：
  1. `[say, ask_user]` 同轮不再整轮拒绝，且 say 在前
  2. 带 IP:端口的正文不再断在点号处
  3. 两轮「无变化」巡检只落一条可见汇报
  4. 卡片正文不含 Markdown 记号

## 边界

只做本地提交。不 push、不部署、不重启任何节点、不改数据。本机要生效需用户
`sudo systemctl restart mindfs`。
