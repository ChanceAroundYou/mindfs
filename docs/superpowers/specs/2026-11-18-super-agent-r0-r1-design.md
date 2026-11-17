# Super agent R0/R1：工头闭环设计

日期：2026-11-18。方向与执行授权来自当前用户请求；具体接口与默认值为本计划提出的工程决策。

## 目的与批准范围

让用户的一次任务请求能经过明确派发、执行、主动汇报，异常时有可解释状态。
当前执行范围为 R0/R1；R2–R5 只记录后续依赖，不在本计划内实现。
先通过 R0 验证再接 R1。若发现设计级缺口，工头先修订本设计及对应任务，不让 worker 自由扩权。

## 当前证据

- main 基线 `3b6c171030ffee8b4c2d6cc416235677d714970a`，主树有另一会话的安卓后台推送设计，保持原样。
- `/mindfs/health` 返回 control；本机项目 root 为 `mindfs`，主账户 id 为 `u_24f56c12a0235e05`。
- HTTP 支持建任务、rebuild-worktree、发 activities、查 router-decisions 与 workflow-run。
- P1 route 已有 typed action 与 apply；coordinate 尚未进入 Enqueue 支持模式。
- 当前结算从节点输出提取结果，不自动再运行 Router；中断 applying 标 failed，不等同自动续做。
- 必须先验证运行中留言守卫是否在 Router 之前拒绝，以及排队时 brief 快照是否过期。
- 历史 p50 34.3s / p95 41.7s 仅为 shadow 样本，不能代替本轮 route 实测；用户已接受几十秒量级等待。

## 实现方式与取舍

1. 扩展现有 Router、workflow 生命周期与 SQLite：复用现有模型、界面与审计，采用此方案。
2. 外部常驻 agent 轮询任务：可快速演示，但会增加独立状态真源与重复触发风险；只用本会话作临时工头。
3. 从全局多 worker 总控起步：依赖目标、验收、恢复等能力，留到 R4/R5。

执行由主会话调用本机 MindFS HTTP API；每个工程任务绑定独立 worktree。
主会话持有计划与验收责任，worker 不再创建子任务/子代理，避免重复派发。
本轮不修改生产 Router 配置，不重启本机，不发布 APK；生产源码不直接改动。

## R0 场景合同

| 场景 | 期望 | 证据 |
|---|---|---|
| 简单实质问题 | 转交执行者，返回实质答案 | activity、decision、会话或 Run |
| debug / 新功能 / 单步任务 | 选择与描述匹配的路径 | action、workflow id、最终结果 |
| 关键信息缺失 | 询问缺失信息 | ask_user 活动 |
| specified / disabled | 严格绕过 Router | Router 决策增量为零 |
| 活动 Run 中补充留言 | 唯一活动节点可转交；0/多节点明确解释 | outbox / continue request / 可见问句 |
| Router 错误、超时、非法动作 | 单次回退、复用占位、不重复执行 | 决策状态、活动数量、Run 数量 |
| 同任务消息排队 | 执行前看到正确当前状态，原消息不丢 | 顺序、brief、实际动作 |

单元/集成先验证确定行为；隔离实例验证源码版本和真实 agent；部署后的线上行为只能由 e2e/live 验收。
延迟只报告本轮样本分布，不沿用旧 3s 准入，也不把 90s 超时当作质量达标。

## R1 事件与协调

事件只来自 Router 发起的 Run，通过结果活动的 router_decision_id 判定归属。
事件种类：run_settled、run_failed、gate_waiting、run_stalled、startup_reconcile。
唯一键包含 task_id、run_id、kind、事件实例标识；不能只按 task_id 去重。
gate 标识包含 node run/attempt；stall 使用固定 5 分钟时间桶，并在消费前重查是否仍停顿。
先提供持久事件表、领取/完成状态与启动对账，再把生命周期事件接入，不能只在内存里 enqueue。
记录 originating user_activity_id、chain_depth、source decision 和确定 turn_id，避免汇报给错用户回合。
R1 每条原始留言最多 3 个 Router turn；超过上限记录 skipped，不调用模型。

用户留言在 coordinate 中沿用 route；运行期事件第一版只允许 say / ask_user / silent / inspect_run。
运行期事件不自动创建新 Run、不转交普通 agent 开新一轮，不调用 cancel/retry/审批/收尾。
R2 才设计目标验收与返工循环；本限制避免把运行通知变成无界自动执行。

Situation Brief 补充事件来源、原始要求、Run 和 Agent 节点的有界结果摘要。
不把整个转录、文件、密钥放进 Router；不开放 shell。
完成汇报说明结果与未验证项；失败汇报给出证据和可选下一步；gate 提醒不代批。
stalled 必须有证据阈值且不把正常长回合误判失败；阈值由 T1 基线核查后定稿，未定稿前不启用该触发。

## 状态与恢复边界

事件未处理必须可重启后再发现；同一事件并发领取只允许一个执行者。
“已领取”不等于“副作用已完成”；不能先递增游标再假定外部执行成功。
可见协调活动使用确定 effect 标识，写活动与完成事件尽可能同事务；跨事务时必须先查询既有效果再恢复。
本计划只恢复 R1 通知与协调事件；通用 worker 外部副作用续做留给 R3。
模型调用可以被重试，但不得因此重复产出用户可见活动；失败与预算耗尽必须有终态。

## 六阶段后续分解

| 阶段 | 后续小任务 | 进入条件 |
|---|---|---|
| R2 | 验收条目存储；计划修订；证据关联；遗漏检查；待审核交付 | R1 事件闭环通过真实验收 |
| R3 | 执行检查点；发送状态对账；不确定状态升级；重启/断网故障矩阵 | R2 状态与证据模型稳定 |
| R4 | worker 能力目录；任务依赖图；独立工作区；结果汇总；组合验收 | R3 能安全解释每条执行状态 |
| R5 | 记忆来源与失效；项目优先级；成本预算；跨任务调度；长期回归 | R4 协作可观察、可验收 |

## 验证与交付

每个代码任务需要定向失败→通过证据和独立评审；定向命令统一 GOMAXPROCS=1、go test -p 1。
最终检查 Go 全量、web、tsc、build 与 check-upstream；源码行数和 fork 定制三件套仍有效。
部署仅在集成 main 后用 ship 流程；本机重启始终由用户执行。
所有报告写到派发表规定的位置，主会话核对实际文件、提交、测试和 API 状态后才标完成。
