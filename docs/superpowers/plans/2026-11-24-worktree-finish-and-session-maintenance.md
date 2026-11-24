# 把 worktree-finish / session-compress 吸收为 MindFS 产品能力

> Scope IDs: G-QV（claimed 2026-11-24T11:05Z）

## Context

`~/.agents/skills/worktree-finish`（收尾 + 卡死会话 repoint + verify）和
`~/.agents/skills/session-compress`（诊断 / 清图 / 清锁 / 空会话 / compact）目前是**面向
Claude 的公开 skill**：它们假定调用方是 Claude、假定转录只在 `~/.claude/projects`、
假定调用方自己能看到收尾结果。这带来三个真实问题：

1. **只能给 Claude 用**。MindFS 跑 claude 与 codex，codex 的 repoint 根本不需要搬转录
   （rollout 全局存放，cwd 每轮由 `ThreadOptions.WorkingDirectory` 传入），但 skill 做不到。
2. **收尾要人点**。MindFS 已经有 `FinishTaskWorktree` / `BeginFinishWorktree` /
   `FinishWorktreeAndRepoint`，但只有按钮入口；任务终态后不自动收，会留下一堆树。
3. **卡死会话没人扫**。skill 的 `verify` 会全项目找「运行时目录已删、会话还钉在上面」的
   会话，MindFS 只有单会话的 `worktree-ownership` 清理端点，没有巡检。

目标（用户裁定）：

- 入口 = **UI + Router 工具 + 自动能力**；收尾已有，**不新增 commit 按钮**（提交留在
  收尾回合内由 agent 用显式 pathspec 完成）。
- session-compress **只做无损维护**：不剥思考、不截断工具负载、不 `--drop-tools`、
  不改对话正文。
- 落地后**删除两个 skill 目录**（用户已授权）。

---

## A. worktree-finish → 任务收尾能力

### A1. 跨 harness repoint（当前硬编码 claude-only）

现状：`server/internal/api/usecase/session_repoint.go:95` 直接拒绝非 claude。
实际上两类 harness 的差异只有一步：

| | claude | codex |
|---|---|---|
| 转录位置 | `~/.claude/projects/<slug(cwd)>/<uuid>.jsonl`，**跟 cwd 走** | `<CODEX_HOME>/sessions/**/*.jsonl`，**全局** |
| 换 cwd 要搬转录 | 要（fork 出新 id） | 不要；`ResumeThread` 用当轮 `WorkingDirectory`（`agent/codex/session_open.go:68`） |
| agent_session_id | 换成 fork 后的新 id | **不变** |
| 游标 / native id | 改游标 + `ClearNativeIdentity` | 不动 |

做法：把 `RepointSession` 里 claude 专属的三步抽成能力表
`server/internal/api/usecase/session_repoint_adapters.go`：

```go
type repointAdapter interface {
    Supported() bool
    Preflight(ctx, rootAbs string, b AgentBinding) error  // claude: HasTranscript；codex: 无
    MoveTranscript(ctx, rootAbs string, b AgentBinding) (moved *MovedTranscript, err error) // codex 返回 nil
}
```

- `claude`：包 `claude.HasTranscript` + `claude.RepointTranscript`（`agent/claude/repoint.go:41`），
  保持现有顺序不变：预检 → `pool.Close` → fork/搬 → `RepointAgentBinding`（单事务）
  → `ClearRelatedWorktree` → `RemoveRelatedFilesUnder` → `SetWorkDir` → `ClearNativeIdentity`。
- `codex`：`pool.Close` → `ClearRelatedWorktree` → `RemoveRelatedFilesUnder`（仅当旧目录确实是
  工作树）→ `SetWorkDir`。**不换 binding id、不动游标与 native id。**
- 其它 harness：返回可读错误「当前 harness 不支持换目录」，并在能力查询里报 `false`，
  前端据此隐藏入口（不再靠前端写死 `agent === "claude"`）。

复用：`session/manager_repoint.go` 的 `SetWorkDir`（114）、`ClearRelatedWorktree`、
`RemoveRelatedFilesUnder`；`session/manager_agent_bindings.go:147` 的 `RepointAgentBinding`。

### A2. 卡死会话巡检（skill `verify` 的等价物）

- 把 `usecase.sessionRuntimeRootPathFor`（`session_repoint.go:269`）抽到
  `server/internal/session/runtime_path.go` 的 `RuntimeRootPath(*Session) string`，
  api 与 usecase 两处改为引用同一份（现在是一份复制，口径必须唯一）。
- 新只读端点 `GET /api/sessions/runtime-issues?root=<id>`：列出
  `RuntimeRootPath(s) != ""` 且目录不存在的会话，分类
  `worktree-missing` / `workdir-missing`，带 sessionKey、agent、路径、任务号。
- 新写端点 `POST /api/sessions/runtime-issues/sweep`：逐条修复 —— 目录已删时 claude 走完整
  repoint（否则 CLI 在根 slug 下找不到转录、`--resume` 静默失败），codex 只清归属 + 回根目录。
  正在回复的会话一律跳过，返回 `skipped: running`。
- **自动**：装配期起一拍（启动后）+ 每 `worktree.sweep_interval_minutes`（默认 10）执行；
  只处理「目录确实不存在 + 会话空闲」的会话，幂等，日志一行一条，不写用户可见消息。
  接线位置参照 `kanban/service_archive.go:164` 的 ticker 模式，由 `server/app` 启动。

### A3. 自动收尾（不是按钮）

保留现有按钮与端点，新增自动触发 `kanban/service_worktree_auto_finish.go`：

- 触发点：任务被显式置为终态（complete / cancel）或归档时。
- 幂等：每个任务只排一次，落任务事件 `finish_auto_queued`，后续重复触发直接返回。
- 前置：任务有可用 worktree、`Finishing() == false`、`assertNotRunning` 通过、
  相关会话无活跃使用（复用 `pool.HasAnyActiveUse` 的判据）。
- 分两档（省掉不必要的模型回合）：
  1. 工作树干净且分支已并入主干（`git status --porcelain` 空 + `merge-base --is-ancestor` 为真）
     → 直接 `FinishTaskWorktree` 清场，不起回合。
  2. 否则 → `BeginFinishWorktree` 跑收尾回合（agent 用显式 pathspec commit + merge），
     成功后走现有 `FinishWorktreeAndRepoint`。
- 失败不静默：沿用 `agentFallbackPlan` / `SessionWarning`，任务留在 `waiting_user` 并写明原因。
- **不新增 commit 按钮、不新增 commit 端点**：提交只在收尾回合提示词里发生
  （`worktree_finish_turn.go:28` 已有显式路径与禁止 `git add -A` 的规定）。
- 配置：`worktree.auto_finish`（默认 true），只走 `config.json`。

### A4. Router 工具

新增 `router_finish_worktree`（`server/internal/kanban/router_tool_finish_worktree.go`，
注册进 `router_tools.go:86` 的 `tools()`）：

- `mode: "status" | "begin" | "teardown"`
  - `status`：只读，返回 worktree 可用性、是否有未提交改动、是否已并入、是否 `Finishing`。
  - `begin` / `teardown`：写动作，走既有 collect-then-apply（新增 action kind
    `finish_worktree`，`router_types.go:221` 那组），幂等键 `finish-worktree:<decision>:<seq>`。
- 校验：任务运行中拒绝；无 worktree 拒绝；`teardown` 遇到用户改动返回可读原因，
  交给 Router 决定 `notify_user`。
- brief 里补一小块 worktree 现状（可用 / 未提交数 / 已合并），让 Router 多数情况不必先调工具。

### A5. 前端

- 任务卡片/详情：保留「收尾」按钮与状态；新增自动收尾的进行中/结果提示。
  **不加提交按钮。**
- `SessionHeaderMenu.tsx:54` 的 `canRepoint` 改为读服务端能力
  （`GET /api/sessions/{key}/repoint-capability` 或随会话详情下发 `repoint_supported`），
  claude 与 codex 都可用，其它 harness 隐藏并显示原因。

---

## B. session-compress → 会话无损维护

### B1. 已有能力（直接复用，不重写）

- `/compact`：`usecase.CompactSession`（`session_hygiene_wire.go:270`），
  `hygieneAutoCompactSupported` 已经覆盖 claude + codex。
- live-compact 折叠：`agent/fold` + `agent/proxy/harness.go:106` 的按 harness 适配器。
- 读口：`GET /api/sessions/{key}/context-saving`。

### B2. 新增（全部无损）

新包 `server/internal/sessionmaintenance/`，按 harness 适配器组织：

```go
type Adapter interface {
    Harness() string
    TranscriptPaths(sessionKey, agentSessionID string) ([]string, error)
    LocksDir() string            // codex 返回 "" = 无锁机制
    SupportsImages() bool
}
```

- **diagnose（只读）**：每会话 size / 行数 / 记录数 / 图片字节 / 陈旧锁 / 运行时目录是否存在。
  新端点 `GET /api/sessions/maintenance/diagnose?root=`，另有跨项目汇总入口。
- **clean-images**：把原生转录里的图片块**整体**替换成文本占位
  （`{"type":"text","text":"[image removed: …]"}`），**绝不留下空 data**
  （会触发 `unknown variant 'image_url'`）；保留记录、`uuid`、`parentUuid` 链；
  写临时文件 + 原子换名，原文件留 `.bak`。会话正在回复时拒绝。
  codex 若无法安全改写 rollout → `SupportsImages=false`，接口显式回「该 harness 暂不支持」，
  不做半套。
- **clean-locks**：删 `~/.claude/sessions/<pid>.json` 中 PID 已死的锁（skill 里
  `_pid_alive` 的等价判断用 `os.FindProcess` + signal 0）。codex 无此机制，报 0。
- **purge-empty**：删除既无用户文本又无助手文本的会话。**只手动**、必须 `confirm: true`，
  不进自动扫描。

**明确不做**：思考剥离、工具负载截断、`--drop-tools`、任何改写对话正文的操作。

### B3. 自动 vs 手动

- 自动（启动一拍 + 每 `session_maintenance.sweep_interval_minutes`，默认 10）：
  清陈旧锁 + A2 的运行时目录修复。两者幂等、不改变模型可见内容。
- 手动（UI / Router）：「清图片」「删空会话」—— 会改变历史或删数据，必须由人确认。

### B4. 端点与 Router 工具

- `GET  /api/sessions/maintenance/diagnose`
- `POST /api/sessions/{key}/maintenance/clean-images`
- `POST /api/sessions/{key}/maintenance/clean-locks`
- `POST /api/sessions/{key}/maintenance/purge-empty`（body `confirm:true`）
- Router：`router_session_maintenance`，`action: diagnose | clean_locks | strip_images | purge_empty`；
  `purge_empty` 必须带 `confirm:true`，无确认直接拒绝。

### B5. 前端（三层拆分：view 不 import services）

- `services/session/sessionMaintenanceApi.ts`（HTTP）
- `hooks/useSessionMaintenance.ts`（状态与副作用）
- `shared/sessionMaintenance.ts`（纯函数：报告分组、可清理字节格式、能力文案）
- `components/session/SessionMaintenancePanel.tsx`：会话头部菜单新入口「会话维护」，
  展示诊断、单条清理、回收前/后大小。
- `components/app/SessionHealthPanel.tsx`：侧栏 ⋯「会话体检」，跨项目列出问题会话并一键修复。

---

## C. 配置与装配

- `cli/cmd/startup.go`：新增
  ```json
  "worktree": { "auto_finish": true, "sweep_interval_minutes": 10 },
  "session_maintenance": { "enabled": true, "sweep_interval_minutes": 10, "keep_backups": true }
  ```
  零值 = 上述默认；未知值归一化。
- `server/app/server.go`（StartOptions 95-144）→ 装配两个 ticker 与能力表；
  参照 `server/app/session_hygiene.go:29` 的转换函数写法。
- 主节点/worker 分工：收尾与 repoint 是**数据面**，逐节点本地执行；
  控制面只放配置，不新增跨节点调用。

---

## D. 删除旧 skill（落地后执行，需用户在场）

1. `rm -rf ~/.agents/skills/worktree-finish ~/.agents/skills/session-compress`
2. `grep -rn "session_maintenance\|session_debloat\|session_cleanup" ~/.agents` 确认无引用后，
   删除 `~/.agents/claude/scripts/session_{maintenance,debloat,cleanup}.py`
   （`session_debloat.py` 是被明确否决的破坏性路径，不吸收）。
3. `~/.agents` 是 git 仓：删除要单独提交；这一步在实施阶段由用户确认后执行，不在计划里自动做。

---

## 关键文件清单

**新增**
- `server/internal/api/usecase/session_repoint_adapters.go`
- `server/internal/session/runtime_path.go`
- `server/internal/api/http_session_runtime_issues.go`
- `server/internal/kanban/service_worktree_auto_finish.go`
- `server/internal/kanban/router_tool_finish_worktree.go`
- `server/internal/sessionmaintenance/{adapter,claude,codex,diagnose,images,locks,purge}.go`
- `server/internal/api/http_session_maintenance.go`
- `server/internal/api/usecase/session_maintenance.go`

**修改**
- `server/internal/api/usecase/session_repoint.go`（去 claude 硬编码）
- `server/internal/kanban/router_tools.go` / `router_types.go`（新工具与 action kind）
- `server/internal/session/manager_repoint.go`（如需暴露退出参）
- `cli/cmd/startup.go`、`server/app/server.go`（配置与 ticker）
- `web/src/components/session/SessionHeaderMenu.tsx`、`web/src/services/session/sessionRepointApi.ts`
- `web/src/components/task/TaskCardFinishButton.tsx`、`useTaskDetailPanel.ts`（自动收尾状态）
- `docs/upstream-customizations.yaml`（新 Scope 组，用 `scripts/scope-id claim` 取号）

---

## 验证

**单元 / 变异（先红后绿，逐条打红）**
1. codex repoint：binding id 不变、`SetWorkDir` 生效、没有转录搬移调用；变异（误调
   `RepointTranscript`）打红。
2. 巡检：目录仍存在的会话**不被**清理；变异（去掉 exists 判断）打红。
3. 自动收尾幂等：同一任务重复终态只排一次；变异（去掉事件去重）打红。
4. 图片清理：清理前后记录数、`uuid` 集合、`parentUuid` 边完全一致，只有 image 块内容变化；
   变异（删记录 / 留空 data）打红。
5. 清锁：只删死 PID；变异（不看 PID 直接删）打红。

**隔离实例端到端**（`scripts/mindfs-iso.sh`，端口 74xx，不碰 7331）
- claude：带 worktree 任务 → 完成 → 自动收尾回合提交合并 → 拆树删分支 → 会话 repoint 到根目录
  → 在根目录继续一轮成功。
- codex：同上，确认没有转录搬移、thread id 不变、下一轮 cwd 就是根目录。
- 手工删掉某会话的 worktree 目录 → 10 分钟内巡检把它搬回根目录（claude）/ 清归属（codex）。
- 造一条含大 base64 图片的会话 → 清图 → 回收字节可见、会话仍能打开并续聊。

**门禁**
`go test -count=1 ./...`、`npx tsc -b --noEmit`、`npx vitest run`、`make build`、
`make check-upstream`（新组号已登记、计划里 Scope 行可解析）。

**未覆盖（写明，不假装）**
- 未在真实 codex 桌面端并发场景下验证 repoint（桌面端有同 CODEX_HOME 的线程）。
- 图片清理对 Codex rollout 的支持取决于其内容形态；不支持时显式报错，不做静默降级。


## 执行记录（2026-11-24）

**实现范围**：A1 的 repoint 能力表把 Claude 的转录搬移与 Codex 的纯工作目录切换分开；
A2 的运行时目录巡检列出并修复「会话还钉在已删目录」的状态；A3 自动收尾分两档
（干净且已合并直接清场，否则走收尾回合），失败写 `finish_auto_failed` 解除占坑并允许重试；
A4 补 Router 工具 `router_finish_worktree` / `router_session_maintenance`；A5 前端补会话维护面板、
侧栏体检入口与按服务端能力显示的 repoint 菜单。B 部分只做无损维护：只读诊断、陈旧 PID 锁清理、
图片块整体替换为文本占位（保留记录数与 uuid 链、写唯一 `.bak`）、显式确认后删空会话。
**没有**吸收思考剥离、工具负载截断或 `--drop-tools`，也没有新增 commit 按钮。

**关键修正（先红后绿）**：

- `TestAutoFinishWorktreeTearsDownCleanMergedTreeWithoutFinishTurn` 先红：自动收尾原来无条件
  起回合；新增 `FinishPlan.Merged` 后，只有已合并且干净的树走机械清场。
- `TestAutoFinishWorktreeStartsTurnForCleanUnmergedTree` 先红：干净但未合并不能跳过回合；
  守卫从 `plan.Mechanical` 改回 `plan.Merged`，变异后该用例按预期打红。
- `TestAutoFinishRetriesAfterFailure` 先红：`finish_auto_queued` 失败后永久占位；新增
  `finish_auto_failed` 与收尾回合失败回写，变异成无条件占坑后该用例按预期打红。
- 图片清理一开始对 JSONL 做整份 `json.Unmarshal`，多行转录一个图片都清不到；改成逐行改写，
  补记录数/uuid 链用例，并让备份写 `.bak` / `.bak-1` 且用 `O_EXCL`，不再覆盖初版。
- Codex 的 rollout 形状与 Claude 不同，套用空会话启发式会误删完整会话；`SupportsPurge()`
  只给 Claude 开，Codex 明确拒绝。
- 维护空闲守卫原来查裸 session key，而 pool 实际用 `<agent>-<key>`；改为先查真实键。
- `worktree.sweep_interval_minutes` 原先解析后无人读取；现在运行时巡检用该键，锁清理继续用
  `session_maintenance.sweep_interval_minutes`，源码契约用例钉住两条路径。
- Windows 原先 `pidAlive` 恒 true，陈旧锁永不清理；改为 `OpenProcess` + `GetExitCodeProcess`，
  查询失败仍按存活处理（宁可漏删）。

**验证**：定向后端 `go test -count=1 ./server/internal/kanban/ ./server/internal/sessionmaintenance/
./server/internal/api/usecase/ ./server/internal/api/ ./server/app/ ./cli/cmd/` 中 kanban、
sessionmaintenance、api、app、cli 全绿；`usecase` 的全量并发跑出现过一次
`TestManualCompactPendingFollowsTheRealTurn` 的既有 SQLite 竞态，单跑 `-count=1` 通过。
`app` 的 `TestWorkspaceWorkerDerivesAccountID` 也是既有的 TempDir 清理竞态，重跑通过。
前端 `web/node_modules/.bin/tsc --noEmit -p web/tsconfig.json` clean，
`web/node_modules/.bin/vitest run --config ./vitest.config.ts` 275 文件 / 977 通过 / 6 跳过。
`GOOS=windows GOARCH=amd64 go build ./server/internal/sessionmaintenance/` 通过。

**边界**：未部署、未 push、未重启任何节点；两个旧 skill 目录与
`~/.agents/claude/scripts/session_*.py` 均未删除。图片清理仍整文件读入，空闲检查与改写之间
存在 TOCTOU 窗口；锁清理只处理 `<pid>.json`，不碰可能的 `.key` 旁车文件。Codex 桌面端并发
repoint 与真机大数据图片续聊未验。
