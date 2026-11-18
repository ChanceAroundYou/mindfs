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
| T4 真实验收 | 依赖 T3；独占隔离实例 | 不与其他计时/真实求解并发 | 独立运行根 + task-4-report.md | R0 场景逐例完整证据 | 模型不可用则 BLOCKED，不改生产配置 | 实现/真实求解 1 | blocked：首轮 6/7，D-1..D-3 已修，待重跑 S5/S7 |
| T5 事件账本 | R0 PASS 后冻结接口 | T4 → T5 | worker branch + task-5-report.md | 去重/领取/重启/隔离测试 | R0 未通过或接口未冻结不启动 | 实现 1 | pending |
| T6 事件生产 | T5 后冻结 brief | T5 → T6 | worker branch + task-6-report.md | 来源/重扫/结算窗口测试 | 接管 legacy/显式任务即拒绝 | 实现 1 | pending |
| T7 消费预算 | T6 后冻结 brief | T6 → T7 | worker branch + task-7-report.md | 模式/链深度/动作权限矩阵 | 自动开执行/cancel/retry 即拒绝 | 实现 1 | pending |
| T8 汇报证据 | T7 后冻结 brief | T7 → T8 | worker branch + task-8-report.md | 来源/摘要/确定 effect 测试 | 串话/重复可见回复即拒绝 | 实现 1 | pending |
| T9 故障矩阵 | T8 后派发 | T8 → T9 | 测试提交 + task-9-report.md | 重启与并发故障点断言 | 生产重启、盲目重放即停止 | 实现 1 | pending |
| T10 集成验收 | T9 后独占运行 | 全链依赖 | 总任务分支 + 全量日志 | 计划 T10 全量门禁与真实闭环 | 任一门禁失败不集成发布 | 实现/真实求解 1 | pending |

T2–T10 启动前更新其实际 task id、worktree、绝对报告路径与定稿 brief；未补全不得派发。

## 任务实例

- #73：总任务，只作工头工作区与计划归档；未派 worker，避免本会话重复执行。

## 验收记录

- 2026-11-18：通过本机 API 读取项目、工作流、health；创建 #73，调用 rebuild-worktree 成功；尚无代码实施或真实 route 验收通过声明。

## 尚未解锁

- R1 T5–T10：等待 R0 门禁和接口定稿。
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
- **尚未完成**：S5 与 S7 需在隔离实例上重跑确认（D-1 是唯一 fail 的直接原因，D-2 对应 S7 首轮异常）。R0 仍未 PASS，R1/T5 保持锁定。

