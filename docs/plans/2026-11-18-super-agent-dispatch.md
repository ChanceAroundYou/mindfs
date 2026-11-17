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
| T2 R0 回归 | specified 空白执行，Scope G-MC | T1 → T2，独立 worktree | worker branch + task-2-report.md | R0 集成用例及 RED/GREEN | fixture 无法稳定构造则 NEEDS_CONTEXT；不修源码 | 实现 1 | running：#75 |
| T3 P1 修复 | 依赖 T2 后定稿并派发 | T2 → T3，工头集成测试提交 | worker branch + task-3-report.md | T2 红转绿 + 原守卫测试 | 根因未确认不改源码 | 实现 1 | pending |
| T4 真实验收 | 依赖 T3；独占隔离实例 | 不与其他计时/真实求解并发 | 独立运行根 + task-4-report.md | R0 场景逐例完整证据 | 模型不可用则 BLOCKED，不改生产配置 | 实现/真实求解 1 | pending |
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
