# Super agent 派发表与进度

总任务：MindFS #73 / `task_7db338ea2d38e2c3`。
工头 worktree：`/home/xiaokubao/projects/mindfs/.worktree/task-73`，分支 `task-73`。
基线：`3b6c171030ffee8b4c2d6cc416235677d714970a`。
授权：当前用户已明确要求以 worktree + 任务派发执行；[goal](2026-11-18-super-agent-goal.md) 记录范围。
共享记录仅工头修改；worker 禁止子派发。实现并发 1，只读并发 1，所有本轮命令限制 1 线程。

## 派发表（开始前必须完整）

| 任务 | 工作流/执行方式 | 独立性/依赖 | 输出路径 | 验收命令/标准 | 停止条件 | 并发占用 | 状态 |
|---|---|---|---|---|---|---|---|
| T1 基线核查 | MindFS specified 空白执行，单 worker | 独立只读；不与工头重复审计 | `.superpowers/sdd/2026-11-18-super-agent-r0-r1/task-1-report.md` | 计划 T1 的 route 定向测试；逐项证据 | 权限/模型失败、超范围、同一路径连续两次失败即报告 | 只读 1 | running：#74 |
| T2 R0 回归 | 依赖 T1 后定稿并派发 | T1 → T2，独立 worktree | worker branch + task-2-report.md | R0 集成用例及 RED/GREEN | brief 未冻结不启动 | 实现 1 | pending |
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
