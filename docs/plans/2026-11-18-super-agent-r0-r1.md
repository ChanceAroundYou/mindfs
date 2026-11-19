# Super agent R0/R1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development or executing-plans. 本轮由主会话通过 MindFS 原生任务 HTTP API 派发；worker 不得再派子代理。只执行获派的一个任务，按 checkbox 记录证据。

**Goal:** 验证 P1 路由真实行为，建立 Router-owned Run 的持久化、受限运行期协调。
**Architecture:** 沿用 kanban.RouterService、workflow runtime、SQLite；先验证 P1，再实现事件账本、事件生产、协调消费与结果汇报。每项独立 worktree，依赖项通过审核后由工头集成。
**Tech Stack:** Go / SQLite / React-TS；真实 agent 由现有 MindFS 工作流承载。
**Spec:** `docs/superpowers/specs/2026-11-18-super-agent-r0-r1-design.md`
**授权:** 用户已要求文档、小任务、worktree 与实际派发；当前先执行 R0，R1 在 R0 门禁后按依赖推进。R2–R5 不执行。

**状态（2026-11-19）：R0 与 R1（T1–T10）全部通过工头验收，已集成到总任务分支 `task-73`（HEAD `c6a4e629` + 登记提交）；R1 收尾（汇报折叠、直达工作流会话、事件 brief 假截断）与留存缺口收口（G-MP）同日完成，见下节。已合并 main（`7f9e710c`）、`bash scripts/deploy-all.sh` 部署四端（`v0.5.5-1120-4-g7f9e710c-dirty`）、本机已由用户重启（2026-11-19 11:14:46，PID 3817171，`/proc/<pid>/exe` 指向 `~/.local/bin/mindfs` 且非 deleted）。生产 `router.mode` 在本机为 `route`：该值 2026-11-18 17:12 写入 `~/.config/mindfs/config.json`，重启前进程不读取文件变更，故重启后才生效；`GET /api/router/status` 已确认 `mode=route`，用户 2026-11-19 决定保持观察。重启后至 11:20 无新活动（`task_activities` 与 `router_events` 均 0 条），route 尚未被真实流量触发。**

## Global Constraints

- 本机 system mindfs 不重启；不改生产 Router/账户/工作流配置；不动其他会话文件。
- 一个任务一条执行线；实现 worker 最多 1 个，只读 worker 最多 1 个，任何本轮进程限制 1 线程。
- 每任务独立 worktree；worker 不改主 checkout、工头 worktree、共享计划、派发表或定制清单。
- worker 可在自己的任务分支提交，不 push、不部署、不合并/删除 worktree、不自动批准 gate。
- 共享文档和 docs/upstream-customizations.yaml 只由工头写；每个提交必须 Conventional Commits 中文和 Scope。
- R1 只协调 Router-owned Run；specified / disabled / legacy Run 不被自动接管。
- 每条原始留言最多 3 个 Router turn；运行期只允许 say / ask_user / silent / inspect_run。
- R1 不自动开新 Run/direct、不 cancel/retry、不 merge；用户留言仍遵守 D3 单活动执行。
- 有界摘要不包含文件、密钥和全量转录；原始用户要求、事件与结果必须对应同一回合。
- 不以 fake tests 代替真实 agent 验收；隔离结果不宣称为线上验证。
- 新增 Go 文件不超过 500 行，超过 300 行需判断拆分；完整回滚不删除历史任务数据。

## Review Focus

1. 运行中留言被 prepareUserTurn 提前拒绝，导致 deliver 实际不可达。
2. 同任务多消息排队仍沿用入队时 brief，导致重复派发或错误转交。
3. 事件生产与结算跨事务，进程退出导致漏事件或重复可见回复。
4. 旧 Run 事件抵达时任务已经进入新回合，造成误汇报/状态覆盖。
5. 误接管显式 workflow、HumanGate 普通留言代审批、链式触发无限循环。

## 文件与职责

| 文件 | 职责 | 拥有任务 |
|---|---|---|
| docs/plans/2026-11-18-super-agent-dispatch.md | 派发与完成状态 | 工头 |
| router_r0_audit_test.go | P1 入口、排队、回退行为 | T2 |
| activity_service.go / router_service.go | 只修 T1 证实的 P1 入口问题 | T3 |
| e2e/test_task_router_r0.py | 隔离真实路由证据 | T4 |
| router_event_types.go / router_event_store.go | 持久事件、唯一键与领取 | T5 |
| router_event_sources.go | 从权威运行状态补齐事件 | T6 |
| router_coordinate.go | 消费、深度限制、允许动作 | T7 |
| router_event_brief.go / router_prompt.md | 事件证据和汇报契约 | T8 |
| router_coordinate_recovery_test.go | 并发/重启/事件隔离 | T9 |
| e2e/test_task_router_coordinate.py | 真实协调闭环 | T10 |

T5–T10 接口在 T1 审计和 R0 结束后由工头定稿再派发，未定稿的任务保持 pending，不能凭下文概述开写。

## Task 1: 基线核查与可执行性审阅（R0，第一批）

**Files:** 只读当前源码；只写派发表指定的独立 T1 报告。禁止改源码与共享文档。
**Consumes:** spec、当前任务 API、P1 route 实现与测试。
**Produces:** 文件/行号证据表；可运行验收命令；T2/T3 是否需要调整的明确结论。

- [ ] 核对 git HEAD、branch、worktree 与在线 health；报告只允许输出自身任务资料。
- [ ] 阅读 router_service、activity_service、service_user_turn、router_brief、route tests。
- [ ] 跟踪唯一活动节点 deliver 入口，判断是否被执行守卫提前挡住；给最小复现步骤。
- [ ] 跟踪同任务排队 brief 的取值时机，判断状态是否会过期；给最小复现步骤。
- [ ] 核对中断 applying 的准确恢复行为，禁止把标 failed 写成自动续做。
- [ ] 运行 `GOMAXPROCS=1 OMP_NUM_THREADS=1 OPENBLAS_NUM_THREADS=1 go test -p 1 ./server/internal/kanban -run 'TestRouterRoute|TestRouterStartupRecovery|TestRouterClaimAppliedSeq' -count=1`，保存退出码和完整日志路径。
- [ ] 审阅本设计：指出不可执行、接口未定稿与权限冲突；分别标事实/推断/未验证。
- [ ] 报告 DONE / DONE_WITH_CONCERNS / BLOCKED，附最小后续任务建议；不提交、不派子代理。

**验收:** 报告存在，包含每个检查项结论与证据；工头读取实际测试日志并独立裁定。

## Task 2: 钉住 R0 关键回归（R0，依赖 T1）

**Files:** Create `server/internal/kanban/router_r0_audit_test.go`；必要 fixture 独立 test 文件。
**Consumes:** T1 确认的路径和已有 route fixture；工头提供定稿 brief。
**Produces:** 真实 Service/Store 集成测试，不仅是源码字符串断言。

- [ ] 本批先写三个期望行为测试：运行中 Agent 留言进入 Router；HumanGate deliver 不得遗留 pending；route 消费时刷新旧 brief。specified/disabled 与回退沿用已有测试并复跑。
- [ ] 使用现有 fake RouterAgent 和真实 SQLite/Service，验证请求入口到实际 effect。
- [ ] 对已发现缺口先运行测试，保存预期失败的 RED 证据，不改源码掩盖失败。
- [ ] 对已有正确行为保存 GREEN；提供每个新增测试钉住的行为表。
- [ ] 本任务若为故意 red 的契约提交，明确标记并由工头同 T3 一起集成，禁止当可部署版本。
- [ ] 提交 `test(router): 钉住任务路由入口与排队行为`，Scope 由工头在派发前分配。

**验收:** 测试能证明发现的问题；无永久 skip、弱化断言或实现镜像测试。

## Task 3: 修复审计证实的 P1 缺口（R0，依赖 T2）

**Files:** T1/T2 证明需要的 `activity_service.go`、`activity_service_turns.go`、`service_user_turn.go`、`router_service.go`、`router_brief.go`、`router_tools.go`、`activity_route_apply.go`；实际最小清单由 T2 结果确定。
**Consumes:** T2 的失败用例和已有执行守卫。
**Produces:** 不削弱 D3/HumanGate 的路由入口与消费前状态校验。

- [ ] 在执行守卫处保留 closed/paused 保护；让合法协调留言能进入 Router，但执行动作仍受 D3 守卫。
- [ ] 排队回合开始前重新读取当前运行态；触发用户留言 ID/正文保留原回合，禁止串话。
- [ ] 有并发时在 apply 再校验执行资格；旧 brief 不构成开第二个 Run 的许可。
- [ ] 跑 T2、原 route/shadow、user-turn guard tests；保存通过日志。
- [ ] 提交实现，附定制文件和测试说明，由工头统一登记。

**验收:** T2 红转绿；显式绕过、审批守卫、单活动执行原语义仍通过。

## Task 4: 隔离真实 agent 验收（R0，依赖 T3 或 T1 证明无需修复）

**Files:** Create `e2e/test_task_router_r0.py`，标准库 HTTP/JSON；只写独立运行目录。
**Produces:** 场景报告 JSON、每场景请求/决策/活动/Run、latency 样本和失败原因。

- [ ] 使用唯一隔离根、独立配置、独立项目、空闲端口；先自证没有读取线上会话与写入线上库。
- [ ] 覆盖 spec 的全部 R0 场景；指定模型/配置来自当前实际可用设置，不复制凭据到报告。
- [ ] 外部模型不可用时明确 BLOCKED，保留已通过的确定性证据，不伪造通过。
- [ ] 核对隔离进程配置、源码 HEAD 与测试对象；停止本轮创建的隔离进程。
- [ ] 报告每场景通过/未通过，p50/p95 只基于实际样本。

**验收:** 场景逐例可追溯；线上保持在线；工头裁定 R0 是否通过并解锁 R1。

## Task 5: 持久事件账本（R1，依赖 R0 门禁）

**Files:** Create `router_event_types.go`、`router_event_store.go`、对应 tests；复用 TaskStore SQLite。
**Interfaces:** 工头在派发前定稿 `RouterEvent`、唯一键、Insert/Claim/Complete/ListPending 签名与恢复语义。

- [ ] 唯一键覆盖 task/run/kind/实例；记录 originating user、turn_id、chain_depth、attempt、timestamps。
- [ ] 先测重复插入、并发领取、持久化重开、失败终态、账号/root 隔离。
- [ ] 实现增量 migration，旧数据库可重复启动；不改既有历史数据。
- [ ] 明确 claim/effect/complete 分界，避免先推进完成游标再丢副作用。
- [ ] 记录回滚：关闭 coordinate 后事件表保留，不删历史库。

**验收:** 定向测试覆盖上述边界，接口已供 T6/T7 使用。

## Task 6: 生命周期事件生产（R1，依赖 T5）

**Files:** Create `router_event_sources.go` 与 tests；Modify 生命周期结算钩子。

- [ ] 先测 Router-owned 成功、失败、gate，显式/legacy Run 不产协调事件。
- [ ] 事件保存与结算保证可恢复：原子写或权威状态扫描补齐，禁止仅内存回调。
- [ ] stall 只在已配置 probe 连续两次 30s 扫描明确 inactive、NodeRun 仍 running 且启动至少 60s 时产事件；probe 未配置/未知不认定失败。按 5 分钟桶去重，消费前复查证据；用可控 probe/时钟覆盖正常长回合与真实中断。
- [ ] 覆盖旧 Run 到达、新回合已开始、用户手工取消和暂停，不覆盖新回合状态。

**验收:** 重复扫描无重复事件；结算窗口中断后能补齐；权限矩阵通过。

## Task 7: coordinate 消费与预算（R1，依赖 T6）

**Files:** Create `router_coordinate.go` 与 tests；Modify service/config 接线。

- [ ] coordinate 用户留言沿用 route；runtime 事件进入同任务串行 lane。
- [ ] 恢复 pending 事件；完整 turn_id 和事件映射可查；超过链深度 3 记录 skipped。
- [ ] runtime 动作仅 say/ask_user/silent/inspect_run；非法动作拒绝且不自动回退为新执行。
- [ ] 外部调用失败不留永久 processing；不自动重试外部 worker。
- [ ] 模式 off/shadow/route 的行为保持现状；启用 coordinate 必须显式配置。

**验收:** 模式矩阵、同任务串行、链上限、非法动作、失败与恢复测试通过。

## Task 8: 证据 brief 与里程碑汇报（R1，依赖 T7）

**Files:** Create `router_event_brief.go` 与 tests；Modify `router_prompt.md` 与受影响 API 投影。

- [ ] brief 提供原始要求、事件、Run/节点结果和未验证状态，有界裁剪并标明截断。
- [ ] 完成摘要、失败原因、gate 提醒、silent 理由分别有契约测试。
- [ ] 汇报活动使用确定 effect ID，不能重新绑定旧 Run 的唯一结果活动。
- [ ] 保持 worker 自然输出；不增加强制 JSON 文案给产品用户。

**验收:** 摘要可追溯至实际输出；旧事件不串话；重复消费不会重复可见汇报。

## Task 9: 并发与重启故障矩阵（R1，依赖 T8）

**Files:** Create `router_coordinate_recovery_test.go`，必要故障注入仅限测试接口。

- [ ] 覆盖事件持久化后退出、领取后退出、活动写入后完成前退出。
- [ ] 覆盖同一事件并发领取、旧 Run 延迟事件、暂停/关闭任务和链预算耗尽。
- [ ] 证明缺陷注入能令关键保护测试失败，恢复正常实现后再跑目标 race tests。
- [ ] 解释剩余不确定状态，不将 R1 通知恢复描述成 R3 通用副作用恢复。

**验收:** 每个故障点都有重启后断言；无丢事件、重复可见回复或盲目外部执行。

**T9 前置（工头修正 2026-11-18，Scope G-MK）**：冻结 brief 前用两个临时探针（跑完即删）
实测 T7 replay 的两个崩溃窗口，都是真缺陷，已修并各留一条回归用例：

- 运行期决策先 `ClaimAppliedSeq` 推游标、再写可见活动：崩在两者之间后 replay 跳过该
  动作并把事件标 done「replayed」，汇报永久丢失。运行期副作用 ID 是确定的，改为先写
  效果、后推游标（`AdvanceAppliedSeq`，单调、不要求 seq 精确衔接）；用户留言仍先抢
  游标，避免重复建 Run / 插活动。
- 决策行已插入、模型回合零结果就崩溃：零动作的 applying 被 replay 当成已应用，事件静默
  done。改为 `DeleteInterruptedDecision` 只删「零动作 + 游标未动 + 无会话键」的行并重跑
  模型；其它无法收尾的状态一律落事件 failed，不再永久留在 claimed。

回归用例：`TestCoordinatorReplayRepairsUnwrittenRuntimeEffect`、
`TestCoordinatorReplayRetriesInterruptedModelTurn`（`router_coordinate_turn_test.go`）。
`router_store.go` 的会话部分同时纯移动到 `router_session_store.go`，让账本文件回到
500 行硬线内。T9 的矩阵从「这两个窗口已被钉住」出发，不重复它们。

**T9 派发（2026-11-18）**：MindFS #83 / `task_072cb6c084d1c07c`，worktree `task-83`，
brief 在 `.superpowers/sdd/2026-11-18-super-agent-r0-r1/task-9-brief.md`。冻结要点：
只新增 `router_coordinate_recovery_test.go`（>500 行可再拆一个 `_restart_test.go`），
生产文件零改动，发现真缺陷报 BLOCKED；「重启」用同库 `newRouterService(...)` 新实例 +
立即 `Close()` 收回收权，断言只认最终状态；8 条行为（pending 存活 / claimed 回收 /
效果先于游标 / 并发领取单效果 / 旧 Run 迟到事件 / 暂停终态仍汇报 / 预算跨重启 /
无法重放即终态）；RED 证据是 5 个强制缺陷注入（M1 去 `RecoverClaimedEvents`、M2
`ClaimEvent` 恒真、M3 effect ID 退回随机、M4 删预算分支、M5 failed 也 retry），
每个都要有失败原文，还原后 GREEN 且生产文件零 diff。

**T9 结果（2026-11-18，PASS）**：交付 `4e4d2235`（491 行、8 条行为），集成 `d5d31a5e`，
登记 G-ML（`9a418944`）。worker 做了 6 个注入（含加严变体 M5b 与可选 M6），工头另做两次
独立注入复核；定向 3.451s、`-race -count=3` 24.777s、kanban 全包 135.456s、
`check-upstream` 1905/312 全绿。诚实记录：worker 首版 M2 用例没钉住 CAS（B 的 lane 在 A
结束后才跑），改成「A 阻塞期间判定 B 是否进模型回合」后才拿到真 RED；M4 首版删除分支
导致编译失败，明确不作为证据。残留：未覆盖 kill -9 / SQLite 单语句中断 / 真实双进程 /
时钟注入，不宣称 R3 通用副作用恢复。T10 解锁。

## Task 10: 真实闭环与工程集成（R1，依赖 T9）

**Files:** Create `e2e/test_task_router_coordinate.py`；文档与定制清单由工头更新。

- [ ] 隔离真实 agent 演示：一次需求→执行→主动汇报；中途留言→有归宿；失败/gate→明确说明。
- [ ] 重启仅限隔离进程；核对待处理事件和已有结果；线上 health 前后通过。
- [ ] 独立评审需求和质量；工头集成各提交到总任务分支。
- [ ] 跑 `GOMAXPROCS=1 go test -p 1 ./...`、web npm test、tsc、make build、make check-upstream。
- [ ] 产出已完成/未完成/未验证列表；只有经过工头验收才进入待审核。
- [ ] 合回 main 和 ship 遵守对应技能；本机重启留给用户，不自动出 APK。

**验收:** R0/R1 逐项证据齐全、门禁通过、独立评审无未处理重要缺陷。

**T10 派发（2026-11-18）**：MindFS #84 / `task_94ec32d147fcc1d6`，worktree `task-84`，
brief 在 `.superpowers/sdd/2026-11-18-super-agent-r0-r1/task-10-brief.md`。冻结要点：
只新增 `e2e/test_task_router_coordinate.py`（≤500 行、仅标准库，可选 `e2e/t10-iso-driver.sh`
≤200 行），生产代码与既有测试零改动，发现真缺陷报 BLOCKED；隔离根 `/tmp/mindfs-r1-t10`、
端口 7433、`MINDFS_ISO_KEEP=1`，隔离配置 coordinate + sonnet / effort low /
`turn_timeout=180s`（T4 实测真实回合尾部 155.9s > 90s，只放宽隔离配置）；`router_events`
无 HTTP 端点，只读 SQLite + `/router-decisions` 验证；四场景 A 需求→执行→主动汇报、
B 中途留言有归宿、C 失败/gate 明确说明（普通留言不得批准、approve 200、重复 409）、
D 仅重启隔离进程并核对 pending/claimed 与已有结果。

**T10 结果（2026-11-19，PASS）**：交付 `c6a4e629`（6 个 `e2e/test_task_router_coordinate*.py`，
200/285/301/192/471/75 行全部 ≤500，纯标准库，产品代码/既有测试/docs 零改动），
集成到 `task-73`（fast-forward），登记 G-MM。工头独立复跑门禁：Go 全仓 28 ok / 10 no-test / 0 FAIL、
`tsc` clean、web 232 文件 / **651 passed / 6 skipped**、`make build` `v0.5.5-1119-69-gc6a4e629`、
`make check-upstream`（登记后）1925 delta / 314 组。库侧只读断言：`router_events` 21 行全 `done`
（13 settled / 5 failed / 3 gate_waiting），无 pending/claimed；21 条 `act-rtev-*` 的
`reply_to_id` 全部指回 `user_message`，正文对内部 ID 0 命中；每任务 effect 行数 == 不同决策数；
`continue_requests` 0 行；场景 D 重启前 `pending` → 重启后 `done`，`run_count` 1→1，新增活动只有
一条确定 effect。四场景 A/B/C/D 全部可追溯（B 的两条中途留言都被判 `say`，`deliver` 路径本轮
真实流量未触发）。**发现的展示冗余不阻断 R1**：结算回填的 reply 行与 `run_settled` 汇报行都含
结果句——设计规格要求汇报「说明结果与未验证项」，三份汇报都额外带了截断提示 / 失败节点与错误 /
「结论没有实际验证支撑」的未验证项；静默汇报会删掉这些内容，去掉结算回填则事件回合失败时用户
完全看不到结果（结算行是保证路径、汇报行是尽力路径）。登记为 UX 残留：后续 UI 任务可折叠同一
留言下的 `purpose=progress` 行；T8 冻结契约禁止把汇报绑定/改写到旧 Run 的结果活动。
**残留（非阻断）**：`deliver` 分支无真实流量证据；`ask_user` / `silent` / 3 回合预算未在真实场景触发；
未覆盖 kill -9、SQLite 语句中断、真实双进程；汇报正文含「事件数据被截断」这类证据口吻。
**未做**：push / deploy / 合并 main / 重启；切 coordinate 与生产启停需用户单独授权。

**R1 收尾（2026-11-19，用户要求修已知问题 + 加直达入口）**：两处 UX 残留落地，数据路径零改动。

- **运行汇报折叠（`Scope: G-MO`）**：T10 裁定的「结果句说两遍」只在前端展示层处理。结算 reply
  留在原位；同一 `reply_to_id` 下、`purpose=progress` 的汇报行折进紧跟父行的「运行汇报 N 条」，
  展开复用 `ActivityMessage`。折叠前提是父行已结算（completed/failed/waiting_user 且 summary
  非空）——运行中的 progress 行是用户唯一进度信号；无 `reply_to_id` 的汇报没有归属，同样保持
  可见；归属按留言精确映射，不是「最近一条结算行」。纯派生函数 `conversationRows` 单测 +
  SSR 默认折叠断言（9 例），两个关键守卫做过变异验证（只认 completed、按最近结算行归属，均
  会打红）。
- **直达工作流会话（`Scope: G-MO`）**：任务详情页运行视图入口旁新增按钮，落点由纯函数
  `pickWorkflowSessionKey` 决定（running > waiting_user > 最近一条有 `session_key` 的节点；
  空白键跳过；末尾无会话节点不能顶掉前面的真会话）。节点行在引擎绑定到 workflow 时即预取、
  按 `refreshKey` 重取（`session_key` 是回合中途绑定到 NodeRun 的），不需要展开运行视图。
  重取期间已知键仍可点（绑定后不再变化），只有首次加载且无键时显示「加载中」，不把未完成
  误报成「暂无工作流会话」。10 例测试；优先级与空白键过滤做过变异验证。
- **事件 brief 的假截断（`Scope: G-MJ` 补登）**：生产端自己写的事件载荷键（`nodes` /
  `nodes_truncated` / `workflow_id` / `revision`、停摆的 `started_at`、启动对账的
  `open_node_runs` / `resumed` / `unknown`）此前全被当成「未知键」，导致每条真实事件在汇报里
  多一句「载荷被截断、信息不全」。改为受控键集合（仍不进 brief 载荷，节点证据走
  `brief.evidence`）；真未知键 / 超长值 / 坏 JSON 照旧标 truncated。停摆与对账两类载荷各留
  回归用例，删 `resumed` 与删 `started_at` 的变异均打红。

**门禁（2026-11-19）**：`go test -count=1 ./...` 全绿、web 235 文件 / 677 通过 / 6 跳过、
`tsc --noEmit` clean、`make build`、`make check-upstream`（见提交后记录）。未 push / 未部署 /
未重启；生产 `router.mode` 未动。

**R1 留存缺口收口（2026-11-19，`Scope: G-MP`）**：R1 完成时列的四处「跑起来才会撞」的缺口
按 1 → 4 → 2 → 3 逐个 RED→GREEN，每处都做变异验证（故意破坏守卫 → 对应用例打红 → 还原复绿）。

1. **删卡级联 + 关 lane**：`router_events` 的 `unique_key` 含 task_id，删卡却留行 → 任务用同 ID
   重建时 `InsertEvent`（ON CONFLICT DO NOTHING）静默吞掉每一条新事件，账本看起来「这次一条都
   没有」。级联单收到 `TaskStore.DeleteTask`（删卡的唯一事务），删掉只给测试调用的
   `DeleteTaskRouterData`（口径对但生产零调用者，钉住它等于没钉）；`Service.DeleteTask` 在删库
   前先 `CloseRouterTask` 关进程内 lane（清 session、丢排队回合），不让排队回合拿已不存在的任务
   调模型。变异：移出 `router_events` 列表 → 两条账本用例红；删 `CloseRouterTask` → lane 用例红。
2. **运行期 `inspect_run`**：事件 brief 有意清空 `LatestRun`（不许模型拿任务最新 Run 顶替），
   而 `inspectRun` 只认 `LatestRun` —— 事件回合里唯一的读工具 100% 报 "no workflow run"，模型
   只能凭摘要猜哪个节点失败。改为事件回合以 `brief.Event.RunID` 为目标并按 ID 读库，用户回合
   仍走 `LatestRun`；传别的 run id 一律拒绝（不退回去用最新 Run）。变异：事件分支回落
   `LatestRun` → 外来 run 用例红。
3. **领取租约**：claimed 行原来只有进程重启（`RecoverClaimedEvents`）能救。新增
   `ReclaimStaleEvents(before)` 按 `claimed_at`（不是 `created_at` —— 排队久但刚领走的事件是活
   的）把超租约行打回 pending 并保留原领取者，接进 `coordinateTick` 每拍执行；CAS 保证误判时第
   二个回合拿不到同一事件，运行期 effect ID 确定所以不重复副作用。变异：循环里去调用 → tick
   用例红；删 canonical 时间条件 → 「新鲜领取不许被抢」用例红。
4. **账本保留策略**：`PruneEvents(cutoff, limit)` 只删 done|failed|skipped 且按 id 子查询 + LIMIT
   分批（不依赖 `SQLITE_ENABLE_UPDATE_DELETE_LIMIT`）；pending/claimed 再老也留着（删它们等于
   静默丢事件）。`coordinateTick` 里只第 0 拍与每 120 拍跑一次（一拍 30s ≈ 1 小时），回收不是
   热路径。`router_decisions` 明确不回收（诊断面）。变异：去终态过滤 → 未办完行被误删；去 LIMIT
   → 分批用例红；循环里去调用 / 去掉拍数节流 → 两个 tick 用例红。

**门禁（2026-11-19，G-MP 后）**：`go test -count=1 ./...` 28 包全绿、定向 `-race` 9.708s 全绿、
`make check-upstream` 1957 delta / 316 组全绿（G-MP 18 锚点逐个核对存在，G-MG ⑥ 与
`DeleteTaskRouterData` 锚点已同步改写）。已于 2026-11-19 合并 main（`7f9e710c`，与 main 的两条 notify 提交零冲突合并）并
`bash scripts/deploy-all.sh` 部署四端，版本 `v0.5.5-1120-4-g7f9e710c-dirty`（`-dirty` 来自本机
另一会话未提交的 live-compact 文档）。本机重启后 `router.mode=route` 生效（见文首状态），
观察结论见下节「R2 准入评估」。

## R2 准入评估（2026-11-19）

**spec 的进入条件是「R1 事件闭环通过真实验收」，这一点已满足**：T10 在隔离实例用真实
claude 跑通需求→执行→主动汇报、中途留言归宿、失败/gate 说明、进程重启对账；库侧 21 条
`router_events` 全 `done`、每任务 effect 行数 == 决策数、重启后 `pending→done` 且不重放 Run。

**但还有三类证据缺口，直接开 R2 会把「验收条目/证据关联」建在沙地上**：

1. `deliver`（运行中留言转交）只有单测与隔离用例，没有真实流量证据；`ask_user` / `silent` /
   预算耗尽分支从未被真实触发。
2. 生产 `route` 2026-11-19 11:14 才首次生效，到 11:20 决策与事件都是 0 条——线上决策成功率、
   fallback 比例、p95 延迟尚无一条真实样本（隔离样本 p50 11.8s / p95 22.5s）。
3. 未覆盖 kill -9、SQLite 语句中断、真实双进程（G-ML boundary 已写明，不宣称通用副作用恢复）。

**结论：技术上可以推进 R2，但先过一个观察窗，且 R2 要另立 goal/brief 与授权（本计划明确
R2–R5 不执行）。** 观察窗建议至少 24h 或 ≥10 条真实留言，判据：

- 决策成功率与 fallback 原因可解释，没有连续 LLM/校验失败；
- `router_events` 的产生数与效果数对齐，无重复可见汇报（`reply_to_id` 全部指回原留言）；
- 每个 Router turn 的延迟分布有样本（按 spec，不沿用旧 3s 准入，也不把 90s 超时当达标）；
- 出现 `deliver`/`gate_waiting`/`run_stalled` 任一真实场景时单独记证据。

未过判据就先修观测到的问题，不进入 R2；过了再由用户批准 R2 的第一批（验收条目存储 +
证据关联）。

## 执行门禁与依赖

T1 → T2 → T3 → T4 → R0 PASS → T5 → T6 → T7 → T8 → T9 → T10。
T1 无代码修改；T2/T3 的确认缺口由工头补入 brief 后执行；T5–T10 完整接口冻结后才派发。
所有任务开始/停止、MindFS task id、worktree、commit、测试和评审路径见派发表。
只创建任务卡不等于派发；HTTP 接受请求不等于 worker 已运行；worker 报 DONE 不等于验收通过。
