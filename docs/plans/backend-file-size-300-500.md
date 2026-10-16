# 后端文件拆分到「软 300 / 硬 500」+ 同包内重组

> 状态：Active。日期：2026-10-16。
> 目标：后端 Go 文件（含 `*_test.go`）统一套用 **软 300 / 硬 500** 行限制（与前端口径一致），
> 并在同包内重组文件夹结构；单向依赖可抽子包的另行评估。
> 约定验收：`make test` 全绿 · `go run ./scripts/check-upstream` 全绿 · 零行为变化（同包内移动）·
> fork 三件套（登记 + 针对性测试 + `Scope: G-X`）。

## 一、为什么要改规则口径

后端原口径是软 2000 / 硬 4000，前端是软 300 / 硬 500。两条规则服务同一个目的
（「一个文件一个职责，改哪读哪」），数值差 6 倍没有技术理由 —— 后端已经有
`server/internal/api/usecase/session.go` 1496 行、`appcontext.go` 1465 行这类
「单个文件里 4–5 个不相干职责」的形态。本次把后端对齐到前端口径。

存量测量（非测试 161 文件 / 测试 134 文件）：

| 口径 | 源码 | 测试 |
|---|---|---|
| >500 | **43** | **18** |
| 300–500 | 27 | 9 |
| 200–300 | 26 | 20 |
| ≤300 | 65 | 87 |

## 二、方案的边界（为什么同包内拆，不切子包）

`server/internal/api` 的符号流是**双向的**，不是预设的「handler → helper」单向：

- `http.go` 反向引用 20+ 个 handler 文件，同时被 15 个文件反向引用；
- `agent_api_provider.go ↔ _apply.go ↔ _files.go` 是**三向环**（29/7/20 个 ident）；
- `stream_hub.go ↔ appcontext.go ↔ http_sessions.go` 也是环；
- `http_session_message.go` 反向依赖 `ws.go`。

`api/usecase` 同构（`session_send.go` 用 `session.go` 17 个 ident，反向 4 个）。

切子包要先打断这些环（接口提取 + 参数重构），那是**行为风险**，不是移动风险。
因此本轮只做**同包内移动**；单向依赖抽子包单独评估后**放弃**（见 §五）。

## 三、批次

| 批次 | 内容 | 风险 |
|---|---|---|
| B1 | 规则文档 + TODO + 本计划 | 零 |
| B2 | 叶包：`agent/types`、`update`、`webpush`、`auth`、`fs`、`commandexec` | 零 |
| B3 | `session` 包源 + 测试 | 低 |
| B4 | `api/usecase` 源 | 中 |
| B5 | `api/usecase` 测试 | 低 |
| B6 | `kanban` 源 + 测试 | 中 |
| B7 | agent 运行时源（probe/config/pool/claude/codex/acp） | 中 |
| B8 | agent 导入器 + 测试 | 低 |
| B9 | `api` 源 + 测试（单读 goroutine 铁律，逐文件提交） | 高 |
| B10 | 收尾：全量门禁 + TODO 勾项 + 索引标 Completed | 零 |

执行原则：每批独立提交，批内可多文件；B9 内部**逐文件**提交并单独跑
`go test ./server/internal/api/...`。

## 四、兼容策略

- 全部为同包内移动或同包内新增文件：**无签名变化、无 type 位置变化（跨文件同包可见）、
  无 import 路径变化**。
- 不碰 SQLite schema、`registry.json`、任何落盘格式；零数据迁移。
- 现有调用方零改动；Go 编译器当场校验符号解析，`make test` 绿即移动正确。
- fork 定制清单 `docs/upstream-customizations.yaml`：每批为被拆分的**已登记**文件
  追加新文件条目（旧文件仍在 delta 中，断言 2 不报；追加是满足断言 1 的必要条件）。
- 记账粒度按「一批一组」，`boundary` 列多个 `X→Z` 映射，避免 55 个新组把清单
  信息密度冲垮（G-GJ..G-GM 一格一组是因为它们各自单一主题；本方案是批量重组）。

## 五、原方案的调整及理由（实现期发现）

1. **`agent/probe` 与 `agent/config` 不抽子包，改为同包拆文件。**
   实测 `probe.go` 反向依赖 `agent.Pool`/`Config`/`Definition`/`Protocol`，抽子包必须引入
   接口重构（`RuntimeOpener`/`RuntimeCloser`）才能打破包间环 —— 那是行为风险。
   `config.go` 虽无反向依赖（`agent` 包内 0 处其它文件用它的未导出符号），但被
   **9 个外部文件**以 `agent.Config`/`agent.Definition` 引用，抽走要改 9 处 import 与
   232 组清单的锚点，收益（少一层目录）远小于代价。故两处均改为同包内拆文件。
2. **`agent/codex/importer_tool_calls.go`（850）与 `kanban/worktree_finish_stage.go`（689）
   本轮不拆。** 二者分别是 G-GK / G-GI 刚拆过、边界已论证过的产物，再拆等于推翻
   前一轮结论。登记到软线待办。

## 六、执行进度

| 批次 | 状态 | 提交 |
|---|---|---|
| B1 规则文档 + TODO + 计划 | 完成 | `4a84505b` |
| B2 叶包（agent/types、update、webpush、auth、fs、commandexec） | 完成 | `c92d597c` |
| B3 session 包源 + 测试 | 完成 | `1ea6b593` / `219aaa8d` / `5ebbc50b` |
| B4 api/usecase 源 | 完成 | 本次 |
| B5 api/usecase 测试 | 完成 | 前两次提交 |
| B6 kanban 源 + 测试 | 完成 | 本次 |
| B7 agent 运行时源 + 测试 | 完成 | 本次 |
| B8 agent 导入器 + 测试 | 完成 | `ac05bc52` |
| B9 api 源 + 测试（逐文件提交） | 完成 | 逐文件提交 |
| B10 收尾：全量门禁 + TODO 勾项 + 索引标 Completed | 完成 | 本次 |

B4/B5 记录：`usecase_test.go` 3207 → 11 个职责测试文件 + `usecase_test_helpers_test.go`
（跨文件共享的假 registry/agent session），`external_sessions_test.go` 892 →
import / names / ownership / sync 四个。测试函数零改动。

B4 记录：`session.go` 1496 → `session.go` 339 + `session_turn_registry` 114 +
`session_agent_open` 218 + `session_recovery` 142 + `session_command_shell` 294 +
`session_subagent_synthetic` 108 + `session_subagent_stream` 260；
`fs.go` 1158 → `fs.go` 203 + `fs_git` 396 + `fs_upload` 244 + `fs_managed_dirs` 350；
`candidates.go` 733 → `candidates.go` 217 + `candidates_registry` 202 +
`candidates_file_skill` 334；`external_sessions.go` 778 → `external_sessions.go` 271 +
`external_sessions_sync` 423 + `external_sessions_names` 102。

B10 记录：`gitview/worktree_finish.go` 1040 → 6 文件、`worktree_finish_test.go` 779
→ 4 文件（31 个用例逐个核对零丢失）；`gitview/gitview.go` 933 → types / actions / status，
`gitview_history.go` 653 → read / stats / parse，`gitview_test.go` 517 → history / repo /
stats + 助手文件；`fs/shared_watcher.go` 630 → 4 文件；`scheduled/tasks.go` 661 →
tasks / service / runner；`cli/cmd/mindfs.go` 1059 → startup / service_pid /
service_control / managed_dirs / task_cli / open_target。
拆分中发现两处**钉住文件名的源码守卫**必须跟着改，否则守卫失效：
`ws_test.go` 的 `TestWSRequestHandlersAreDispatchedAsynchronously` 与
`pending_identity_test.go` 的 `TestScheduledPathCarriesTurnIdentity`
（后者已用变异验证：把 `EndSessionTurn` 的身份改成哨兵值仍报警，且报的是新文件名）。

B9 记录（每文件一次提交，单读 goroutine 约束）。`appcontext.go` 1465 → 7 个职责文件
（roots / worktree / services / broadcast / notify / task_flags）；`ws.go` 1408 →
upgrade / broadcast / session_message / session_queue / session_slash / turn_tracker；
`stream_hub.go` 1302 → types / build / pending / queue / replay；
`agent_api_provider.go` 1291 + `_apply.go` 769 → handlers / sync_ops / urls / names /
caps / probe / modeltest + codex / env；`http_sessions.go` 1192 → list / detail / actions /
external / command_shell；`http_tasks.go` 1048 → templates / stages / overview /
begin_finish；`agent_config.go` 786 → backup / switch / env；`http.go` 652 →
routes / preferences / local_cli；`http_files.go` 575 → query / managed_dirs。
测试 `ws_test.go` 563、`http_tasks_begin_finish_test.go` 888、`http_window_test.go` 705
同样按边界拆。`TestWSRequestHandlersAreDispatchedAsynchronously` 改为在
`ws.go` / `ws_upgrade.go` 两个文件里查找 `handleWSRequest`，守卫不随拆分失效。

B8 记录：`claude/importer.go` 1398 → `importer.go` 240 + `importer_subagents` 225 +
`importer_files` 361 + `importer_transcript` 341 + `importer_exchange` 282；
`codex/importer.go` 1047 → `importer.go` 236 + `importer_files` 294 +
`importer_subagents` 124 + `importer_transcript` 296 + `importer_exchange` 147。
测试按同边界拆（transcript / scan / cursor / tool_calls）。

过程中发现解析器缺陷并修掉：`func f(` 形式的多行签名（如
`sendCommandMessage(`、`appendImportedExchange(`）被误判成「单行声明」，
导致跨 2..300 行的大段被吞进下一个声明的尾部。修法是单行判定加
「不以 `(` 结尾」条件。

**结论（2026-10-16）**：B1–B10 全部完成。改造前 43 个源码 + 18 个测试文件超硬线，
现在只剩 `server/internal/agent/codex/importer_tool_calls.go`（850）—— 它在 §五 已论证
不再拆（G-GK 刚拆过，按工具类型拆会把同一个归一化 switch 切成三份），登记在 TODO.md
的「论证过边界、本轮及以后都不再拆」里。

**收尾补漏（2026-10-16，同日晚）**：B10 只修了 **Go 侧**两处钉文件名的守卫，
漏了 **前端侧**同样钉 Go 文件名的那批 —— 它们住在 `web/tests/`，由 Vitest 跑，
`make test`（go test）根本扫不到，所以 B1–B10 期间一直是红的没人发现。
`scripts/deploy-all.sh` 的门禁把 web 测试跑出来才暴露：7 个文件假红，
实现一行没少，只是搬了家（`http.go` → `http_routes.go`、`stream_hub.go` →
`stream_hub_build.go`、`appcontext.go` → `appcontext_broadcast.go`、
`service.go` → `service_run_moves.go`、`http_sessions.go` → `http_sessions_detail.go`
/ `http_sessions_actions.go`、`gitview/worktree_finish.go` → `worktree_merge.go`、
`scheduled/tasks.go` → `tasks_runner.go`）。

假红已用两个 worktree 证明与本次改动无关：`12a7f4e5`（B 系列之前）7 个全绿，
`024c3dbe`（B10 完成）7 个全红。

修法是新增 `web/tests/go-source.mjs` 的 `readGoPackage(relDir)`：按**包**读全部非测试
`.go`，按文件名排序后拼接（顺序稳定，因为守卫里有按 `indexOf` 比先后的断言）。
7 个文件改的只是「从哪儿读」，判据一条没动。改完逐条核对过包级读取不会让**负例**
假红：`"branch", "-D"` 只出现在 `_test.go`（已被过滤）、`"merge", "--abort"` /
`ReplayPending(rootID, clientID, …)` / `claudeModelSupportsEffortAt` / `if first.AutoAdvance`
全包皆无、`respondJSONConditional(…"items": items)` 全包恰好 2 处（与断言相等）。
`conditional-request.test.mjs` 的 `helpers.go` 保持单文件读 —— 那几条是带
`[\s\S]{0,80}` 窗口的负例，摊到整包上会撞别的文件的无关代码而假红。

涉及文件：`worktree-finish` / `upstream-restore` / `session-window` /
`task-panel-unification` / `session-list-node-failure` / `conditional-request` /
`session-archive` 七个 `.test.mjs` + 新增 `go-source.mjs`（登记在 G-IY）。

## 六、验收

```bash
make test                            # go test ./... 全绿
go run ./scripts/check-upstream      # 定制清单全绿
```

每批改完跑一遍；B9 每文件跑 `go test ./server/internal/api/...`。
