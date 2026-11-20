# #86 卡死：unknown 算已收尾 + 启动对账补投影 + Router 读会话（router-unknown-settled-and-read-session）

- 日期：2026-11-20
- 状态：三轮均已实现、门禁绿、已推送 origin main（`ea896ab2`、`ddbf7238`、`53e38632`、
  `7df7279a`、`191e1124`，第三轮 `a0060a80`）；第二轮隔离验收（端口 7438，真实模型）全部通过；
  已部署四端 `v0.5.5-1200-52-gb61b81d8`（2026-11-20 14:14，本机二进制已装，待用户重启 system 单元）
- 上游：无（Router 与工作流运行时是本 fork 自有能力）
- 定制登记：`Scope: G-NJ`
- 上游计划：[Router 收尾死锁](2026-11-19-router-settled-run-livelock.md)（同一条闸门的上一轮修复）

## 背景

用户报任务 #86「看板归档」执行时卡死，要求先分析、合理放宽限制、不确定的先讨论。
随后逐条裁定：

> 1. unkown可以算已经收尾，但是最好是不要出unkown　我们让router有读取会话的权限就好了啊
> 2. 可以
> 3. 给router专门设置一个工具，他有全部读取的权力，但是一般只能读取到最后一个文本，可以通过offset读到前面

## 现场证据（只读复制线上库）

- 上一轮 Router 回合在 11:35 撞 90s 死线，回落旧路径，建 Run `run-61f8d92ace189e15`。
- 该 Run 只有一个 `execute` 节点，NodeRun 在进程重启时被标 `unknown`
  （`startup_ambiguous_side_effect`），但 `workflow_runs.node_states_json` 仍是空。
- brief 因此挂上 `active_execution: {current_nodes: []}`，`validateRouterActions` 永久拒绝
  `run_workflow` / `message_agent`；`deliver_to_active_run` 要 1 个活动节点、`answer_waiting_node`
  要 `waiting_user` 节点，都不适用 —— Router 只剩 say/ask_user。
- 时间线显示 0 个节点（投影没写），回复活动 `act_bde1960c77dbe52e` 从 18:29 起永远 `running`。

## 根因

1. **投影只在节点终态写**。`transitionWorkflowNodeRun` 把 running 改成 unknown 时不写
   `node_states_json`，于是「空投影」被 `workflowRunHasOpenNodes` 当成「刚建出来、driver 还没
   claim 的 Run」，永远未收尾。
2. **unknown 在闸门上没有归宿**。它是「重启时结果不明」，既不该重放（`EngineReadyNodes`），
   也不该被当成终态写库（`workflowNodeStateTerminal`，被取代时要冻结成 cancelled），但它确实
   不需要任何人再等它动 —— 闸门缺第三套判断。
3. **Router 没有读会话的能力**。节点结果写在会话转录里，唯一的读工具 `inspect_run` 只看投影；
   投影空白时它没有任何办法弄清上一轮做了什么，只能反复让用户「先结算 Run」。
4. **回复活动没人结算**。回合的收尾在 agent 那一侧，进程重启把它带走了。

## 修复

### P0 闸门与启动对账

- `workflowNodeStateSettled`（`router_brief_open_nodes.go`）：success/skipped/fail/cancelled/**unknown**
  都算已收尾。文件头写明三套判断的分工：`workflowNodeStateTerminal`（能不能冻结）、
  `EngineReadyNodes`（能不能重放）、本函数（还会不会自己动）。
- `workflowRunHasOpenNodes(run, rows)`：行与投影一起看，优先级 open 行 > 投影 > 已结行 >
  两边都没有=未收尾。行优先保证重试安全（attempt+1 排队时投影还写着上一次的 fail）。
- `buildSituationBrief` 只取一次 NodeRun 行并传给闸门与 `briefRun`；`briefRun` 增加 `unknown`
  桶，并在投影缺节点时用行的最新状态补齐（Loop 内部行、非最高 attempt 跳过）。
- `recoverWorkflowRunsOnStartup`：本轮有 unknown 行、`!wake && !paused` 时调
  `projectWorkflowRunStopped` —— 5 次 revision-CAS，unknown 行写进投影（保留 NodeOutputs），
  其余未收尾节点冻结成 cancelled，落 `node_unknown` 事件；再调 `settleWorkflowReplyUnknown`
  把 running/pending 的回复活动结算成 cancelled 并留一句可读说明。

### P1 Router 读会话工具

- `router_read_session`（Class `RouterToolRead`）：默认读「本任务最近一次节点执行的会话」，
  缺省只给最后一段文本，`offset` 往前翻，`limit` ≤ 5；单块 8KB、整次 16KB 上限。
- 白名单来自 `TaskRelatedSessionKeys`（主会话 + 节点会话 + Run 共享会话 + 活动会话 + 子会话），
  外来 key 一律拒绝；只返回文本块，工具调用/推理/计划块不进 Router 上下文。
- 端口 `RouterTranscriptReader` 由 `api` 层用 `session.Manager.ReadBlocks` 实现，
  `server/app/workspace_build.go` 装配期注入；nil 时工具显式报错，不假装能读。
- brief 的 `PolicyHints` 写明 unknown 的含义与该工具的翻页方式。

## 第二轮（2026-11-20）：历史回填 + 合并不明的 unknown

用户六条裁定：①补回填 ②重启后能不能自动检查恢复、Router 要学会管理自己的任务
③「操作员完成时不冻结」保持 ④按建议区分 merge 与重启产的 unknown ⑤可以部署
⑥可以测试。③ 本轮不改代码（保持现状），②的答复见 §重启后自动检查与恢复。

### 缺口一：历史 Run 永远不会被回填

第一轮把回填放在 `if len(open) > 0` 里，而 `ListOpenWorkflowNodeRuns` 只选
`queued`/`running`，外层还 `continue` 掉终态任务 —— #86 这种已经停下、任务后来
还收尾/归档的 Run 永远扫不到：投影 0 个节点、回复活动一直转圈。

修复（`workflow_recovery.go`）：启动对账末尾按 `workflow_node_runs.status='unknown'`
反查所有 Run（新 store 方法 `ListWorkflowRunIDsWithUnknownNodes`），逐个
`backfillWorkflowRunStopped`：

- 只补**缺失**的投影键，已有终态证据一律不动（循环/合并只写投影、没有行的节点不会被冻
  结成 cancelled）；
- 最新尝试行是终态或 `waiting_user` → 照写；仍停在 queued/running（已无人推进）或从没
  跑过 → cancelled；
- 正在继续的一轮（有 open 行或本轮被重排/续合并）跳过：它们的回复属于还在跑的回合；
- 投影补完后结算仍 running/pending 的回复活动（复用 `settleWorkflowReplyUnknown`）；
- 幂等：投影齐了不再写事件，活动已终态不再改。

### 缺口二：merge 产的 unknown 与重启产的 unknown 混为一谈

第一轮把所有 unknown 都算已收尾。独立 worktree 的节点在「重启续合并」阶段失败时，分支
可能只合了一半 —— 放行新一轮会让节点在状态不明的主树上继续写。

修复（`router_brief_open_nodes.go`）：

- 闸门按**每节点最新 attempt** 判（`workflowLatestTopLevelNodeRuns`），Loop 内部行不
  参与 Run 级判断；最新行是终态就以行为准（投影即使停在 running 也只是没写完的旧值）。
- `workflowUnknownBlocksRelease`：unknown + 独立树 + 输出带 `worktree_error` → 继续挡。
  判别依据不是事件账本：续合并路径转 unknown 前一定写 `worktree_error`，而重启标 unknown
  的 transition 会把 output 整体换成 `{"reason": ...}`，两种形状不会混。
- 同节点重试成功后旧 attempt 的合并不明不再挡（最新 attempt 是成功）。

## 重启后自动检查与恢复（裁定 ②的答复）

启动时 `RecoverOnStartupForRoot` 已做：legacy outbox 对账 + 工作流 NodeRun 对账
（queued 重排 / running 无会话重排 / 探针证明可恢复才重排，其余标 unknown）+
第一轮的投影回填与回复结算 + 第二轮的历史回填 + 可恢复时唤醒 driver + 给 Router 写
一条 `startup_reconcile` 事件。

它**故意不做**的：重放结果不明的回合（`SessionStateProbeForRoot` 恒
`resumable=false`）、Router 自动 cancel/retry（P2 排除）。

Router 侧的「自己管理任务」现有能力：`inspect_run`（投影 + 节点 + 事件）+
`router_read_session`（默认最后一段文本、`offset` 往前翻）+ brief 里的
`latest_run.node_summary.unknown` 与 `startup_reconcile` 事件。所以重启后它可以自己
查清上一轮做到哪、结果是什么，再决定新开一轮还是先问用户；它不能自己重放或取消节点。

## 验收

隔离/本地验证（不碰生产 7331）：

- 新增 15 条测试：`router_brief_settled_test.go`（6）、`workflow_recovery_unknown_settled_test.go`（3）、
  `router_read_session_test.go`（7，含默认最新块、offset 翻页、limit 新到旧、外来会话拒绝、
  未装配端口报错、超大块截断）。
- 五处变异各自打红对应用例：去掉行的参与（只剩投影）/ 把 unknown 从 settled 拿掉 /
  不写投影 / 不结算活动 / offset 不生效。
- 第二轮新增 5 条测试：`TestSituationBriefMergeUnknownNodeRunStillBlocksNewExecution`、
  `TestSituationBriefMergeUnknownSupersededByRetryAllowsNewExecution`、
  `TestStartupRecoveryBackfillsHistoricalRunProjection`（含幂等）、
  `TestStartupRecoveryBackfillKeepsExistingProjection`、`TestStartupRecoveryBackfillSkipsLiveRun`。
- 五处变异各自打红：闸门不看 `worktree_error` / 回填不跳过 live Run / 回填覆盖已有投影 /
  关掉回填入口 / 最新 attempt 取成最早 attempt。
- `go test ./server/internal/kanban/` 全绿；`go test ./...` 全绿（一次 `TestCoordinatorTickReclaimsStaleClaims`
  的 TempDir 清理偶发失败，属仓库既有 flake，复跑零失败）。
- `go build ./server/...` 成功。

### 隔离实例验收（2026-11-20，端口 7438，用户裁定 ⑥「可以测试」）

隔离根 `/tmp/mindfs-r2-iso`，二进制从干净 worktree `/tmp/mindfs-cu-gnj2`（= `7df7279a`）
构建，`router.mode=route` / `claude` / `sonnet` / `turn_timeout=180s`；生产 7331 全程未碰
（隔离脚本前后自证：端口独立、配置目录零句柄、线上仍在线）。

- **场景 A（真实数据，14/14 PASS 的一部分）**：建任务 → `workflow_choice=specified` 起一个
  长回合 agent 节点（提示词要求写 1200 字）→ 节点 `running` 且 `node_states_json` 仍为 `{}`
  时 `kill -9` → 重启。启动对账结果：投影 `{"execute":"unknown"}`、回复活动
  `running → cancelled` 且带「进程重启时这一轮还在执行，结果无法确定…」、账本
  `node_unknown` 两条（行转移 + 投影回填）。
- **场景 B（复刻 #86 的历史落库状态）**：任务已终态 + `node_states_json='{}'` + `execute`
  的 `unknown` 行 + `running` 回复活动（这种状态只有上一版二进制会生产，所以用 SQL 复刻）。
  重启后：投影 `{"execute":"unknown","verify":"cancelled"}`、活动结算 cancelled 带说明、
  账本落 `{"reason":"startup_projection_backfill","filled_nodes":["execute"],
  "cancelled_nodes":["verify"]}`；**再重启一次不新增事件**（幂等）。
- **Router 闸门（#86 的死锁点）**：重启后先发一条普通留言，决策 `applied`（`say`，
  24.8 s，正文自己说明「上次在飞的节点因为进程重启结果不明…现在没有节点在运行」）；
  再发一条「请直接开一轮新的工作流执行」，决策 `applied` / `run_workflow` / 8.4 s，
  真的落下新 Run `run-2af873c5ab3d238d`（随后取消任务收掉那个长回合）。
  #86 的卡死状态里 `say` 也是允许的，所以只看 `applied` 不够 —— 必须看到执行类动作。
- 证据文件：`/tmp/mindfs-r2-iso/result.json`、`/tmp/mindfs-r2-iso/result-gate.json`
  （脚本 `result.json` 由 `/tmp/r2_accept.py` 产出；两个脚本都在 /tmp，不入库 ——
  它们依赖本机隔离账户与端口，而行为本身已被 20 条单测钉住）。

### 部署（2026-11-20，用户裁定 ⑤「可以部署」）

共享工作区里有其他会话在飞的未提交改动，所以部署从 `/tmp/mindfs-deploy-gnj`（只含已提交
状态 `191e1124` 的克隆，origin 指 GitHub，`web/node_modules` 软链）跑
`bash scripts/deploy-all.sh`，退出码 0：

- 门禁：`go test ./...` 全绿、`tsc --noEmit` clean、Vitest 760 通过；
- 推送 `7df7279a..191e1124` 到 origin `main`；
- 四端 `v0.5.5-1200-43-g191e1124` 逐字一致 —— WSL / fn `systemctl --user is-active` = `active`
  且 `/mindfs/health` 200；pc-win `mindfs.exe` PID 13236（Session 0，计划任务启动）+
  `/mindfs/health` 200 `{"ok":true,"role":"worker"}`；
- 本机 `make build && make install` 已装（md5 `e2f7933…`），运行中的 `/proc/3222145/exe`
  仍指向已删除的旧二进制 —— **待用户 `sudo systemctl restart mindfs`**；本机静态资源对账一致
  （`assets/index-CEZQJY_u.js`）。

## 第三轮（2026-11-20）：直连回复的收尾

用户裁定「请动手补吧」。第一、二轮的结算只覆盖**绑定 workflow run** 的回复
（`settleWorkflowReplyUnknown` 按 `run_id` 找行）；直连回合（Router `message_agent` /
纯对话）的回复 `run_id` 为空，收尾写在跑回合的 goroutine 里，进程重启把它连同
goroutine 一起带走 —— 活动永远 `running`、任务永远 `running`，卡片不落「待审核」，
自动归档（只收终态）也永远轮不到它。

现场（重启后核对）：任务 #95「fork bug」的 `act_1f1792a2c1fa2b8f` 从 04:21 转到次日
13:07，日志显示 12:41 `response stream ended unexpectedly` → 恢复重试 3 次
（`transport is closed`）→ `wait attempt=2/3 delay=30s` 之后再无下文；该会话不在
`/api/replying-sessions` 里。另有一条 11-19 的 pending 占位（#86）与一条 11-12 的
legacy pending 回复同样无人推进。

修复（`Scope: G-NJ`）：

- `activity_store_orphan.go`：`ListOrphanDirectReplies`（`type=agent_reply`、
  `status IN (pending,running)`、`run_id` 为空）、`SettleOrphanReply`（条件更新，
  只有仍是 pending/running 才改成 cancelled，返回 `changed` 供调用方决定是否广播）、
  `LiveReplyExists`（任务上还有没有活着的回复行）。
- `service_recover_direct_reply.go`：`settleOrphanDirectReplies` 在
  `RecoverOnStartupForRoot` 里、workflow 侧结算之后执行。两层判据：`run_id` 为空
  （绑定 Run 的归 Run 侧）＋任务主会话不在回复（`s.sessionRunning(task.MainSessionKey)`，
  复用 `WireSessionRunningProbe` 的 `StreamHub` 活跃位）。结算后广播
  `status/summary/body` 三个字段并 `ClearBody`，与 workflow 侧同一句说明
  （`restartInterruptedReplySummary`）。
- 任务状态：只有「任务确实停在 `running`、会话不在回复、任务上再没有别的活着的回复」
  三条同时成立才放回 `waiting_user` —— 暂停/终态/待审核是操作员状态，workflow 侧刚被
  唤醒的那一轮也不能被顺手拉走。
- 幂等：`ListOrphanDirectReplies` 与 `SettleOrphanReply` 两处都挡非 pending/running，
  第二次启动不动已结算的行（不写 `updated_at`、不覆盖 summary、不改任务状态）。

验收：`service_recover_direct_reply_test.go` 6 条（陈旧 running / pending 占位 /
活会话不碰 / Run 绑定的不碰 / 重复对账幂等且不覆盖操作员状态 / 另一条回复还活着时
任务保持 running）。变异验证 5 处各自打红：去掉活会话判据（打红「活会话不碰」）、
去掉 `LiveReplyExists`（打红「另一条回复还活着」）、去掉 `run_id` 过滤（打红
「Run 绑定的不碰」）、同时去掉两处 status 守卫（打红幂等用例的 `updated_at`）、
去掉任务放回（打红 waiting_user 断言）。`go test ./server/internal/kanban/` 与
`go test ./...` 全绿。

## 残留与待裁定

1. **unknown 会频繁出现的根因没动**：`SessionStateProbeForRoot`（`http_tasks_finish_teardown.go`）
   按策略恒返回 `resumable=false`，所以任何重启只要撞上在飞节点就必定 unknown。要少出 unknown
   需另一次裁定：保持 / 按转录分类成 waiting_user / 只在转录证明无副作用时重排。
2. **「操作员完成时不冻结」**（本轮讨论点 4）：`finishWorkflowRunByOperator` 只取消
   `queued`/`running` 行，`waiting_user` 的节点（human gate / 被挡住的 agent）保持原样。
   任务终态时看不见；「回到待审核」后 Run 仍投影成 `waiting_user`，闸门会挡住新执行，直到那个
   节点被 `answer_waiting_node` 答复或被取代。那是真的停在那里等答复的节点，不是 #86 的幽灵，
   本轮不改。
3. Router 自动 cancel/retry 仍不做（P2 排除）。
4. 合并不明的 unknown 没有自动解除入口：要操作员 retry 该节点，或先人工处理主树。
   这是有意的 fail-closed —— 主树可能只合了一半，自动放行会让新节点接着写。

## 第三轮部署（2026-11-20 14:14）

`bash scripts/deploy-all.sh` 从干净 clone（`/tmp/mindfs-deploy-gnj3`，origin 指
`ChanceAroundYou/mindfs`）构建部署，四端逐字一致 `v0.5.5-1200-52-gb61b81d8`：

- 门禁：`go test ./...` 全绿、`tsc` clean、Vitest 772 项通过。
- WSL / fn：`systemctl --user is-active` = `active`，`/mindfs/health` 200，`mindfs --version` 与部署版逐字相同。
- pc-win：装完由计划任务拉起，PID 48720（Session 0），`:7332` ESTABLISHED + `/mindfs/health` 200。
- 本机：`~/.local/bin/mindfs` 已装 `v0.5.5-1200-52-gb61b81d8`（md5 `47210c47…`），
  **运行中仍是 13:05 起的旧进程（`/proc/3572932/exe` 指向已删除的旧二进制），待用户
  `sudo systemctl restart mindfs`** —— 重启后本轮的启动对账才会对生产库生效。

部署期间撞到两次门禁红，都不是本轮改动：

1. 第一次 `go test ./...` 里 `server/internal/kanban` 的
   `TestRouterShadowDoesNotMutateAndSnapshotsBriefBeforeDispatch` 报
   `attempt to write a readonly database (1032)` —— 已知的负载相关 sqlite 偶发（见
   memory `kanban-gate-flaky-sqlite-io`），单跑通过。
2. 同一次 `server/app` 的 `TestWorkspaceWorkerDerivesAccountID` 报
   `TempDir RemoveAll cleanup: ... directory not empty`。在**改动前**的提交 `6cf945ef`
   上复现（8 次 1 红），属既有 flake：测试用 `context.Background()` 建 workspace，
   账户 build 留下的后台对账会在测试结束后 `ListRoots` → `removeMissingRoot`
   （日志 `[registry] remove.missing_root root=proj-worker`，因为测试只往注册表里写了
   一个从未创建的目录）重写 `registry.json`，与 `t.TempDir` 清理赛跑。低负载下 15/15 不复现。
   本轮不改别人的测试，记录在此。

第二次重跑时 origin/main 已被并发会话推进到 `b61b81d8`（`a0060a80` 是它的祖先），
fast-forward 后按 `b61b81d8` 构建部署，所以版本号后缀是 `gb61b81d8`。

重启后待核对的生产事实（重启前只读扫描所得）：mindfs root 共有 3 条直连孤儿回复 ——
`act_f4eb2482fbe9750a`（任务已 `success`，只结算活动）、`act_1f1792a2c1fa2b8f`（#95）、
`act_942fa611ee63df0a`（#97），后两条应连同任务一起回到 `waiting_user`；llmux root 为 0 条。
