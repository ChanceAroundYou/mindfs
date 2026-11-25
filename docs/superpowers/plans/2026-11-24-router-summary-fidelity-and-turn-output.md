# Router 总结可信化与回合输出收口

> Scope IDs: G-RI / G-RJ / G-RK（claimed 2026-11-24T18:54Z）
> 合回 main 前撞号避让：原 G-RE / G-RF / G-RG 已被 Edge Relay 占用（`05c6e905` 裁决），
> 按「后来者让号」整体改为 G-RI / G-RJ / G-RK；本分支提交已按新号重建。

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

## 实现记录（2026-11-24）

### 提交 1：S1 + S2（Scope: G-RI，`a48411ae`）

按设计落地，无偏移。`normalizeRouteActionOrder` 在校验前把 `[ask_user, say]` 归一成
`[say, ask_user]` 并重排 `Seq`；`validateRouterActions` 的唯一约束改成「同轮至多一个
`ask_user`，其余动作只能是 `say`」；两条 finish 路径在校验失败时先按 summary 应用
纯文本 say，再写 `route-rejected:<decision>:<seq>` 说明行。`router_silent` 仍必须单独
出现，执行类动作不受影响。

### 提交 2：S3 + S4 + S5 + S6（Scope: G-RJ，`564a9d7c`）

- **S3**：`cutAtSentence` 全角 `。！？；` 直接算句末；ASCII `.!?;` 必须后随空白/换行/
  结尾。结构化块（`-`/`*` 列表项、「没做」「未验证」段）从下半段起头时整块放弃，
  不切一半。`maxStoredSummaryRunes` 600 → 1000（`service_activity_output_text.go`）。
- **S4**：新增 `summary_change.go`。判据四条同时成立才降级成 `progress`：触发类是
  `turn_settled`、`HadToolCalls == false`、brief 的 `out_of_band` / `answered_question`
  都为假、与 `task.ResultSummary` 的字符三元组 Jaccard > 0.6。缺证据（brief 解析失败、
  卡片无上一条摘要）一律不降级。
- **S5**：新增 `inspection_dedupe.go`（指纹 = 触发类 + 任务状态 + 去空白去标点小写后的
  正文 hash，30 分钟窗口，4096 条上限，进程内不落库）。登记簿与 `turnTerminations`
  同构：`Service.inspectionDedupe` + `ActivityServiceForRoot` 注入。命中时落库但
  `hidden=1` / `hidden_reason=duplicate_inspection`，摘要不变。
- **S6**：`routerSettlementExcerptRunes` 1000，`headTailExcerpt` 头 500 + 尾 500，
  中段用 `structuralExcerptLines`（200 字预算）保留要点行，省略处写明省掉多少字。

两处计划外但必要的偏移：

1. **巡检回合放行 `router_silent`**。S5 让提示词引导模型「零变化就沉默」，但
   `applyRouteAction` 的白名单只有 `say/ask_user/notify_user/escalate` —— 模型照提示词
   做出来的 `silent` 会被拒成一条可见说明，比说出那句「没有变化」更吵。沉默没有副作用，
   加进白名单；规则 11 原文同步改为三者都允许。
2. **文件拆分**。`activity_route_apply.go` 加完接线后 502 行，超过后端 500 硬线，
   `applyRouteSay` 整块搬到新文件 `activity_route_say.go`（纯移动 + 新增判据）；
   `activity_service.go` 已在存量超线清单里，新加的 setter/accessor 单独放
   `activity_inspection_dedupe.go`，不往超线文件里加行。

验证：`go test -count=1 ./...` 42 包全绿（kanban 335.2s，中途一次
`TestCoordinatorTickReclaimsStaleClaims` 是既有并发 flake，单跑即过）、
`npx tsc -b --noEmit` exit 0、`npx vitest run` 274 文件 / 968 通过 / 6 跳过。
变异逐一打红后还原（S4 判据、S4 的 `out_of_band` 守卫、S5 去重、S5 的 `silent` 放行、
S3 边界回退、S6 头尾保留）。

### 提交 3：S7 + S8（Scope: G-RK）

- **S7**：规则 18 改成固定四个标签行（`结论：` / `证据：` / `没做或未验证：` / `下一步：`），
  标签行内允许 `- ` 列表与行内代码，禁止表格、多级标题与代码块；硬上限 600 → 800 字。
  规则 15 的「不要套固定模板」补上例外，否则两条规则自相矛盾（与 S1 修的是同一类问题）。
  内容守卫 `TestRouterPromptStatesSummaryContract` 的锚点从「硬上限 600 字」改成
  「硬上限 800 字」+「没做或未验证：」。
- **S8**：新增 `web/src/shared/summaryText.ts::summaryPlainText`，语义照
  `server/internal/notify/plaintext.go` 的 `stripMarkdown`（围栏/分隔线整行丢弃、
  表格分隔行丢弃、表格行竖线换空格、标题/引用/列表标记去除、行内代码反引号去除、
  链接/图片留文字、粗体/删除线/斜体去标记），唯一区别是保留换行 —— 四个标签行要
  各自成行。`TaskCardRows` 的正文与 `title` 都用同一份纯文本，存量带 Markdown 的
  摘要不改库、刷新即正常。

验证：`tsc -b --noEmit` exit 0、`vitest run` 275 文件 / 974 通过 / 6 跳过。Go 侧
`go test -count=1 ./...` 与随后单跑 kanban 各命中一次既有 TempDir 清理竞态
（`TestCoordinatorTickReclaimsStaleClaims`：`directory not empty`，改前就存在，
`-count=10` 单跑复现 1 次），重跑 `./server/internal/kanban/` `ok 318.5s`；
其余包在这一轮全量里全绿。变异三处各自打红后还原（提示词标签行 → 内容守卫红；
`summaryPlainText` 不去粗体 → 两条断言红；卡片 `title` 退回原文 → 源码契约红）。
