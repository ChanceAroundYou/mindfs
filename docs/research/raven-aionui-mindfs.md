# Raven / AionUi 可吸收能力分析

- 状态：讨论稿
- 日期：2026-11-28
- 范围：比较 Raven、AionUi 的公开能力，并映射到 MindFS 当前架构
- 目标：形成后续逐项讨论的候选清单，不直接改变产品范围或实现计划

## 1. 结论

MindFS 已经具备多 Agent 接入、ACP、会话恢复、子 Agent、任务看板、工作流、定时任务、远程 Web/PWA、通知、文件关联和多节点控制面。后续最值得吸收的能力集中在三条主线：

1. 把任务执行过程统一记录为可回放、可诊断的运行轨迹。
2. 把多 Agent 协作提升为带角色、依赖、消息和汇总的 Team Mode。
3. 把技能和项目记忆变成可检索、可挂载、可评估的长期能力。

Raven 的主要参考点是 DAG 编排、轨迹观测、长期记忆、技能检索和 harness 评估。AionUi 的主要参考点是统一 Agent Connector、Team Mode、技能分层、无人值守调度和远程渠道。

## 2. MindFS 当前基础

| 领域 | 当前基础 | 相关位置 |
|---|---|---|
| Agent 接入 | Claude、Codex、Gemini、ACP 及其他 CLI；支持探测、配置和切换 | `server/internal/agent`、`server/internal/api/agent_*` |
| 会话 | 底层 Agent 会话绑定、导入、恢复、双向同步、fork | `server/internal/session` |
| 子 Agent | 自动发现并展示 Codex / Claude Code subagent | `server/internal/agent`、前端会话树 |
| 任务 | 对话模型、Router、并发任务、worktree、工作流 | `server/internal/kanban`、`server/internal/api/appcontext_workflow.go` |
| 定时 | cron、会话复用、运行状态和结果通知 | `server/internal/scheduled`、`web/src/components/task` |
| 远程 | Web/PWA、Web Push、通知脚本、飞书中继 | `server/internal/notify`、`server/internal/channels` |
| 文件 | 文件、会话、任务、worktree 深链接 | `server/internal/fs`、前端文件与会话组件 |
| 多节点 | control plane / data plane 分离，worker 和 edge | `docs/multi-node-control-plane.md` |
| 记忆 | 当前已有 Agent 资源内存指标和会话上下文能力 | `server/internal/agent/resource_usage.go`、`http_agent_memory.go` |

这里的“记忆”指运行资源指标与会话上下文管理，尚未形成 Raven 意义上的长期 Agent 记忆库。

## 3. 候选能力清单

### 3.1 P0：统一 Run / Step 轨迹

Raven 的 trajectory view 将一次多 Agent 运行展开为时间线和 waterfall。MindFS 已经拥有 WS Agent event、session turn、工具调用、文件变更、任务状态、工作流节点、审批和 scheduled run 等事件来源，适合建立统一运行模型。

建议模型：

```text
Run
 ├── task_id
 ├── parent_run_id
 ├── status
 └── Step[]
       ├── agent / session / node
       ├── step_type
       ├── start / finish
       ├── input / output summary
       ├── artifacts
       └── error / retry
```

建议先提供三种视图：

- 时间线视图：回答当前运行到哪里。
- DAG 视图：回答步骤依赖和并行关系。
- 步骤详情：回答哪个 Agent、哪个工具或哪个文件造成了结果。

讨论点：事件是否全部持久化，还是只持久化摘要；`run_id` 与现有 `session_key`、`task_id`、`requestID` 的关系；跨节点事件如何合并；从失败步骤继续时如何保证幂等。

### 3.2 P0：Team Mode

AionUi 的 Team Mode 由 Leader 拆解任务，Teammate 并行执行，通过异步 mailbox 和共享任务板协作。MindFS 已有 subagent、并发任务、worktree、审批和 Agent Pool，这个方向与现有架构匹配度高。

建议模型：

```text
Team
 ├── leader_session
 ├── teammate_sessions[]
 ├── shared_task_id
 ├── mailbox
 ├── shared_artifacts
 └── approval_state
```

第一版可以由 Router 生成受约束的团队计划：

```json
{
  "leader": "codex",
  "teammates": [
    {"role": "research", "agent": "claude"},
    {"role": "implementation", "agent": "codex"},
    {"role": "review", "agent": "gemini"}
  ]
}
```

需要明确共享目录与 worktree 两种策略，记录每个 teammate 的权限、输入、输出、状态和失败原因。

讨论点：Team 是否属于一个 task；Leader 是否允许动态增删 teammate；mailbox 是否持久化；共享文件冲突如何处理；团队汇总是否必须经过审批。

### 3.3 P0：Agent Connector 能力矩阵

AionUi 将不同外部 Agent 统一为可配置后端，并根据能力注入 MCP / ACP。MindFS 当前 Agent 配置、探测和运行时能力已经存在，但 UI 和 Router 仍需要一个统一的能力描述。

建议每个 Agent Profile 暴露：

- 连接类型：ACP、CLI 或 API。
- executable、模型和默认参数。
- 是否支持 subagent、取消、流式输出、MCP、无人值守、会话恢复、附件和权限请求。
- 最近探测结果、版本和健康状态。

这让 Router 和 Team Mode 基于能力选择 Agent，减少按 Agent 名称写分支。

### 3.4 P1：DAG 工作流、失败续跑与运行策略

MindFS 已有可视化工作流和对话 Router。可吸收 Raven 的依赖图语义，把 agent、shell、审批、通知和汇总都表示为步骤，支持并行、失败重试、从失败节点续跑和等待多个上游完成。

建议保留“任务是对话”的产品模型，DAG 作为一次 Run 的执行计划，不恢复旧的固定 stages 概念。

讨论点：DAG 是用户显式编辑、Agent 草拟，还是两者兼有；节点输入输出是否使用结构化 artifact；重试是否新建 Step；续跑是否允许重新执行有副作用的步骤。

### 3.5 P1：技能分层、检索与会话挂载

AionUi 将技能分为内置、用户自定义和扩展三层。Raven 的 SkillForge 进一步加入技能检索和使用评估。MindFS 已有技能目录、规则体系和 composer candidate，可以整理为统一技能注册表。

建议元数据：

```text
source: built-in / project / user / extension
name
description
compatible_agents
required_tools
input_types
output_types
risk_level
version
```

会话启动时展示当前技能，支持搜索、启用和禁用。任务结束后记录技能是否被使用、结果如何，供后续排序。

讨论点：技能定义是否继续使用 Markdown；技能来源是否允许远程仓库；项目技能和用户技能的覆盖规则；技能启用是否需要审批；技能版本如何与任务结果绑定。

### 3.6 P1：项目级长期记忆

Raven 的 EverOS Memory 保存用户上下文、Agent 经验、世界知识和可复用技能。MindFS 可以先实现轻量、可审阅的项目记忆，不直接引入复杂向量数据库。

建议目录：

```text
memory/
  user.md
  project.md
  agent.md
  decisions/
  failures/
  skills/
```

每条记忆包含 `scope`、`content`、`source_session`、`created_at`、`updated_at`、`confidence` 和 `tags`。第一版优先支持项目决策、用户偏好、失败原因和修复方法，并允许 Agent 提议、用户确认后写入。

讨论点：记忆的账户、项目、节点作用域；跨节点是否同步；自动写入还是确认写入；删除和纠错；检索结果如何进入 Agent context；与现有 session context saving 的边界。

### 3.7 P1：定时任务运行历史和无人值守策略

MindFS 已有 `TaskCron`、`LastRunAt`、`LastSuccessAt`、`LastError`、`NextRunAt` 等字段。AionUi 提供了 cron、固定间隔、一次性任务、时区和 24×7 运行的完整产品闭环。

建议补充：

- 一次性任务和明确时区。
- 最大运行时长。
- 失败重试和退避。
- 并发策略。
- 最近 N 次运行记录。
- 从某次运行继续。
- 任务级审批策略。
- 结构化结果和产物链接。

### 3.8 P2：结构化通知动作和更多渠道

MindFS 已有 Web Push、通知脚本和飞书中继。可以吸收 AionUi 的渠道适配器思路，统一事件和动作：

```text
event_type
task_id
run_id
session_key
status
summary
artifacts
actions: view / approve / retry / stop
```

先完善 Web Push 和飞书，再按实际需求增加 Telegram、Slack、钉钉等渠道。

### 3.9 P2：Agent 能力评估与回归样例

Raven 的 Evolver 通过基准任务评估 harness 变化。MindFS 可以先实现 Agent capability probe 和任务结果统计，不开放 Agent 自动修改运行逻辑。

建议统计：

- 成功率。
- 耗时。
- 重试次数。
- 人工介入次数。
- 产物是否通过校验。
- 按 Agent、模型、项目和技能分组。

## 4. 暂缓吸收的内容

### 4.1 Raven 的 harness 自我演化

Raven 可以修改 Memory、Planning、Capability、Action 等 harness 模块并通过评估安装。MindFS 当前先吸收配置版本、diff、基准任务、用户批准和回滚机制，再考虑自我修改。

### 4.2 AionUi 的大量行业助手模板

PPT、Word、Excel 和 Dashboard 等能力更适合作为技能包与项目模板，暂不进入 MindFS 核心产品模型。

### 4.3 一次性接入全部聊天渠道

Telegram、Lark、钉钉、微信和 Slack 会带来账号、回调、权限和消息一致性工作。先抽象通知接口，再根据实际使用量增加渠道。

## 5. 建议讨论顺序

1. Run / Step 轨迹模型。
2. Team Mode 的数据模型和执行边界。
3. Agent Connector 能力矩阵。
4. DAG 工作流与失败续跑。
5. 技能注册表和会话挂载。
6. 项目级长期记忆。
7. 定时任务运行历史和无人值守策略。
8. 通知动作与渠道扩展。
9. Agent 评估与 harness 演化。

每一项讨论完成后，再决定是否进入 Active plan，并补充 API、数据迁移、测试范围和节点部署影响。

## 6. 外部参考

- Raven：<https://github.com/EverMind-AI/Raven>
- Raven trajectory、orchestration、memory、SkillForge、Evolver：<https://github.com/EverMind-AI/Raven#-core-systems>
- AionUi：<https://github.com/iOfficeAI/AionUi>
- AionUi Team Mode：<https://github.com/iOfficeAI/AionUi#-team-mode--coordinated-multi-agent-collaboration>
- AionUi Skills：<https://github.com/iOfficeAI/AionUi#-skills-and-assistants>
- AionUi Scheduled Tasks：<https://github.com/iOfficeAI/AionUi#-scheduled-tasks--cowork-on-autopilot>
- MindFS 多节点控制面：[`docs/multi-node-control-plane.md`](../multi-node-control-plane.md)
- MindFS 多账户约束：[`docs/multi-user-prd.md`](../multi-user-prd.md)
