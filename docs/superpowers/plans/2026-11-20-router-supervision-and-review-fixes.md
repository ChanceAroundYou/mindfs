# Router 主动监管 + 2026-11-20 复审五项修复

- **日期**：2026-11-20
- **授权**：用户直接指令「这些问题请你全都补进去，列好清单和解决方案之后，请你再思考另一个问题：我希望 router 能够更加主动地去把自己的任务给管理起来……而非死板的每次都要等用户说完了只回复一次，出了问题也不会重试 也不会继续处理 只会报错」
- **范围**：Part A 是已授权的实现（五项复审发现）；Part B 是设计提案，**未实现**，等用户确认口径后再拆执行计划。
- **Scope**：G-NQ

---

## Part A · 复审五项修复（已实现）

复审对象是 2026-11-20 重启后（`v0.5.5-1200-52`）的生产库只读副本与运行日志。

### A1 直连回合失败后任务退回 `pending`（输入框被禁用）

- **证据**：任务 `task_7280e0df4f5c8518`（「任务卡卡死」）`status=pending`、`result_summary="response stream ended unexpectedly"`；同一条回复活动已是 `failed`。用户看到一条报错，却因为任务「未开始」而回不了话。
- **根因**：`executeDirectActivity` 的成功分支调用 `markTurnTaskWaitingUser`，失败分支只调 `restore()` 把任务回滚到进入本轮之前的状态。
- **修法**：失败分支在 `restore()` 之后同样调用 `markTurnTaskWaitingUser`（暂停/终态不复活、已是 waiting_user 不重复写）。失败与成功一样以「等用户」收尾。
- **测试**：`activity_task_status_test.go::TestActivityService_FailedDirectTurnLandsInWaitingUser`（注入必失败 executor，断言任务落到 waiting_user 且失败原因留在回复 body）。先红（`task status = "pending"`）后绿。

### A2 `message_delta` 不在已知帧白名单，日志被刷爆

- **证据**：两小时 1,672 条 `[agent/claude] output.unhandled.partial`，全部来自 `message_delta`（携带 `stop_reason`/`usage` 的正常帧）。
- **修法**：`isKnownNonDisplayPartialEvent` 的事件类型白名单补 `message_delta`；delta 类型白名单不动。
- **测试**：`session_partial_events_test.go` 两条 —— 正常帧必须算已知；没见过的帧类型仍要报出来（防止白名单退化成「什么都放行」）。先红后绿。

### A3 legacy 迁移快照永远转圈（第三类死角）

- **证据**：`act-legacy-reply-task_ebe31569ee25f4ff`（2026-11-12）停在 pending，绑着 `run-legacy-…`，任务早已 success 且归档；另有 11 条 legacy NodeRun 停在 pending/running（最早 2026-09-28）。
- **根因**：`legacy_import` Run 被两套结算同时排除 —— `recoverWorkflowRunsOnStartup` 按 `triggered_by` 排除；直连孤儿回复只认 `run_id=''`。于是迁移那一刻正好在跑的节点/回复永远没人收尾；重开这类历史任务时，Router brief 还会看到「还有开放节点」。
- **修法**：
  - `ListLegacyImportRunsOnTerminalTasks`：只挑**任务已终态**的 legacy Run（任务非终态不碰，可能是用户正在继续处理的历史任务）。
  - `SettleLegacyImportRun`：一个事务里把 pending/queued/running 的 NodeRun 冻结成 cancelled、把投影里所有非终态冻结成 cancelled（`revision` CAS），并记一条 `node_cancelled` 事件（`reason=legacy_import_leftover`）。没有可改的东西时只读判一次、不写库。
  - `settleLegacyImportLeftovers`：随后把绑在该 Run 上、仍 pending/running 的回复结算成 cancelled（`SettleOrphanReply` 条件更新），广播 `status/summary` 并清正文；结案文案说明「历史任务迁移时留下的执行快照，任务已经结束」。
  - 接进 `RecoverOnStartupForRoot`，排在直连孤儿结算之后。
- **测试**：`service_recover_legacy_leftovers_test.go` 三条（回复结算、节点行+投影冻结、非终态任务不碰）。两条先红后绿；变异验证两处 —— 去掉终态任务过滤（非终态守卫打红）、跳过投影冻结（投影用例打红）。

### A4 正常空闲关闭被记成 `last_error='idle timeout'`

- **证据**：生产 `router_sessions` 里每一行已关闭会话都带 `last_error='idle timeout'`，排查时全部像出过错。
- **修法**：`closeSession` 只把 reason 写日志，`UpdateSessionStatus` 的 `last_error` 传空。
- **测试**：`router_session_close_test.go::TestIdleSweepClosesSessionWithoutRecordingError`。先红后绿。

### A5 归档/取消归档不留任务事件

- **证据**：`task_events` 里查不到任何 `archived` 记录；归档刻意不动 `updated_at`、也不经过状态机。
- **修法**：`SetTaskArchived` 只在标记真翻转时追加 `archived`/`unarchived` 事件（payload 带时间戳），幂等调用不刷屏。
- **测试**：`task_store_archive_event_test.go` 两条（两种事件都出现、重复调用只记一条）。先红后绿。

### 门禁

- `go test ./internal/kanban/ -count=1` 全绿（269.7s）；`go test ./internal/agent/claude/` 全绿。
- 未动前端；`make check-upstream` 结果见提交后记录。

---

## Part B · Router 主动监管（设计提案，未实现）

### B0 现状：一半的骨架已经有了，缺的是「能动手」

R1 已经把**事件驱动**做出来了，只是刻意停在「只汇报」：

- `router_events` 账本已支持 `run_settled` / `run_failed` / `gate_waiting` / `run_stalled` / `startup_reconcile`，5 分钟桶去重、CAS 领取、跨重启幂等（`router_event_types.go`）。
- 停摆检测已经在跑：NodeRun `running` + 有会话 + 有 `started_at` + **会话探针说没在回复** + 运行 ≥60s + **连续两次观测间隔 ≥30s**，才落一条 `run_stalled`（`router_event_sources.go`）。正常长回合不会误报。
- coordinate 循环 30s 一拍，消费事件前重查证据（`stallEvidenceHolds`：节点行必须仍 running），过期事件标 skipped。

**缺口**：运行期事件回合的动作白名单是 `say / ask_user / silent`（`validateRuntimeEventActions`），也就是说 Router 发现问题后**只能说话**：不能重试节点、不能恢复 Run、不能取消、不能替用户决定。这正是用户说的「出了问题也不会重试 也不会继续处理 只会报错」。另外：

- `run_failed` 没有失败分类，重试预算无从谈起；
- 直连回合（无 WorkflowRun）只有启动时的孤儿结算，运行期没有监管；
- 没有「监管台账」，无法回答「Router 这一小时干预了几次、成了几次」。

### B1 目标（按用户原话拆）

| 用户的问句 | 系统要能回答的证据 | 监管动作 |
|---|---|---|
| 派出去的 worker 执行得怎么样了？ | 每个开放节点的水位：`started_at`、最近会话活动、attempt、最近输出时间 | 无（进 brief） |
| 是不是卡死了？ | 会话探针 + 水位停滞时长 + 连续观测次数 | `resume_run` / `retry_node` |
| 有没有需要决策的？ | HumanGate waiting、等待用户答复、worktree 合并冲突 | `ask_user` / `notify_user` |
| 需不需要继续恢复？ | 节点行 running 但会话空闲；driver 没有在推进 | `resume_run`（只唤醒 driver，不重跑节点） |
| 失败了要怎么处理？ | 失败分类 + 已用 attempt + 历史重试结果 | `retry_node`（有预算）/ `escalate` |

### B2 监管观测（supervision observation）

不新增轮询模型：每拍（沿用 coordinate 的 30s 循环，或独立 60s tick）对**本 root 的开放 Run** 做一次纯 DB + 内存判定，产出一个 `SupervisionObservation`：

```
run_id / task_id / task_status
open_nodes[]: node_id, node_run_id, attempt, status, started_at, idle_seconds, session_replying
last_progress_at        // max(node_run.updated_at, 会话活动, run.updated_at)
last_error_kind         // 来自失败的 NodeRun 输出/活动，分类见 B4
retry_budget_left       // B5
already_escalated       // 本 attempt 是否已升级过
```

数据来源全部是现成的：`workflow_node_runs` 行、`ListReplyingSessions`（会话 hub，已有）、`task_activities`、`workflow_runs`。**不读转录**，避免高成本。

### B3 触发与动作

新增事件类型（或复用 `run_stalled` 扩 payload）：`supervise`，实例键 = `runID + ":" + 桶`。动作白名单按证据分级放开：

| 动作 | 语义 | 前置条件 |
|---|---|---|
| `silent` | 观察后判定无需干预 | 默认 |
| `notify_user` | 写一条可见系统行（不提问） | 停滞已确认但不需要决策 |
| `retry_node` | 指定 NodeRun 建 attempt+1（`reason=supervisor_retry`，复用既有 retry 路径） | 失败可重试 + 预算未耗尽 + 没有不确定副作用 |
| `resume_run` | 只唤醒 driver（`WakeWorkflowRun`），不重跑任何节点 | 节点 running 但会话空闲 + 没有任何未知副作用 |
| `cancel_run` | 有界取消并把任务置回 `waiting_user`，留可见原因 | 重试预算耗尽或不可重试错误 + 已升级过一次 |
| `ask_user` / `say` | 既有 | 不变 |

**永不自动做**：批准 HumanGate、重跑「副作用不明」的节点（合并了一半的 worktree、unknown 且带 `worktree_error`）、对同一节点并发起两个 attempt。

### B4 失败分类（决定重试与否）

- **可重试**：上游 5xx/超时/连接中断（如 `response stream ended unexpectedly`）、进程重启留下的 unknown 且无合并证据、会话起不来但可重建。
- **不可重试**：校验/配置错误、鉴权、HumanGate 驳回、worktree 合并冲突、用户取消、同一错误连续两次。
- **不确定**：`unknown` + `worktree_error`（独立树可能只合了一半）→ 只升级给用户，绝不自动重试。

分类器优先读结构化证据（NodeRun `Output["error_kind"]` 若存在），没有就按错误文本正则兜底，并把判定写进决策 `reason`，方便复盘。

### B5 预算与防抖

- 每节点：最多 2 次监管重试（attempt 本身另有上限）。
- 每 Run：每小时最多 6 个监管动作（与 `MaxActions` 同量级）。
- 同一 (run, node, attempt, 水位) 只决策一次：决策幂等键里带上水位哈希，重复 tick 直接跳过。
- 升级路径：第 1 次失败 → 重试；第 2 次失败 → 通知；耗尽 → 升级 `waiting_user` 并写清「试过什么、最后错误、建议下一步」。
- 全部阈值走 `config.json` 的 `router.supervision.*`，默认关；`mode=shadow` 只记录不应用（与现有 shadow 语义一致）。

### B6 分期（每期独立可回滚）

1. **S0 观测**（零模型调用）：supervision tick 只写账本 + `/api/router/status` 暴露「开放 Run / 停滞 / 已升级」计数。跑 24h 校准阈值。
2. **S1 影子决策**：`supervise` 事件进 Router，但动作只落 `router_decisions`，不应用；对比「Router 想做什么」与「实际发生了什么」。
3. **S2 有限执行**：放开 `retry_node` + `notify_user` + `silent`，预算生效；`resume_run`/`cancel_run` 仍禁用。
4. **S3 完整**：放开 `resume_run` / `cancel_run` / `escalate`，接入直连回合监管（活动 pending 且会话空闲超阈值 → 结算 + 最多重试一次）。
5. **S4 可视化**：任务详情显示「监管记录」时间线（谁在什么时候重试/升级），与现有运行时间线同一套行。

### B7 明确不做

- 不做跨节点监管（数据面各节点自有，控制面只在主节点）。
- 不自动改工作流定义、不自动扩并发、不自动批准任何审批。
- 不在 `mode!=coordinate` 时应用监管动作。
- 不因为「跑得久」就判失败：只有会话探针 + 水位双证据才动作。

### B8 待用户确认的口径

1. 阈值：停滞确认（当前 60s 起判 + 两次观测）是否维持；重试预算 2 次、每小时 6 个动作是否合适。
2. 升级去向：耗尽后停在 `waiting_user` 等用户，还是允许 Router 直接 `cancel_run`？
3. 直连回合是否纳入监管（S3），还是只监管工作流。
4. 是否按 S0→S4 分期落地，还是先做 S0+S1 看数据再定。

### 交付记录（2026-11-20 16:40）

- 提交：`f1293e7a`（主体，15 文件）+ `ad4db85a`（用例注释去掉 `''`，gofmt 会把它改写成右引号）。均已随 origin/main 推送。
- 门禁：`go test ./internal/kanban/ -count=1` 全绿（269.7s）；`go test ./internal/agent/claude/` 全绿；`go build ./server/... ./cli/...` OK；干净 worktree（HEAD）`make check-upstream` 绿 —— 2119 个 delta 文件被 344 组完整覆盖。
- 安装：从干净 worktree 构建并 `make install`，本机已装 `v0.5.5-1200-72-gad4db85a`；运行中进程仍为旧版，**待用户 `sudo systemctl restart mindfs`** 后五项修复（尤其 legacy 快照结算）才在生产生效。
- 未部署其他节点。Part B 只写设计，等用户对 B8 四个口径拍板后再拆执行计划。

### B8 裁定（2026-11-20，用户回答）

1. **阈值与预算**：维持现值 —— 60s 起判 + 两次观测确认；每节点最多 2 次监管重试；每 Run 每小时最多 6 个动作。
2. **预算耗尽**：停在 `waiting_user` 等用户，Router 不自动 `cancel_run`。
3. **直连回合**：纳入监管（S3 一起做）。
4. **节奏**：S0→S2 一口气实现；S3 及之后等后续授权。

### B9 S0–S2 执行拆分（按上述裁定）

- **S0 观测**：`SupervisionObservation`（开放节点水位、空闲秒数、会话是否在回复、attempt/已用重试、失败分类、预算余额）挂到 `run_failed` / `run_stalled` 事件 payload 的 `supervision` 块；配置 `router.supervision.{mode,max_retries_per_node,max_actions_per_hour}`（默认 off）。零模型调用。
- **S1 影子**：`mode=shadow` 时新动作只落 `router_decisions`（status=shadow），事件标 done + reason=supervision_shadow，不产生任何副作用。
- **S2 有限执行**：`mode=on` 放开 `retry_node`（走 `Service.CreateWorkflowNodeAttempt`，reason=supervisor_retry）、`notify_user`（可见系统行）、`silent`；预算与幂等在水位哈希上做；失败分类为「不确定」的一律只 `notify_user`。

### B10 S0–S2 执行记录（2026-11-20，已实现，默认关）

**实现形态**

- **配置**：`router.supervision.{mode,max_retries_per_node,max_actions_per_hour}`，只走 `config.json`
  （`cli/cmd/startup.go`、`cli/cmd/mindfs.go`、`server/cmd/mindfs-server/main.go`、
  `server/app/server.go`）。缺省空值归一化为 `off` + 2 次/6 个动作；未知 mode 也归一化成 `off`。
  不写这段配置时整条链路与改动前逐字节等价。
- **S0 观测**（`router_supervision.go`）：`supervisionObservation` 纯读任务库 + 会话活水位，
  产出 `SupervisionObservation{mode, run_id, task_id, open_nodes, actions_last_hour,
  failed_nodes[{node_id,node_run_id,status,attempt,failure_kind,summary,retries_used}]}`；
  失败分级 `retryable`（超时/流断/429/5xx/overloaded）、`non_retryable`（invalid/unauthorized/
  校验/合并冲突/not found/cancelled）、`uncertain`（`worktree_error` 或 `unknown` 状态）。
  `router_event_sources.go` 在 `run_failed` / `run_stalled` 事件 payload 里挂 `supervision` 块
  （已登记为 event-brief 的 producer key，不会被当成未知键截断；payload 不含 output/session/git/transcript）。
- **S1 影子**：`router_tools.go` 只在 brief 带 `Supervision`（mode != off）的事件回合追加
  `router_retry_node` / `router_notify_user`；`validateSupervisionActions` 与 on 同一套证据与预算校验；
  `router_coordinate_turn.go` 用 `supervisionActionSplit` 把监管动作从 apply 集合里摘出，
  决策标 `shadow`、事件 `done` reason `supervision_shadow`，零副作用。重放的 shadow 决策同样
  以 `done` 收尾，不会变成 failed。
- **S2 有限执行**：`mode=on` 时监管动作照常 apply —— `retry_node` 经端口
  `ActivityNodeRetrier` → `Service.RetryFailedWorkflowNode`（`CreateWorkflowNodeAttempt`，
  账本 reason 固定 `supervisor_retry`；下一次 attempt 已存在时幂等返回同一行）；
  `notify_user` 落一条 `Role=system` 的可见活动，effect ID 为
  `route-notify:<decision>:<seq>` + `InsertOrIgnore`，重放不会写第二条。
  预算用尽抛 `*supervisionBudgetError`（与校验错误分开）：`stopSupervisionForUser` 把决策标
  rejected、写一条可见系统说明、任务放回 `waiting_user`、事件 `done` reason
  `supervision_budget_exhausted`。Router 不自动 cancel_run。
- **水位新鲜度**：`turnBrief` 在决策时重算 `supervisionObservation`（不复用事件生产时的快照），
  否则同一轮连发的两个事件会各自看到「还有余额」。

**验证**

- 新增 7 条行为用例 `router_supervision_actions_test.go`：影子零副作用且决策状态为 shadow /
  on 重试失败节点（attempt+1、`PrevAttemptID`、账本 reason、任务回 running）/ uncertain 拒绝重试 /
  预算耗尽停 `waiting_user` 并写系统说明 / notify_user 幂等（重放仍一条）/ 重试幂等（一行账本）/
  监管工具只在带 supervision 的事件回合暴露。S0 另有 4 条用例在 `router_supervision_test.go`
  （配置归一化、观测内容、payload 块、shadow 模式）。
- 7 处变异各自打红后还原：去重试幂等检查、无条件暴露监管工具、影子模式照常 apply、
  允许重试 uncertain、关掉两条预算判断、notify effect ID 改随机、`turnBrief` 不重算观测。
- 门禁：`go test ./server/internal/kanban/ -count=1` 全绿（196.7s，重跑一次因既有 sqlite
  readonly flake）。`go build ./server/...` 当前被另一条会话在飞的 `agent/fold/anthropic`
  挡住（与本组无关），本组只依赖 kanban 包。
- **未部署、未重启任何节点**；`router.supervision.mode` 缺省 `off`，部署了也不改变行为。
- **提交与门禁（2026-11-20 18:0x）**：`2d1fac21`（实现 + 登记 + 计划）、`c0cb4961`（G-NF 锚点跟随
  StartOptions 对齐）、`515fc827`（撤回误入 main 的他人暂存改动）、`c9e3c76f`（补回 G-NQ 登记）。
  后两个是事故修复：共享工作区索引里另一条会话暂存的 `git mv` 删除被我的提交一并带走，HEAD
  上 `server/internal/agent/proxy` 无法编译；已按 `d114eaef` 恢复 17 个文件与 G-MN 登记。
  在干净 worktree（`c9e3c76f`）上复验：`go build ./...` 成功、`go test -count=1 ./...` 全绿、
  `make check-upstream` 全通过（**2160 个 delta 文件被 344 组完整覆盖**）。
- **本机未重新安装二进制**：主仓 HEAD 随后被另一条会话推进（`a009965d` fold 重构），从干净
  worktree 装 `c9e3c76f` 会漏掉那次提交；本机继续跑已装的 Part A 版本（`v0.5.5-1200-72-gad4db85a`）。
  监管默认 `off`，这一点不影响任何现有行为。
- **S3（直连回合监管）未实现**，按 B8 第 4 条等后续授权。

### B11 S3 + S4 执行计划（2026-11-20，用户指令「请继续往后实现 S3 等所有内容，不再等待」）

S3 拆成三个可独立回滚的提交，S4 一个；每步都按 TDD（RED → GREEN + 变异验证）。

**S3-1 `resume_run`（唤醒停住的 Run）**

- 类型：`RouterActionResumeRun = "resume_run"`；工具 `router_resume_run`（可选 `reason`）。
- 可用性/校验（`validateSupervisionActions`）：必须有 supervision 块；不得存在
  `failure_kind=uncertain` 的失败节点（证据不明时只许 `notify_user`）；必须至少有一个
  `Replying=false` 的 open 节点（真有回合在跑就不该唤醒）；每小时动作预算同样生效。
- 应用：`Service.WakeWorkflowRun(ctx, rootID, taskID, "supervisor_resume")`，走既有
  single-flight 唤醒（driver 重算 ready 节点，没有可推进的节点时自然无事发生）。
- shadow：归入 `supervisionActionSplit` 的监管动作集合。

**S3-2 `escalate`（升级给用户）**

- 类型：`RouterActionEscalate = "escalate"`；工具 `router_escalate`（`summary` 必填 ≤ 4 KiB）。
- 应用 = `stopSupervisionForUser` 的可见部分：确定性系统行 `route-escalate:<decision>:<seq>` +
  任务放回 `waiting_user`（`cancelled` / `success` 不复活）。**不 cancel Run**。
- 把「写可见系统行 + 放回 waiting_user」抽成共用 helper，`stopSupervisionForUser` 复用它。

**S3-3 直连回合监管（新事件 `direct_stalled`）**

- 事件：`RouterEventDirectStalled = "direct_stalled"`；实例键 `activityID + ":" + unix/300`；
  允许 `RunID=''`（仅这一种 kind），`InsertEvent` 对该 kind 放宽非空校验。
- 扫描（`router_event_direct.go`）：`type=agent_reply`、`status IN (pending,running)`、
  `COALESCE(run_id,'')=''`、创建 ≥ 60s、`!sessionRunning(task.MainSessionKey)`、与停摆观测表
  同一套「两次 inactive 观测（≥30s）」确认，复用 5 分钟桶去重。
- 触发映射：`RouterTriggerDirectStalled = "direct_stalled"`；`routerTriggerForEvent` 直连分支；
  `supervisionActionsLastHour` 把该触发计入每小时预算。
- brief：`SupervisionObservation` 增加 `Direct *SupervisionDirectState`（activity_id、状态、
  起始时间、空闲秒数、`session_replying`、`retries_used`、`retry_allowed`）；`turnBrief` 对
  direct 事件重算直连水位；event brief 在无 Run 时按 direct 分支给证据（不再报 run missing）。
- 动作：`retry_direct`（工具 `router_retry_direct`）。校验：direct 块存在、`retry_allowed`
  （同一轮直连最多一次，按 applied 且 `actions_json` 含 `retry_direct` 的决策计数）、小时预算。
  应用：端口 `ActivityDirectRetrier` → `Service.RetryStalledDirectReply` —— 先
  `SettleOrphanReply` 把旧行结算成 cancelled 并广播，再用确定性新行 ID
  `direct-retry:<decisionID>` 建一条新的 pending 回复（正文由 `ReplyToID` / 最近用户留言解析），
  经 `executeDirectActivity` 重跑。重放靠新行 ID 幂等。
- `router_prompt.md`：工具清单补三个新工具，规则 8/11 改成「运行期事件回合允许汇报类动作与
  监管动作（retry_node / notify_user / resume_run / escalate / retry_direct），仍禁止开新执行、
  代批 gate、cancel Run」。

**S4 可视化**

- 后端：`RouterStatus` 增加 `supervision_mode` / `supervision_actions_last_hour` /
  `supervision_decision_count`（本 root 最近的监管决策数），透出到 `GET /api/router/status`。
- 前端：任务详情新增 `SupervisionEntry`（与 `RunViewEntry` 同级折叠行），消费既有
  `GET /api/tasks/{id}/router-decisions`，只列监管类决策（trigger_kind ∈
  run_failed / run_stalled / direct_stalled，或 action_kinds 含监管动作），按时间倒序显示
  「触发 → 动作 → 结果」，空时整行隐藏。i18n 文案 + 源码契约测试。

**不放开 `cancel_run` 的理由**：B8-2 用户裁定「预算耗尽停 waiting_user，由用户接手，Router 不
自动 cancel_run」。原提案里 cancel_run 的唯一触发条件正是「预算耗尽 / 不可重试且已升级」，
放开它等于和该裁定直接冲突；`escalate` 已覆盖「机器停手 + 告诉用户」的语义。

**文件硬线**：本轮同时把 `router_tools.go`(512) 与 `router_event_sources.go`(509) 拆回 500 行内
（`router_tools_inspect.go`、`router_event_payload.go`）。

### B12 S3/S4 执行记录（2026-11-20）

**S3-1 / S3-2 已实现**（`router_supervision_s3_test.go` 6 条：resume 唤醒、有节点在回复时拒绝、
uncertain 失败拒绝、escalate 落系统行 + waiting_user、shadow 无副作用、resume 校验矩阵）。
`resume_run` 走 `WakeWorkflowRun`（single-flight），`escalate` 复用
`applyVisibleSystemRow` + `parkTaskWaitingUser`；两者都在 `supervisionActionSplit` 里算监管写动作，
shadow 只记录。

**S3-3 已实现**（`router_supervision_direct_test.go` 8 条 + 7 处变异各自打红）：
`direct_stalled` 事件、两次空闲观测、`SupervisionDirectState`、`retry_direct` 工具与校验、
`applyRouteRetryDirect`、`directEvidenceHolds` 证据复核、`RouterStatus` 用量字段。

与 B11 计划的两处偏离（实现后回看更稳，记录下来避免下次照抄计划）：

1. **重开没有走 `ActivityDirectRetrier` 端口**：实现直接在 `ActivityService` 里做（它本来就持有
   `activities` / `directExecutor` / 广播器）。加一层 `Service.RetryStalledDirectReply` 端口只会
   把同一段幂等逻辑（`direct-retry:<decision>` + `SettleOrphanReply` 条件更新）复制一遍。
   `ResumeRun` 仍走端口，因为它需要 `Service` 的 lane 与 driver。
2. **同一轮直连只自动重开一次**（`supervisionDirectMaxRetries = 1`）：B8-1 的「每节点 2 次」是
   Run 侧水位，直连没有节点；同一轮第二次仍失败说明重开解决不了，直接由预算错误走
   `stopSupervisionForUser`（可见系统行 + waiting_user），而不是再赌一次。

变异清单（每条都先打红再还原）：去掉第二次观测确认、会话在回复时不跳过、去掉 60s 最小运行时长、
`retry_direct` 不看 `Replying`、直连回合仍暴露 run-only 工具、去掉 `directEvidenceHolds`（补了
`TestDirectStalledEventSkippedWhenReplyAlreadySettled` 才捕获）、重放不按确定性行 ID 幂等。

**S4 已实现**：

- 后端：`RouterStatus` 三字段 + `supervisionDecisionStats`（24h 内按三种监管触发统计，最近一小时
  单独一列），`TestRouterStatusExposesSupervisionUsage` 钉住；变异（统计 SQL 去掉
  `direct_stalled`）打红后还原。
- 前端：`services/task/routerDecisions.ts`（`appURL` + 显式 `nodeId`）、
  `supervisionEntryModel.ts`（过滤/排序/标签键，纯函数）、`useSupervisionEntry.ts`（挂载即拉，
  `refreshKey` 重拉）、`SupervisionEntryList.tsx`（presenter，行 = 时刻 · 触发 → 动作 · 状态，
  错误另起一行）、`SupervisionEntry.tsx`（折叠行，无记录时整行隐藏），挂在 `TaskDetailPanel`
  的 `RunViewEntry` 之后；zh/en 各 18 个 `task.supervision.*` 键。
- 测试：`web/tests/task-supervision-entry.test.mjs` 7 条（筛选、动作去重、时间倒序与同刻稳定、
  坏时间容错、i18n 键映射、列表渲染、源码契约），六处变异各自打红后还原（去掉
  `direct_stalled`、不过滤动作、不排序、面板不挂载、空列表仍渲染、service 丢 `nodeId`）。
- 门禁：`npx tsc -b --noEmit` clean；`npx vitest run` 253 文件 / 799 通过 / 6 跳过；
  `go build ./server/...` OK；`go test -count=1 ./server/internal/kanban/` 全绿（193.2s）。
- **已提交并推送 origin main**：`7373d8fc`（S3-1/S3-2/S3-3 + S4）、`9019fa5f`（拆分后
  G-MH/G-MP 锚点跟随：`router_event_payload.go`、`router_tools_inspect.go`）、`68678fdc`
  （监管记录 hook 收尾）。干净 detached worktree 上 `go build ./...` + `go test -count=1 ./...`
  全绿、`make check-upstream` 2182 delta / 344 组全过。未部署、未重启任何节点；
  `router.supervision.mode` 仍缺省 `off`，要开启由用户改 `~/.config/mindfs/config.json` 并重启本机。
