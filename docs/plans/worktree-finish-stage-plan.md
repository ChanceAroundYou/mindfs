# 收尾（worktree 收工）做成一个真正的流水线阶段

## Context

mindfs 的看板任务可以跑在 git linked worktree 里。外部脚本 `wt-finish.sh` 的 `run` 流程是
**`check → commit → merge → cleanup → repoint`**（脚本 usage 第 62 行原话）。其中「commit + merge
到 main」这一步，脚本是**让 agent 自己去干**的（agent 有未提交改动时自己 commit、遇到冲突就
`die` 并把解法打印给人），只有「拆 worktree / 删分支」这种确定性的清场动作才由脚本自己做。

现在服务端只内建了**清场那一半**（`FinishTaskWorktree`：合并 → 拆 worktree → 删分支 → 清归属），
而且 `commit` 那一步是刻意不做的 —— `worktree_finish.go:17-21` 的注释写着「自动 commit 等于把
agent 的半成品中间态写进历史，走独立的 commit 接口」。

结果就是用户点一下「收尾」，服务端直接拿 agent 可能根本没提交过的工作树去 merge：
- agent 的活还没 commit → merge 拿不到任何东西，等于白点一次；
- 有未提交改动 → `git worktree remove` 拒绝，卡在「合并已完成但拆不掉」。

**目标**：点「收尾」= 进入一个**固定的收尾阶段**，像触发 worktree-finish skill 那样先让 agent
自己 commit / merge；不顺利就停下来问你；**只有这一段成功**了，才自动调用现有的收尾 + repoint。

顺带修掉本轮提出的另四项界面问题（确认弹窗、转圈、非红 toast、按钮按阶段给法）。

### 已确认的口径（用户拍板）

- 开始执行要**弹窗确认**；正常完成走 **info toast**；错误走 **error toast**。
- 「还有下一步阶段」由**各组件用现有数据自己判断**（看板卡片只有 `stages + current_stage_index`，
  详情面板有 `stage_runs`），**不改后端加字段**。
- 收尾时**自动搬会话**（repoint 并入收尾）。
- 「自动 commit 怎么办」→ 不是服务端自动 commit，而是**交给 agent 在收尾阶段里做**（本方案）。

---

## 关键约束（读代码确认过的，不是推测）

| 事实 | 位置 | 对方案的影响 |
|---|---|---|
| agent 段成功 = 输出 `[mindfs-stage-done:N]` 标记，服务端据此判成功 | `service.go:40-68`、`appcontext.go:398` | 收尾阶段的「成功」沿用同一套，agent 自己喊 done |
| agent 段受阻 = 输出 `[mindfs-stage-blocked:N 原因]` | `matchStageOutcome` `service.go:59-68` | 冲突时 agent 走这条 → 任务停在 `waiting_user`，**不收尾** |
| `AddStage` 已完整实现「加一段 + 准入锁 + 推进 + 起 agent」 | `service.go:514-570` | 收尾阶段直接复用，**不新写一遍** |
| `AddStage` 要求 `task.Status == StatusWaitingUser` | `service.go:524` | 只在「待审核」态可发起；其他态要明确报错 |
| agent 段成功后 `executeTask` 回到 `run.Status == StageStatusSuccess` 分支 | `service.go:1308-1320` | **这就是 hook 点**：失败/受阻会走 `waitForUser`（1321）而不是这里 |
| `AutoAdvance=false` 时成功 → `waitForUser("agent_stage_done")` | `service.go:1321` | 收尾阶段设 `AutoAdvance=false`，成功即停在待审核 |
| `task_stages_json` 是整段 JSON blob | `task_store.go:66-90`、`590/598` | `StageTemplate` 加字段**不需要迁移** |
| `kanban.Service` **够不到** `usecase.Service` | `appcontext.go:42-71` 无该字段 | repoint 编排只能在 **api 层**做 |
| api 层现成构造：`(&HTTPHandler).service()` = `&usecase.Service{Registry: h.AppContext}` | `http.go:75-77` | 不必给 kanban 加依赖 |
| `RepointSession` 第 1 步是 `pool.Close`，会杀掉活着的 agent 进程 | `session_repoint.go:103-105` | 必须在 agent 收工**之后**调；脚本正是为此 `--detach --delay 30` |
| `RepointSession` 已经清了任务侧归属 | `session_repoint.go:137` `clearTaskWorktree` | 收尾不用重复清 |
| 分支已是 main 祖先 → merge 短路返回 `Merged:false` | `gitview/worktree_finish.go:170-174` | agent 自己 merge 过之后，服务端 merge 变幂等空操作 |
| `Next` 遇到「最后一段 + waiting_user」= **直接完成整个任务** | `service.go:783-797` | 「没有下一步阶段就别给执行键」是对的 |
| `confirmDialog` 已存在，带 `danger`，`await` 语义 | `services/dialog.ts:94`、`NodeManagerPanel.tsx:243` | 确认弹窗是**一行复用** |
| `reportError` 已支持 `severity` | `services/error.ts:315-322` | 非红 toast 是**直接复用** |
| Toast `info` 档用 `var(--accent-color)`、3 秒自动消失 | `components/Toast.tsx:100-106`、`21` | 「非红 + 会消失」= 现成组件 |

---

## 方案

### 一、服务端：收尾阶段（新建 `server/internal/kanban/worktree_finish_stage.go`）

**1. 阶段身份：给 `StageTemplate` 加一个 `Kind` 字段**

```go
// StageTemplate 加：
Kind string `json:"kind,omitempty"`   // "" = 普通阶段；"worktree_finish" = 收尾阶段
```

- `task_stages_json` 是 JSON blob（`task_store.go:590`），加字段零迁移、零 SQL 改动。
- 不用「阶段名/提示词里塞标记」：用户能在看板上改阶段名（`UpdateStage`），标记会被抹掉。
- 不加 aux_flag：aux 是任务级，标记必须跟着**段**走（同一任务可能重试收尾）。

**2. 收尾阶段的提示词模板**（新增常量，`web` 不参与，agent 侧只认 prompt）

内容对齐 `wt-finish.sh` 的 `cmd_check`/`cmd_commit`/`cmd_merge`，要点：

- 先 `git status` 看清楚有什么要提交，**显式列出要提交的文件路径**（脚本 `:170` 强制不用 `-A`）；
- 拒绝 `node_modules/`、`dist/`、`build/`、`*.log`、`*.tmp`、`*.swp`、`.env*`（脚本 `:176-180`）；
- `git add -- <路径>` + `git commit -m "<type>: <描述>"`（Conventional Commits，项目规范）；
- `git merge` 到 main（脚本 `:215` 先 `checkout main`）；
- **合并撞上冲突时不要自作主张**：停在那儿，说明哪些文件冲突、给出你的取舍判断，
  用 `[mindfs-stage-blocked:N 原因]` 收尾 → 任务转 `waiting_user` 等用户；
- 全部顺利 → `[mindfs-stage-done:N]`，**到此为止，不要自己去删 worktree 或分支**（那是服务端的事）。

`Agent`/`Model` 取该任务上一个 agent 段的值（`inheritAgentStage` 的现成逻辑，`appTask.ts:28` 镜像），
没上一段就用 `DEFAULT_TASK_AGENT`/`DEFAULT_TASK_MODEL`。`SessionReusePolicy` 用 `task_main`
（默认就是它，`template_store.go:374`），这样收尾阶段复用任务主会话 —— **这正是要 repoint 的那个会话**。

**3. `Service.BeginFinishWorktree(ctx, BeginFinishInput) (TaskDetail, error)`**

- 前置校验（**复用现成判据，别重写**）：
  - `task.CreateWorktree` 否则报「该任务没有 worktree」；
  - `task.WorktreePath` 非空且 `!worktreeMissing`（判据用 `worktreeDirUsable`，同 `service.go:1327`）；
  - `!isTerminalStatus(task.Status)`；
  - `task.Status == StatusWaitingUser`（`AddStage` 的前提，`service.go:524`），
    不满足就报「任务正在执行中，先停止再收尾」；
  - **不许已经有收尾阶段**（扫 `task.Stages` 找 `Kind == "worktree_finish"`），否则报「已在收尾流程中」——
    否则用户反复点会堆出一串收尾段。
- 复用 `AddStage`（`service.go:514`）追加收尾阶段并起 agent。**不重写这段逻辑**。

**4. Hook：收尾阶段成功 → 触发清场 + repoint**

`executeTask` 里 `run.Status == StageStatusSuccess` 分支（`service.go:1308-1320`）之后插一段：
识别当前段 `Kind == "worktree_finish"` → 调一个**回调**。

**编排不能放在 kanban 里**（够不到 `usecase.Service`），所以：

```go
// kanban.Service 加一个可选回调字段（不加进 Runner 接口 —— Runner 有两个测试 fake，
// 加方法会连带改两处；而且这是「完成后的动作」，不是造 agent 的能力）
StageSucceeded func(rootID, taskID string)   // 异步触发，绝不能阻塞 executeTask
```

api 层在装配时挂上：`StageSucceeded = h.scheduleWorktreeFinish` → 里面
`go h.service().FinishWorktreeAndRepoint(rootID, taskID)`。

**必须是异步的**：`pool.Close` + git merge/remove 要好几秒，阻塞在 `executeTask` 里会把
`RunTask` 的 goroutine 占住，而且 `acquireTaskFinish` 与 `taskRun` 会互相卡。

### 二、api 层：`FinishWorktreeAndRepoint`

```go
// server/internal/api/http_tasks_finish_orchestrate.go（新文件）
func (h *HTTPHandler) FinishWorktreeAndRepoint(rootID, taskID string) FinishWorktreeResult
```

**顺序不可换**（这是整个方案最容易写错的地方）：

1. **先 repoint**：`RepointSession(rootID, key=task.MainSessionKey)`。
   必须早于拆目录 —— 否则就制造出「转录还在已删 slug 目录、`related_worktree` 还钉着、
   在 mindfs 里再也聊不了」这个 wt-finish 花大力气要消灭的状态（脚本把 repoint 放最后，
   是因为它由 agent 自己触发、掐断自己；服务端在 agent 收工后调，安全性更高）。
   - 会话不在 worktree 上 → repoint 返回 `"session is not bound to a worktree"`，
      按**幂等跳过**处理（脚本 `cmd_repoint_child` 也是这么分的：`_st 0 "本来就不在 worktree 上"`）。
2. **再收尾**：`svc.FinishTaskWorktree(...)`，用现成的一切（冲突 → 409、已达祖先 → 幂等、
   残留目录只列不删）。`task.MainSessionKey` 会因为 repoint 的 `clearTaskWorktree`
   （`session_repoint.go:137`）被清掉，但**清完再收尾反而正好** —— 收尾第 4 步
   `ClearWorktreeRefs`（`worktree_finish.go:214`）本来就是干这个的，幂等。
3. repoint 失败**不阻断**收尾：会话搬不搬得动和 worktree 该不该拆是两件事，
   脚本也是这个口径（`cmd_repoint_child` 里 warn 之后 `_st 1` 但 `return 0`）。
   把 repoint 的错误塞进结果的 `session_warning` 字段，前端提示但不弹红框。

### 三、路由与前端

**服务端**：`http.go` 加 `r.Post("/api/tasks/{id}/begin-finish", ...)` → `handleKanbanTaskBeginFinish`
（`http_tasks.go`），口径与同文件其它动词一致：409 给「已在收尾 / 正在执行」这类需要人处理的状态，
400 给请求本身不成立的。**保留** 456 行原有的 `finish-worktree` 路由不动 —— 它仍是
「跳过 agent 阶段直接清场」的后门，脚本等价路径要用它。

**前端**（`web/src/`）：

| 项 | 改动 | 复用 |
|---|---|---|
| (b) 确认弹窗 | 卡片/面板点收尾先 `await confirmDialog({...})` | `services/dialog.ts:94` |
| (c) 转圈 | 起收尾阶段期间把按钮换成 spinner 并 disable | 现有 `TaskQueuedSpinnerIcon` |
| (d) 徽标 | 四档徽标已落地（`3d7ed4b`），收尾阶段跑起来时补一个「收尾中」态 | `taskIcons.tsx` 的 `WorktreeTagState` |
| (e) toast | 成功 `reportError(code, msg, {severity:"info"})`；失败 `severity:"error"` | `services/error.ts:315`、`Toast.tsx:100` |
| (f) 按钮给法 | 见下 | `appTask.ts` 的纯函数 |

**(f) 的具体判据**（用户选了「各自用现有数据判断」）：

- 看板卡片（`TaskCardRows.tsx`，看板和工作台共用一份，改一处两边都生效）：
  - `const hasLaterStage = task.current_stage_index < (task.stages?.length ?? 0) - 1`
  - `canComplete = !terminal && !hasLaterStage`（还有下一段就不给「完成」）
  - `showAdvance = !terminal && !stageRunning && hasLaterStage`（没有下一段就不给「执行」）
- 详情面板（`TaskDetailPanel.tsx`）：已有 `runnableStageIndex = nextRunnableStageIndex(...)`
  和 `advanceable = canAdvanceFromCurrentStage(...)`（`:140-141`），直接用它们，
  比卡片的粗判据更准（有 `stage_runs`）。

**收尾阶段跑起来时**：`canFinishWorktree` / `canComplete` / `showAdvance` 全部为 false
（正在收尾，唯一该给的是状态），避免半路再点。

### 四、测试

**新增 `web/tests/worktree-finish-stage.test.mjs`**，钉源码形态（沿用 `worktree-finish.test.mjs` 的
「先 slice 出函数体再断言」写法 —— 整文件跑正则会恒真，这个坑上一轮踩过）：

- `begin-finish` 路由存在且 `protectedEndpoint` 包着；
- `StageTemplate` 有 `Kind` 字段且 `json:"kind,omitempty"`；
- 收尾提示词常量里含「显式列出路径」「拒绝 node_modules/dist/.env」两条脚本同源约束；
- hook 挂在 `run.Status == StageStatusSuccess` 分支之后，不是 `runAgentStage` 里。

**新增 Go 测试** `server/internal/kanban/worktree_finish_stage_test.go`：
- 真 store：`BeginFinishWorktree` 追加 `Kind=="worktree_finish"` 的段并置 running；
- 已有收尾段时拒绝；
- 终态 / 无 worktree / worktree_missing 时拒绝；
- `run.Status == success` 且段是收尾段时，`StageSucceeded` 被调用一次（用 fakeRunner + 计次）。

**端到端**（部署后，需用户执行本机 restart）：
1. 造一个有未提交改动的任务 → 点收尾 → 弹窗 → 确认 → 卡片转圈、徽标转「收尾中」；
2. agent 阶段跑起来：应看到它在 worktree 里 commit + merge；
3. 制造冲突 → agent 停住问 → 任务转 `waiting_user`，**worktree 和分支都还在**（什么都没丢）；
4. 顺利走完 → info toast → worktree 拆掉、branch 删掉、徽标转灰「已收尾」、
   会话能在 mindfs 里继续聊。

## 风险

- **`pool.Close` 的时机**：repoint 会杀 agent 进程。放在「阶段 success 已落库」之后是安全的
  （`runAgentStage` 已返回），但必须**异步**，不能挡在 `executeTask` 里。
- **agent 可能不听话**：提示词让 agent 自己 merge，它可能只 commit 不 merge、或者报告 done
  但没真合。服务端清场那步的 `MergeBranch` 仍会兜底（已是祖先就幂等短路），
  没合成就正常合 —— **服务端能力不依赖 agent 老实**，这是有意的双保险。
- **收尾段会留在流水里**：段一旦加上去就删不掉（用户可以删，但默认留着）。这是流水快照的
  既有语义（`CLAUDE.md` 第 13 条），不是新问题。
- **`StageSucceeded` 回调在 kanban 里是裸函数字段**：没装配就是 nil，nil 检查必须有，
  否则测试里的 fake 会 panic。

## 不做

- 不删 `finish-worktree` 路由（跳阶段直接清场的后门）
- 不做服务端自动 commit（commit 归收尾阶段的 agent，正如脚本的分工）
- 不给 `Runner` 接口加方法（两个 fake 会连带改；用可选回调字段）
- 不改 `finishTask` 保留 `worktree_path` 的既有语义
---

## 归档注记（2026-10-02）

**status: implemented** —— 本方案已全部落地，但**实现与下文命名不同**，按本文件的文件名去找会找不到：

| 本方案写的 | 实际落地为 |
|---|---|
| `server/internal/api/http_tasks_finish_orchestrate.go` | `server/internal/api/http_tasks_finish_teardown.go` |
| `kanban.Service.StageSucceeded` 回调字段 | `SetFinishStageFinished(FinishStageFinishedFunc)`，命名更贴切 |
| `web/tests/worktree-finish-stage.test.mjs` | 拆成 `worktree-finish.test.mjs`（接线/i18n）+ `worktree-badge.test.mjs`（徽标四档） |

**保留下文的原因**：它记录了两个后来没写进代码注释的决策理由。

1. **收尾回调必须异步**（已落地：`worktree_finish_stage.go` 的 `notifyFinishStageOutcome`）。
   回调要跑 git merge / worktree remove / pool.Close，几秒起步；同步挡住会让 `RunTask` 的
   goroutine 一直占着 `taskRun` 标记，之后任何收尾都撞上「任务正在执行中」。
2. **编排必须放 api 层**：kanban 的 `Service` 够不到 `usecase.Service`，而 repoint 在 usecase。
   所以 kanban 只暴露一个可选回调字段，具体清场逻辑由 api 层装配。

**方案里没写、后来才补上的**：

- 服务端 merge 是**兜底**不是依赖 —— agent 自己 merge 过之后，已是祖先就幂等短路。
  服务端能力不依赖 agent 老实，这是有意的双保险。
- `repoint` **不再清任务侧归属**（`736abd6`）。repoint 的语义是「这个会话搬回主 checkout」，
  `ClearTaskWorktree` 的语义是「这个任务的活收工了」。焊死两者会让一次纯搬会话的 repoint
  把还在用的任务目录记录清空，卡片随后命中「路径为空 + missing=false」渲染成「已收尾」——
  一个从未收过尾的任务声称收工了。
- 转录**已经在主 slug** 时不需要 repoint（2026-10-02 实测，见 CLAUDE.md 第 14 条）。
