# 任务审批收尾：末端 gate 禁定义、审批卡可读化、附言交给 Router（task-gate-and-reports-unfold）

- 日期：2026-11-19
- 状态：已实现并提交、隔离验收（真实模型）通过（未 push / 未部署 / 本机待用户重启），执行记录见文末
- 上游：无（本轮修复的是本 fork 自己的 Router / 工作流展示层）
- 定制登记：`Scope: G-MY`（另把 G-MO 的「运行汇报折叠」登记改写为平铺）

## 背景

用户对任务 #88「收尾」的任务详情面板逐条报告（原话）：

> 你再处理一下这个  现在完全没有可读性  我都不知道要我审批什么，而且1.运行汇报为什么要加折叠功能？
> 2.运行汇报的状态是"等待你处理"，审批的状态是已完成？3.我除了批准和拒绝  都不给我额外输入的端口
> 4.已经执行完的 没必要多余卡一个审批在这里，任务有自己的完成按钮，不需要这样审批，我满意了自然会点击完成

现场数据：`workflow-new-feature:v1` 的图是 `input → plan → implement → verify(human_gate)`，
`verify` 就是最后一个节点 —— 整轮跑完后再让用户批一次，批了也不会发生任何事。

两处收窄由用户当面裁定：

- 末端 gate：**「末端不仅是不渲染  直接就不能作为最后一个节点，不需要。」**
- 附言入口：**「审批框内加附言，作为补充  交给router驱动执行，普通输入框还是按约定保持禁用」**

## 设计

1. **末端 gate 不允许做最后节点**
   - 定义期：新增 `ValidateWorkflowDefinitionForAuthoring` = 基础校验 + 拒绝「`human_gate` 没有出边」，
     只挂在草稿创建 / 更新与 typed patch 校验上。**不能**进 `ValidateWorkflowDefinition` ——
     已发布的 `workflow-new-feature:v1` 必须仍然能建 Run。
   - 前端画布 `validateWorkflowGraph` 镜像同一条规则，用户在画布上连完就能看到红字。
   - 运行期兼容：引擎看见末端 gate 直接 `autoPassTerminalGate`（success、`auto_approved:true`、
     不写等待卡、不写假的「已批准」气泡）；升级前已经停在 `waiting_user` 的末端 gate 由
     driver 每次推进前的 `healParkedTerminalGates` 与启动对账的 `hasParkedTerminalHumanGate`
     唤醒收尾，`AutoApproveTerminalHumanGate` 在同一事务里 CAS 节点行、合并 run 投影、补事件、
     把审批卡结算成 completed。有下游的 gate 行为完全不变。
2. **审批卡说清「在批什么」**：`gateActivityPayload` 生成 `prompt` + `node_name`（缺名字回落节点 id）
   + `upstream`（入边节点的产出摘要，没有产出的跳过）；`TaskHumanGateCard` 渲染节点名与上游摘要，
   没有上游时不渲染空块。
3. **附言交给 Router**：卡片内新增附言输入框，只随「批准」提交；后端
   `ResolveWorkflowHumanGate` 对 approve 且非空附言走 `QueueUserActivity` 落成排队用户留言，
   等这一轮真的 `StatusSuccess` 收尾后由 `dispatchQueuedGateNote` → `DispatchQueuedUserActivity`
   交给 Router 决定下一轮。失败/取消的回合附言留在队列、不假装执行；驳回永不排队；
   普通输入框仍然禁用（D4 不变）。
4. **运行汇报不再折叠**：删除 `ActivityReportsGroup`、`conversationRows`、`isSettledReply` 与
   `task.activity.reports/reportsHide`；`purpose=progress` 的行按时间顺序原样平铺，
   只在页脚加浅色「运行汇报」标记（`isRunReport` + `activity-report-tag`）。
5. **状态文案一致**：gate 等待态写「待你审批」；同一 Run 有等待中的审批卡时，
   `visibleConversationActivities` 隐藏那条 `waiting_user` 的占位回复（#88 现场两张冲突卡），
   gate 结算后占位回复回到结果行。

## 验证

- `go test -count=1 ./server/internal/kanban/ ./server/internal/api/` 全绿（192.2 s / 45.1 s）。
- web 定向 4 个文件 25 例全绿；全量门禁在干净提交树上复跑，见[执行记录](#执行记录提交-637fdb5e-scope-g-my)。
- 新增测试：`workflow_terminal_gate_test.go`（定义期拒绝、运行期自动放行、停放 gate 自愈、
  附言落成用户留言且驳回不排队）、`workflow_human_gate_context_test.go`（节点名 + 上游摘要）、
  `task-human-gate-card.test.mjs`、`gate-waiting-consistency.test.mjs`、`activity-reports-inline.test.mjs`、
  `workflow-canvas.test.ts`。
- 隔离验收（`e2e/test_task_router_gate_note.py`，隔离实例 7435 / `router.mode=route` / sonnet，
  线上 7331 全程未碰）：**3 / 3 PASS**

```
A pass 15022ms  payload={node_id: gate, node_name: 确认继续, prompt: 上面的准备结果可以继续吗？,
                        upstream:[{name:准备, node_id:prepare, summary:准备完成：已整理好待审批的内容。}]}
B pass 89082ms  note act_7a6488f8bbec2ab3 "批准。另外请顺手确认收尾节点确实执行了。" (queued)
                → decision rdec_13c68bc340ce8fc5 applied ["say"]
                → reply act_45fe8870d2120c60 reply_to_id=note, completed；节点 prepare/gate/finish 全 success
C pass 15024ms  authoring 400 'human_gate "verify" cannot be the last node: add a node after it;
                a gate with no downstream has nothing to approve'
                legacy gmy-legacy-terminal:v1 → 两节点全 success、waiting_cards=0、gate 输出 auto_approved
```

场景 C 的旧版本无法从 HTTP 建出来（定义期已拒绝），脚本把已发布版本直接写进**隔离实例**的
`workflows.json`（`--iso-root` 白名单校验，线上路径一律拒绝），专门验证「升级前已发布的末端 gate
不会再卡住用户」。

## 执行记录（提交 `637fdb5e`，`Scope: G-MY`）

在 `git worktree add --detach /tmp/mindfs-gmy HEAD` 的**干净提交树**上跑全量门禁（不在脏工作区跑，
理由见下面第一条）：

| 门禁 | 结果 |
|---|---|
| `go test -count=1 ./...` | 28 包全绿，`server/internal/kanban` 247.9 s |
| `npm test`（Vitest） | 240 文件通过 / 5 跳过，704 例通过 / 6 跳过 |
| `npx tsc -b --noEmit` | clean |
| `npx vite build` | 成功（`✓ built in 1m 4s`） |
| `go run ./scripts/check-upstream` | **未通过**：4 个 delta 文件未被任何组登记，全是 G-MQ 的 `*_session_worktree_ownership*`（另一条会话的提交 `702e6ef1` 已在 main 上，但它的 G-MQ 登记还只存在于那个会话的未提交工作区里）。本轮 G-MY 自身零告警：无未覆盖文件、无锚点失配、无幽灵条目。 |

**本轮真实缺陷（已修）**：第一次提交漏了 `workflow_lifecycle_guards_test.go` 里
`ResolveWorkflowHumanGate` 的调用点适配（多一个 `note` 参数）。工作区里这份文件已经改过，所以
只跑定向包测试看不出来；在干净树上 `go test ./...` 时 `server/internal/kanban` **直接编译失败**。
用临时索引 `git commit --amend` 补上该文件、并把它加进 G-MY 的 `files` 列表后复跑通过。
教训：签名变更必须全仓 `grep` 调用点（含 `_test.go`），并且门禁要在干净提交树上跑 —— 脏工作区
会用未提交的改动把编译错误掩盖掉。

## 未做 / 残留

- 不迁移历史任务、不重发旧工作流版本（旧版本靠运行期自动放行）。
- 不新增 Router 动作：附言复用既有用户留言通道；普通留言仍然不能批准/驳回（D4）。
- 不做运行视图重构与虚拟化改动。
- 未 push、未部署；本机 `sudo systemctl restart mindfs` 仍需用户执行。
