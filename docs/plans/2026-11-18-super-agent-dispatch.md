# Super agent 派发表与进度

总任务：MindFS #73 / `task_7db338ea2d38e2c3`。
工头 worktree：`/home/xiaokubao/projects/mindfs/.worktree/task-73`，分支 `task-73`。
基线：`3b6c171030ffee8b4c2d6cc416235677d714970a`。
授权：当前用户已明确要求以 worktree + 任务派发执行；[goal](2026-11-18-super-agent-goal.md) 记录范围。
共享记录仅工头修改；worker 禁止子派发。实现并发 1，只读并发 1，所有本轮命令限制 1 线程。

## 派发表（开始前必须完整）

| 任务 | 工作流/执行方式 | 独立性/依赖 | 输出路径 | 验收命令/标准 | 停止条件 | 并发占用 | 状态 |
|---|---|---|---|---|---|---|---|
| T1 基线核查 | MindFS specified 空白执行，单 worker | 独立只读；不与工头重复审计 | `.superpowers/sdd/2026-11-18-super-agent-r0-r1/task-1-report.md` | 计划 T1 的 route 定向测试；逐项证据 | 权限/模型失败、超范围、同一路径连续两次失败即报告 | 只读 1 | 审计完成；缺口转 T2 |
| T2 R0 回归 | specified 空白执行，Scope G-MC | T1 → T2，独立 worktree | worker branch + task-2-report.md | R0 集成用例及 RED/GREEN | fixture 无法稳定构造则 NEEDS_CONTEXT；不修源码 | 实现 1 | 已完成：三例 RED，审阅通过 |
| T3 P1 修复 | 依赖 T2 后定稿并派发 | T2 → T3，工头集成测试提交 | worker branch + task-3-report.md | T2 红转绿 + 原守卫测试 | 根因未确认不改源码 | 实现 1 | 已完成：T2 红转绿、两项竞态 + 直连摘要守卫；集成 `8cace21e`、`47785ee6` |
| T4 真实验收 | 依赖 T3；独占隔离实例 | 不与其他计时/真实求解并发 | 独立运行根 + task-4-report.md | R0 场景逐例完整证据 | 模型不可用则 BLOCKED，不改生产配置 | 实现/真实求解 1 | 已完成：R0 PASS（最终代码 7/7） |
| T5 事件账本 | R0 PASS 后冻结接口 | T4 → T5 | worker branch + task-5-report.md | 去重/领取/重启/隔离测试 | R0 未通过或接口未冻结不启动 | 实现 1 | 已完成：13 例全绿，集成 `68a07c5e`；工头补迁移/级联 `3a1aa766` |
| T6 事件生产 | T5 后冻结 brief | T5 → T6 | worker branch + task-6-report.md | 来源/重扫/结算窗口测试 | 接管 legacy/显式任务即拒绝 | 实现 1 | 已完成：10 条行为 PASS，集成 `df074149`；硬线拆分 `c35ae226` |
| T7 消费预算 | T6 后冻结 brief | T6 → T7 | worker branch + task-7-report.md | 模式/链深度/动作权限矩阵 | 自动开执行/cancel/retry 即拒绝 | 实现 1 | **PASS（2026-11-18）**：#81 交付并集成，12/12 行为、`Scope: G-MI` |
| T8 汇报证据 | T7 后冻结 brief | T7 → T8 | worker branch + task-8-report.md | 来源/摘要/确定 effect 测试 | 串话/重复可见回复即拒绝 | 实现 1 | **PASS（2026-11-18）**：#82 交付并集成（`93f96f14` + 工头修正 `f7dcc5bb`），8 条行为 + 工头补 3 条，`Scope: G-MJ` |
| T9 故障矩阵 | T8 后派发 | T8 → T9 | 测试提交 + task-9-report.md | 重启与并发故障点断言 | 生产重启、盲目重放即停止 | 实现 1 | **PASS（2026-11-18）**：#83 交付（`4e4d2235`）并集成 `d5d31a5e`，8 条行为、6 个缺陷注入，`Scope: G-ML` |
| T10 集成验收 | T9 后独占运行，`Scope: G-MM` | 全链依赖 | `e2e/test_task_router_coordinate.py` + task-10-report.md + 全量日志 | 计划 T10 全量门禁与真实闭环 | 任一门禁失败不集成发布 | 实现/真实求解 1 | **已派发（2026-11-18）**：#84 / `task-84`，brief 已冻结 |

T2–T10 启动前更新其实际 task id、worktree、绝对报告路径与定稿 brief；未补全不得派发。

## 任务实例

- #73：总任务，只作工头工作区与计划归档；未派 worker，避免本会话重复执行。

## 验收记录

- 2026-11-18：通过本机 API 读取项目、工作流、health；创建 #73，调用 rebuild-worktree 成功；尚无代码实施或真实 route 验收通过声明。

## 解锁状态

- R1 T5：**已完成并集成**（#79，账本 + 工头补的迁移/级联，见「T5 结果」）。
- R1 T6：**已完成并集成**（#80，事件生产 + 硬线拆分，见「T6 结果」）。
- R1 T7：**已完成并集成**（#81，coordinate 消费 + 预算 + 有界扫描，见「T7 结果」）。
- R1 T8：**PASS 并集成**（#82，交付 `93f96f14` + 工头修正 `f7dcc5bb`，登记 `bf1071d5`，见「T8 结果」）。
- R1 T9：**PASS 并集成**（#83，交付 `4e4d2235` + 集成 `d5d31a5e` + 登记 `9a418944`，见「T9 结果」）。T10 已解锁并派发（#84，见「T10 实例」）。
- R2–R5：仅路线图，不属于本轮执行。

### T1 实例

- MindFS #74 / `task_58f8b5239a488688`。
- Worktree：`/home/xiaokubao/projects/mindfs/.worktree/task-74`。
- 工作流：指定 `workflow-blank:v1`；claude / sonnet，沿用现有已发布定义。
- Brief：`/home/xiaokubao/projects/mindfs/.worktree/task-73/.superpowers/sdd/2026-11-18-super-agent-r0-r1/task-1-brief.md`。
- 独立报告：`/home/xiaokubao/projects/mindfs/.worktree/task-74/.mindfs/dispatch/task-1-report.md`；日志同目录。
- 只读 worker 1；实现 worker 0；只写该报告目录，禁止改源码、共享文档和其他 worktree。

- T1 已由 API 读取到实际执行节点 `run-a1c13b9bc4b2fc40:execute::0:1`，claude / sonnet，status=running；会话 `1794955134-88d1fd9376b4`。请求接受与实际启动分别核对。

## 本轮实际使用的内部接口

本机 base 为 `http://127.0.0.1:7331/mindfs`；所有任务请求明确携带 `user=u_24f56c12a0235e05` 与 `root_id=mindfs`（任务列表用 `root=mindfs`）。

| 操作 | HTTP 接口 | 使用方式 |
|---|---|---|
| 读取项目/工作流 | GET /api/dirs、/api/workflows/published | 确认 root 与已发布版本，不修改配置 |
| 建任务 | POST /api/tasks | input 为空、start_immediately=false，避免建卡即意外运行 |
| 创建 worktree | POST /api/tasks/{id}/rebuild-worktree | create_worktree=true 的新任务显式建树，核对返回路径 |
| 派发单任务 | POST /api/tasks/{id}/activities | specified + workflow-blank:v1，完整 brief 路径在正文 |
| 核对实际启动 | GET /api/tasks/{id}/workflow-run/nodes | 读取 node status 与 session_key，不能仅凭 POST 200 |
| 验收结果 | GET /api/tasks/{id}/activities + 独立报告/测试日志 | 工头核对后才完成任务，不自动批准 gate |
| 路由审计 | GET /api/tasks/{id}/router-decisions | 后续 R0 自动选择场景核对 Router 决策 |

本轮工程派发是显式 workflow，故绕过 Router；这证明工头能调用内部执行接口，不证明自动路由已通过 R0 验收。
实际进程核对：`/proc/1276775/cwd` 为 `task-74`，comm 为 claude，确认 worker 没有在主 checkout 执行。
工程文档提交 `b0f580f0`；`make check-upstream` 退出 0，1867 delta / 305 组通过。

## T1 报告与裁定

- 报告：`/home/xiaokubao/projects/mindfs/.worktree/task-74/.mindfs/dispatch/task-1-report.md`，DONE_WITH_CONCERNS。
- 工头已独立读取原始测试日志：10 个目标测试通过，首轮 3.518s；verbose 复跑也通过，但包含 fixture 清理期间 readonly DB / 非 git 目录日志，不能称为日志全净。
- 工头已独立查询在线 Router：mode=shadow，turn_timeout_ms=20000；生产未切到 route 行为。本轮不改配置、不重启。
- F1/F2/F3 进入 T2 复现；F2 的永久 pending、F3 的并发后果仍需行为测试，不将静态推导写成已复现实验。
- F4 中断恢复准确边界已写进设计；F5 Run/task 状态口径、F7 转交结果可见性、F8 直连并发列为 R0 后续风险，不因本轮三项测试遗漏而宣称 R0 全部通过。
- 采纳运行中留言转交、agent-only 投递目标、route 新鲜状态；保持 apply 失败不回退，要求占位终态。完整裁定在设计文档末尾。
- 拒绝无真实证据更换用户指定模型；拒绝以 bug 现状作为应该保持的绿色契约。

### T2 实例

- MindFS #75 / `task_27ed9e5e9cfce8a6`，worktree `/home/xiaokubao/projects/mindfs/.worktree/task-75`。
- Brief：`/home/xiaokubao/projects/mindfs/.worktree/task-73/.superpowers/sdd/2026-11-18-super-agent-r0-r1/task-2-brief.md`；报告：`/home/xiaokubao/projects/mindfs/.worktree/task-75/.mindfs/dispatch/task-2-report.md`。
- 已冻结三项期望行为测试，只新增测试文件；允许 RED 契约分支，不集成部署。
- 验收：三个正确原因的 RED + 原有目标组 GREEN；不改产品源码。
- 工作流 `workflow-blank:v1`，claude / sonnet；实现并发 1。

## 本轮最终核对（截至 2026-11-18）

- T1 / #74：原生 NodeRun 已 success，finished_at=2026-11-17T22:56:39Z；工头已读取报告、日志并裁定关切项。审计完成，不等于 R0 通过。
- T2 / #75：原生 NodeRun `run-0058b108bf1254df:execute::0:1` 为 running，会话 `1794956358-251b0ddfdf4a`；实际 claude PID 1378875 的 cwd 为 task-75。
- 当前未修产品源码，未跑真实 route 场景，未完成 R0/R1，未合回 main/推送/部署。
- 下一门禁：读取 T2 报告与 RED 证据 → 独立审阅测试 → T3 最小修复 → R0 真实隔离验收。

## 继续执行：T2 独立审阅

- #75 NodeRun 已 success；提交 `3a94538218a210a302b377e3cbd4d36b6a9e7284`，只新增 280 行测试文件。
- 工头已读取完整报告：三项期望行为均 RED，原有 10 项锚定测试 GREEN；正则前缀碰撞已记录，不误判原组回归。
- 审阅线 R2：只读 Codex 子代理；实现并发 0、只读并发 1；不得派子代理、修改代码或重复跑现有测试。
- 输入：T2 frozen brief、实现报告、base `3b6c1710` 到 head `3a945382` 的独立 diff 包。
- 输出：`/home/xiaokubao/projects/mindfs/.worktree/task-75/.mindfs/dispatch/task-2-review.md`。
- 验收：分别给 spec compliance 和 quality verdict；具体缺陷带行号。重要缺陷未裁定不派 T3。
- 停止条件：缺少证据、需要扩大任务范围或发现重要缺陷则报告，不自己修复。

### T2 修订轮 1/5

- 独立审阅要求三项重要修订：F1 验证 continue_requests；F2 验证可见解释且等 lane 完成；不可只凭决策终态。
- 工作流：通过 HTTP `/api/sessions/{key}/messages` 续发原 worker 会话 `1794956358-251b0ddfdf4a`，不新建执行线。
- 工作区 /home/xiaokubao/projects/mindfs/.worktree/task-75；输出沿用 task-2-report.md 并追加本轮测试证据；Scope G-MC。
- 验收：三条仍因产品缺口 RED、原组 GREEN，独立 scoped re-review；实现并发 1，只读审阅已结束。
- 停止：不可恢复原会话则改用同卡任务 follow-up；不修改产品源码，不改其他 worktree。

### T2 修订轮 2/5

- 工作流：续发原 MindFS worker 会话；工作区 task-75，唯一修改测试文件。
- 剩余问题：F2 必须验证用户可见文字表达无法投递或等待人工审批；不能把任意非空正文当解释。
- 输出：同一 task-2-report.md 追加测试与提交证据；独立复审 task-2-rereview-2.md。
- 验收：F2 原因准确 RED，原有其他断言保留；只读审阅与实现各占 1 个并发以内。
- 停止：无法构造语义断言或发现越界则报告，不改产品源码。

### T3 准备（尚未派发）

- MindFS #76，worktree `/home/xiaokubao/projects/mindfs/.worktree/task-76`；等待 T2 修订与独立复审通过。
- 主分支已推进到 `055d138d`，自原基线以来没有 kanban 代码差异；T3 由新 worktree 基线接入审阅通过的测试提交。

### T2 修订轮 3/5：更换执行方式

- 前两轮复审连续发现 F2 提示语义未收紧；按用户级工程规则更换方法和执行者，改为产品现有审批错误及明确投递失败字符串的精确断言。
- 工作流：Codex 子代理，使用 #75 原有 worktree，仅修改测试文件；原 MindFS 会话已结束。
- 独立性：只占实现 1；评审完成后占只读 1，不与其他实现并行。
- 输入：原 brief、两轮评审、当前 HEAD 49e8edb1；报告 `.mindfs/dispatch/task-2-fix3-report.md`。
- 验收：F2 对产品审批错误/确切无活动 Agent 错误为真，对“已转交”“没有可用的工作流”为假；原 F2 在修复前仍因 pending RED。
- 停止：需要修改产品代码、改变用户契约、原 worktree 非干净或基线不符立即报告。

### T2 修订轮 4/5

- 第三轮复审指出等待审批的合理短语仍会误拒；使用更强的独立实现者完成最小修正。
- 工作区 task-75；仅改 router_r0_audit_test.go；报告 task-2-fix4-report.md。
- 验收：明确“等待你审批”及带成功提示引述的拒绝案例为真，“没有可用的工作流”与“已转交”为假；原 F2 仍 RED。
- 并发：实现 1，审阅最多 1；无法满足则停止并报告，不改产品代码。

### T3 实际派发

- MindFS #76 worktree `task-76`；使用本会话 Codex 实现代理派发，任务卡与分支保留在 MindFS 中，执行状态以此记录为准。
- 已 cherry-pick 经审阅测试至 `9b289f4e`；它是故意 RED 的实现基线，禁止部署。
- Brief：`task-73/.superpowers/sdd/2026-11-18-super-agent-r0-r1/task-3-brief.md`。
- 输出：`task-76/.mindfs/dispatch/task-3-report.md` 和独立提交。
- 验收：三项测试 GREEN、原 route/shadow/guard 测试通过、所修改源文件 <=500 行；独立评审后集成。
- 停止：根因不成立、需要越过指定范围或同一路径连错两次时上报。实现并发 1。

### T3 独立审阅

- 实现提交 eab1ea09；定向 17.540s、kanban 包 106.164s 通过；G-MC 路径迁移待工头登记。
- 审阅方式：只读子代理，输入 T3 brief、报告和 9b289f4e..eab1ea09 diff；独立性：实现任务已完成，审阅与实现不并行。
- 输出：task-76/.mindfs/dispatch/task-3-review.md。验收：需求与代码质量两个 verdict、证据行号；重要问题清零后集成。
- 停止：发现守卫倒退、未覆盖副作用或上下文不足时报告；审阅不改源码、不重复跑现有整套测试。只读并发 1。

### T3 修订轮 1/5

- 独立评审确认两项重要竞态：模型运行期间暂停/终态后仍应用动作，及进入 HumanGate 后仍按旧状态启动直连。
- 续用 #76 原实现子代理，独占实现槽位；输入 task-3-review.md，输出追加 task-3-report.md。
- 验收：三个确定性竞态测试先 RED 后 GREEN，定向原回归仍通过；投递状态与绑定事务的先后有明确保障。
- 停止：需要改变明确纯对话行为、无法保障事务先后或两次方法失败即报告；不自动回退产生重复副作用。

### T3 修订轮 1/5 结果

- 实现提交 `7a8f0906`（Scope G-MC）：动作应用前逐次重读任务状态；暂停/终态拒绝并收尾空占位；`DeliverActivityToActiveNodeRun` 在同一绑定事务内读任务状态；Router 发起的直连在 gate 出现时拒绝，用户显式纯对话保留。
- 测试先 RED 后 GREEN：`TestRouterApplyRechecksStateAfterModel` 三个子例（paused→deliver、success→say、gate→direct）与 `TestRouterCompletionBetweenAppliedRepliesKeepsFirstEffect`；失败原因分别为“暂停任务接受绑定并写 continue_requests”“终态摘要被迟到回复覆盖”“gate 已 waiting_user 仍启动直连”。
- 工头独立复跑定向集（15.901s）与完整 kanban 包（113.290s）均通过；修改源文件均 <500 行。
- 实现报告未按要求追加修订段落，证据以上述提交与原始日志为准，本记录补上。

### T3 独立复审（修订轮 1 后）

- 复审提交 `7a8f0906`，输出 CHANGES REQUIRED：1 HIGH / 2 MEDIUM / 2 LOW。
- HIGH：Router 发起的直连回合收尾仍用无状态条件的 `UpdateTaskResultSummary`；回合中完成任务后，迟到的直连回复会覆盖已验收摘要（overlay 实测 `summary="迟到的直连回复"`）。
- MEDIUM 1：say/ask_user/silent 仍是检查后写入；暂停恰好落在检查与写入之间时，可见回复可能落在已暂停任务上，决策仍记 applied。
- MEDIUM 2：G-MC 锚点仍指向已迁移的 `activity_service.go`，新守卫文件与新测试未登记；`check-upstream` 在 HEAD 为红。
- LOW：摘要投影从吞错改为返回错误后，已生效决策可能被标 failed；终态状态列表在 Go 与 SQL 两处重复。

### T3 修订轮 2/5

- 因原实现子代理已结束，本轮由工头在主循环实现，保持“实现后独立复审”的分离；实现槽位仍为 1。
- HIGH 先 RED：新增 `TestRouterDirectResultDoesNotOverwriteTerminalSummary`，实测 `task status="success" summary="迟到的直连回复"` 失败，原因正确。
- 修复：直连回合的摘要投影改走与 Router say 共用的 `recordReplySummaryIfActive`（带 `status NOT IN (paused,success,fail,cancelled)` 条件）；提交 `2b50ec9b`。新增用例 GREEN，三个竞态用例与完整 kanban 包（115.268s）通过。
- MEDIUM 1 裁定：不扩大活动写接口。暂停本就允许在途节点收尾，可见回复落在暂停任务是同一语义；必须与暂停严格排序的是**投递绑定**，已由事务内判定覆盖。该裁定写入 G-MC `why`。
- MEDIUM 2 已处理：G-MC 锚点迁移到 `activity_service_dispatch.go`，登记 `activity_service_dispatch.go`、`router_brief.go`、`router_service_sessions.go`、`task_store_router_guards.go`、四个 R0 测试文件与新增守卫锚点；`make check-upstream` 绿（1884 delta / 306 组）。
- LOW 保留：摘要投影错误显式上报，不静默吞掉；终态状态列表在 `task_store_router_guards.go` 加同步注释。

### T3 集成

- 合并提交：`8cace21e`（T2/T3 + 登记 + README 双条目冲突解决）、`47785ee6`（直连摘要守卫 + 锚点）。
- 门禁：定向 15.901s、kanban 包 113.290s / 115.268s、`make check-upstream` 1884/306、gofmt 干净（顺带修 `router_route_test.go` 末尾换行）。
- 下一步：T4 首轮已完成并裁定；按 §T4 结果 修完 D-1..D-3 后，由同一 worker 合入 `task-73` 重跑 S5/S7，再判 R0 PASS/BLOCKED。

### T4 实例

- MindFS #77 / `task_39c5ae0402585f89`；worktree `task-77`（创建时基线 `7f1b60ee`，worker 必须先 `git merge task-73`，HEAD 需含 `aac02bfd`）。
- 工作流 `workflow-blank:v1`；claude / sonnet；NodeRun `run-556dd00ebb9888ca:execute::0:1`，会话 `1794984216-7210049c9d5b`，`started_at 2026-11-18T06:43:36Z`。
- Brief：`task-73/.superpowers/sdd/2026-11-18-super-agent-r0-r1/task-4-brief.md`；报告：`task-77/.mindfs/dispatch/task-4-report.md` 与 `task-4-run.json`。
- 隔离根 `/tmp/mindfs-r0-t4`、端口 7432；只允许改隔离配置（router.mode=route、turn_timeout=90s）；结束时停实例并核对线上 7331 仍在线。
- 验收：spec R0 七类场景逐例证据；p50/p95 只用实际样本；模型不可用必须 BLOCKED；不改产品代码、不部署、不重启本机。

### T4 结果（2026-11-18，首轮 → 修复，待重跑）

- Worker 报告：`task-77/.mindfs/dispatch/task-4-report.md`（worktree 本地，`.mindfs/` 已 gitignore）；结果 JSON `/tmp/mindfs-r0-t4/result.json`。HEAD `f787808c`（已合入 `task-73`），唯一代码改动是 `e2e/test_task_router_r0.py`（`cb91412b`）。
- 隔离自证全通过：7432 ≠ 7331、隔离配置目录 `XDG_CONFIG_HOME=/tmp/mindfs-r0-t4/config`、`/proc/<pid>/fd` 命中线上配置目录 0 次、线上 health 200 且 `config.json`/`agents.json` 指纹前后一致；结束时实例已停、7432 无监听。
- **裁定：R0 未通过（6 pass / 1 fail / 0 blocked）。** 逐场景：S1 message_agent pass；S2 run_workflow pass（debug / feature 各一）；S3 ask_user pass；S4 specified/disabled 绕过 Router pass（决策数 0）；**S5 fail**；S6 1s 超时 fallback + 占位复用 pass；S7 同任务排队 pass（二轮）。
- 延迟（8 个真实决策样本）：13,070 / 15,505 / 20,127 / 22,690 / 24,390 / 35,611 / 39,652 / 47,869 ms → p50 23,540ms、p95 47,869ms；S6 的 1,003ms 为超时诱发值未计入。
- 缺陷与处置（工头独立复核代码后逐条实现，均先 RED）：

| id | 级别 | 现象 | 处置 |
|---|---|---|---|
| D-1 | HIGH | `deliver_to_active_run` 绑定成功但投递必 `agent required` | `SendWorkflowComment` 下传节点 agent profile；`7cbe9e5e` |
| D-2 | MEDIUM | 真实模型空回合被合成 `silent`，占位 hidden、留言消失 | 空文本视为决策缺失 → fallback 复用占位；`80039064` |
| D-3 | MEDIUM | 取消后复用占位的回复永远 `running` | 投影后重读 Run 再结算；`59d56538` |
| D-4 | INFO | 真实回合尾部 155.9s > 90s 超时，退化为 fallback | 不修；作为 route 准入证据（生产仍 shadow） |

- 门禁：`go test ./server/internal/kanban/`（110.570s）、`go test ./server/internal/api/`（24.126s）全绿；gofmt 对改动文件干净。
- **当时未完成**：S5 与 S7 需在隔离实例上重跑确认（D-1 是唯一 fail 的直接原因，D-2 对应 S7 首轮异常）。当时 R0 未 PASS、R1/T5 锁定；后续裁定见下。

### T4 裁定（2026-11-18）：R0 PASS

- 复核轮（HEAD `05aee6c3`）：S5 **PASS**（`continue_requests=consumed`、节点会话真实续聊回合、无 `agent required`）、S7 **PASS**（两条留言均产生真实动作与可见回复，`silent_turns=0`）。
- S6 复验：1s 超时诱发 `fallback`（1,003ms）→ 取消后复用回复活动 **1.5ms 结算为 `cancelled`**，无 `running`/`pending` 残留（对照首轮同一路径 20s 后仍 `running`）。
- 全量轮（产物 HEAD `8765b748`，产品代码与 `05aee6c3` 逐字节一致，差异仅验收脚本新增 `cancel_settle` 判据）：**七场景 7 pass / 0 fail / 0 blocked**，8 条真实决策延迟 p50 11,804ms / p95 22,527ms；隔离自证与收尾复验通过（7432 无监听、线上 health 200、`config.json`/`agents.json` 指纹全程一致）。
- 结论：**R0 通过，解锁 R1**。残留（非阻断）：Router 占位在 deliver 后仍是 `hidden=true`+`pending`（用户不可见）；样本仅单机单账户、llmux 别名模型，不能外推生产分布；D-4（真实回合尾部 155.9s > 90s 超时）不修，生产仍 shadow。
- 证据：`task-77/.mindfs/dispatch/task-4-report.md`（首轮 / 复核轮 / 全量轮三节）与 `task-4-run-final.json`；验收脚本 `e2e/test_task_router_r0.py` 已合入 `task-73`（`8608e60b`）。

### T5 实例

- MindFS #79 / `task_4a39f4fa385bbd34`；worktree `/home/xiaokubao/projects/mindfs/.worktree/task-79`，分支 `task-79`（创建基线 `7f1b60ee`）。
- Worker 必须先 `git merge --no-ff task-73`（无冲突；有冲突停止报告）。
- 冻结接口与八条必钉行为：`task-73/.superpowers/sdd/2026-11-18-super-agent-r0-r1/task-5-brief.md`。
- 允许写：`router_event_types.go`、`router_event_store.go` 与其测试；不改 Router 决策/应用逻辑、workflow runtime、docs、线上配置。
- 报告：`task-79/.mindfs/dispatch/task-5-report.md`；提交信息 `Scope: G-MG`（登记由工头补）。
- 派发（2026-11-18）：用户留言 `act_c62fe7a51d2abb5f`，`workflow_choice=specified` + `workflow-blank:v1`；实际启动节点 `run-6ab2f1d3c3ece7c8:execute::0:1`，claude / sonnet，会话 `1794990651-0f954e4a2491`，status=running（POST 200 与实际启动分别核对）。

### T5 结果（2026-11-18）：PASS

- Worker HEAD `f22b3cb0`，四个新文件 1,036 行；定向 13 例 `ok ... 5.937s`，全包 `ok ... 125.359s`，`go vet` 干净、`gofmt -l` 干净。
- RED 分两段：无实现编译失败原文 + 占位实现 13 例逐条失败原文；GREEN 13 例全 PASS。变异验证两处：唯一键去掉 `kind` → settled/failed 去重用例红；`CompleteEvent` 去掉 `status='claimed'` 闸门 → pending 直接 complete 未报错而红。
- 八条行为逐条 PASS（重复插入、并发领取单赢家、重开持久化、failed 不复活、complete 前必须 claimed、task/root 隔离、唯一键含 kind+实例、旧库迁移可重复）。
- 工头复核发现两处必须修的设计问题（T5 报告自己列为风险 1、2）：建表挂在首次 `InsertEvent` 的惰性路径，违反仓库「迁移只接 `migrateSchema`」约定，启动对账先读表会撞 `no such table`；`DeleteTaskRouterData` 不删事件行，任务重建同 ID 会命中旧 `unique_key`。已按 RED→GREEN 修（`3a1aa766`），T5 的旧库迁移用例改用裸 `sql.DB` 建模。
- 集成：`68a07c5e`（merge）；`make check-upstream` **1890 delta / 307 组**绿；`go test -p 1 ./server/internal/kanban/` 138.774s 全绿。
- 登记：G-MG（`891235c1`），11 个 anchors 逐个核对命中。
- 残留（非阻断）：`ListEventsByTask` 不带 rootID，隔离依赖「每 root 一份库」；`RecoverClaimedEvents` 无租约，只能在启动对账调用；`ensureRouterEventTable` 的按 `*sql.DB` 指针缓存已冗余，留待清理。

### T6 实例

- MindFS #80 / `task_d0affbea94e9ceee`；worktree `/home/xiaokubao/projects/mindfs/.worktree/task-80`，分支 `task-80`（创建基线 `7f1b60ee`）。
- Worker 必须先 `git merge --no-ff task-73`（须含 `68a07c5e` / `3a1aa766` / `891235c1`；有冲突停止报告）。
- 冻结接口与 10 条必钉行为：`task-73/.superpowers/sdd/2026-11-18-super-agent-r0-r1/task-6-brief.md`。
- 允许写：新增 `router_event_sources.go` 与其测试，改 `service_activities.go` / `workflow_runtime.go` / `workflow_driver.go` / `workflow_recovery.go` / `service_recover_startup.go` 的钩子点；不改 Router 决策与应用逻辑、T5 的 `router_event_*`、前端、docs、线上配置。
- 报告：`task-80/.mindfs/dispatch/task-6-report.md`；提交信息 `Scope: G-MH`（登记由工头补）。
- 派发（2026-11-18）：用户留言 `act_0192f4dce04935d0`，`workflow_choice=specified` + `workflow-blank:v1`；实际启动节点 `run-6cc5d8c70156c1b3:execute::0:1`，claude / sonnet，会话 `1794991629-e435c8c72f95`，status=running。


- 节点收尾：status=success，提交 `4ec3d41b feat(kanban): 从权威状态产出 Router 生命周期事件（Scope: G-MH）`，工作区干净。

### T6 结果（2026-11-18）：PASS

- **交付**：`router_event_sources.go`（`ScanRouterEventSources` + `emitRouterEvent` + Router-owned 判据 + 有界 payload + 确定性 TurnID + 进程内 stall 观测）与 10 条行为测试；四处生命周期钩子（结算窗口、操作员终态、HumanGate 物化、启动对账）。工作区 `task-80` HEAD `4ec3d41b`，1038 insertions / 1 deletion。
- **10 条行为逐条 PASS**（工头独立复跑定向 10.039s 绿，读码核对）：幂等 settled、failed 独立行、gate 按 attempt、显式/legacy 只写 Notes、结算窗口可补齐且与钩子同键、stall 七子例只认明确证据、5 分钟桶去重、扫描前后权威表逐行 dump 相等、取消/暂停不产结算、迁移幂等。
- **RED 诚实性**：9/10 可打红；行为 9 是负向断言（stub 天然满足），worker 在报告里明写「RED 无法把它打红」，未拿它冒充证明。
- **工头复核**：四个钩子均为「先落权威状态、再写事件」，失败只记日志由扫描补齐；`settleWorkflowReply` 的调用点放在 `workflowReplyOutcome` 之前有报告解释且成立（终态来自任务行，投影不完整时后者会提前 return）；`finishWorkflowRunByOperator` 的 `-1` 行是返回值重构，无行为变化；启动对账只在 `open>0` 时产事件且不改任务状态。
- **硬线处置（`c35ae226`）**：`workflow_runtime.go`（基线 501 → T6 前 508 → 515）与 `service_activities.go`（基线 534 → 538）**在 T6 之前就已超 500 行硬线**，`docs/agent/rules/backend/file-size.md` 无存量豁免，T6 又各追加钩子。按职责纯移动拆出 `workflow_runtime_cancel.go`（66 行，取消/中断收尾）与 `service_activity_output_text.go`（126 行，节点输出可读文本），拆分后 459 / 421 行；`go vet`/`gofmt` 干净、`go test -p 1 ./server/internal/kanban/` 123.762s 全绿。
- **集成**：`git merge --no-ff task-80` → `df074149`；登记 G-MH（`c35ae226` 内），锚点逐条核对命中；`make check-upstream` **1894 delta / 308 组**绿。拆分同时迁移 G-LN 的 `nodeRunOutputSummary`、G-LX 的 `"route": true,` 两个锚点。
- **交给 T7 的约束（不是 T6 缺陷）**：扫描当前遍历 root 下**全部** Run（含历史终态），每个 Run 4–5 次读 + 一次 `InsertEvent` 尝试。30s 循环接线前必须给扫描加窗口（未终态 Run + 近期终态，或先按唯一键判存在再插入），否则长寿命 root 每拍都付 O(全部历史 Run) 的 SQLite 写锁代价。
- **残留（非阻断）**：stall 观测存进程内，重启后需重新累计两轮；`startup_reconcile` 对所有 open 的 Router-owned Run 产生（含 paused，仅读不改）；worker 用普通 `git merge` 而非 `--no-ff`（仅历史形状，集成侧已用 `--no-ff`）；新测试文件恰 500 行、无余量。

### T7 实例

- MindFS #81 / `task_da47cdee31bdd769`；worktree `/home/xiaokubao/projects/mindfs/.worktree/task-81`，分支 `task-81`（创建基线 `7f1b60ee`）。
- Worker 必须先 `git merge --no-ff task-73`（须含 `df074149` / `c35ae226` / `060f2b70`；有冲突停止报告）。
- 冻结接口、12 条必钉行为、允许文件与边界：`task-73/.superpowers/sdd/2026-11-18-super-agent-r0-r1/task-7-brief.md`。
- 关键冻结决策：coordinate 用户留言沿用 route；运行期事件只允许 `say` / `ask_user` / `silent`（`inspect_run` 为只读工具），四类执行/转交动作在校验阶段拒绝且不回落旧路径；每条原始留言最多 3 个 Router 回合，链深度 >3、预算耗尽、stall 证据失效、缺 originating user activity 一律 `skipped` 且不调模型；事件回合按 `event.TurnID` 复用决策幂等，崩溃后按 applied/applying/rejected 三态 replay，先副作用后 `CompleteEvent`；30s 循环使用**有界扫描**（`updated_at` 1h 内 ∪ 含 open NodeRun 的 Run），不再每拍遍历全部历史 Run（T6 约束）；`router_service.go` 已 499 行，允许把 `routerAgreement` 纯移动到 `router_agreement.go`，最终必须 ≤500。
- 报告：`task-81/.mindfs/dispatch/task-7-report.md`；提交信息 `Scope: G-MI`（登记由工头补）。
- 派发（2026-11-18）：用户留言 `act_f0d9055e85645016`，`workflow_choice=specified` + `workflow-blank:v1`；Run `run-276635592ec27a89` 节点 `execute` 启动，会话 `1794994523-fd2c47cdca2c`，进程 cwd 已核对为 `task-81`，status=running。
- 交接给 T7 的既有约束：T6 扫描窗口（见上）；`service_activities.go` / `workflow_runtime.go` 刚拆分，改动前先看新文件位置。

### T7 结果（2026-11-18）：PASS

- **交付**：`router_coordinate.go`（327 行：触发映射、消费、30s 循环、有界扫描、coordinate 专用启动回收）与 `router_coordinate_turn.go`（354 行：claim/闸门/replay/白名单/失败终态）；`router_agreement.go`（41 行，纯移动）；三个行为测试文件 447/176/211 行。`router_service.go` 499 → 489 行（搬走 `routerAgreement` 后新增事件闸门/分派/Close）。工作区 `task-81` HEAD `b764dd49`。
- **12 条行为逐条 PASS**：模式矩阵（off/shadow/route 零读写、事件仍 pending、Event 触发被忽略；coordinate 起循环且 Close 收敛）、coordinate 用户留言沿用 route 同步占位（empty 应用、specified/disabled 零决策）、settled 事件汇报（turn/kind/ref 与事件一致、回复线程指向原始留言）、同任务串行/跨任务并行、回合预算（3 条决策后第 4 个事件 skipped 零模型）、链深度 3 处理 / 4 skipped / 缺原始留言 skipped、运行期动作白名单三类执行动作各自拒绝且零新 Run/零直连、stall 重查（NodeRun 已终态 skipped、running 正常汇报）、模型失败即终态且不重试、崩溃 replay（构造期回收 + 零模型 + 零新增活动）、有界扫描窗口（2h 前终态不产、1h 内正常产、窗口外有 open NodeRun 仍扫描）、运行期 say 不覆盖任务结果摘要。
- **RED 诚实性**：worker 在 `/tmp/t7-red` 用「基线 86561f0e + 三个测试文件 + 空桩」复跑，12 条全部在各自断言处失败（原文进报告）；空桩覆盖两个冻结接口与 `RouterTrigger.Event`，没有一条靠空实现或 `t.Skip` 通过。
- **工头独立复核**：定向 `-run TestCoordinator` 7.199s 绿；kanban 全包 `-p 1 -count=1` 137.448s 绿；崩溃 replay 用例 `-count=10` 3.163s 绿；`go build ./...` 退出 0；`gofmt -l` 与 `go vet ./server/internal/kanban/ ./server/app/` 无输出；行数逐文件复核均 ≤500。
- **工头批准的偏移**：① `activity_route_apply.go` 的终态闸门从「所有动作」收窄为 `trigger_kind == user_message`——Run 结算后任务本来就该是终态，不豁免会把运行期汇报挡成 `ErrTaskClosed`；用户留言语义由行为 2 单独钉住，行为 3 用 `StatusSuccess` 任务钉住运行期路径。② 事件决策的 applying 不参与启动期 `RecoverApplyingDecisions`，改由 replay 重跑 `ApplyRouteActions`（`ClaimAppliedSeq` 幂等），避免把已经可见的汇报标成失败。
- **登记与门禁**：G-MC 的 `activity_service_dispatch.go` 锚点更新为 route/coordinate 共用条件；新增 G-MI（17 个锚点，逐条 grep 命中）；`make check-upstream` **1900 delta / 309 组**绿。集成提交 `f939c4a5`。
- **残留（非阻断）**：① 有界扫描的取舍——钩子漏写且早于 1h 窗口、又无 open NodeRun 的终态 Run 不会被 30s 循环补齐（启动对账的全量扫描兜底）；② `waiting_user` 计入 open 比 brief 字面更宽，理由是 gate 可能停数小时；③ brief 构建失败只落事件 failed、不写决策行（决策行在 brief 成功后插入）；④ 事件决策若永远 replay 不成功会停在 applying，只能等下次消费同一事件再试；⑤ 崩溃 replay 用例的「构造期回收 → 手动消费」之间有一个很窄的调度窗口（后台首拍可能先消费），`-count=10` 未复现，语义仍由同用例的零模型/零新增活动断言兜底；⑥ 循环里的扫描/消费用独立 2 分钟 ctx，`Close` 最多等 5s，正在跑的一拍会在关库后自然报错退出。
- **下一步**：T8（brief 运行期证据 + 里程碑文案）现在可冻结派发；T9/T10 继续 gated。

### T8 实例

- MindFS #82 / `task_232b8afa0212c92c`；worktree `/home/xiaokubao/projects/mindfs/.worktree/task-82`，分支 `task-82`（创建基线 `7f1b60ee`）。
- Worker 必须先 `git merge --no-ff task-73`（须含 `f939c4a5` / `6a1e5bd6`；有冲突停止报告）。
- 冻结接口、8 条必钉行为、允许文件与边界：`task-73/.superpowers/sdd/2026-11-18-super-agent-r0-r1/task-8-brief.md`。
- 关键冻结决策：事件 brief 只读 `event.RunID` 指向的 Run（不得用任务最新 Run 顶替），并清空 `LatestRun` / `ActiveExecution` / `Workflows` 防止串话；`Trigger.Ref=event.ID`、原始留言正文放宽到 2000 字节；节点证据复用 `nodeRunOutputSummary`，≤20 条、失败/等待/运行优先保留，未验证项 ≤10 条；payload 只留白名单键、≤2KB；任何裁剪置 `Truncated`，序列化后仍 ≤24KB；运行期 say/ask_user 用 `act-rtev-<sha256(decisionID:seq)[:20]>` 确定 ID + `InsertOrIgnore`，`RunID` 保持空、默认 `purpose=progress`，绝不改写旧 Run 的唯一结果活动；`silent` 理由继续落在 `router_decisions.actions_json`，不改事件语义。
- 报告：`task-82/.mindfs/dispatch/task-8-report.md`；提交信息 `Scope: G-MJ`（登记由工头补）。
- 派发（2026-11-18）：用户留言 `act_fc8476a6fa4faadf`，`workflow_choice=specified` + `workflow-blank:v1`；Run `run-3c5376128222f681` 节点 `execute` 启动，会话 `1794997998-e28baf3f7e3e`，进程 pid 3907496 的 cwd 已核对为 `task-82`。

### T8 结果（2026-11-18，工头裁决 PASS）

- **交付**：`93f96f14 feat(kanban): 运行期事件证据 brief 与确定性里程碑汇报（Scope: G-MJ）`；新增 `router_event_brief.go`（427 行）+ 两个测试文件，改 `router_brief.go` / `activity_route_apply.go` / `router_prompt.md`。工头修正 `f7dcc5bb`，登记 `bf1071d5`，集成 `2fa683da`。
- **8 条行为 + 工头补 3 条，全部 PASS**：① settled 证据三段齐全 + 原始留言 + 两节点输出可读 + 不泄露别的 Run/workflow；② failed 节点错误摘要与 `run_status=failed`（不是任务状态）+ attempt 透传；③ gate_waiting 带 `waiting_user`/`human_gate` 节点 + 策略提示明确不能代批；④ 25 节点 / 超长输出 / 超大 payload → ≤20 节点、`omitted_nodes>0`、`truncated`、payload 只剩白名单、序列化 ≤24KB；⑤ 旧事件不串话（事件指 `run-old`、任务引擎已 `run-new`，证据只含 `run-old` 节点、`LatestRun` 空）；⑥ 同一决策/动作重复应用只落一条、ID 为 `act-rtev-<sha256>`、`RunID=""`、`purpose=progress`、旧 Run 结果活动逐字节不变；⑦ 运行期 silent 理由落库可读、事件 done、可见活动不增加；⑧ 假 agent 捕获 prompt 含事件 ID 与两个节点摘要、最终 say 在原始留言线程且 `ReplyToID` 指向它。工头补：⑨ `router_say` 工具默认值在事件回合为 `progress`（变异验证：去掉即红）；⑩ 模型只说话时合成 say 也是 `progress`（同上）；⑪ 24KB 紧急裁剪的未验证项保留「还有 N 条」标记。
- **RED 诚实性**：worker 在 `/tmp/t8-red`（基线 `6a1e5bd6` + 两个测试文件 + 只编译不实现的桩）复跑，8 条全部在各自断言处失败；报告如实记录首轮第 5 条因 fixture 非终态而「错误地红」，修正 fixture 后重跑才是干净的 RED。
- **工头裁决 NEEDS_CONTEXT**：worker 报告的唯一冲突是 `TestCoordinatorRuntimeEventActionWhitelist/run_workflow` 文案——事件 brief 按冻结条款置空 `Workflows` 后，工具处理器先以「workflow 不可用」早退，遮住了 T7 钉住的白名单文案。裁决：**不改 T7 测试**，而是让事件 brief 跳过目录可用性检查（`isRuntimeEventBrief`，工具处理器与 `validateRouterActions` 两处），理由是这样拒绝原因才准确（「运行期不许跑 workflow」而非「目录里没有」）；两种行为的执行语义本来就一致（零新 Run、决策 rejected、事件 failed）。
- **工头复核发现并修的三处**：① `router_say` 工具默认 `chat` 让应用层的 `progress` 默认不可达；② 合成 say 绕过同一默认值；③ 5 条裁剪把「还有 N 条」标记吃掉，brief 看起来像证据完整。三处都有定向用例并做变异验证。
- **门禁**：定向 4.728s 绿；kanban 全包 `-p 1 -count=1` 136.763s 绿；`go vet ./server/internal/kanban/` 干净；`gofmt -l` 全部改动文件无输出；行数逐文件 ≤500（427 / 370 / 252 / 482 / 442 / 361 / 461）；`make check-upstream` **1903 delta / 310 组**绿（G-MJ 12 锚点逐条 grep 命中）。
- **未做**：未 push / 未 deploy / 未合并 main / 未重启 any node；生产 `router.mode` 未动。
- **残留（非阻断）**：① 事件 brief 不携带 workflow 目录是设计本意，代价是模型在事件回合看不到可开执行，由白名单兜底；② 未验证项文案是给模型看的证据而非成品文案，靠提示词规则 12 约束；③ effect ID 公式若被改，行为 6 会红（有意为之）。
- **下一步**：T9（并发/重启故障矩阵）已解锁，需先冻结 brief。

### T9 前置（2026-11-18，工头修正）

- **两个真实崩溃窗口**（临时探针实测，非推断）：① 运行期决策先推 `applied_seq` 再写可见活动 → 崩在中间时 replay 跳过动作、事件 done「replayed」，汇报永久丢失；② 零动作 applying（模型回合中途崩溃）被主动当成已应用，事件静默 done。
- **修复**：运行期副作用确定 ID 本身就是幂等闸门，改为先写效果、后推游标（`AdvanceAppliedSeq`，单调）；零动作 applying 由 `DeleteInterruptedDecision` 按「零动作 + 游标未动 + 无会话键」删除后重跑模型；其它无法收尾的状态落事件 failed。
- **回归用例**：`router_coordinate_turn_test.go` 的 `TestCoordinatorReplayRepairsUnwrittenRuntimeEffect`（先 RED 于「no activity carries the decision」）与 `TestCoordinatorReplayRetriesInterruptedModelTurn`（先 RED 于「model calls = 0, want exactly 1 retry」）；kanban 全包 `-p 1 -count=1` 144.295s 绿，`go vet` 干净。
- **附带的纯移动**：`router_store.go`（527 → 412 行）的会话部分移到 `router_session_store.go`（164 行），账本文件回到 500 行硬线内；G-LP files 已补新文件。
- **登记**：G-MK（9 个锚点）；集成在 `task-73`，未 push / 未 deploy / 未合并 main。

### T9 实例（2026-11-18）

- MindFS #83 / `task_072cb6c084d1c07c`；worktree `/home/xiaokubao/projects/mindfs/.worktree/task-83`，分支 `task-83`（创建基线 `7f1b60ee`，任务 API 返回 `worktree_built=true`）。
- Worker 必须先 `git merge --no-ff task-73`（须含 `2fa683da` / `f7dcc5bb` / `bf1071d5` / `a292a7f7` / `a219174c`；有冲突停止报告）。
- 冻结 brief：`task-73/.superpowers/sdd/2026-11-18-super-agent-r0-r1/task-9-brief.md`。
- 关键冻结决策：**只写测试**（`router_coordinate_recovery_test.go`，>500 行可再拆一个 `_restart_test.go`），生产文件一律不动，发现真缺陷报 BLOCKED 由工头另开前置修正；「重启」统一用同库 `newRouterService(...)` 新实例 + 立即 `Close()` 收回消费权，断言只用最终状态，不允许假设构造期首拍没跑；8 条行为覆盖「pending 跨重启存活 / claimed 跨重启回收 / 效果已写游标未推进 / 并发领取单效果 / 旧 Run 迟到事件 / 暂停与终态仍汇报 / 预算跨重启 / 无法重放即终态」；RED 证据改为**5 个强制缺陷注入**（M1 去掉 `RecoverClaimedEvents`、M2 `ClaimEvent` 恒真、M3 effect ID 退回随机、M4 删预算分支、M5 failed 也 retry），每个都要有红的用例与失败原文，还原后 GREEN 且生产文件零 diff；注入撤改一律 `cp` 备份还原，禁止 `git checkout --` / 裸 stash。
- 不得重复 T7 已钉的三个 replay 用例（`TestCoordinatorCrashReplayCompletesWithoutModelCall`、`TestCoordinatorReplayRepairsUnwrittenRuntimeEffect`、`TestCoordinatorReplayRetriesInterruptedModelTurn`）。
- 报告：`task-83/.mindfs/dispatch/task-9-report.md`；提交信息 `Scope: G-ML`（登记由工头补）。
- 派发（2026-11-18）：用户留言 `act_9df0d93ee963dc78`，`workflow_choice=specified` + `workflow-blank:v1`；Run `run-6c11062be74b8e1e` 节点 `execute` 启动（`run-6c11062be74b8e1e:execute::0:1`，claude / sonnet，会话 `1795002744-753e92ae2bf4`），进程 pid 4185625 的 `/proc/<pid>/cwd` 已核对为 `task-83`。

### T9 结果（2026-11-18，工头裁决 PASS）

- **交付**：`4e4d2235 test(kanban): 补齐并发与重启故障矩阵（Scope: G-ML）`；唯一改动是新增 `server/internal/kanban/router_coordinate_recovery_test.go`（491 行、8 个 `TestRecovery*`），`git diff --stat -- 'server/**/*.go' ':!*_test.go'` 为空（生产文件零改动）。集成 `d5d31a5e`，登记 `9a418944`。
- **8 条行为全部 GREEN**：① pending 跨重启存活（模型 1 次、汇报 1 条、决策 1 条）；② claimed 跨重启由 `RecoverClaimedEvents` 回收（模型 1 次、`claimed_by` 不再挂死进程）；③ 效果已写游标未推进 → `replayed`、零模型、活动总数不变、effect 行恰好 1 条且正文不变、`applied_seq=1`；④ 两实例同事件并发领取 → B 未进模型回合、模型 1 次、决策 1 条、事件 done；⑤ 旧 Run 迟到事件 → `run-new` 的 `NodeStates` 逐键不变、零新 Run、零直连、任务状态不变；⑥ paused/success/cancelled 三例都收到汇报且状态不变；⑦ 预算跨重启仍 skipped、零模型、决策仍为 3；⑧ failed 决策不重放 → 事件 failed 且带原失败文案、零模型、零可见活动。
- **RED 证据是缺陷注入（6 个，含 1 个加严变体）**：M1 去 `RecoverClaimedEvents` → ② 红（`event ... stuck at status "claimed" (claimed_by="dead-process")`）；M2 `ClaimEvent` 去 status 条件 → ④ 红（`the second instance entered a model turn instead of losing the claim CAS`）；M3 effect ID 退回随机 → ③ 红（`activities before=2 after=3`）；M4 删预算分支（`&& false` 保编译）→ ⑦ 红；M5 failed 分支也 retry → ⑧ 红在「interrupted decision could not be recovered」；M5b 同时放宽 `DeleteInterruptedDecision` → ⑧ 红在「期望 failed 得到 done」，证明「不盲目重跑」这道闸门承重。可选 M6（去掉运行期终态豁免）→ ⑥ 三子例全红。所有 `/tmp` 备份 md5 与 `HEAD:` 逐一核对，还原后生产文件零 diff。
- **工头的诚实记录**：worker 首版 M2 注入后用例仍 GREEN，它没有放过——用一次性探针确认注入有效而用例没钉住（B 的 lane 在 A 结束后才跑），改成「A 仍阻塞时判定 B 是否进入模型回合」后才取得真实 RED。M4 首版直接删分支导致编译失败，明确不作为证据，改成 `&& false`。
- **工头独立复核**：定向 `-run TestRecovery` 3.451s 绿；`-race -count=3` 24.777s 绿（无 race 报告）；kanban 全包 `-p 1 -count=1` 135.456s 绿；`go vet` 与 `gofmt -l` 干净。另做**两次独立注入**（未信报告）：effect ID 追加 `return ""` → ③ 红 `activities before=2 after=3`；`RecoverClaimedEvents` 调用替换为空 → ② 红 `stuck at status "claimed"`；两次都从 `/tmp` 副本还原并复绿。
- **门禁**：`make check-upstream` 1905 delta / 312 组绿（G-ML 9 锚点命中）。
- **残留（非阻断，已写入 G-ML boundary）**：未模拟 OS 级 `kill -9`/断电、SQLite 单语句中途崩溃、真实双进程（同进程两个 `RouterService` 近似）、30s 循环时钟注入；只证明 R1 通知/协调恢复，**不**证明 R3 通用副作用恢复；行为 4 依赖进程内诊断接口 `activeLanes()`。
- **未做**：未 push / 未 deploy / 未合并 main / 未重启任何节点；生产 `router.mode` 未动。
- **下一步**：T10（真实闭环与工程集成）已冻结 brief 并派发，见「T10 实例」。

### T10 实例（2026-11-18）

- MindFS #84 / `task_94ec32d147fcc1d6`；worktree `/home/xiaokubao/projects/mindfs/.worktree/task-84`，分支 `task-84`（任务 API 返回 `worktree_built=true`）。
- Worker 必须先 `git merge --no-ff task-73`（须含 `186e10e7` / `4e4d2235` / `d5d31a5e` / `9a418944` / `9af54217`；有冲突停止报告）。
- 冻结 brief：`task-73/.superpowers/sdd/2026-11-18-super-agent-r0-r1/task-10-brief.md`。
- 关键冻结决策：
  - **只新增** `e2e/test_task_router_coordinate.py`（≤500 行，仅标准库）+ 可选 `e2e/t10-iso-driver.sh`（≤200 行）；生产代码、Go 测试、共享文档、定制清单、其他 worktree、主 checkout、`~/.config/mindfs` 一律不写；发现真缺陷报 BLOCKED 并给复现，不做顺手修复。
  - 隔离根 `/tmp/mindfs-r1-t10`、端口 **7433**（避开 T4 的 7432）与 `MINDFS_ISO_KEEP=1`；隔离配置 `router.mode=coordinate` + sonnet / effort low / `turn_timeout=180s`（T4 实测真实回合尾部 155.9s > 90s，D-4 不改产品默认，只在隔离配置放宽）。
  - 场景 A 需求→执行→主动汇报（第二条决策 `trigger_kind=run_settled` + 事件行 done）；B 中途留言有归宿（`continue_requests` 终态或可见活动；`delivery_failed` 带「agent required」= D-1 回归，直接 BLOCKED）；C 失败 + gate（未知 agent 确定性失败；普通留言不得批准 gate；专用端点 approve 200、重复 409、下游节点继续）；D 重启只限隔离进程（同根重启，断言无 pending/claimed 残留、无重复 effect、无新 Run；错过 pending 窗口可如实记 blocked）。
  - 验证口径：`router_events` **没有 HTTP 端点**，只允许只读取 SQLite（`file:...?mode=ro`）+ `GET /api/tasks/{id}/router-decisions`；脚本入参 `--base/--user/--root/--db/--out/--scenarios`，结果 JSON `{scenario,pass,status,evidence,latency_ms,notes}`，退出码 0/1/2。
  - web 门禁允许 `blocked`（worktree 无 node_modules，只能符号链接主 checkout 的只读依赖后 `make build-web`），须写明原因；不跑 android/APK/deploy。
- 报告：`task-84/.mindfs/dispatch/task-10-report.md`；提交信息 `test(e2e): 隔离真实 Router coordinate 闭环验收（Scope: G-MM）`（登记由工头补）。
