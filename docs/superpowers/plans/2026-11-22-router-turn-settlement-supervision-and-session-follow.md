# Router 收尾可信化：每轮可读总结 · 任务级巡检 · 跟进会话

Plan 2026-11-22。用户要求「审阅任务运行和 Router，至少找出三点问题，结合现在待办出一个新规划」，
并点明三处现场：

> 1. 总结消息的可读性极低。mindfs #100 就是一个很好的例子，router 根本没有总结到点上。
> 2. router 没有管理好任务，没有定时检查，系统重启或其他原因中断时不会主动恢复。
> 3. 会话被推进时也完全没有跟进，比如 mindfs 的任务 #68。

本计划先给只读取证与根因，再给 W1–W5 波次。**尚未写代码、未改配置、未部署、未重启任何节点。**

## 0. 只读取证（2026-11-22，`/home/xiaokubao/projects/*/.mindfs/tasks/task-kanban.db`）

| 现场 | 证据 | 指向 |
|---|---|---|
| mindfs #100 卡片摘要 | `tasks.result_summary` = 「已交给执行会话：逐项对齐详情右上角按钮…」——Router 的**转交口信**，不是结果 | 总结归属错（G1） |
| mindfs #104 卡片摘要 | `result_summary` 9,001 字，是 Agent 连续叙述原文（「继续只做取证与方案讨论。先摸清…」） | 原文顶替总结（G1） |
| mindfs #68 卡片摘要 | `result_summary` 空；任务 11-16 建，11-19…11-22 在节点会话里作答 11 次提问，`router_decisions` **0 条**、`router_events` **0 条** | Router 输入只有一条（G3） |
| mindfs 主项目 11-20 起的 Router 流量 | 47 条决策**全部** `trigger_kind=user_message`；`router_events` **0 条**；动作 45×`message_agent` + 19×`say(progress)` + 1×`ask_user` + 1×`say(chat)`，`purpose=summary` **0 条** | 事件/监管/结算链路在真实流量里**从未启动**（G1+G2） |
| 决策耗时 | p50 22.9 s、p95 39.0 s、max 47.3 s（47 条） | 每次转交要 20–47 s，转交口信必然晚于或早于执行结束，谁最后写谁赢 |
| #100 两轮回复 | 正文以 `Prompt is too long` 开头（05:52 / 08:51），仍记 `completed` | harness 错误行进正文（G1） |
| #100 两轮回复 | `agent_reply` 0 字节却 `completed`（11-21 15:23）；`cancelled` + 0 字节（11-21 05:31） | 空产出算完成，无说明（G1） |
| 重启 | 唯一一条 `startup_reconcile` 是 11-19 的 backlog（`skipped`）；直连回合被重启带走只走 `settleOrphanDirectReplies`，**不发事件** | 中断不通知、不恢复（G2） |
| 提问卡作答 | `MarkTaskQuestionAnswered`（`service_task_prompts.go:95`）只 `ResolvePrompt` 收卡；不建用户活动、不发事件、不动任务状态 | 会话推进 ≠ Router 输入（G3） |
| 任务状态 | `beginTurnTaskStatus` 只服务任务自己发起的回合；#68 四天里一直 `waiting_user` | 看板不跟随会话现实（G3） |

## 1. 三个根因

**G1 · 总结没有归属。** 谁能写 `tasks.result_summary` 取决于「这轮有没有 Run」，而不是「这是不是结算时刻」：
- `applyRouteSay` 对**任何** say 调 `recordReplySummaryIfActive`（`activity_route_apply.go:265/273`、`task_store_router_guards.go:42`），没有 purpose 闸门 → 转交口信成了摘要；
- 没有 summary 时退回 `recordTurnResultSummary` 的原文，上限 `maxStoredSummaryRunes = 20000`（`service_activity_output_text.go:128`）；
- 异步压缩（90 s / ≤800 字，`service_activities_summary.go`）失败即保持原文，且它与转交口信抢同一个字段，CAS 经常对不上而静默失效；
- `purpose=summary` 只在 `run_settled` 保留（G-NX 的规则 18），而线上 100% 是直连回合（45/47 次 `message_agent`）→ **没有结算回合，也就永远没有总结**。

**G2 · 巡检半径只覆盖 workflow Run。** 30 s `coordinateTick` 只扫 `router_events` 与 1 小时内的 Run 状态
（`router_coordinate.go:211`）；监管动作只对 `run_failed` / `run_stalled` / `direct_stalled` 开放
（`router_supervision.go:381`）。重启对账对 workflow 发 `startup_reconcile`，但那一档只允许 say/ask/silent，
**只能汇报不能恢复**；直连回合被重启带走则一条事件都不发。停滞判定还要求「会话探针空闲」——会话进程僵住但
状态仍是 running 时永远不触发；全仓没有回合输出水位（无 `last_output_at`），无法回答「这轮到底卡了多久」。

**G3 · Router 只有一个输入。** 只有 `CreateUserActivity`（任务输入框的新留言）会触发 Router。提问卡作答、
用户在会话视图里直接继续同一段会话（#68 的 11 次提问）都不产生用户活动、不发事件、不改任务状态，
所以任务卡、看板、Router 全都不知道会话其实在往前走。

## 2. 设计原则

1. **结算由「回合结束」定义，不由「有没有 Run」定义。** 任务绑定的任何会话回合结束都必须产出一条结算事实。
2. **总结由 Router 写，但写不出来的代价必须是可读的兜底，而不是原文。** 兜底是确定性函数，不依赖模型。
3. **巡检只看任务级事实**（有没有活会话 / 有没有 open 节点 / 输出水位 / 有没有在等谁），
   与 workflow 引擎解耦；事件与动作沿用既有账本、预算与幂等口径，不另立一套。
4. **不静默。** 中断、停滞、空产出、被取消，都要在对话里有一句人话，并让 Router 有机会处理。
5. 旧行为可回退：所有新事件/新动作在 `router.supervision.mode=off` 下只落库不执行，与现状一致。

## 3. 波次与任务

### W1 · 总结归属与可读性（Scope: G-PL）

| # | 任务 | 落点 | 测试（先红） | 变异 |
|---|---|---|---|---|
| W1-1 | `result_summary` 只接受 `purpose=summary`；`chat` 仅在「本轮无任何执行动作」时允许；`progress` 永不写任务摘要 | `activity_route_apply.go`、`task_store_router_guards.go` | 转交口信（progress）不写摘要；纯聊天 chat 写；summary 写 | 去掉 purpose 闸门 → 转交口信用例红 |
| W1-2 | 摘要**硬上限 600 字**；超限按「最后一段完整句」截断；正文不受影响 | `service_activity_output_text.go` | 20k 原文只存 ≤600 字且不截断在半个词上 | 上限改回 20000 → 用例红 |
| W1-3 | 回合结束却没有 summary → **确定性兜底摘要**（`本轮未生成总结 · 用户诉求：… · 最后进展：… · 状态：成功/失败/中断`），禁止回落原文 | `activity_service_turns.go`、`service_activities_summary.go` | 兜底结构四段齐全、无原文泄漏、幂等 | 兜底改回原文 → 用例红 |
| W1-4 | 0 字节 / 只有错误行的回合记 `failed` 而非 `completed`，写一条 `role=system` 可见说明并发 `turn_failed` 事件；已知 harness 错误行（`Prompt is too long` 等）从正文剥离 | `activity_service_turns.go:130` 附近、`appcontext_activity.go`、新增 scrubber | 三种输入（空、仅错误行、错误行+正文）分别断言状态与正文 | 去掉 scrubber / 把空产出记 completed → 各自打红 |
| W1-5 | 转交口信默认不发：`message_agent` 成功后不再自动 `say(progress)`；只有「换路/被挡/需要解释」才写 progress | `router_prompt.md` 规则 18/21、`router_tools.go` 校验（不改协议） | 一轮 `message_agent` 只产生执行回复、0 条 progress | 恢复自动 progress → 用例红 |

验收口径：新产生的 `result_summary` 100% 来自 summary 或兜底，且 ≤600 字（#100/#104 类型不再复现）。

### W2 · 每轮结算事件 `turn_settled`（Scope: G-PM）

| # | 任务 | 落点 | 测试 | 变异 |
|---|---|---|---|---|
| W2-1 | 新事件 `turn_settled`：`EndSessionTurn`（回合唯一终结器，`appcontext_task_flags.go:57`）对**任务绑定的任何会话**（主会话 / 节点会话 / 委派会话）发出一条，payload 带：会话 key、turnID、时长、是否有产出、是否 out_of_band、是否来自提问作答、最后一条助手文本的前 400 字、git 侧变化（提交 sha / 改动文件数，取得到才写） | `appcontext_task_flags.go`、`router_event_types.go`、`router_event_sources.go` | 三种回合各产一条；同一 turnID 重放幂等；非任务会话不发 | 去掉事件 / 不计幂等 → 各自打红 |
| W2-2 | 结算回合动作集：`turn_settled` 允许 `say(purpose=summary)`、`ask_user`、`silent`、`notify_user`；`summary` 只在 `turn_settled` 与 `run_settled` 保留，其它回合降级 progress（沿用 G-NX 的降级闸门） | `router_coordinate_turn.go`、`activity_route_apply.go` | 误写 summary 的其他回合降级且不写摘要 | 放开降级 → 用例红 |
| W2-3 | 提示词：规则 18 从「run_settled 结算总结」扩成「**任何结算回合**（`run_settled` / `turn_settled`）都必须写一条 summary」，四段式（结论 / 证据 / 没做或未验证 / 下一步）、200–400 字；失败与中断也要写 | `router_prompt.md` | prompt 内容守卫（含 `turn_settled`） | 删掉 `turn_settled` → 守卫红 |
| W2-4 | 预算：`turn_settled` 不计入用户留言的 3 回合预算（与监管同类，`countUserTurnDecisions` 排除），每任务每小时上限 20（复用现有常量，不新增配置） | `router_coordinate_turn.go` | 连续两轮直连不被预算拒绝；超限 skipped 有原因 | 不排除预算 → 用例红 |

### W3 · 任务级巡检与主动恢复（Scope: G-PN）

| # | 任务 | 落点 | 测试 | 变异 |
|---|---|---|---|---|
| W3-1 | 输出水位：流帧更新回合 `last_output_at`（回复行 + NodeRun 各一份；内存为主、每 N 帧/每 30 s 落一次库，重启后退化为「未知」而不是「停滞」） | `session_stream_events.go`、`activity_store_running.go`、`workflow_node_runs` 写入侧 | 帧推进水位；静默不推进；重启后未知 | 不推进 / 把未知当停滞 → 各自打红 |
| W3-2 | `taskHealthSweep`（每拍，只读非终态任务）：分类 `healthy` / `stalled_turn`（在飞但水位 ≥10 min 不动）/ `orphaned`（任务 running 且无活会话、无 open 节点、无在飞回复）/ `waiting_busy`（waiting_user 但会话正在回复）→ 产 `turn_stalled` / `task_orphaned` 事件（实例键含时间桶，5 min 去重） | 新增 `router_task_health.go`，接进 `coordinateTick` | 四类各一条；同一现场 5 min 内只一条；健康任务零事件 | 去水位判据 / 去去重 → 各自打红 |
| W3-3 | 重启恢复升级：直连回合被重启带走 → 先 `turn_interrupted` 事件，再按任务状态决定；`startup_reconcile` / `turn_interrupted` / `turn_stalled` / `task_orphaned` 允许 `retry_direct` / `resume_run` / `retry_node` / `notify_user` / `escalate`（预算沿用每 Run 12 + 每小时 20） | `service_recover_direct_reply.go`、`router_supervision.go`、`router_tools.go` | 重启后 1 个 tick 内产生事件；`mode=on` 自动重试恰好一次；暂停/终态不重试 | 去掉自动重试 / 不限一次 → 各自打红 |
| W3-4 | 用户可见：任务详情「监管记录」已有条目；卡片在 `turn_stalled` / `turn_interrupted` 未处理时显示角标，处理后消失（zh/en 各若干键） | `web/src/components/task/*`、i18n | 前端契约用例（角标出现/消失） | 角标常显 → 用例红 |

### W4 · 会话推进跟随（Scope: G-PO）

| # | 任务 | 落点 | 测试 | 变异 |
|---|---|---|---|---|
| W4-1 | 提问卡作答 → 记一条 `user_answer` 活动（`role=user`，不占 3 回合预算）并让随后的 `turn_settled` 带上 `answered_question=true`；Router 因此看到「用户答复了提问，会话继续了」 | `service_task_prompts.go`、`appcontext_task_prompt.go` | 作答后活动存在、问答闭环、预算不变 | 不作答记录 → 用例红 |
| W4-2 | 任务绑定会话的 out-of-band 回合：开始时把 `waiting_user` 任务推进到 `running`（复用 `beginTurnTaskStatus`），结束时 `turn_settled(out_of_band=true)` 让 Router 决定是否汇报/收尾 | `appcontext_task_flags.go`、`activity_service_task_status.go` | waiting_user → running → 结算后回 waiting_user；节点会话同样生效 | 只处理主会话 → 用例红 |
| W4-3 | 任务「当前会话」解析：`main_session_key` 为空时用最近活跃的相关会话（`TaskRelatedSessionKeys` 已有）作为会话视图入口与轮询目标，避免 #68 那样「面板空白、真实工作在节点会话」 | `task_store` 查询侧、任务详情 API | 无主会话但有节点会话时能拿到 key；无任何会话返回空 | 回退到只看 main → 用例红 |
| W4-4 | 立即汇报一次的能力保留：`router_read_session` 已有，结算回合可读；不新增读工具 | — | 现有用例 | — |

### W5 · 验收（不改行为，只加验收脚本与记录）

1. 隔离实例（`scripts/mindfs-iso.sh`，真实模型）三场景：
   - **A / #100 型**：直连长回合（>10 min）→ 期间卡片摘要不是转交口信 → 回合结束有一条四段式 summary；
   - **B / #68 型**：提问卡作答两次 → 任务状态 running → 两次 `turn_settled` → 至少一条 summary 覆盖会话进展；
   - **C / #106 型**：长回合中途 `kill -9` 重启 → 5 min 内出现 `turn_interrupted` 与可见说明 → `mode=on` 自动重试一次并成功（或明确失败并告知）。
2. 线上只读审计（`/tmp` 一次性脚本，不入库）：任意 24 h 窗口内
   - `result_summary` 为空/「已交给执行会话」/`>600 字` 的条数 = 0；
   - 每条终态或等待中的任务都有最近一条 summary 或兜底；
   - `turn_settled` 数 ≈ 任务会话回合数（允许非任务会话不计）；
   - 中断/停滞事件都能在 1 个 tick 内被消费（`status=done`）。
3. 门禁：`go test -count=1 ./...`、`tsc -b --noEmit`、`npx vitest run`、干净 worktree `make check-upstream`。

## 4. 指标（进/出）

| 指标 | 现状（2026-11-22） | 目标 |
|---|---|---|
| 卡片摘要可用率（非空、≤600 字、不是转交口信、不以错误行开头） | 3/6 不可用（#68 空、#100 口信、#104 9k 原文） | 100% |
| 直连回合的 Router 结算覆盖 | 0 / 47 | 100% |
| 中断后用户可见说明 | 重启带走直连 → 0 条事件 | 100%，≤5 min |
| 停滞发现时间 | 仅在会话探针空闲时；无水位 | ≤10 min，且不依赖会话探针 |
| 提问作答后的 Router 跟进 | 0 次（#68 11 次作答 0 决策） | 每次作答都进结算事实 |
| 每任务每小时 Router 调用 | — | 硬上限（20）+ 每 Run 12 不变 |

## 5. 风险与回滚

- **模型成本**：每轮多一次结算回合（p50 23 s）。可接受，但需 W2-4 的每小时上限；`mode=off` 时结算事件只落库不调模型。
- **重复动作风险**：自动 `retry_direct` / `resume_run` 可能重复执行写操作 → 只对「被打断/停滞」的回合、每轮最多一次，先读会话再动手；写 workspace 的动作沿用现有 writer gate。
- **事件风暴**：所有新事件走时间桶 + 实例键去重，N 分钟只一条；巡检每拍只读一次任务表（有界查询）。
- **回滚**：`router.supervision.mode=off` 回到现状；`turn_settled` 事件保留但无人消费（与现在 `router_events` 一致），不影响任务流转。
- **依赖**：任务 #106（会话运行中重启丢记录）是会话层缺陷，本计划不修；若转录真丢了，恢复回合只能明说「会话记录不可读，需要你重新描述」，绝不假装继续。

## 6. 非目标

- 不改 workflow 引擎语义（run_settled / 节点重试口径保持）。
- 不做跨节点中继（属飞书 M4）。
- 不自动取消等 gate 的节点、不自动删除 worktree。
- 不引入新的 UI 框架；角标复用现有状态徽标组件。
- 不为「摘要」新增独立模型配置。

## 7. Scope 与登记

- W1 `Scope: G-PL` · W2 `Scope: G-PM` · W3 `Scope: G-PN` · W4 `Scope: G-PO`（均未占用）。
- 每个任务落地时同步登记 `docs/upstream-customizations.yaml`（文件 / 改动 / 为什么保留 / 可见症状 + 针对性测试表），并跑 `make check-upstream`。
- 与既有计划的边界：G-NX 的「结算总结」规则保留，本计划把它的适用范围从 `run_settled` 扩到 `turn_settled`；
  G-PG（超长会话提示）与本计划无冲突，`turn_settled` 可复用其上下文胶囊数据；G-PF（离线控制面）不涉及。

## 8. 默认值（实现时按此执行，用户可改）

| 项 | 默认 |
|---|---|
| 摘要硬上限 | 600 字（正文不变） |
| 结算回合 | 每任务每小时 ≤20；不计入 3 回合用户预算 |
| 停滞阈值 | 输出水位 10 min 不动 → `turn_stalled`；会话探针空闲不再是必要条件 |
| 中断恢复 | 非暂停/非终态 → 自动重试/恢复一次；再失败只通知不重试 |
| 转交口信 | 默认不发（`message_agent` 成功不再自动 progress） |
| 空产出 | 记 `failed` + 可见说明 + 事件 |
| 角标 | 只在 `turn_stalled` / `turn_interrupted` 未处理时显示 |
