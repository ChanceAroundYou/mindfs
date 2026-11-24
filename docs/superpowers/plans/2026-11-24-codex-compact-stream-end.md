# codex 手动 `/compact` 不再误报「response stream ended unexpectedly」

> Scope IDs: G-QV（claimed 2026-11-24T11:29Z）

Scope: G-QV

## 现象

用户原话：「codex response stream ended unexpectedly。pc-wsl 节点下 codex 每次手动 compact
后都会如上报错，虽然不影响使用但是很糟心。请定位到根因并修复，很可能在其他节点其他 harness
也存在。」

pc-wsl 上 codex 会话里手动输入 `/compact`：压缩本身成功（上下文确实被压掉），界面上却弹一条
会话错误。同一节点普通回合、Claude 会话都不复现。

## 取证（先复现，不猜）

1. **生产日志（pc-wsl，2026-11-24 16:57）**：手动 `/compact` 那一轮只有
   `[agent/codex] input … content="/compact"` → 压缩条目相关事件，**没有 `output.done`**
   （`output.done` 只在收到 `TurnCompletedEvent` 时打印），紧接着
   `[session] turn.send.no_response … action=fail_without_recovery` 与
   `codex response stream ended unexpectedly`。
2. **SDK 源码**（`github.com/fanwenlin/codex-go-sdk` v0.0.0-20260928161246）：
   - `codex/thread.go:615 runCompactCommandStreamed`：exec 实现 `compactExec`（`RunCompact`）
     时直接转发它的事件流，**不会再合成任何事件**；只有不支持 `RunCompact` 的 exec 才走
     `syntheticMessageStream`（那条路径会补一条 `turn.completed`）。
   - `codex/app_server_exec.go:850-880 RunCompact`：发起 `thread/compact/start` 后，
     收到 `item/completed` 的 `contextCompaction` 条目就 `return nil` 关流 ——
     **协议上压缩回合根本没有 `turn.completed`**。
3. **适配器**：`session.go SendMessage` 把「通道关闭但没见到 `TurnCompletedEvent`」一律当
   意外结束（这条判据本身是对的：进程中途死亡同样只关流），压缩回合于是必撞。
4. **为何以前没被发现**：自动压缩（`hygieneCompact`）走 `runHygieneAutoCompact`，同一错误被
   `stream_error_after_compact` 宽限逻辑吞掉，只影响日志；手动 `/compact` 走普通
   `SendMessage` → `BroadcastSessionError`，错误直达界面。

## 影响面

- 只影响 codex harness（Claude 侧不经这条判据）；与节点无关 —— 任何节点上 codex 手动
  `/compact` 都会重现，pc-wsl 只是用户实际在用的那一台。
- 其他 codex 斜杠命令不受影响：`/review`、`/status`、`/goal`、`/shell`、`/init` 要么走真实回合流
  （有 `turn/completed`），要么由 SDK 合成一条，只有 `/compact` 依赖 `contextCompaction` 收尾。

## 修复（最小）

- `session_events.go`：`handleStreamedEvents` 返回值从 `bool` 换成 `streamOutcome{TurnCompleted,
  CompactionCompleted}`；`ItemCompleted` 分支命中压缩条目（`isContextCompactionItem`：
  `CompactedItem` 或 `GetType()=="contextCompaction"`）时记 `CompactionCompleted`。
- `session.go`：`SendMessage` 在「没见到 `TurnCompleted`」分支里，仅当
  `outcome.CompactionCompleted && isCompactSlashCommand(content)` 时按成功收尾（打
  `[agent/codex] compact.done` 日志）；普通回合中途自动压缩过、随后流断掉，仍然报错。
  `isCompactSlashCommand` 与 SDK 的 slash 解析同形（只看第一行、命令名到空白为止）。
- 落盘/卫生链路无需改动：`EventTypeCompact` 已在 `session_send.go` 里调用
  `markHygieneCompacted`；`finishHygieneTurn` 对 `/` 开头的输入直接返回 false；
  `UpdateLastContextWindow` 对零值窗口是 no-op（`validContextWindow`）。旧路径的
  `planSendFailure` 本就是 `no_assistant_output`（不重试），所以这次不会引入「压缩重发」。

## 验证

- RED：`session_compact_turn_test.go` 三条用例先红 —— 真实事件形状（thread.started +
  item.started/completed(contextCompaction)，无 turn.completed）下 `SendMessage("/compact")`
  报 `codex response stream ended unexpectedly`。
- GREEN：修复后三条全绿，另有一条 SDK 命令表漂移守卫
  （`TestIsCompactSlashCommandMatchesSDKCommandList`）。
- 变异（各自打红后逐字节还原）：
  1. 去掉 `isCompactSlashCommand(content)` 条件 → 普通回合对照用例红；
  2. 去掉 `CompactionCompleted` 记录 → 压缩用例红；
  3. 放宽成「见过压缩条目即放行」→ 只有 started 的用例红。
- 门禁：下文「执行记录」。

## 边界与未验证

- 未在真机 pc-wsl 上重跑（需要部署新二进制，本轮明确不做 push / 部署 / 重启）；隔离实例里的
  真实 codex 端到端已验（见下）。
- 不改 SDK；若上游将来给压缩回合补 `turn.completed`，本分支退化为无害的提前返回。

## 执行记录（2026-11-24）

- RED：修复前 `go test -count=1 -run 'TestSendMessageCompactTurn|TestSendMessageNormalTurn' ./server/internal/agent/codex/`
  三条全红，第一条报的正是用户看到的 `codex response stream ended unexpectedly`。首次写 M1 时用例
  没能打红 —— 假 exec 的 `Run` 返回空通道，对照用例里根本没有出现过压缩事件，判不出
  `isCompactSlashCommand` 的作用；把 `Run` 改成同样按脚本吐事件、并断言
  `compactCalls()==0` 且「确实观察到了压缩事件」之后才转红（见 session_compact_turn_test.go
  的注释）。这类「测不出对错」的用例不算证据，已改正。
- GREEN：同一命令三条全绿；`TestIsCompactSlashCommandMatchesSDKCommandList` 防 SDK 命令表漂移。
- 变异（各自打红后逐字节还原）：
  1. 去掉 `isCompactSlashCommand(content)` 条件 → 普通回合对照用例红；
  2. 去掉 `CompactionCompleted` 记录 → 压缩成功用例红；
  3. 放宽成「见过压缩条目即放行」（started 也算完成）→ 只有 started 的用例红。
- 门禁（本轮实跑）：`go test -count=1 ./server/internal/agent/codex/` ok（0.464 s）；
  `go test -count=1 ./server/internal/api/usecase/ ./server/internal/api/` 两包 ok
  （30.455 s / 50.468 s）；`go build ./...` 与 `go vet ./server/internal/agent/codex/` 均 exit 0。
  `make check-upstream` 在提交后单独复核。

## 隔离实例端到端 A/B（2026-11-24）

环境：本机真实 codex CLI `0.171.0`（ChatGPT 登录，`gpt-6-luna`），`scripts/mindfs-iso.sh`
起两个隔离实例（独立 `XDG_CONFIG_HOME` + 独立项目根，端口 7480/7481），线上 7331 全程只读；
探针经 `/ws` 发两轮：普通消息（确认回合链路正常，日志出现 `output.done`）→ 同一会话发
`/compact`。

| 臂 | 二进制 | `/compact` 后 11 s 的日志 | 结论 |
|---|---|---|---|
| 基线 | `6f34fd4c`（修复前） | `turn.send.no_response … action=fail_without_recovery` → `turn.send.error err=codex response stream ended unexpectedly` → `[ws] session.message.error` | 复现用户症状 |
| 修复 | `be0389d4`（本次） | `[agent/codex] compact.done` → `[ws] session.done`，无任何 error 行 | 通过 |

两侧 codex 自己的 rollout 里都落了 `{"type":"compacted", …}` 与 `item_completed
ContextCompaction`，`cwd` 分别指向各自隔离项目根 —— 压缩在两条臂上都真的执行了，差别只在
MindFS 是否把「没有 turn.completed」误报成失败。

收尾：两个隔离实例 `stop`、基线 worktree 与 `web/dist` 符号链接删除、临时目录与探针脚本清理；
`/mindfs/health`（7331）200、7480/7481 无监听。codex 会话文件留在 `~/.codex/sessions/`
（真实 codex home 共用，未删数据）。
