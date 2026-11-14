# 阶段任务 → 对话 + WorkflowRun 迁移

2026-11-12 起，MindFS 的任务模型只有一种：**对话 + WorkflowRun**。历史阶段任务
不再以兼容视图存在，而是在服务启动时一次性转换。本文记录转换规则、备份位置、
验证方式与回滚步骤。

## 转换完成后的模型（2026-11，D3）

阶段概念已经从**运行模型、API 与前端里彻底删除**。"彻底"的含义：

| 层 | 现状 |
|---|---|
| 任务类型 | `Task` 没有 `Stages` 字段；新建任务只收一条用户留言（`CreateTaskInput.Input`）。 |
| 执行 | 每一轮由 Router（`kanban.ActivityService`）决定：命中 workflow 就跑 `WorkflowRun`；`workflow_choice=disabled` 或没有可用 workflow 时走直连执行器 `AppContext.RunDirectActivity`，在任务主会话里跑一轮普通对话并回写一条 Agent 回复。 |
| 收尾 | 也不再是"阶段"：`Service.runFinishTurn` 用固定提示词跑一轮普通对话，`Finishing` 是内存标记，不落任何阶段数据。 |
| HTTP | `/api/tasks/{id}/comments` 与所有 `/stages*` 端点已删除；留言统一走 `POST /api/tasks/{id}/activities`。 |
| 前端 | 没有 `StageTemplate`、阶段编辑器/选项条、默认流水、推进/立即执行按钮；任务详情只有一条 `ConversationView`，卡片只有完成/暂停/继续/取消/收尾/删除。 |
| 数据 | `task_stages_json`、`task_type`、`current_stage_index`、`stage_runs` 仅作为**冻结的审计证据**与一次性转换输入保留；生产路径不再读写（新任务写入固定值 `task_type='conversation'`）。 |

`task_type='stage'` 的行只可能来自转换前的旧库；启动时的 `ConvertStagesToConversation`
会把它们全部转完（见下文），第二次启动 `converted=0`。

## 触发方式

| 场景 | 行为 |
|---|---|
| 正常启动 | `TaskStore.migrate()` 在 schema 迁移之后调用 `ConvertStagesToConversation`；失败只记日志、不阻断启动，下次启动重试。 |
| 手动预演 | `go run ./server/scripts/convert_stages_to_conversation --db <副本> --dry-run` |
| 手动转换 | `go run ./server/scripts/convert_stages_to_conversation --db <副本>` |

`--dry-run` 不碰源库：先用 `VACUUM INTO` 生成临时快照，在快照上补齐 schema 并执行
转换，然后回滚事务。源库一个字节都不会改。

**只对副本运行手动脚本**。生产库由服务启动时的同一段代码处理；不要在服务运行时
直接打开生产库执行脚本。

## 转换规则

### 活动（task_activities）

| 来源 | 产物 | 说明 |
|---|---|---|
| `stage_runs[0]`（用户输入段） | `user_message` 活动 | 正文取该段 `input`；ID 为 `act-legacy-user-<taskID>`。 |
| 整个任务 | `agent_reply` 活动 | 关联到转换出的 Run，ID 为 `act-legacy-reply-<taskID>`；状态由任务状态投影，`summary` 给出「已完成 N/M 个阶段」等摘要。 |

`agent_reply` 的 `session_key` 取任务的主会话；没有时退到最近一条带会话的阶段运行，
这样「打开会话」仍然可用。

### WorkflowRun

- `stage_runs[1..n]` 每个阶段变成图里的一个节点，节点 ID 为 `stage-<stage_index>`，
  按阶段顺序线性串联。
- 节点类型：`role=user` → `human_gate`，`role=agent` → `agent`。
- Run ID 为 `run-legacy-<taskID>`，`triggered_by=legacy_import`。
- `graph_json` **内联保存**真实图快照；控制面里没有对应 workflow 也能完整渲染运行视图。
- `version` 只作来源标注：历史模板名能映射到已发布 workflow 时写入
  `workflow-new-feature:v1` / `workflow-debug:v1` / `workflow-blank:v1`；映射不上
  （自定义、backlog、空模板）留空，不谎报来源。

### 状态映射

| 阶段状态 | 节点状态 |
|---|---|
| `success` / `approved` | `success` |
| `fail` / `failed` | `fail` |
| `cancelled` / `rejected` | `cancelled` |
| `running` | `running` |
| `waiting_user` | `waiting_user` |
| 其它 / 空 | `pending` |

任务自身的 `status` 不改写。运行投影由节点状态计算；`legacy_import` Run 不参与启动
对账，避免把早已结束的历史节点重新跑一遍。

### 任务行

- `task_stages_json` 清空；
- `task_type` 置为 `conversation`；
- `current_stage_index` 归零；
- `updated_at` **不变**：迁移是后台数据整形，不能把所有历史任务的时间改成迁移时刻；
- `task_template_id/name` 作为冻结来源标签保留（Phase 3 的既定行为）。

清理完成后这几列对运行路径没有任何意义：它们只是旧库仍然存在时的兼容外壳，
不会被回填成新模型的一部分。

## 备份与保留

转换前会创建并写入两张备份表：

| 表 | 内容 |
|---|---|
| `tasks_backup_legacy_stages` | 转换前的完整 `tasks` 行。 |
| `stage_runs_backup_legacy` | 转换前的 `stage_runs` 行。 |

此外，原始 `stage_runs` 表**不删行**，作为只读审计证据保留。前端不再读取它。

## 迁移日志

默认写到任务库同目录的 `stages-to-conversation.migration.log`，每次运行追加一行：

```
convert at <UTC 时间>: seen=64 converted=64 skipped=0 already=0 activities=128 runs=64 node_runs=132 backed_up_tasks=64 backed_up_stage_runs=189 no_stages=0 running_nodes=1
```

`--dry-run` 的日志行会带 `dry-run` 标记。

## 已验证的转换结果（2026-11-12）

对生产库副本与 llmux 库副本分别执行：

| 数据库 | 任务 | 活动 | Run | NodeRun | 备份任务 | 备份阶段运行 | 说明 |
|---|---|---|---|---|---|---|---|
| mindfs | 64 | 128 | 64 | 132 | 64 | 189 | 34×「任务输入→方案→实现」、15×「任务输入→定位→修复」及变体。 |
| llmux | 15 | 30 | 15 | 30 | 15 | 42 | 13×「任务输入→方案→实现」、2×「任务输入→定位→修复」。 |

两个库第二次运行都是 `converted=0`，确认幂等。生产库在验证期间未被写入。

## 回滚

转换是**单向**的，不支持把 conversation 任务反向拆回 `stage_runs`。需要回退时：

1. 停止服务，备份当前已转换的数据库文件。
2. 从备份表恢复 `tasks`（或直接用迁移前的整库备份替换）。
3. 删除本次转换产生的 `task_activities` / `workflow_runs` / `workflow_node_runs` /
   `task_execution_engines` 行（ID 均带 `legacy` 前缀，可精确定位）。
4. 启动旧版本服务。

因为 `stage_runs` 原行一直在，恢复 `tasks.task_stages_json` 与 `task_type='stage'`
后旧版本即可继续按阶段模型读取。生产上线前应额外保留一份整库文件备份。

## 相关代码

| 文件 | 作用 |
|---|---|
| `server/internal/kanban/task_store_convert_stages.go` | 转换实现（事务、备份、幂等、日志）。 |
| `server/internal/kanban/task_store_migrate.go` | 启动时接线与 schema 迁移。 |
| `server/scripts/convert_stages_to_conversation.go` | 手动预演/转换脚本。 |
| `server/internal/kanban/task_store_convert_stages_test.go` | 成功转换、跳过新任务、legacy Run 不阻断新留言/恢复。 |
| `server/internal/kanban/activity_service.go` | Router：每轮留言决定走 workflow 还是直连回复。 |
| `server/internal/api/appcontext_activity.go` | 直连执行器 `RunDirectActivity`：复用任务主会话跑一轮普通对话。 |
| `server/internal/kanban/worktree_finish_turn.go` | 收尾回合（固定提示词）与收尾幂等锁。 |