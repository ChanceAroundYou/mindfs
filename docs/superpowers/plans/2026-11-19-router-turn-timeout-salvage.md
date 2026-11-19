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

### 独立验收（2026-11-20，验收会话 mindfs-7b）

实现由另一条会话提交（`28af85d1`/`b3a9770c`/`9c18b6ac`/`82c6b4d5`/`07544178`），这里的
结论全部由验收会话在**未被实现方触碰**的干净 worktree（`/tmp/mindfs-verify`，`git worktree
add --detach HEAD`）与隔离实例上重新跑出来：

- **全量门禁（干净 worktree）**：`go test -count=1 ./...` 28 个包全绿、`tsc` clean、
  web 242 文件 / 711 用例通过 / 6 跳过、`make build` 成功（`v0.5.5-1121-27-g07544178`）、
  `make check-upstream` 2032 delta / 329 组全绿。共享工作区里 check-upstream 曾报 G-MN 锚点红，
  经同 HEAD 的干净 worktree 复跑排除为另一条会话在飞的未提交改动（live_compact）。
- **变异复验（验收侧独立做，逐个还原）**：去掉 `ListTasksDueForArchive` 的
  `archive_dismissed_at = ''` → `TestUnarchivedTaskIsNotAutoArchivedAgain` 红；
  去掉三处留痕清理 → `TestArchiveDismissalLifetime` 红。
- **隔离实例（端口 7453，专用 `XDG_CONFIG_HOME`，线上 7331 全程未碰）**：归档写
  `archived_at`、看板保留而工作台消失、取消归档恢复；把 `completed_at` 老化 8 天 → 重启
  触发启动扫描，日志 `[kanban/archive] auto-archived=1` 且库里 `archived_at` 真被写入；
  再取消归档 → 二次重启不再被抓走（库里 `archived_at=''`、`archive_dismissed_at` 有戳、
  `completed_at` 未被改动）。
- **浏览器端到端**：新增 `e2e/test_kanban_archive.py`（隔离后端 + Playwright，跑两遍均
  PASS，单遍 ~13 s）——归档键与删除键同排、点归档后卡片落进「已结束 → 归档」、工作台
  看不到、取消归档回到「完成」子组。**注**：用例在登录后显式 reload 一次，绕开一个与
  本功能无关的首屏竞态（登录后 `protectedAPIReady()` 仍为 false，看板/工作台加载 effect
  被静默挡掉且不再重试：首屏看板四列全 0、`/api/tasks?root=` 一次都不发，reload 后正常）。
  这是既存问题（G-NB 只改动 8 个前端文件，未触 init/loader），已在小节里写明，未顺手修。

### 部署（2026-11-20）

`deploy-all.sh` 从 `HEAD` 干净 worktree 构建（共享工作区当时有 live-compact 会话的在飞文件，
不能编进二进制），四端同版本 **`v0.5.5-1121-31-g9e29cdd2`**；`origin/main` 已推到 `9e29cdd2`
（连同本次之前积压的全部计划提交）。中途另一会话提交 `9e29cdd2`，首轮构建因此挂上 `-dirty`
且源码落后一个提交，同步后重跑一轮，最终四端都是干净版本号。独立核对：WSL/fn
`mindfs --version` 逐字一致、`systemctl --user is-active`、`/mindfs/health` 200；pc-win
PID 19820、health 200；本机二进制与 `/` 引用的 bundle 均为刚装的那版，system 单元仍在跑旧
进程（exe 已 deleted），待用户 `sudo systemctl restart mindfs`。

**复跑与现场复核（2026-11-20 01:02–01:10）**：首轮本会话的 `deploy-all` 在 WSL 打包处报
`tar: agents.json: Cannot open`（同一时刻另一会话也在部署，属并发抢跑）；随后单独复跑
`deploy-wsl.sh` / `deploy-fn.sh` / `deploy-pcwin.sh` 全部通过，三端仍是
`v0.5.5-1121-31-g9e29cdd2`（WSL/fn `is-active` + health 200，pc-win 当前 PID 38800 + health 200）。
本机 `curl /` 返回的 bundle 与刚装的 `web/dist` 不一致 —— 运行中仍是 11-19 17:33 启动的旧进程
（exe deleted），重启后消解。

**抬版 `v0.5.5-1200`（2026-11-20 01:2x）**：用户要求把版本号直接抬到 1200。tag `v0.5.5-1200`
打在 `fee35758`（当时 HEAD），已推 origin（安卓 APK 也从该 tag 出包，见安卓计划）。并发会话
在部署期间继续提交，`deploy-all` 的分支跟随两次拿到 `v0.5.5-1200-N-g…-dirty`，改为
`git worktree add --detach fee35758` 钉死提交：门禁（`go test ./...`、`tsc`、Vitest 712 通过）
→ `make build`/`make install` → `deploy-wsl.sh` / `deploy-fn.sh` / `deploy-pcwin.sh` 显式传
`v0.5.5-1200`。独立核对：四端 `mindfs --version` 逐字一致 `v0.5.5-1200`，WSL/fn `is-active` +
`/mindfs/health` 200，pc-win PID 47836 + health 200，本机 `/` 的 bundle 与刚装的一致；本机
system 单元仍是旧进程，待用户 `sudo systemctl restart mindfs`。


## 追加轮（2026-11-20）：归档任务后侧栏会话必须一起消失

**用户现场**：「现在的归档还是失败，把任务归档了，会话还在原地。」

**根因（两步，缺一不可）**：

1. **级联范围不全**：`TaskRelatedSessionKeys` 只汇总「列上有 key」的会话（任务主会话 /
   节点会话 / Run 共享会话 / 活动会话 / Router 会话）。对话式任务里挂在任务下、但没写进
   这些列的会话（以及 subagent 派生的子会话，`task_id` 为空）收不进来 —— 归档后它们仍是
   `archived_at = NULL`，列表接口照常返回。
2. **界面没有收敛通道**：`handleKanbanTaskArchive` 只广播 `task.updated`，前端那一下只重拉
   看板；会话列表没有任何东西能让它知道「这些会话刚被归档」。结果是库里已归档（老路径覆盖到
   的那部分）、侧栏还挂着「看板归档 / #86」—— 用户看到的就是「归档了任务，会话没归档」。

**修法**：

- `kanban.SessionArchiver` 入参改成 `(ctx, rootID, taskID, seeds)`：种子为空也要调用，
  实现方还要按 `taskID` 找齐名下会话。
- api 层 `taskSessionArchiver` 用纯函数 `taskArchiveTargetKeys` 算目标集：
  **种子 ∪ `task_id` 命中 ∪ 按 `parent_session_key` 递归到的全部后代**（去重），逐条
  `SetArchived(true)`（单个 key 失败跳过，尽力而为）。
- 落库后逐条广播 `session.meta.updated`，`sessionMetaUpdatedPayload` 新增 `archived_at`；
  前端 `realtimeWsSessionMeta.ts` 命中 `archived_at` 就把该会话从 `sessionCacheRef` 与主列表
  摘掉、`bumpCacheVersion`、必要时重拉多项目分组，并 **提前 return**（归档不改 `updated_at`，
  走增量刷新既拉不回来也删不掉）。未归档帧照常合并 + 刷新，取消归档后会话能回来。

**测试（都做了变异验证）**：

- `server/internal/api/http_tasks_archive_test.go`：纯函数三条（范围/去重/空输入）+
  真实 `session.Manager` 落库一条（`task_id` / 种子 / 子孙全部 `archived_at` 非空，无关任务不误伤）+
  广播载荷带 `archived_at` 一条。变异：去掉 `task_id` 匹配 → 红；去掉父子 BFS → 红。
- `web/tests/task-archive-prunes-sessions.test.mjs`：transpile + vm 跑**真 handler** 两条
  （归档帧摘缓存/摘列表/不触发增量刷新；未归档帧照常合并并刷新）。变异：把 `archived_at`
  分支关掉 → 红。

**门禁（2026-11-20）**：`go test -count=1 ./...` 全绿；web 247 文件 / 743 通过 / 6 跳过；
`tsc -b --noEmit` clean。登记并入 G-NB（同一组定制，未新开 ID）。

**提交与安装（2026-11-20）**：提交 `07424b66` 并推到 `origin/main`；`make check-upstream`
2057 delta / 335 组全绿。因共享工作区有另一条会话的在飞文件，从 `git worktree add --detach
07424b66` 的干净树构建安装，`~/.local/bin/mindfs --version` = `v0.5.5-1200-21-g07424b66`，
静态资源同步到 `~/.local/share/mindfs/web/`。

**未生效部分**：本机 system 单元仍在跑 02:55 启动的旧进程（PID 990089，`/proc/990089/exe`
指向 `(deleted)`），要看到侧栏真的消失，需用户执行 `sudo systemctl restart mindfs`。
WSL / fn / pc-win 未分发（下次 `deploy-all` 自然带上这个提交）。
