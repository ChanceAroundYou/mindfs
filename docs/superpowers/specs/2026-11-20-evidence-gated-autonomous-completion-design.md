# 证据门 + 自动补做：让任务可以从头做到尾

- 日期：2026-11-20（v2，含 builder check 后的修订与两项用户裁定）
- 状态：设计待批准
- 相关：[Router 主动监管](../plans/2026-11-20-router-supervision-and-review-fixes.md)、
  [任务路由 P1](../plans/2026-11-14-task-router-super-agent.md)、
  [对话式任务](../plans/2026-11-12-conversation-driven-task-implementation.md)

## 1. 意图

用户的原话是目标，也是验收口径：

> 核心要实现的是各个组件自动运转，工作流可以自己跑起来，router 主动管理，我发布个任务，
> 就能从头做到尾，只有特殊情况需要我介入审核。而且能及时提醒我，外部出口提醒也可以留着，
> 未来可以接。同样也可以作为外部入口。

以及一条硬标准：

> 必须有可以核对的证据。

所以这一轮要补的不是「更聪明的 Router」，而是**任务的收尾权**：让 Router 在证据成立时
自己把任务判成完成，证据不成立时不许收尾，而是自己回去补做。

## 2. 现状：为什么现在做不到「从头做到尾」

四条结构性阻断，全部有代码依据：

1. **Run 成功不等于任务完成。** `taskStatusFromRun`（G-NC，2026-11-19）把 Run 的
   success/fail 一律投影成任务 `waiting_user`，任务的成功/失败只能由显式动作写入
   （`Service.Complete` → `finishWorkflowRunByOperator`，`service_run_moves.go:170`）。
   这条规则是对的（一次节点失败不该封板），但它同时意味着**没有任何自动路径能把任务收尾**。
2. **Router 没有收尾工具。** 现有 14 个工具（`say` / `ask_user` / `silent` /
   `run_workflow` / `message_agent` / `deliver_to_active_run` / `answer_waiting_node` /
   `notify_user` / `escalate` / `inspect_run` / `read_session` / `retry_node` / `resume_run` /
   `retry_direct`）没有一个能写任务终态。`RouterActionSuggestFinish`（`router_types.go:196`）
   是从 P2 留到现在的空枚举，从未被生产、从未被应用。
3. **运行期事件回合是受限的。** `validateRuntimeEventActions`
   （`router_coordinate_turn.go:422`）只允许 `say` / `ask_user` / `silent` 与五个监管写入
   （`retry_node` / `resume_run` / `notify_user` / `escalate` / `retry_direct`），
   且监管动作要求 brief 带观测块。也就是「发现问题只能重试或报错」——重试成功之后仍然
   停在 `waiting_user`。
4. **没有任何「做完了没有」的判据。** 节点的结构化信封是
   `{outcome, output, reason}`（`workflow_node_executor.go:434`），它天然携带可机检的产出，
   但从来没有人拿它当收尾依据。

结果：一条留言能触发一轮 Run，Run 能跑完，然后一切停在 `waiting_user`，等人来点「完成」。

## 3. 目标 / 非目标 / 反目标

**目标**

- G1 Router 获得 `complete_task`：证据成立才写任务成功，证据不成立一个字都不写。
- G2 证据分两层：工作流可声明**机检契约**（服务端求值，模型碰不到），没有契约时退到
  **独立复核回合**（另起只读会话给判定）。两层都必须产出人能核对的清单。
- G3 证据不成立时自动补做：落一条可见清单 + 一个去重的 `completion_blocked` 事件，
  由 coordinate 再给 Router 一个回合去补；补做有独立预算，用尽后停在 `waiting_user`
  并写清缺什么。
- G4 外部出口/入口只留接缝：定义端口，不实现飞书、桌面、ACP、webhook。
- G5 工作流任务与纯对话任务**都**适用（用户裁定「是，一起做」）。
- G6 撞用量上限的节点挂起到重置时刻，不在几秒内烧光重试预算（吸收 agent-autoloop 的
  `limits` + 到期索引；见 §6.9）。
- G7 节点信封解析失败先做一次格式修复轮，把「格式坏了」和「没做完」分开（吸收
  autoloop 的 `repairPrompt`；见 §6.4.3）。
- G8 证据缺口逐条编号、要求逐条回应（吸收 autoloop 审查账本的发现编号；见 §6.5）。
- G9 崩溃注入与变异测试脚本化，让「重启不丢事实、对账幂等」和「每条守卫的测试真的会红」
  变成可重复证据（见 §6.10）。
- G10 外部入口的准入校验形状与默认审核分类（见 §6.11）；跨 harness 一致性探针（见 §6.12）。

**非目标（本轮不做）**

- 延迟复核 / 定时复核（agent-autoloop 的 `later`）：证据暂时不成立但预期以后会成立
  （例如部署还没起来）的语义，本轮不做，落 `waiting_user` 交给人。
- 外部入口真正接进来（飞书消息 → 任务）。
- `cancel_run`（B8-2 已裁定不做）。
- 契约的可视化编辑器：本轮契约写在 workflow 定义里，由 agent-first 授权流程或 JSON 写入。

**反目标（做到也不算成功）**

- 任何「模型说自己做完了」被当成证据。模型的自然语言断言之于收尾，与它对代码的断言一样不可信。
- 证据缺失时静默收尾、或把缺证据的任务留在 `running`。
- 绕过 HumanGate：gate 未批准的 Run 永远不能靠 `complete_task` 收尾。
- 求值器做任何出站请求（HTTP/SSH）。证据只来自 MindFS 自己已经落库的事实。

## 4. 约束（不可违反）

- **不看模型脸色**：`complete_task` 的入参只有 `summary`（给人看的一句话）。契约求值在服务端
  由 Go 完成，Router 只负责「提议收尾」，不提供也不影响证据。
- **事件 payload 禁区**：`router_events.payload_json` **整体（键与值）**不得含 `output` /
  `session` / `git` / `transcript` 子串（`router_event_sources_test.go:150-166` 扫的是整串，
  不只键名）。因此证据进事件时**只放稳定 ID 与机器状态**（`id` / `kind` / `pass`），
  人读的 `detail` 只进活动行，不进 payload。
- **单文件 500 行硬线**；新逻辑按职责分文件。
- **fork 定制三件套**：登记 `docs/upstream-customizations.yaml`（新组，暂定 G-NP）、提交信息
  `Scope: G-NP`、干净 worktree 上 `make check-upstream`。
- **不改变既有语义**：`taskStatusFromRun` 不动（Run 成功仍投影 `waiting_user`），完成任务
  依旧只能由显式动作写入 —— 只是现在 Router 也能显式做这件事。
- 生产 `router.model=ok`、`mode=coordinate`、`turn_timeout=180s` 保持不变；**任何可能跑
  几十秒的工作（复核回合）不得放在 Router 回合里**，否则必然撞 180s 死线。

## 5. 方案比较

**A. 让 Run 成功自动收尾**：`taskStatusFromRun` 遇 success 直接写任务成功。
最省事，但它把「节点都跑完」当成「事情做完了」，正是用户否掉的失败截图。**排除。**

**B. 只做声明式证据契约**：工作流必须声明证据，没有契约的任务永远不自动收尾。
证据最硬，但对存量工作流与对话任务等于没有变化。**作为第一层保留，不单独使用。**

**C. 两层证据 + 自动补做（用户选定）**：有契约走 Go 求值（硬证据）；无契约走独立复核
回合（软证据，界面明确标注「模型判定」）。两层都不通过就不收尾，并把缺口变成下一次
Router 回合的输入。

## 6. 设计

### 6.1 契约形状

`WorkflowDefinition` 增加一个可选的 `completion`（`workflow_types.go:72`）：

```go
type CompletionContract struct {
	Requirements []CompletionRequirement `json:"requirements,omitempty"`
	// VerifyWhenUnverifiable 表示契约里有求值不了的项时，允许退到独立复核回合（默认 true）。
	VerifyWhenUnverifiable *bool `json:"verify_when_unverifiable,omitempty"`
	MaxRepairs             int   `json:"max_repairs,omitempty"` // 默认 2，夹 [0,3]
}

type CompletionRequirement struct {
	ID       string   `json:"id"`    // 稳定短 id，进系统行与事件 payload
	Label    string   `json:"label"` // 给人看的一句话
	Kind     string   `json:"kind"`
	NodeID   string   `json:"node_id,omitempty"`
	Keys     []string `json:"keys,omitempty"`
	Path     string   `json:"path,omitempty"`
	Decision string   `json:"decision,omitempty"`
}
```

`Kind` 只有四种，全部来自已落库的事实：

| Kind | 判据 | 数据来源 |
|---|---|---|
| `node_done` | 该 node 最新 attempt 的 NodeRun `status=success` 且 `Output["outcome"]="done"`；`Keys` 非空时每个键在 `Output` 顶层存在且非空 | `workflow_node_runs` |
| `gate_approved` | 该 gate 最新 attempt `status=success` 且 `Output["decision"]` 等于 `Decision`（缺省 `approve`） | `workflow_node_runs` |
| `all_nodes_terminal` | Run 投影里每个节点都在 `{success, skipped}`，且 `workflowRunHasOpenNodes` 为 false（复用现成判据） | `workflow_runs` |
| `file_exists` | 工作区（见下）里的普通文件/目录存在 | `os.Lstat` |

**冻结语义（builder check #3）**：`WorkflowRun.Graph` 名为「创建时快照」，但
`TaskStore.EditWorkflowRunGraph`（`task_store_workflow_runs.go:474`）能在 CAS 下整体替换它。
所以「只读 Run 快照」不足以保证判据不漂移，必须加守卫：

- `EditWorkflowRunGraph` 在写入前比较旧图与新图的 `completion`（规范化 JSON 后比对），
  **不同即拒绝**（错误原文：「completion 契约不能在中途修改，请开新一轮 Run」）；
- `SaveWorkflowRun` 路径同样不可改 `completion`（Run 的 CAS 保存只应动运行态字段，
  这条作为断言加进 `SaveWorkflowRun` 的实现）；
- 求值器永远只读 `run.Graph.Completion`，不读 `WorkflowVersion` / `WorkflowDraft`。

`file_exists` 的**工作区判定（builder check #10）**：若该项声明了 `NodeID`，用该 node 最新
attempt 的 `WorktreePath`（独立树时），否则依次回落到任务 worktree、项目根；未声明 `NodeID`
则直接用任务 worktree / 项目根。安全边界：`filepath.Clean` 后拒绝绝对路径与 `..`，拼出的
路径必须仍在所选根内（`strings.HasPrefix(root+sep)`），只做 `Lstat`，不跟随符号链接读内容。

### 6.2 求值器

新文件 `server/internal/kanban/workflow_completion.go`：

```go
type CompletionItemStatus struct {
	ID string; Label string; Kind string
	Pass bool; Detail string // 人读：实际看到什么
}
type CompletionReport struct {
	RunID    string
	Contract bool // 是否存在契约
	Items    []CompletionItemStatus
	Passed   bool
	Hash     string // 规范化后的报告哈希，用于事件去重与复核复用
}

func (s *Service) EvaluateCompletion(ctx context.Context, store *TaskStore,
	task Task, run *WorkflowRun) (CompletionReport, error)
```

- 纯读：只查 `workflow_runs` / `workflow_node_runs` / 任务工作区，不写库、不发事件、不调模型。
  可以被 brief 构建、apply 校验、UI 三处复用。
- 只取每个 node 的**最新 attempt**（`PrevAttemptID` 链尾），历史 attempt 不参与。
- 无契约时 `Contract=false`、`Passed=false`，并给出 `Items=[{ID:"contract", Label:"工作流未声明完成证据", Pass:false}]`
  —— 缺契约本身就是一个可见的缺口，不是隐形的「大概可以吧」。
- 任一 item 求值出错（读库失败、`Lstat` 失败但非 NotExist）一律 `Pass=false` + Detail 写原因，
  **fail-closed**。

**直连任务（无 Run，用户裁定纳入）**的机检项固定为三条，不读契约：

| ID | 判据 |
|---|---|
| `reply_settled` | 该任务最新一条用户留言存在且已结算的回复（不是 pending/running） |
| `no_live_reply` | 任务上不存在 pending/running 的回复活动（复用 `LiveReplyExists`） |
| `no_open_run` | 任务没有非终态 Run（防止对话收尾把还在跑的 Run 甩在后面） |

直连任务没有契约，所以它**总是**走 6.4.2 的复核路径（除非用户直接点完成）。

### 6.3 `complete_task` 动作

- 新 `RouterActionComplete RouterActionKind = "complete_task"`（`router_types.go:189` 旁）。
- 工具 `router_complete_task`，参数只有 `{summary, reason?}`，`Class: RouterToolWrite`。
- **删除** `RouterActionSuggestFinish`：从未生产、从未应用，且现在有了真正的收尾动作，
  留着就是两套近义枚举。
- 校验（`validateRouterActions`，`router_tools.go:317`）：
  - 一轮最多一个 `complete_task`；
  - 与 `run_workflow` / `message_agent` / `deliver_to_active_run` / `answer_waiting_node`
    互斥（不能同一轮既开工又收工）；与 `say` 可共存（收尾同时告诉用户）；
  - brief 里 `ActiveExecution` 有 open 节点时拒绝（没跑完不许收尾）；
  - 任务已在终态时拒绝。
- 运行期事件回合白名单加入 `complete_task`，**且不要求观测块**（它不属于监管动作，
  `run_settled` 这类事件回合本来就没有 `Supervision` 段）。其余约束不变：事件回合依旧
  不允许 `run_workflow` / `message_agent`。

### 6.4 应用路径与持久状态

新文件 `server/internal/kanban/service_completion_apply.go`，只在 apply 阶段执行：

```
applyCompleteTask(ctx, decision, action)
  ├─ 读任务；确定目标 Run（见下）
  ├─ report = EvaluateCompletion(...)                     // 纯读，毫秒级
  ├─ report.Passed == true
  │    └─ commitCompletion(report, summary, decision)
  ├─ 无契约 / 有不可求值项 且 允许复核
  │    ├─ 写 completion 行 status=verifying（幂等：decision_id 相同不再起第二次）
  │    ├─ 起异步复核（6.4.2），apply 立即返回「复核已开始」
  │    └─ 复核回写后：pass → commitCompletion；fail/uncertain → blockCompletion
  └─ 其它（有契约且明确不通过）
       └─ blockCompletion(report, decision)
```

**目标 Run 的确定（builder check #8）**：

- 运行期事件回合 → 只用 `brief.Event.RunID`（与 G-MP 给 `inspect_run` 定的先例一致）；
  读不到该 Run 就拒绝，绝不回落到「最新 Run」。
- 用户留言回合 → 取任务最新非终态 Run；没有则取最近一个已结算 Run；都没有 → 直连分支。

**持久状态（builder check #7）**：新增任务库表 `task_completion`（`migrateSchema` 建表）：

| 列 | 含义 |
|---|---|
| `task_id` | 主键 |
| `run_id` | 本次判定针对的 Run；换 Run 时 `repairs_used` 归零 |
| `status` | `pending` / `verifying` / `completed` / `blocked` |
| `report_json` | 最近一次 `CompletionReport` 的 JSON（项目/证据快照） |
| `repairs_used` | 本轮 Run 已用掉的自动补做次数 |
| `verifier_key` | `(run_id, report_hash)`；同一缺口复用上一次结论，不重复起会话 |
| `verifier_verdict` | `pass` / `fail` / `uncertain` / 空 |
| `decision_id` | 最近一次处理的决策，保证 apply 幂等 |
| `updated_at` | |

写入一律 `INSERT ... ON CONFLICT(task_id) DO UPDATE`。重启时把仍停在 `verifying` 的行改成
`blocked` 并写一条可见说明（「复核被重启打断，需要你再确认」），**不留静默悬挂**。

`commitCompletion` 是唯一的完成写入点：

- 写一条确定性系统行「完成证据」（ID `completion-evidence:<decisionID>`，`InsertOrIgnore`）；
- 有 Run：`finishWorkflowRunByOperator(StatusSuccess, "completed", runCompleted, summary)`
  （与人点完成共用同一实现，不再造第二套收尾路径）；
- 无 Run：`setTaskStatus(StatusSuccess, "completed", summary)`；
- `task_completion.status = completed`。

`blockCompletion`：

- 写系统行「还不能收尾」+ **逐条编号**的缺口（`1. … 2. …`，最多 8 条，其余折叠成「另有 N 项」；
  ID `completion-blocked:<decisionID>`，`InsertOrIgnore`）。编号是本轮与下一轮之间的契约：
  Router 的下一轮 brief 里缺口带同样的序号，回答必须逐条对应（§6.5），不接受「还没好」这种笼统回复；
- `InsertOrIgnore` 一条 `completion_blocked` 事件（6.5），`repairs_used+1`；
- 任务状态不动（绝不写 `running`）。

#### 6.4.2 独立复核回合（verifier）

**必须异步（builder check #4）**：它要读工作区、跑一轮模型，几十秒起步；放进 Router 回合里
必然撞 `turn_timeout=180s`，而超时会把整轮动作丢掉。所以它在 apply 之后由服务自己起，
用服务生命周期的 ctx（不是回合 ctx），**单次上限 90 s**（夹 [60,120]）。

- 用 agent pool 起**隐藏会话**，key 前缀 `verify:<task>:<run>:<n>`，不推进任务主会话、
  不进用户可见会话列表（复用 Router 会话的隐藏规则）。
- **只走 Claude harness（builder check #5）**：工具白名单 `Read` / `Grep` / `Glob` 只在
  Claude 适配器的 `ToolPolicy` 上强制（`pool_open.go:162` 对其它协议直接拒绝 ToolPolicy）。
  节点用的 harness 是 Codex 也照样用 Claude 复核 —— 复核是读工作区，与执行 harness 无关。
  该节点没有可用的 Claude agent → 复核不可用 → fail-closed，走 block + 交给人。
- cwd 为任务工作区（有任务 worktree 用它，否则项目根）。
- 输入：用户原始留言（≤8 KiB）+ Run 节点名/状态/`outcome`/`reason`（≤8 KiB）+ 未求值的契约项。
  **不给会话转录**（避免把执行者的说法当证据）。
- 输出严格 JSON：`{"verdict":"pass|fail|uncertain","findings":[{"item":"...","detail":"..."}],"missing":["..."]}`。
  解析失败、超时、进程错误一律 `uncertain` → 不收尾。
- 结论落 `task_completion.verifier_verdict`，系统行与 UI 明确标注「模型判定，非机检证据」。
- 预算：一次 Run 最多 1 次复核，按 `(run_id, report_hash)` 复用结论；复核本身也算一次
  `repairs_used`。

#### 6.4.3 节点信封修复轮（吸收 autoloop 的 `repairPrompt`）

现状：节点必须回一个严格 JSON 信封 `{outcome, output, reason}`（D5），解析失败即
`unknown`，直接甩给人。而「模型这一轮没按格式回」与「模型真没做完」是两件事，前者不该占用
人的注意力，更不该被判成失败。

设计（`workflow_node_executor.go` 的信封解析失败分支）：

- 判为「格式可疑」而不是失败：同一会话、紧随其后追加一条修复提示 ——
  只说「你的最后一条消息不是约定的信封。不要重做任何工作、不要调用任何工具，
  只把结果重写成一个信封 JSON」；预算 2 次，单次上限 120 s。
- 修复轮**不带任何工具**（ToolPolicy 为空），超时/再失败 → 才落 `unknown`（维持现状）。
- 交叉核对（不可省）：修复得到的 `outcome=done` 必须与已发生的事实相容 ——
  该节点 `writes_workspace=true` 且会话里没有任何写操作时，不接受 `done`，降为
  `blocked` 并写明「修复轮声称完成，但本轮没有观察到写操作」。核对只用已有的
  NodeRun 输出与工具调用记录，不读转录正文。
- 修复轮自身不产生新的 NodeRun，不推进 attempt；它只是同一 attempt 的收尾。
- 事件与监管：修复成功后节点正常 `success`；修复失败按今天的 `unknown` 处理，无新事件类型。

### 6.5 缺证据 → 自动补做

新事件 kind `completion_blocked`（`router_event_types.go:21` 旁）：

- **唯一键** `completion_blocked:<runID|taskID>:<reportHash>`：缺口变了才算新事件，
  同样缺口重复出现不刷屏。
- **允许空 RunID**（直连任务）：照 `direct_stalled` 的先例放宽
  `router_event_store.go:108-120` 的校验，只对这一种 kind 放行，`instance_key` 用 `task:<id>`。
- payload 只有 `{task_id, run_id?, items:[{id, kind, pass}], repairs_used, repairs_max}` ——
  键与值都避开禁区子串，`detail` 不进 payload。`items` 按 `id` 排序，**序号即系统行里的
  编号**，下一轮 brief 与模型回复都按这个序号对齐。
- coordinate 侧：`routerTriggerForEvent` 映射到 `RouterTriggerCompletionBlocked`，回合 brief
  带上 `completion` 段，模型看到「还差这几项」。

**补做能做什么（builder check #2，按现有白名单如实收窄）**：事件回合只允许
`say` / `ask_user` / `silent` + 五个监管写入。所以补做**只有一条真路径**：

- `retry_node`：**扩充语义** —— 除了现有的「重跑失败节点」，允许以
  `node_run_id` 指向「执行成功但证据不达标」的节点（`node_done` 项失败时），
  建 attempt+1 重跑。这需要在 `validateSupervisionActions` 里加一个分支，
  并在 `SupervisionObservation` 里把「证据不达标的节点」也列进可重试目标
  （新增 `evidence_failed` 失败类，只在本事件回合出现）。重跑会消耗既有的
  每节点 2 次预算。
- `message_agent` / `run_workflow` **在事件回合里依然不可用**。当缺口不是节点形状的
  （`file_exists` 缺文件、`gate_approved` 未批准、`all_nodes_terminal` 还有 open 节点），
  补做无从下手 → Router 只能 `escalate` + `notify_user`，任务停 `waiting_user`。
  这一点必须写进设计而不是假装能补。
- `gate_approved` 失败时**禁止**任何自动动作（人没批就是人没批）。

**预算**：

- 每个 Run 的补做次数 = `task_completion.repairs_used`，上限取契约 `MaxRepairs`（默认 2，夹 [0,3]）；
  换 Run 归零。`completion_blocked` 回合**不占** `RouterCoordinateMaxTurnsPerUserActivity`
  （那条预算的算术是「入口 1 + 跑完 1 + 补做 1 = 满」，一条留言只够补一次，机制会形同虚设）。
  链深度上限照旧。
- 每小时资源预算：`supervisionActionsLastHour`（`router_supervision.go:200`）的
  `trigger_kind IN (...)` 增加 `completion_blocked`，与监管共用 6 次/小时。
- 用尽后：写可见系统行「自动补做 N 次仍未满足证据，需要你介入」+ 逐项缺口，停 `waiting_user`，
  不再产生同 hash 的新事件。

### 6.6 brief 与工具面

`SituationBrief` 增 `Completion *RouterBriefCompletion`（`router_brief.go:34` 旁）：

```go
type RouterBriefCompletion struct {
	Present     bool   `json:"present"`      // 是否存在契约
	Items       []RouterBriefCompletionItem `json:"items,omitempty"` // id/label/kind/pass/detail(≤200)
	RepairsUsed int    `json:"repairs_used"`
	RepairsMax  int    `json:"repairs_max"`
	CanComplete bool   `json:"can_complete"` // 服务端预判：现在调用会不会通过
	VerifierAvailable bool `json:"verifier_available"`
}
```

- 只在「可收尾位置」挂上：Run 已结算且无 open 节点，或直连任务没有在跑的回复。
  其余时刻为 nil，工具也不暴露 —— 与 `Supervision` 段的既有做法一致。
- `policy_hints` 增一条：证据缺口只能用真实动作补（重跑/补做），不得把缺口写进 summary 蒙混；
  summary 里的文字不会被当证据。
- 工具暴露条件：`CanComplete==true` **或** 存在未通过项（后者让模型能「试一次」并拿到
  服务端给的缺口清单，作为补做的输入）。两种情况下 apply 都重新求值，模型无法用调用本身
  改变结果。

### 6.7 可见性与接口

- 系统行（时间线）：完成 → 「完成证据」逐项 ✅/❌ + 复核标注；阻塞 → 「还不能收尾」逐项缺口。
- 新端点 `GET /api/tasks/{id}/completion`（与 `/engine`、`/router-decisions` 同形），返回
  `{completion: {present, status, items, passed, repairs_used, repairs_max, verifier_verdict, updated_at}}`。
  builder check 指出仓库里没有 `GET /api/tasks/{id}`，所以不挂在「详情」上，走独立只读端点。
- 前端在运行视图下方渲染紧凑清单（复用现有详情面板样式，不新开浮层）。
- `/api/router/status` 增 `completion_blocks_last_hour`，便于观察有多少任务卡在证据上。
- **误判的恢复路径**：任务详情已有「回到待审核」（G-NC `reopen`），加上证据行可回看，
  不需要新开关；真要整体停手就是 `router.mode=off`（既有配置）。

### 6.8 外部出口 / 入口：只留接缝

- 出口：本轮**不新增**通知类型。`webpush.ValidateRelayedPayload`
  （`webpush/relay.go:100`）只认 `session.done` / `session.ask_user` / `scheduled.done|failed` / `test`，
  新增 `completion_blocked` 要同时改 relay 与安卓壳，超出这轮范围。阻塞告知走既有
  `notify_user` 动作 + 系统行（应用内可见）。外部渠道（飞书/webhook）只定义端口：
  `kanban.ExternalNotifier { Notify(ctx, TaskOutcome) error }`，实现留空、默认 nil。
- 入口：定义 `kanban.TaskIntake { Accept(ctx, IntakeRequest) (TaskID, error) }` 端口与
  `WorkflowTriggeredBy` 新增 `"external"` 常量。本轮不实现 HTTP 端点。
- 两个端口都不注册到任何构造函数：只要接口与常量，不产生死代码路径。

### 6.9 配额挂起：撞上限的节点等到重置（G6）

**现状是缺陷**：`classifySupervisionFailure`（`router_supervision.go:236-241`）把
`rate limit` / `429` / `overloaded` 判成 `retryable`，而 `retry_node` 立刻建 attempt+1 ——
全仓 kanban 没有任何 `not_before` / 延迟概念（`grep -rn "not_before\|NotBefore"` 零命中）。
配额撞墙时两次重试在几秒内烧光、预算耗尽、escalate 给人；证据门的自动补做会踩同一个坑。

**设计**：

- `workflow_node_runs` 增加 `not_before`（TEXT，UTC，空 = 立即可跑）。
- `retry_node` / 自动补做在配额类失败时写 `not_before`，**不**消耗每节点 2 次重试预算
  （改消耗每个 Run 的 `holds_used`，上限 3；超过才 escalate）。「等待」不是「重试」。
- 重置时刻的来源，按可信度排序：
  1. 错误文本里可解析的重置时间（`resets at` / `reset at` / `重置于` / ISO 时间），
     解析结果必须落在 `now + 1min … now + 7d`，出界视为误读并丢弃（autoloop 同款判断：
     用量上限不会超过一周）；
  2. 解析不到 → 默认 `now + 15min`（只走 `config.json` `router.supervision.quota_hold`，
     缺省 15m，夹 [1m, 6h]）。
- 到期唤醒：coordinate 的 30 s 扫描增加一步「`not_before <= now` 的行改回 queued 并
  `WakeWorkflowRun`」，不需要新定时器。
- 引擎侧：`EngineReadyNodes` 跳过 `not_before` 仍在未来的行（与现有「暂停时不 claim」同形）。
- 可见性：系统行写「节点 X 撞到用量上限，已挂起到 HH:MM，到点自动继续」；任务停在
  `waiting_user`，不报错、不 escalate。
- 直连回合不受影响（没有 NodeRun）。

### 6.10 崩溃注入 + 变异账本（G9，吸收 `sim/crash.ts` / `sim/mutate.ts`）

两件独立的工程工具，都是 opt-in，**不进 `deploy-all` 门禁**（kanban 全包 190–270 s，
逐条变异跑不起全量）。

**`scripts/crash-sim.sh`（崩溃注入）**

- 起隔离实例（复用 `scripts/mindfs-iso.sh` 的形状），跑一条真实任务，在随机时刻
  `kill -9`（至少覆盖：claim 后未绑定会话、节点回合结束后未写投影、效果写入后未推游标、
  回复结算前）。
- 重启后断言四条不变量，任一不成立即失败并打印现场：
  1. 没有 `running` 的回复活动残留（要么结算，要么被启动对账收口）；
  2. 没有「任务非终态、Run 已结算、投影与行都在终态」的悬挂组合；
  3. 第二次重启不新增任何事件（对账幂等）；
  4. 数据库可读、无半写行（`PRAGMA integrity_check`）。
- 默认 5 轮 × 每轮 1 次 kill，`CRASH_SIM_ROUNDS` 可调；输出一份可贴进计划的报告。

**`make mutate`（变异账本）**

- 表驱动：每个变异点写 `{name, file, before, after, package, run}`，脚本在临时副本上
  逐条注入、跑**定向** `go test -run`，断言变红；输出 `killed / survived / invalid` 比例。
- 首批收录本轮与 G-NQ/G-NP 已手工验证过的变异点（evidence 求值、repair 预算、
  not_before、信封修复、对账幂等），让「我验证过会红」从口头变成可重放。
- `survived` 非零即失败退出（守卫失效或测试没盖到）。

### 6.11 入站准入与默认审核分类（G10，吸收 `protocol/rules.ts`）

本轮**不接任何真实渠道**，只把接入那天必须先有的形状固化下来：

- `kanban.IntakeRequest` 的校验器：未知字段拒绝、必填校验、**一次性列出全部问题**
  （而不是遇错即返），与 `protocol/rules.ts` 的 `RulesError.issues` 同形。
- 一张默认审核分类表（常量，不是配置）：删除数据 / 停止服务 / 花钱 / 对外发消息 /
  改产品方向 / 新建或轮换密钥 / 修改权限 —— 命中即要求人工确认，Router 不得自动执行。
  这张表同时写进 Router 提示词的 `policy_hints`，让模型知道哪些事必须问。
- 端口 `TaskIntake` 保持未实现、未注册。

### 6.12 跨 harness 一致性探针（G10，吸收 `protocol/conformance.ts`）

- `server/internal/agent/conformance`：对**隔离实例的真实 HTTP/WS** 跑一组探针，
  断言 claude 与 codex 两个适配器在共同面上给出同样形状（会话打开/关闭、芯片与用量、
  错误事件、取消、compact 事件的三种形态）。
- 只覆盖「两者都该有」的部分；harness 特有的字段显式列在例外表里。
- 与 `scripts/crash-sim.sh` 一样只跑在隔离实例上，绝不指向 7331。

## 7. 与既有机制的关系

| 既有机制 | 本设计如何相处 |
|---|---|
| `taskStatusFromRun` → `waiting_user`（G-NC） | 不动。完成仍只由显式动作写入，Router 现在有资格做这个动作 |
| `Service.Complete`（人点完成） | 不动。Router 走 `commitCompletion`，共用 `finishWorkflowRunByOperator` |
| S0–S4 监管 | 保留。新增 `evidence_failed` 失败类只在本事件回合出现；预算共用一个每小时口径 |
| coordinate 事件扫描 | 新增一种 kind（允许空 RunID），沿用唯一键去重、claim、replay 语义 |
| `router_events` payload 禁区 | 证据进事件只放 ID 与 pass 位，人读文本只进活动行 |
| HumanGate | 未批准时 `gate_approved` 不可能成立，且该类缺口**禁止**自动补做 |
| agent-first 授权（typed patch/CAS） | 图编辑守卫：`completion` 不可中途修改 |
| 会话复用 / Run 共享会话 | 复核会话独立命名空间，不写进任务会话链 |

## 8. 已经定下的决策（用户）

1. 方案 C：声明式证据契约 + 无契约时独立复核回合。
2. 本轮范围：证据门 + 自动补做；不做延迟复核（`later`）。
3. **直接四端全开**：不加 `off/shadow/on` 模式开关，部署即生效。
4. **纯对话任务一起做**：直连任务也自动收尾。
5. **吸收清单 A + B 全部并入本设计**（「a+b都进设计方案 然后开始执行」）：配额挂起（A1）、
   信封修复轮（A2）、缺口编号（A3）、崩溃注入与变异账本（B1）、入站校验与审核分类（B2）、
   跨 harness 一致性探针（B3）、回合用量差分（B4，落在账本字段上）。
   **C 档明确不吸收**：完整事件溯源、规则引擎 DSL、插件独立进程模型、`cards` 状态机、
   `later` 延迟复核。

## 9. 留给 builder（实现自由，但结论必须写回计划）

- 契约的**写入方式**：本轮通过 draft graph JSON / agent-first 提案写入，不新增 typed patch 操作，
  也不做编辑器 UI。
- 复核会话的提示词与 findings 的渲染细节。
- `task_completion` 的字段类型与迁移写法（`migrateSchema` 既有约定）。
- 系统行文案与前端清单排版。
- 求值器的单元测试矩阵（四种 kind × 缺 attempt / 失败 attempt / 键缺失 / 路径穿越 /
  `Lstat` 错误 / 独立树工作区 / 换 Run 归零）。
- `evidence_failed` 目标如何进入 `SupervisionObservation`（只在本事件回合注入）。

## 10. 我的判断（已按此写进上文，builder 可推翻但要说明）

- **删除 `suggest_finish` 枚举**：死枚举 + 新动作 = 两套近义语义。
- **`completion_blocked` 不占每留言 3 回合预算**，改用每 Run 的 `repairs_used`；链深度与
  每小时 6 次照旧。
- **`complete_task` 进运行期白名单且不要求观测块**：`run_settled` 是主收尾路径。
- **复核必须异步**：与 180s 回合预算互斥，放进回合必然超时。
- **复核只走 Claude**：ToolPolicy 只有 Claude 适配器能强制，其它 harness 上「只读」无法保证。
- **无契约时「缺契约」本身作为失败项展示**：诚实优于默认放行。
- **事件 payload 只放 ID 与布尔**：禁区扫整串，人读文本不该进事件。
- **配额等待不消耗重试预算**（等待 ≠ 重试），单独记每 Run 3 次 hold；否则「挂起到重置」
  会挤掉真正的重试机会。
- **信封修复轮上限 2 次、单次 120 s、不带工具**：再宽松就等于允许模型重做一遍而不留痕迹。
- **崩溃注入与变异账本不进 `deploy-all` 门禁**：全量太慢，逐条变异会让每次部署多花十几分钟；
  它们是证据工具，不是门禁。

## 11. 风险与诚实边界

- **契约覆盖的是「能机检的事」**：`node_done` 只能证明节点按约定输出了 `done` 和指定键。
  真正的正确性仍依赖工作流作者把要求写进节点提示词与 `keys` 里。
- **复核是模型判定，不是证明**：界面必须如实标注；它存在的意义是让没有契约的任务也能
  自动收尾，而不是让证据变硬。
- **自动补做只覆盖节点形状的缺口**：缺文件、gate 未批、还有 open 节点这三类只能交给用户。
  这是本轮的真实上限。
- **全开意味着误判会直接写进任务状态**：恢复靠「回到待审核」+ 证据行回看 + `router.mode=off`
  急停。这是用户明确选择的取舍。
- **信封修复轮有被模型利用的余地**：它可能「补一个漂亮的信封」而不是补工作。所以只接受
  与已发生事实相容的结果（§6.4.3 的交叉核对），且修复轮不带工具、不改 attempt。
- **配额重置时间靠文本解析**：解析不到就退默认 15 分钟，可能早于真实重置（多挂几轮）或晚于
  真实重置（到点仍失败，再挂一次）。宁可多挂，不空烧。
- **崩溃注入是采样不是证明**：5 轮随机 kill 都过，不等于任意时刻 kill 都安全；报告里必须写
  轮数与 kill 点，不许写成「已验证崩溃安全」。
- **未覆盖**：部署健康这类「证据晚到」的场景；跨节点（worker 无 workflow 库）的契约求值
  按现有「数据面各机器自有」处理，不跨节点读。
- **未验证**：真实模型下 Router 是否会正确调用 `complete_task`（而不是 `say` 一段总结）；
  补做是否真的收敛；异步复核与修复轮在 `ok` 模型下的实际耗时；配额挂起在真实 429 上的
  表现。这几点都必须用隔离实例 + 真实模型验收。

## 12. builder check 的 11 个问题如何落地

| # | 问题 | 处理 |
|---|---|---|
| 1 | 是否加 off/shadow/on | 用户裁定：不加，直接全开（§8.3） |
| 2 | 事件回合里补做能用哪些动作 | 如实收窄为 `retry_node`（扩到证据不达标节点）；其余缺口只能 escalate（§6.5） |
| 3 | Run 图可被整图替换，契约并不冻结 | 加 `EditWorkflowRunGraph` / `SaveWorkflowRun` 守卫（§6.1） |
| 4 | 复核同步做会撞 180s | 改异步 + 90s 上限 + 重启收口（§6.4.2） |
| 5 | 复核用哪个 harness / 模型 | 只走 Claude（ToolPolicy 唯一可强制处）（§6.4.2） |
| 6 | 直连任务是否在范围内 | 用户裁定：在范围内；事件允许空 RunID（§6.2 / §6.5） |
| 7 | 结论存哪、哪个接口暴露 | 新表 `task_completion` + `GET /api/tasks/{id}/completion`（§6.4 / §6.7） |
| 8 | 事件回合判定哪个 Run | 只认 `brief.Event.RunID`，不回落（§6.4） |
| 9 | `completion_blocked` 是否计入每小时预算 | 计入，扩 `trigger_kind IN (...)`（§6.5） |
| 10 | 独立 worktree 里 `file_exists` 以哪个树为准 | 按 `NodeID` 取该节点最新 attempt 的 `WorktreePath`（§6.1） |
| 11 | 通知复用与 payload 禁区 | 本轮不新增通知类型；payload 只放 ID 与布尔（§6.8 / §4） |

## 13. 吸收清单（agent-autoloop）与落点

| 编号 | 吸收项 | autoloop 出处 | 本设计落点 |
|---|---|---|---|
| A1 | 配额挂起到重置，而不是立刻重试 | `kernel/due.ts` 的 `limits` 分支、`decide.ts` 的 `lift` | §6.9 |
| A2 | 信封读不懂时只重写最后一条消息 | `worker/contract.ts` 的 `repairPrompt` | §6.4.3 |
| A3 | 缺口逐条编号、逐条回应 | `plugins/delivery/review-ledger.ts` | §6.4 / §6.5 |
| B1 | 崩溃注入 + 变异账本 | `sim/crash.ts`、`sim/mutate.ts` | §6.10 |
| B2 | 入站一次性校验 + 默认审核分类 | `protocol/rules.ts`（`RulesError.issues`、`askFirst`） | §6.11 |
| B3 | 跨适配器一致性探针 | `protocol/conformance.ts` | §6.12 |
| B4 | 回合用量差分（先落基线，崩溃重读不重复计） | `worker/usage.ts` | 折进 §6.4 的 `task_completion` 与折叠账本字段，不单列 |
| C1 | 完整事件溯源（facts 唯一写入 / 投影 / overlay） | `kernel/evolve.ts`、`decide.ts` | **不吸收**：等于重写任务库，与多账户、数据面各自为库冲突 |
| C2 | 规则引擎 DSL（cron + source 匹配 + lane） | `protocol/rules.ts`、`kernel/cron.ts` | **不吸收**：与 Router 争调度权威 |
| C3 | 插件独立进程模型 | `plugins/*` | **不吸收**：与单二进制 + systemd 部署冲突 |
| C4 | `cards` 作为唯一对外状态机 | `kernel/decide.ts` | **不吸收**：与任务/会话/回复三层重复 |
| C5 | `later` 延迟复核、`5m·2^n` 退避 | `kernel/decide.ts` | **不吸收**（本轮）：`later` 用户已裁定不做；退避并入 §6.9 |
