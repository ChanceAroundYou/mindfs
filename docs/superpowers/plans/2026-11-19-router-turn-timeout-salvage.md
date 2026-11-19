# Router 回合失败不再丢动作 + 超时可见 + brief 状态不再陈旧

- 日期：2026-11-19
- 授权：用户直接指示「先修理 router 相关的，同时把我要你做的事情重新梳理出来，列为 todo」；
  修复范围经 AskUserQuestion 确认为「超时丢动作 + brief 陈旧性、超时丢动作 + 超时可见告知」；
  `router.model` 明确**保持 `ok`**（不切 sonnet）。
- Scope: G-NA

## 现场证据（看板任务 #86「看板归档」）

| 时间 | 事实 | 证据 |
|---|---|---|
| 11:33:32 | 用户发「我还是没有看到归档按钮，也没有看到超时归档机制」 | `task_activities` |
| 11:34:59 | 模型调 `router_ask_user`，工具**已返回** `{"kind":"ask_user","status":"recorded"}` | Router 转录 |
| 11:35:02 | 90s 死线命中，`persistTurnBlocks` 在已取消的 ctx 上失败 | `router_decisions` `rdec_960121…` `status=fallback`、`error="persist turn blocks: context deadline exceeded"`、`latency_ms=90015` |
| 11:35:20 | 回落旧路径，另起 `workflow-debug:v1` Run | `workflow_runs` `run-2b149d…` |

同一进程当天 4 次撞死线（18:40 / 19:12 / 19:35 / 20:18，全部 9001x ms），成功的回合耗时
52.6–81.0 s，余量只有 9–38 s。

三点根因：

1. **超时丢动作**：`runRouterTurn` 的错误分支直接 `finishFallback(nil)`——`routerTurnCollector`
   里已经 recorded 的动作被整轮丢弃。写工具返回 recorded 就是已发生的事实，回合后置失败
   不该把它抹掉。
2. **超时不可见**：用户只看到 90 秒后突然出现一条旧路径回复，无从分辨是 Router 超时、
   模型故意沉默，还是自己的话被吞了。
3. **brief 陈旧**：`briefRun.Status` 与 `briefActiveExecution.Status` 直接抄 `task.Status`。
   任务行由另一条投影写入，可能落后于 Run 的节点事实（实测任务行 `waiting_user`、节点
   却在 `running`）。brief 是 Router 唯一的世界快照，陈旧状态会直接变成错误决策。

## 实现

### 1. 失败回合保留已记录的动作（`router_service_recovery.go`）

- `runRouterTurn` 的错误分支改为 `finishTurnFailure(...)`，把 `collector.snapshot()` 交出去。
- `salvageRouteActions`：动作**照旧过 `validateRouterActions`**（记录下来的动作同样可能非法），
  通过则按正常 route 路径应用：先落 `applying`，再 `ApplyRouteActions`，最后
  `MarkDecisionStatus(applied, note)`；`note` 写明「回合未完成 + 已应用 N 个动作」。
  应用使用独立 `context.Background()`——回合的死线已经过去，落库不该再受它影响。
- 校验不通过才回落；救回动作后**绝不**回落（否则旧路径会另起一条 Run，把同一句话执行两遍）。
- `finishFallback` / `fallbackRoute` 一并移入该文件，`router_service.go` 从 500 行降到 456 行。

### 2. 超时可见告知

- 只在**超时**（`errors.Is(err, context.DeadlineExceeded)`，含 `%w` 包装）时写一条系统行；
  其它失败（CLI 崩溃、空回合）保持原样——回落本身已有旧路径回复，再插一行只是噪声。
- 行：`type=attention`、`role=system`、`status=completed`、ID `router-turn-timeout:<decisionID>`
  （`InsertOrIgnore`，重放/重启不会写出第二行），并 `broadcastWorkflowActivity` 实时推送。
- 文案区分两种结局：已救回动作 → 「超过 90 秒未完成，已保留并执行此前记录的 N 个动作」；
  未救回 → 「超过 90 秒未完成，这条留言已改由原执行路径继续处理」。

### 3. brief 状态跟随 Run 投影（`router_brief.go`）

- 新增 `briefTaskStatus(run, task)`：暂停 / 任务已终态（操作员拥有的状态）优先；
  否则取 Run 投影，但**两份证据都可能落后**——投影说在跑就采信投影；投影说空闲、
  任务行说在跑时采信任务行（宁可多报一次活动，也不要把本该转交的留言判成新开一轮）。
- `briefRun.Status`、`briefActiveExecution.Status`、`ActiveExecution` 的闸门全部改用它。
- 与 G-MZ 的 `workflowRunHasOpenNodes` 闸门叠加：已结算的 Run 仍是 `waiting_user`，
  但不会再被当成「有活动执行」而永久拒绝 `run_workflow` / `message_agent`。
- `briefRun` / `briefActiveExecution` 容忍 `store == nil`（纯函数测试用）。
- brief 的体积裁剪函数拆到 `router_brief_cap.go`（纯移动，守住 500 行硬线）。

## 测试与验证

`server/internal/kanban/router_timeout_salvage_test.go`：

| 测试 | 钉住什么 |
|---|---|
| `TestRouterRouteTimeoutKeepsRecordedActions` | 超时前 recorded 的 `say` 被应用、决策 applied、占位回复落正文、不回落、系统行说「已保留并执行」 |
| `TestRouterRouteTimeoutWithoutActionsNotifiesAndFallsBack` | 无动作的超时照旧回落并复用占位，同时有可见系统行 |
| `TestRouterRouteNonTimeoutFailureWritesNoNotice` | 非超时失败不写系统行（防止下一个人顺手放宽） |
| `TestRouterRouteNonTimeoutSalvageKeepsActionsWithoutTimeoutNotice` | 非超时失败（CLI 崩溃）也要救回已记录动作，但**不能**写「超过 90 秒」的假告知：救回与超时告知是两件事 |
| `TestBriefTaskStatusFollowsRunProjection` | 任务行 `waiting_user` + 节点 `running` → 派生 `running`；暂停/终态覆盖；无 Run 保持原值 |

变异验证（逐个打红后还原）：

1. 关掉 salvage 分支 → `...KeepsRecordedActions` 红（决策永远等不到 applied）。
2. `notifyRouterTurnTimeout` 直接 return → 两条超时用例同时红。
3. `briefTaskStatus` 退回 `return task.Status` → brief 用例红。
4. 救回分支的告知行退回无条件调用 → `...NonTimeoutSalvageKeepsActionsWithoutTimeoutNotice`
   红：CLI 崩溃被写成「调度回合超过 90 秒未完成」（2026-11-19 收尾复验发现并修掉）。

回归：`go test ./server/internal/kanban/ -count=1` 全绿（含既有的
`TestRouterDeliverToolRejectsWithoutSingleActiveNode`、`TestRouterRouteRefreshesQueuedBrief`、
`TestRouterRunningAgentMessageReachesRouter`、`TestRouterApplyRechecksStateAfterModel`
——它们正是「投影落后于任务行」方向的反向守卫）。

## 未做 / 明确排除

- **不切换 `router.model`**：用户明确要求保持 `ok`。
- **不改 90s 超时值**：超时后的行为已经正确，缩短/延长属调参，需单独评估。
- **不改节点间产出传递**（`WorkflowPromptPlaceholders` 只有
  `{previous_input}` / `{task_initial_input}` / `{task_number}`）：`workflow-debug` 的
  `locate` 读不到 `input` 的产出，这是「各节点各说各话」的结构性缺口，属产品决策。
- 不做「把已 recorded 的动作当作完整决策」：救回的动作仍逐条校验，非法的一样拒绝。

## 看板归档（任务 #86 原始需求）—— 已全部实现（2026-11-19，Scope: G-NB）

七条需求逐条落地，语义按用户裁定 A（级联只打标记、不删后代）：

| # | 需求 | 实现 | 钉住的测试 |
|---|---|---|---|
| 1 | 「已结束」列新增第三类「归档」 | `useAppKanbanColumns` 的 `ended.groups` 增加 `archived`（`tone: muted`）；完成/取消两组改用 `!task.archived_at` 过滤，三组之和 == 列头数字 | `web/tests/task-archive.test.mjs`「ended column splits archived tasks into their own third group」 |
| 2 | 终态任务超过 7 天自动归档 | `TaskArchiveAfter = 7d`；`ListTasksDueForArchive` 用 `COALESCE(NULLIF(completed_at,''), updated_at)` 取起算点、只认 success/fail/cancelled、已归档不重复返回；`SweepAutoArchivedTasks` **真写** `archived_at` 并级联；`StartArchiveSweeper` 启动跑一次 + 每小时 | `service_archive_test.go`：`TestListTasksDueForArchiveUsesCompletedAtFallsBackToUpdatedAt`、`TestSweepAutoArchivedTasksOnlyTouchesExpiredTerminalTasks` |
| 3 | 终态卡片在删除图标旁加「归档」按钮，图标沿用会话面板那款 | `TaskCardRows` 终态分支：未归档 = 收尾/回到待审核/归档/删除；复用 `components/session/sessionListShared` 的 `ArchiveIcon` | 同上「a terminal card offers archive next to delete」 |
| 4 | 归档联动：归档任务 → 自动归档相关会话 | `TaskRelatedSessionKeys` 汇总主会话 + 工作流节点会话 + 活动绑定会话 + Router 调度会话（去重）；`Service.ArchiveTask` 归档后经 `kanban.SessionArchiver` 端口级联 | `TestTaskRelatedSessionKeysCollectsAndDedupes`、`TestArchiveTaskCascadesMarksAndUnarchiveLeavesSessionsAlone` |
| 5 | 归档的任务不在工作台显示 | `ListTasksOptions.ExcludeArchived` 进 SQL（`archived_at = ''`），`Service.Overview` 打开；不放 SQL 层会让归档行先占掉 Limit 名额 | `TestOverviewExcludesArchivedTasks` |
| 6 | 级联只打标记、绝不删后代会话与 JSONL | 新增端口 `kanban.SessionArchiver`（结构上只有标记能力），api 侧 `NewTaskSessionArchiver` 只调 `session.Manager.SetArchived`；**没有**复用破坏性的 `usecase.ArchiveSession`，其调用方行为不变 | 同上级联用例 + `web` 侧「archived card …」 |
| 7 | 取消归档只解任务自己的标记；归档卡只留「取消归档 + 删除」 | `ArchiveTask(archived=false)` 只清 `archived_at`、不反解会话，并留 `archive_dismissed_at` 阻止下一拍自动归档把它收回去；`TaskCardRows` 归档态渲染取消归档 + 删除两个键 | `TestArchiveTaskCascadesMarksAndUnarchiveLeavesSessionsAlone`、`TestUnarchivedTaskIsNotAutoArchivedAgain`、`task-archive.test.mjs`「an archived card is reduced to unarchive + delete」 |

另外三条按交互查出来的加固（同一 Scope）：

- **归档只属于终态**：`SetTaskArchived` 对非终态任务返回 `ErrTaskNotArchivable`（HTTP 409）——
  在跑的 Run 被藏进归档组就是「看不见却还在跑」。
- **重开/恢复顺手清归档**：`UpdateTaskStatus` 的非终态分支与 `ReopenTask` 都会写
  `archived_at = ''`；否则并发会话新加的 `ReopenTask` 会让卡片「明明在跑却躺在归档组里」。
- **取消归档不被下一拍再收走**（并行验收会话发现的缺口，已并入本组）：自动归档的唯一判据是
  「`archived_at` 空 + `completed_at`/`updated_at` 够老」，而取消归档按设计不动时间戳 ——
  没有留痕的话，用户点完取消归档，下一个整点扫描会把同一张卡再收起来（「取消归档只维持一小时」）。
  新增 `archive_dismissed_at`：取消归档打戳、`ListTasksDueForArchive` 加 `archive_dismissed_at = ''`、
  显式归档或任务回到非终态时清空（留痕只约束「这一轮终态的取舍」，不能变成永久豁免）。

### 门禁与变异验证

- 归档组：`go test ./server/... ./cli/... -count=1` 全绿（含 `service_archive_test.go`、
  `task_archive_dismissal_test.go`）、`make test-web` 绿（typecheck + 242 文件 / 711 用例通过、6 跳过）。
- 变异验证（逐个打红后还原）：去掉 success 组的 `!archived_at` 过滤 → 分组用例红；
  去掉 `UpdateTaskStatus` 的 `archived_at = ''` → 重开用例红；起算点改回只用
  `updated_at` → 到期用例红；去掉 `ExcludeArchived` 条件 → 工作台用例红；
  去掉扫描条件 `archive_dismissed_at = ''` → `TestUnarchivedTaskIsNotAutoArchivedAgain` 红；
  重开/非终态不再清留痕 → `TestArchiveDismissalLifetime` 红。

### 边界

不删任何数据（任务卡/会话/JSONL 全留，删除仍走 `DELETE /api/tasks/{id}`）；不改
`usecase.ArchiveSession` 的破坏性语义；不做归档批量操作/保留期清理；不新增看板列。
