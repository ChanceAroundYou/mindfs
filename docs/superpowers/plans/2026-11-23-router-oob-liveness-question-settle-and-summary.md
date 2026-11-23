# Router 巡检回合可决策化 · 提问卡收口 · OOB 活跃判定 · 摘要保留决策

> Scope IDs: G-QE（claimed 2026-11-23T08:16Z；批次 2/3 的组号在对应实现时再申请，见文末「组号」一节）

Plan 2026-11-23。起点是用户在本机（local 节点）mindfs 项目上的两条观察：

1. **#104「明明已经回答了，详情界面还在卡死」** —— 用户答完提问，输入框仍然被
   「请先回答上面的问题」挡住，任务卡还在转圈。
2. **#68「Router 总结完全偏离要点，连需要决策的部分都丢了」** —— 用户猜测是不是
   上下文窗口给得太紧，并给出方向：检测到需要决策/需要批核的，转成 `ask_user`
   发到任务详情里，哪怕会话内 Agent 是纯文本。

只读取证把两条都定位到了确定的代码路径（见第 2 节），用户的裁定是「全部按你建议」，
即批准我提出的 7 条清单（第 1 节）。

## 1. 批准的口径

| # | 项 | 决定 |
|---|---|---|
| 1 | #104 根因 A：OOB 恢复不判活跃 | 恢复前用既有 `sessionRunning` 探针确认会话是否还在回复；**还活着就不恢复、不写事件、不清标记** |
| 2 | #104 根因 B：同会话多张等待卡 | 插新等待卡时，把同会话更早仍 `waiting_user` 的卡置 `cancelled`（理由「已被更新的提问取代」） |
| 3 | #104 存量孤儿卡 | 启动清扫：同会话已有更晚 `question` 时收口旧的；直接改库需另行授权 |
| 4 | 巡检回合动作被拒 | 不再整轮静默失败：把被拒动作降级成可见系统说明；`router_prompt.md` 规则 12 改写成巡检回合用 `ask_user` 表达待拍板 |
| 5 | #68 总结口径 | `turn_settled` 回合有真实执行结果时把最后一条 say 提升为 summary；加「Router 总结不被压缩器覆盖」的保护 |
| 6 | 第三部分实现范围 | 先做 A 的 1–3（Router 看得见 + 规则 + 真 question 卡）；审批复用选项卡；兜底 B 暂不做；规则 17 那七类必问事项**不受「不用问了」豁免** |
| 7 | 批次顺序 | 1+2+4 先合一批（止血），3 与 5+6 各自独立批次，各自 TDD + 变异验证 |

## 2. 取证（只读，2026-11-23）

### 2.1 #104 是两处独立缺陷叠加，不是一处

**根因 A：`restoreStaleOutOfBandTasks` 把「超过 30 分钟」直接当成「被重启带走」。**

`service_recover_out_of_band.go` 的循环只判三件事：任务还在不在 `running`、是不是终态、
有没有归档。**完全没有问会话还在不在写**。现场：会话 `1795246727-1856f7b46d25` 当时
正在持续产出（块流 15 秒涨 3,120 B），却在 11:45:32 被判定「已中断」，任务放回
`waiting_user`、标记清掉。12:43:32 又来一次（`task_7280e0df4f5c8518`，会话
`1795149400-…`，13:57 仍在写）。

那条注释（`:17-19`）写的前提是「超过 30 分钟的 OOB 标记一定是被重启带走的 —— 本进程
启动后不会再认领同一轮」。这个前提只对「重启后没人再推这一轮」成立；对「回合本身
就是长的/慢的」不成立。探针 `sessionRunningProbe` 已经存在且已被
`router_event_direct.go` 消费，这条路径只是漏用了。

**根因 B：同会话的多张等待卡没有任何收口机制。**

`q-84825203e4fe3902`（03:21:57Z，5 个问题，零答案）是孤儿卡。Agent 在 03:23:50 用
**新的 `tool_use_id`** 重问了 4 个问题（`q-a1c393a2d3ef383d`，已答），03:26:04 又重问
3 个（`q-8b7377cdee17c4f2`，已答）。去重键是 `(session_key, tool_use_id)`，换了 id
就是新卡 —— `InsertOrIgnore` 拦不住，旧卡也不会被任何路径收口。

而前端 `pendingTaskPrompt(activities)` 取的是**最早**那张等待卡，`ConversationView`
据此把整个输入框换成「请先回答上面的问题」。于是：**用户答的是新卡，挡住输入框的是
最早那张孤儿卡**。这正是「明明已经回答了还卡死」。

**根因 C：C2 的网络是好的，但它的回话被闸门拒了。**

上一轮（G-PX `fdff1e25`）加的 OOB 恢复确实写了 `turn_interrupted` 事件，但巡检回合的
动作白名单只放开 `say`/`ask_user`，任何其它动作在 `activity_route_apply.go:165` 被拒，
整轮 `rdec_47d5992f117dc51b status=failed` —— 用户既没看到卡片变化，也没看到任何说明。

### 2.2 #68 不是上下文窗口问题

prompt tokens 三轮分别是 181,766 → 150,419 → 166,880，**在下降**，没有触顶。

`rdec_c1d22fd89b85125a`（trigger `turn_settled`、applied、prompt 166,880、latency
24,352 ms）里只有**一个**动作：`say purpose=progress`，正文 410 B，把四条决策项完整
复述了一遍。`brief_json` 的 `event.excerpt`（401 字符）里也已经带着这四条。

问题在**去向**：`purpose=progress` 永远不写 `tasks.result_summary`
（`activity_route_apply.go:315` 只认 `purpose=summary`）。用户看到的 293 字是异步
压缩器的产物，它的提示词是「1–3 句、总共不超过 200 字、保留路径/命令/错误原因」——
**结构上就没有「保留待决策项」这一条**。

`settleSummaryActionSeq` 只有在 `run_settled` 且 Run 真跑成时才把最后一条 say 提升成
summary；`turn_settled` 刻意不做这件事（当时的理由：每轮会话都结算，会把普通进度顶成
卡片结论）。所以 #68 这种「会话内推进一轮、Agent 给了四条决策项」的回合，Router 明明
说了、也说了对，但**永远不会成为任务结论**。

### 2.3 功能缺口：Router 的 ask 不是「卡」

`applyRouteAskUser` 落的是 `ActivityTypeAgentReply` + `waiting_user`，选项拼在正文里。
前端 `isPendingTaskPrompt` 只认 `question` / `human_gate`，所以 Router 的提问
**不锁输入框、没有按钮、也不产生 `user_answer`**。真正能当审批卡用的只有 HumanGate。

## 3. 实施

三批，各自独立提交、独立门禁。组号由 `scripts/scope-id claim` 分配（见计划头）。

### 批次 1 —— 止血（Scope: G-QE）

**1. OOB 恢复加活跃判定**（`service_recover_out_of_band.go`）

在「任务还在 running / 非终态 / 未归档」之后、写事件之前插入：

```go
if s.sessionRunning(item.SessionKey) {
    continue // 会话还在写：这一轮没被带走，标记留给它自己收尾
}
```

三条语义必须一起满足：**不恢复、不写 `turn_interrupted`、不清标记**。不清标记是关键
——清掉的话这一轮结束时 `SettleSessionTurn` 找不到 OOB 归属，任务就再也不会被放回
`waiting_user`（等于把 A 的 bug 换成另一种卡死）。

**2. 新等待卡收口旧卡**（`service_task_prompts.go`）

`RecordTaskQuestion` 在 `InsertOrIgnore` **成功插入之后**，收口同会话里更早的
`waiting_user` 提问卡（`ActivityStatusCancelled`，summary「已被更新的提问取代」）。
只在真的插入了新卡时才做，重复广播不误伤。

**3. 巡检回合动作被拒不再静默**（`activity_route_apply.go` + 调用方）

闸门拒绝时不能整轮 failed 什么都不留。改为：把被拒的动作折成一条可见系统说明
（`ActivityTypeAttention`，`route-rejected:<decision>:<seq>`，`InsertOrIgnore` 幂等），
决策仍按 failed 记录、`Error` 保留原文（失败是事实：这一轮确实没做成），
用户至少能看到「Router 想做什么、为什么没做成」。

**4. 提示词**（`router_prompt.md` 规则 11/12）

规则 11 与规则 12 现在是矛盾的：11 说巡检类事件「只能 `say`/`ask_user`」，12 说
「需要用户拍板时 `router_escalate`」。代码执行的是 11。把 12 改写成：**巡检回合要人拍板
就用 `ask_user`**，`escalate` 只在有来源留言的回合用；并写明被拒动作会变成用户可见的
系统说明（不要再靠「反正会被拒」来试探）。

### 批次 2 —— 存量孤儿卡（Scope: 实现时申请）

启动清扫：`RecoverOnStartupForRoot` 里加一拍 —— 同会话存在更晚的 `question` 时，把更早
仍 `waiting_user` 的收口成 `cancelled`（理由「已被更新的提问取代」）。这同时清掉线上
`q-84825203e4fe3902`（**不改库，只靠代码**；直接写库要另行授权）。

### 批次 3 —— 总结与真卡片（Scope: 实现时申请）

**5. `turn_settled` 也允许把最后一条 say 提升成 summary**，但仅当这一轮有真实执行
结果（会话里确实有工具调用/产物，不是纯闲聊）。同轮还要加一道保护：**Router 写进
`result_summary` 的内容不再被异步压缩器覆盖**（压缩器 CAS 的目标值必须仍是它自己写下
的那份）。压缩器提示词补「待用户决策/审批的事项必须逐条保留，不得压缩掉」。

**6. Router 看得见 + 规则 + 真 question 卡**

- `turn_settled` 的 `event.excerpt` 从 400 字符放宽，让 Router 看得到整轮的决策项。
- 规则 18 增补：遇到需要决策/审批的事项，`ask_user` 必须给 2–4 个选项；规则 17 那七类
  必问事项不受「不用问了」豁免（这条已有，本轮只补交叉引用）。
- `applyRouteAskUser` 升级成落真正的 `ActivityTypeQuestion` 卡（带 `payload.questions`），
  按钮、输入框锁、`user_answer` 三样一并得到；审批场景复用同一张卡（批准/驳回/需要修改）。
- 兜底 B（纯文本里嗅探决策项）**暂不做**。

## 4. 边界

- 不碰生产 7331；验证只用隔离实例（端口 ≥ 7431）。
- 不 push、不部署、不重启；本机 `sudo systemctl restart mindfs` 由用户执行。
- 不改 `.mindfs/sessions` 下任何存量数据（700+ MB 块流/aux 维持「不动」裁定）。
- 两条活跃分支 `edge-controller`、`task-intake-confirm-a1` 不碰。

## 5. 执行记录

### 批次 1（Scope: G-QE）—— 已实现，门禁绿

| 项 | RED 证据 | GREEN | 变异验证 |
|---|---|---|---|
| OOB 恢复认活跃会话 | `TestRecoverStaleOutOfBandSkipsLiveSession` 首跑 `restored = 1, want 0` | 加 `sessionRunning` 判定后通过；探针翻成 false 时同一用例仍要求 `restored = 1` + 任务回 `waiting_user` | 去掉该判定 → 用例红（`restored = 1`、任务被改成 `waiting_user`、标记被清） |
| 新提问卡取代旧卡 | `TestRecordTaskQuestionCancelsOlderWaitingCards` 首跑 `older card status = "waiting_user", want cancelled` | `closeQuestionsBefore` 落地后旧卡 `cancelled` + summary 写明「已被更新的提问取代」、`OpenQuestionsBySession` 只剩最新一张 | 把 `keepID` 判定改成「全部收口」→ 用例红（`newest card status = "cancelled"`），确认保护是真在起作用 |
| 巡检回合被拒动作留可见说明 | `TestCoordinatorInspectionEventWithoutOriginRejectsCompleteTask` 首跑 `被拒的动作要留一条可见说明 = 0 条, want 1` | `recordRejectedInspectionAction` 落地后落在 1 条 `attention/system` 行，正文含被拒动作名、不宣布完成、不绑留言 | 把调用换成恒 false 的调用 → 同一用例复红 |
| 规则 11/12 订正 | —（纯提示词） | 规则 11 写明「被拒会留可见说明，但那只是兜底」；规则 12 写明巡检回合改用 `ask_user`、`escalate` 只对有来源留言的回合 | 无（提示词无行为可变异） |

回归修正：`TestCloseOpenTaskQuestionsClosesOnlyWaitingCardsForSession` 原本靠连续调用
`RecordTaskQuestion` 造两张同会话等待卡，新语义下第一张进来就被取代，用例前提不再成立。
改为直接 `InsertOrIgnore` 种子行（含一张 `completed`、一张别会话的卡），保留它真正要钉的
筛选口径 —— 升级前遗留的多张等待卡必须被 `CloseOpenTaskQuestions` 全部收掉、别会话与已答的
不动、二次调用幂等返回 0。

门禁：`go test ./server/internal/kanban/ -count=1` EXIT=0（379 s）、
`make check-upstream` 2625 delta / 383 组全绿。

## 6. 组号

- **G-QE** —— 批次 1（OOB 活跃判定 / 提问卡收口 / 巡检回合可见拒绝 + 规则 11/12），已实现并登记。
- 批次 2、批次 3 的组号在各自实现时由 `scripts/scope-id claim` 现场申请：先实现的批次落地、
  登记、门禁绿之后，下一个批次再取号。一次性 claim 三组会让尚未实现的占位一直把
  `make check-upstream` 的断言 5 打红，抢不到干净的提交点。
