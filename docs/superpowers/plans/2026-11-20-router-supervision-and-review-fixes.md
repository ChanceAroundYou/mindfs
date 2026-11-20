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
