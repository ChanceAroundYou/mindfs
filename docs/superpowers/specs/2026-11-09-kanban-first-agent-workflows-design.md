# MindFS 看板优先的 Agent 工作流架构设计

日期：2026-11-09  
状态：待用户审阅批准；仅架构设计，尚未进入实现  

## 意图

把 MindFS 看板任务提升为 Agent 协作的一等对象：人从任务留言，默认要求该任务继续执行；Agent 的提问、答复、进度、派发与执行结果回到同一个任务活动流。用户无需进入 Agent 会话才能日常推进工作。看板仍是主要工作视图，Agent 会话是执行介质与可深入检查的 transcript，而不是任务真相源。

同时把现有僵硬的任务模板演进为可视化、可复用、agent-first 的工作流定义。工作流规定任务如何在不同 Harness/Agent/模型间协作；任务运行实例从定义生成快照，保留实际执行历史，并允许调整尚未触发的部分。

## 目标

- 任务详情的核心是类似 issue 的交互留言板：独立、持久、追加式的人类留言、Agent 回复和系统/执行活动流。留言板是用户与 Agent **交替发言**的时间线。发布留言时可人工选择工作流、禁用工作流（仅普通续聊、不套工作流），或留空让 Agent/Router 自己选择（见“留言的工作流选择”）。
- Agent 段与运行视图从属于留言板：agent 段是留言板内的可展开条目（查看本段执行情况）；运行视图是留言板之下的下钻只读视图（时间线/树/泳道）。它们不是与留言板平级的独立区域。
- 看板是核心视图，按各种状态分列任务；工作流模板编辑是独立视图，从看板的合适位置直接跳转进入。模板编辑页按桌面端设计，移动端只保证简单查看，不做复杂画布操作。
- 任务详情设 Agent 执行段：以合适的只读视图呈现执行结果（时间线/树/泳道），用户可对未完成节点操作（暂停、重试、跳到 session、编辑未触发部分）。NodeRun 的本质是一个 session：可跳转到对应对话，复用原 session 还是新起 session 按节点/会话实际情况（StageTemplate.SessionReusePolicy）决定。
- 留言默认继续所属任务。活动中的 Agent/Harness 若支持 steer，先尝试注入当前回合；否则按序排队，当前回合完成后由同一任务消费。
- 以版本化工作流模板替代现有线性阶段预设的主要编辑方式；在 React 画布上定义节点、分支、数据交接和显式循环。
- 每次运行固定模板版本的初始快照；运行中对尚未触发的节点允许用户/Agent 修改，修改只影响后续执行；已发生的节点、输入、输出和决策保留为审计历史。
- 同一任务的工作流节点可分派给多个 Agent/Harness/模型并交接结果。第一阶段不创建独立 Kanban 子任务。
- Agent 可协助新建/修改工作流草稿，或从已完成任务建议沉淀模板；发布/覆盖共享模板必须由用户明确确认。
- 单节点服务重启后可从持久化状态与原 Agent session 尝试续跑。无法证明执行状态时标记 `unknown` / 需要用户处理；第一阶段绝不自动重试不确定的节点或留言，因为模型/工具可能已产生外部副作用。操作人可检查已有 transcript/活动记录后显式发起新 attempt；系统保留原 attempt，不声称 exactly-once 副作用。

## 非目标

- 本阶段不接 GitHub Issue 或其他外部 Issue 系统。
- 不承诺跨节点任务接管。节点、文件、session 和 worktree 的可移植性差异太大；节点归属只作为明确记录的扩展信息，不设计成当前租约或迁移保证。
- 不允许 Agent 因普通留言创建独立 Kanban 子任务；跨任务派发及 Router 自主选择子任务工作流属于未来独立能力。
- 不允许运行中的节点定义被覆盖；只可调整尚未触发的运行副本节点。
- 不把任务状态、工作流状态的另一份 LLM 摘要当作权威数据。
- 不引入 Temporal/Hatchet、Redis、Postgres 或独立调度服务作为本阶段必要依赖。

## 已确认的架构约束

- **并行写入边界**：worktree 管理加强为本阶段设计目标：同一 Task 允许拥有多个 worktree，工作流节点可按需自动开启/关闭 worktree 并自动合并结果。默认仍保守：同一 Task 内写同一工作区的节点串行；只有显式开启多 worktree 并行时才允许独立分支并行 coding。并行 coding 需要设计 worktree 生命周期、分支/合并冲突处理、writer gate 按 worktree 而非仅按 Task 的隔离粒度。详细设计见“多 worktree 与自动合并”。
- **看板优先**：Kanban Task 是目标、协作和执行归属主体；看板页按状态分列任务，是应用核心视图。任务视图的核心是 issue 式留言板。工作流模板编辑是独立视图，从看板跳转；桌面端完整画布编辑，移动端仅简单查看。Agent 会话是执行介质与可深入检查的 transcript，而不是任务真相源。现有 Task.Status 继续作为看板聚合状态；WorkflowRun/NodeRun 保存细粒度执行状态，投影到现有列时不能由单个节点状态覆盖 Task 语义。
- **留言即继续**：新留言入库后默认产生该任务的一次继续请求；如运行中，尝试真实的 steer capability，否则 FIFO 排队。当前 MindFS Session 接口没有独立 steer 操作，普通 SendMessage 活跃期间排队；新增明确的 adapter 能力声明前，所有 Harness 默认走 FIFO。只有协议明确回执时才能报告 steer confirmed；留言授权范围是继续该 Task，不自动扩展为发布工作流、跨任务操作、部署、删除或其他高影响动作。
- **模板图与运行图分离**：模板为可复用静态图；任务运行有自己的图快照及节点运行实例。运行图默认用于审阅实际执行，但未触发子图可编辑；每次编辑创建新 revision，已触发节点、已触发边和既有执行结果保留在历史中。
- **循环是一等结构**：仅使用可视化、类型化的 Loop 容器，不开放任意图回边。Loop 必须设置最大迭代次数及超限策略。达到上限时暂停任务、进入“需要你处理”，保留已有结果和评审意见，不自动判失败或丢弃工作。
- **Agent-first 草稿、用户治理发布**：Agent 可创建/改动草稿和运行副本未触发部分；发布共享模板生成新不可变版本并要求用户确认。草稿按现有模板 scope（全局或 project/root）存于共享控制面；作者和 revision 留审计记录。未保存改动在节点/工作流卡片上以醒目标记显示，离开编辑视图时提醒保存。
- **实例内恢复**：任务归属节点负责运行和恢复；重启恢复原 session 是目标，按持久化运行记录协调，不以“复用 session”本身推断上一次 turn 是否成功。无法确认时标注 unknown，给出人工恢复入口；用户选择 retry 时建立新的 attempt ID 并链接原请求/NodeRun，不复制留言、不声称副作用 exactly-once。

## 术语与核心实体

- **Workflow**：有版本的可复用图定义，含节点类型、边/条件、Agent profile、Harness、model、prompt、输入输出映射及 loop 配置。
- **WorkflowVersion**：已发布、不可变的模板版本。Agent 修改总是产生 Draft，不覆写已发布版本。
- **Task**：已有的看板任务，持有目标、当前状态、活动流和当前 WorkflowRun 引用。
- **WorkflowRun**：Task 对某一 WorkflowVersion 的实例化运行。保存起始快照、可编辑的未触发节点增量、活动图版本以及节点运行历史。
- **NodeRun / LoopIteration**：一次节点/循环迭代执行；绑定 Agent/Harness/model、输入、输出、session key、状态与开始/结束证据。重复节点由迭代号区分，不覆盖历史。
- **TaskActivity**：统一按时间显示的 typed 记录（user_comment、agent_reply、system_event、run/node lifecycle、attention/question、delivery state）。消息原文是持久记录，投递状态是单独的执行元数据。
- **ContinueRequest**：由某条 TaskActivity 触发的幂等执行请求，关联 task、activity ID、workflow run 与消费状态；同一留言重复提交不得产生两次执行。

- 工作流数据所有权按项目既有边界：WorkflowVersion/Draft 作为共享控制面数据存放主节点；Task、留言、ContinueRequest、WorkflowRun、NodeRun 是账户/节点数据面真相，存任务所属节点。新建任务时由控制面将选定的不可变 workflow version 连同标识发送/内联到任务所属节点；任务运行和后续修改未触发节点都不依赖 worker 在线访问主节点模板库。Agent 对运行副本的修改写回任务所属节点；Agent 草稿写到控制面 Draft API，发布仍需用户确认。浏览器跨节点留言和状态操作显式传 task node ID，控制节点不得直接写 worker 任务库。
- 新旧执行模型互斥：旧 Task 默认继续由现有 `Stages/StageRun` 引擎推进；只有显式迁移到 WorkflowRun 后才冻结 legacy stage engine。一个 Task 同时最多一个活动执行引擎。旧 Task.Status 继续是看板聚合状态和兼容 API 的外部状态：ready/pending workflow 投影 pending；存在可运行 NodeRun 投影 running；人工 gate、loop 上限或 unknown 投影 waiting_user；旧任务的 paused 继续是 paused；全部必需节点成功投影 success；仅明确不可恢复错误投影 fail；用户取消投影 cancelled。WorkflowRun/NodeRun 是已迁移任务新图执行的细状态真相，legacy `current_stage_*` 不写入/驱动新图。升级时不批量转换旧任务，不对旧引擎双写新运行模型。
- **评论身份**：TaskActivity 记录提交时 MindFS 的 `user=` 账户 ID 和角色（human/agent/system）；此字段只用于归属、显示和审计，不构成安全授权或权限边界。相同 comment ID 若作者或正文不同，返回 conflict。
- **条件分支合流**：合流节点只等待本次运行实际选择且可达的前置分支。未选路径记为 `skipped`，不阻塞合流；所有实际选中的必需前置都必须成功或由显式条件处理其失败后，合流节点才可运行。
- **Draft 编辑/发布治理**：草稿是共享控制面版本对象，编辑需带 `base_revision` 做 compare-and-swap，冲突时返回最新稿并要求重新合并；发布必须确认具体待发布 revision ID。若期间草稿又变更，旧确认不能发布新内容。
- 每条留言使用稳定客户端生成的 comment/request ID；评论行与 ContinueRequest 绑定在同一任务节点的事务里写入。重复 HTTP/WS 重试返回原来的记录和请求状态，不重复 steer、排队或创建 NodeRun。同一个 ID 携带不同 body/author 返回 conflict，不当成新评论；ID 至少在 task scope 内唯一。
- WorkflowRun 的 event ledger 为 append-only、与状态投影同库事务提交；顺序按 `(run_id, seq)`，`seq` 由 run 内事务串行分配。NodeRun 标识为 `(node_id, loop_path/iteration, attempt)`。事件 ledger 是运行视图和审计顺序真相；WorkflowRun/NodeRun 当前状态行是恢复调度的权威投影，若 ledger 与投影不一致，启动 reconciliation 按已提交 ledger 补建投影，不由模型推断。
- Agent side-effect start 走持久化 outbox：先事务提交 ContinueRequest/NodeRun 和 outbox item，再由 worker 消费。外部发送超时且无法判断是否启动时标 delivery/run `unknown`，不得自动重放；运行中并行只读节点的完成事件在单写者事务中分配 seq。新建任务时主控制面不可用就返回明确错误，不允许 worker 自选模板。
- 并行写入规则：画布仅允许 ReadOnly 节点并行；所有可能写入 Task worktree 的 Agent 节点标成 `writes_workspace=true`，默认按 Task 串行（per-Task durable writer gate）；开启多 worktree 时按 worktree 粒度隔离。人类本地编辑冲突通过现有 worktree/git 状态检测暴露，不以文件名猜测隔离。
- **运行时编辑范围**：每次运行编辑通过 compare-and-swap 提交，客户端必须带当前 run revision；revision 不匹配返回 conflict，由用户/Agent 拉取最新图后重新编辑，不用 last-writer-wins。已经触发的节点及已经发生的执行边冻结；仍未触发的子图可增删节点、修改节点配置、调整依赖边与条件。触发定义：NodeRun 已创建且已持久化 queued/started event 后，该节点及定义它的边被冻结。连接“已触发前沿”与“未触发子图”的尚未消费边视为未来边，可调整；已经走过的边保留不可变记录。一个节点处于互斥已触发时，它自己的 config 不可改，但 downstream 可编辑。保存前验证既有执行前缀可解释、future executable path 可达、输入/输出引用和 condition route 有效、loop 上限仍明确；禁止删除已有 NodeRun 引用的节点或改写已触发/完成记录；拒绝有歧义的变更并说明原因。共享 WorkflowVersion 绝不原地改写。

## 工作流图语义

1. **基础节点**：第一阶段只有 `Agent`、`Condition`、`HumanGate`、`Loop`、`Notify` 五类；`Agent` 节点显式配置 `writes_workspace`（默认 true，除非类型/权限由系统证明只读），true 节点进入 per-Task writer gate 串行执行。`Notify` 仅产生 MindFS 内任务通知/活动，不等于对外发布。
2. **普通边**：只表达有向、无环的数据/控制依赖。所有条件是受限、确定性的声明式表达式（比较已持久化的 node output JSON fields；禁用 shell、JS、eval 和外部网络）；同一 node 所有前置完成后才评估 `when`。condition node 只能在其 outputs 声明的固定 route enum 中选路，不允许任意代码。
3. **Loop 容器**：封装一个内部 DAG 子图、退出条件及必填正整数 `max_iterations`；第一版默认上限 5，可在模板中设 1–20。每轮结束以 loop exit expression 决定退出或再开下一轮；exit expression 使用与普通条件相同的安全表达式。一个任务可有多个 Loop，但首版禁止嵌套 Loop，避免预算乘法与递归边界不清。未退出且达到上限时任务暂停为 needs-user，不能静默成功。
4. **多 Agent**：同一 Task 内不同节点可选不同已配置 Agent/Harness/model。只读研究/审查分支可并行。写节点默认同 worktree 串行；多 worktree 并行见“多 worktree 与自动合并”一节（writer gate 按 worktree 粒度隔离）。每个并行节点有独立 NodeRun，合流节点等待所需前序结果。
5. **Router**：任务内确定性路由 + 留言的工作流选择（见“留言的工作流选择”）：已选工作流的留言归属该 WorkflowRun 继续快照；禁用工作流的留言仅普通续聊；留空时 Agent/Router 提出候选。LLM 可建议路由，不直接写状态或越权启动。跨任务 Router 不在本阶段范围内。
6. **工作流变更**：发布模板只新增版本。任务启动时固定 base version；运行副本在节点未触发前允许修改。每次运行编辑保存新 snapshot revision；已开始节点保留当时定义快照，后续未触发节点按最新运行 revision 调度。任何修改不回写共享模板，除非另走草稿发布并经用户确认。

## 留言、steer 与队列行为

1. 先以幂等请求 ID 持久化用户留言及 ContinueRequest，再向执行端投递；持久化成功是 API 接受的边界。comment ID 由客户端在第一次提交前生成并在重试中复用；服务端对 `(task scope, comment ID)` 唯一约束。Activity 与 ContinueRequest 在所属节点同一事务提交。
2. 如果当前 NodeRun 的 Harness/Agent adapter 声明支持 steer，尝试向当前活动 session 注入。**MindFS 当前没有独立 steer API**：Agent `Session` 接口只有 `SendMessage` / `CancelCurrentTurn`，正常活跃回合提交会排队。因此新增明确的 capability contract；第一版不声明 steer 的 adapter 全走 FIFO，不以普通 SendMessage 假装 steer。状态至少区分 `recorded`、`delivery_pending`、`steer_sent`、`steer_confirmed`（仅协议有确认时）、`queued`、`consumed`、`delivery_failed`；不能确认时不得虚报“已读”。
3. 不支持 steer、session 不活跃或 steer 明确拒绝时，将同一 ContinueRequest FIFO 排队。不得同时向同一个 session/worktree 开第二个执行 turn。排队项在当前 NodeRun 结束后由调度器按顺序消费；首版不合并留言，每条评论保留独立 ID、执行归因和投递结果。若 Task 进入终态/取消，未消费请求留在活动流并标为 blocked/needs-user，不隐式丢弃或重新开任务。
4. Agent 回复、节点结果和错误以独立 Activity 类型写回任务并关联 run/node；活动视图可显示“送达/排队/已消费/失败”，不能把投递回执误报为 Agent 已读或已执行。
5. 留言若要求越出 Task 的当前授权范围，Agent 应通过结构化 attention 暂停并等待人处理；不得因“留言默认继续”把授权自动扩大。

## 留言的工作流选择

- 发布留言时提供工作流选择：人工指定一个 WorkflowVersion / 禁用工作流 / 留空。
  - 人工指定：ContinueRequest 绑定到该 WorkflowRun（或在任务尚未绑定时创建/绑定一次新 Run 快照），后续执行走该快照。
  - 禁用：仅普通续聊——留言作为当前 session 的 continuation，不创建或推进 WorkflowRun 节点。
  - 留空：Agent/Router 根据任务目标提出候选（建议不自动执行），用户确认后才绑定。
- 选择结果写入 TaskActivity 记录，随留言持久化；重复提交/重试不重复绑定。
- 不得因“留言默认继续”自动扩大授权：发布共享模板、跨任务派发、部署、删除等高影响动作仍走各自确认流程。

## 多 worktree 与自动合并

- 单 Task 的 worktree 管理加强：同一 Task 允许拥有多个 worktree（每个 worktree 有独立分支名、归属 NodeRun/节点、生命周期状态）。
- 节点可声明需要独立 worktree 时，系统自动创建 worktree + 分支；节点完成或被取消时自动关闭（可配置保留期）。
- 自动合并：并行 worktree 的结果按声明的 merge 策略自动合并到 Task 主工作区/集成分支（如直接 push 到集成分支、或创建可复查的合并提交）。合并冲突进入 needs-user，不自动解决、不覆盖用户改动。
- writer gate 粒度：默认按 Task 串行；多 worktree 模式下，同一 worktree 内写节点串行、不同 worktree 可并行。gate key = (task_id, worktree_id)。
- 现有 AppContext.CreateTaskWorktree / worktree 引用计数收尾逻辑需要扩展为多 worktree 注册表：创建、归属、回收、与 session repoint 的协作（transcript 已在主 slug 时只需清归属）。

## 任务视图与 Agent 执行段

## 任务视图与留言板结构

- 看板页按状态分列任务（核心视图），卡片可跳转任务详情或工作流模板编辑视图。从看板的合适位置直接进入工作流画布（模板 Draft 编辑器）。
- 任务详情核心是 issue 式留言板：用户与 Agent **交替发言**的时间线（不是独立的评论框 + 独立执行段两个平行区域）。留言板即 TaskActivity 的呈现。
- 每条 Agent 发言是一个「agent 段」：可展开/进入查看该段的执行情况（该发言对应的 NodeRun、session、执行结果）。agent 段是留言板内的可展开条目，不另起平行视图。
- **Agent 执行段和运行视图都位于留言板之下**（层级从属）：从 agent 段展开进入执行详情，从留言板整体进入运行视图（时间线/树/泳道，见下）。它们是留言板的下钻视图，不是与留言板平级的独立 tab/区域。
- 运行视图是只读追踪（不复用模板编辑画布语义）。用户对未完成节点的操作（暂停、重试新 attempt、编辑未触发部分）可从 agent 段/运行视图的下钻入口触发。
- NodeRun 本质是 session：从 agent 段或运行视图可跳转到对话。复用原 session 还是新起 session 按 StageTemplate.SessionReusePolicy / 实际情况决定。
- 工作流画布按桌面端设计；移动端只保证简单查看，不做复杂画布操作。画布核心可延后于后端领域模型。

## 恢复与可用性

- 单节点重启恢复只承诺**原 Task 所属节点、本地 workflow run、原 session**。恢复流程先读取持久的 run/node/request 状态，再向 Agent adapter 查询可用 session/turn 状态；只有可确定无重复且可续接时恢复，不可确定时记 `unknown` 并等待人检查。
- **Unknown 不自动重试**：服务重启后若无法判定 Agent 是否已消费留言/节点输入，保留原 Comment、ContinueRequest、NodeRun 和 transcript 证据，不重新发送同一 prompt，也不自动创建 replacement attempt。任务进入 needs-user/unknown；用户检查证据后显式选择继续、创建新 attempt 或放弃。创建新 attempt 使用新 attempt ID 并链接原记录。MindFS 只能保证自身输入去重，不能保证 Agent 工具/外部副作用 exactly-once；用户显式重试仍可能重复已发生的副作用，应在 UI 提示。
- 每个 ContinueRequest、NodeRun、LoopIteration 和 attempt 有稳定 ID/序号；启动恢复及重复 HTTP/WS 请求不得重复消费。
- 需要明确定义状态更新与发起外部 side effect 的 outbox/after-commit 边界，避免事务提交前启动 Agent，或提交成功但未安排工作后永久丢任务。若队列/发送的提交窗口结果未知，记录 delivery unknown，由恢复流程按上条处理，不自动重放。
- 任务数据和工作区依然按现有账户/节点数据面规则归属；模板属于共享控制面。跨节点 resume、worktree 迁移、session 所有权转移都标为未来研究，不作当前产品承诺。

## 研究参考与适用边界

- [Kandev](https://github.com/kdlbs/kandev)（HEAD `36476ce8641da64df9e9bea509dbec2b6b818893`，AGPL-3.0）：Go/React、任务内 chat、可导入导出的 YAML workflow、按步骤 Agent/model profile。其文档所述主要是带事件转换的 workflow step，不等于可视化任意 DAG；本设计参考概念，不移植运行时。
- [DeepSeek dsh-superagent-prototype](https://github.com/deepseek-ai/dsh-superagent-prototype/tree/8c32a9dc5d72c033816c6a8110b37f40cf94fba4)（MIT，README 声明 research prototype、非正式产品）：参考授权、任务/会话分离、留言触发与 durable effects 设计；不把论文/帖子里未实现功能视为原型事实。
- [Raven](https://github.com/EverMind-AI/Raven/tree/d30865ea5332d43fa63b6ed0037399961f56bf8)（Apache-2.0）：参考多 Agent 依赖、派工和结果可观测性；其 DAG 不等于持久化分布式队列。
- [Beads](https://github.com/gastownhall/beads/tree/2edea9ced8c6f6124b6c157896ae53a0e09ff7be)（MIT）：参考 issue graph、replies-to 讨论关系、parent/blocks 关系和 ready/claim 语义；不作为 MindFS 存储引擎依赖。
- [React Flow / xyflow](https://github.com/xyflow/xyflow/tree/1bc405cd113618c17fd181b931a9fe573bec9fee)（核心 MIT）：作为 React/TS 画布 UI 候选；工作流 schema、校验、版本化和运行语义由 MindFS 所有。画布可延后于后端领域模型。
- 避免直接引入需要独立 Redis/Postgres/worker 集群的编排产品作为阶段一必需组件；MindFS 是 Go 单进程/本机安装产物，并有控制面与各节点数据面的分离约束。对 n8n 的 fair-code UI/嵌入条件不做代码复用。

## 实施阶段建议（不是已批准执行计划）

1. **任务 Issue 化**：typed activity/comment 持久化、任务详情留言 UI（issue 式留言板，含工作流选择：指定/禁用/留空）、稳定 comment ID 与默认 ContinueRequest、Agent 回复回写。看板页为核心视图，支持跳转工作流模板编辑视图。先打通单个现有阶段/session 的闭环；工作流能力不变。
2. **运行可靠性与 steer**：adapter capability contract（首发 adapter 默认 queue-only，直到实现并验证真实 steer）、FIFO、幂等请求/outbox、单节点 restart reconciliation、unknown no-auto-retry 与人工新 attempt、运行/留言状态呈现、NodeRun 即 session 的跳转与复用/新建策略。
3. **工作流领域模型 + 多 worktree**：定义版本、Draft、图 schema、普通 DAG 验证、显式 bounded Loop、workflow run snapshot、未来未触发子图编辑 revision、typed run event log、NodeRun 历史、只读并行/写入串行（writer gate 按 worktree 粒度）、多 worktree 自动开启/关闭与自动合并（冲突转 needs-user）；先由 JSON/YAML API/测试验证，旧模板兼容迁移策略单列。
4. **Agent-first 工作流管理**：Agent 生成/改草稿和运行副本未触发部分，支持增删未来节点和改未来边、diff/验证/试运行；共享发布要求显式确认；从成功任务提出模板建议但不自动发布。
5. **画布编辑与运行视图**：用 React Flow 等候选实现编辑草稿（桌面端完整，移动端仅查看）；dirty indicator、离开确认、Loop 容器编辑、验证错误；任务详情 Agent 执行段以时间线/树/泳道展示运行实例，不复用模板编辑画布语义。
6. **未来 Router 扩展**：只在之后单独设计跨任务子任务创建、父子关系/阻塞依赖、路由规则与跨节点归属；不得从本设计推断阶段一就有此能力。

阶段 1/2 可独立交付并先产生用户价值；工作流图模型、agent 草稿编辑和画布可另作阶段计划。当前文档不授权开始任何实现或部署。

## Approaches considered

1. **只在现有流水里继续追加 prompt**：保留当前结构最省事，但无法区分讨论与工作流定义，难以表示并行派工、条件循环、不可变发布版本和运行历史；不采用。
2. **直接采用外部通用工作流引擎/低代码平台**：可快速得到部分画布或 durable task 功能，但引入服务/数据库、运行时与 MindFS 任务/session/worktree 的第二套真相，并违背多节点部署边界；不作为目标架构。
3. **MindFS 自有任务协作层 + typed graph WorkflowDefinition + per-task editable WorkflowRun snapshot**：复用 SQLite、Go/React、Agent pool、session/worktree；持久化任务留言/运行/恢复由 MindFS 控制，画布作为 workflow schema 的编辑器；bounded Loop 显式表达。**推荐并经对话选择**，理由是保持看板为中心、能适配本地 Harness 与数据所有权，同时逐步加复杂度。

## 留给实施设计的问题

- 第一版 Workflows 只用于新建任务选择；存量任务继续 legacy engine。迁移到 WorkflowRun 是单独、显式的用户操作，生成一份独立快照；迁移成功后才停用该任务 legacy engine。当前阶段不要求旧模板自动转换、不双写，并保留 legacy 模板编辑/执行作为兼容路径。
- 第一版并行边界：只读研究/审查可并行；同 worktree 写节点串行；多 worktree 模式下不同 worktree 可并行，合并冲突转 needs-user。
- 实现 steer 的回执语义：本阶段若增加真实 steer adapter，哪些协议回执足以从 `steer_sent` 进到 `steer_confirmed`；没有回执时发送超时转 queued，不得当作已读。MindFS 现有普通 `SendMessage` 不构成 steer。
- workflow schema 的 wire format（JSON 内部 API 与 YAML 导入导出之间的稳定契约）。
- Loop 的停止条件表达式、安全预算/最大耗时和默认迭代上限值。
- 多账户用户身份与评论作者表示；依现有 API 的账户分区是配置隔离，不是安全边界。
- “部署、删除、发布”等高影响操作沿用其各自授权/确认流程，不因任务留言默认执行而获得隐式授权。
- 每条评论绑定一个 ContinueRequest；Comment ID 与 author/scope 唯一。评论到达活动节点时：支持真实 steer 且接收回执则留在当前 NodeRun；否则在当前 NodeRun 完成后，先把所有 queued comments 按序逐条交给**同一节点的同一 session**作为续聊回合。该节点报告完成/阻塞后才评估工作流边、推进下一个节点。若当前节点终态/任务取消或不再有可恢复 session，队列不改投后续节点，而进入 needs-user/blocked 供人决定。
