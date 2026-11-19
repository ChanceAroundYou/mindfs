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
| `TestBriefTaskStatusFollowsRunProjection` | 任务行 `waiting_user` + 节点 `running` → 派生 `running`；暂停/终态覆盖；无 Run 保持原值 |

变异验证（逐个打红后还原）：

1. 关掉 salvage 分支 → `...KeepsRecordedActions` 红（决策永远等不到 applied）。
2. `notifyRouterTurnTimeout` 直接 return → 两条超时用例同时红。
3. `briefTaskStatus` 退回 `return task.Status` → brief 用例红。

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

## 下一步 TODO（看板归档，任务 #86 原始需求）

1. 「已结束」列新增第三类「归档」（现有：完成、取消）。
2. 终态任务超过 7 天自动归档。
3. 完成/取消的任务卡片在删除图标旁增加「归档」按钮，图标样式沿用右侧会话面板那款。
4. 归档联动：归档任务 → 自动归档相关会话（反向不成立）。
5. 归档的任务不在工作台显示。
6. **级联语义已定（用户裁定 A）**：只给会话打归档标记，**绝不删除后代会话与 JSONL**。
   现有 `ArchiveSession` 是破坏性的（`session_lifecycle.go:162-221`），必须新增一条
   「不删子树」的归档路径；现有调用方行为不变。
7. 默认口径：7 天从 `completed_at` 起算（空则回退 `updated_at`），只对
   `success` / `fail` / `cancelled` 生效，由后端低频扫描**真写** `archived_at` 并级联归档会话；
   取消归档只解任务自己的标记、不自动解会话；归档任务保留在看板「已结束 → 归档」子组，
   卡片上是「取消归档 + 删除」。
