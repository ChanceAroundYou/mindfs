# 后端存量事项

> 规则见 [file-size.md](file-size.md)：软 300 / 硬 500，覆盖 `server/` 与 `cli/` 的所有手写
> Go 文件（**含测试**）。本文件是存量的唯一清单；新代码不进来 —— 新代码两条线立即适用。
> 每清掉一批，在这里勾掉，不留「反正还有更差的」的借口。

## 超硬线（>500）—— 已清零（2026-10-16）

`backend-file-size-300-500.md` 的 B1–B10 拆完全部存量；当天晚些的硬线清零轮又把最后两个
例外也拆了。当前全仓无手写 Go 文件超过 500 行：

| 文件 | 改造前 | 现在 | 去向 |
|---|---|---|---|
| `cli/cmd/mindfs.go` | 1059 | 331 | B10 拆成同包职责文件 |
| `server/internal/gitview/worktree_finish.go` | 1040 | 78 | B10 → `worktree_merge.go` 等 |
| `server/internal/gitview/gitview.go` | 933 | 314 | B10 |
| `server/internal/scheduled/tasks.go` | 661 | 96 | B10 → `tasks_runner.go` |
| `server/internal/gitview/gitview_history.go` | 653 | 1 | B10（只剩包声明） |
| `server/internal/fs/shared_watcher.go` | 630 | 165 | B10 |
| `server/internal/gitview/worktree_finish_test.go` | 779 | 50 | B10 |
| `server/internal/gitview/gitview_test.go` | 517 | — | B10 已并入其它测试文件 |
| `server/internal/agent/codex/importer_tool_calls.go` | 850 | 442 | 硬线清零轮拆成 `importer_tool_call_javascript.go` / `importer_tool_call_output.go` |
| `server/internal/kanban/worktree_finish_stage.go` | 689 | 299 | 随 kanban 包拆分（B6）落地 |

维护方式：新超线文件直接进本表并当批清掉，不要重建「批次」列 —— 批次是那轮改造的临时刻度。

## 软线区间（300–500）—— 下次动它时顺手拆

不要求一次清完。判据是「超过软线就判断职责边界并记录拆分安排」，
所以每项都附上**已看过的边界**，下次动这个文件时照着拆，不用重新想。

- [ ] `cli/cmd/autostart.go` — 启动/自引导一条线，可按平台（systemd / launchd / windows）拆。
- [x] `server/app/server.go`（171 行，已达标）
- [x] `server/app/workspace.go`（290 行，已达标）
- [x] `server/internal/agent/acp/elicitation.go`（260 行，已达标）
- [x] `server/internal/agent/acp/importer.go`（225 行，已达标）
- [x] `server/internal/agent/claude/session_models.go`（62 行，已达标）
- [x] `server/internal/agent/claude/session_tool_results.go`（283 行，已达标）
- [x] `server/internal/agent/claude/session_tool_summaries.go`（201 行，已达标）
- [x] `server/internal/agent/codex/session_tools.go`（211 行，已达标）
- [ ] `server/internal/agent/config.go`（335，B2 已拆一轮）— 剩装载/合并一条线，可按「装载」「合并」再拆。
- [ ] `server/internal/agent/pool.go`（303，B2 已拆一轮）— 剩获取/关闭主路径。
- [ ] `server/internal/agent/probe_run.go`（440，B2 已拆一轮）— 探测执行：可按 configured / installed 两条线拆。
- [x] `server/internal/agent/probe_status_query.go`（232 行，已达标）
- [x] `server/internal/api/agent_api_provider_files.go` — JSON family (read/writeJSONObject + normalize* + ensureJSONObject) 拆出 `agent_api_provider_json.go`；YAML/env/model/URL/managed-block 留原文件。
- [ ] `server/internal/api/usecase/git_worktree.go` — worktree 生命周期（建/删/收尾）。
- [x] `server/internal/api/usecase/session_fork.go`（170 行，已达标）
- [ ] `server/internal/api/usecase/session_send.go` — 发送主路径（与 session.go 双向耦合，须同批拆）。
- [x] `server/internal/api/usecase/session_subagents.go`（235 行，已达标）
- [x] `server/internal/api/usecase/session_turn_identity.go`（172 行，已达标）
- [ ] `server/internal/commandexec/process_windows.go` — Windows 进程树；平台相关，凑巧超线。
- [x] `server/internal/commandexec/runner.go`（154 行，已达标）
- [x] `server/internal/fs/registry.go`（258 行，已达标）
- [x] `server/internal/kanban/service_execution.go`（103 行，已达标）
- [ ] `server/internal/kanban/template_store.go` — 模板库持久化。
- [x] `server/internal/pins/store.go`（205 行，已达标）
- [ ] `server/internal/preferences/store.go` — 偏好存储（全账户共享）。
- [ ] `server/internal/session/manager_aux_reads.go` — aux 表读路径。
- [ ] `server/internal/session/manager_exchanges.go` — exchange 表读写。
- [ ] `server/internal/session/manager_search.go` — 正文搜索。
- [ ] `server/internal/session/manager_sqlite.go` — schema 与迁移。
- [x] `server/internal/update/release_fetch.go`（253 行，已达标）

测试文件同样按此口径登记（软线是判据，不是清理项）：

- [ ] `server/app/workspace_test.go` — 按「共享契约 / 账户分区」两组拆。
- [ ] `server/internal/agent/codex/session_test.go` — 按发送 / 导入 / 工具分组。
- [ ] `server/internal/api/http_tasks_finish_test.go` — 按 finish 的正常/异常路径拆。
- [ ] `server/internal/api/usecase/session_identity_dedup_test.go` — 按身份判重的各分支拆。
- [ ] `server/internal/auth/auth_test.go` — 按 store 的三个职责文件对齐拆（B2 已拆源码）。
- [ ] `server/internal/commandexec/runner_test.go` — 按平台 / 超时 / 取消拆。
- [ ] `server/internal/kanban/worktree_finish_dispatch_test.go` — 按派发路径拆。
- [ ] `server/internal/pins/store_test.go` — 按账户分区 / 回填拆。
- [ ] `server/internal/update/service_test.go` — 按拉取 / 校验 / 安装三段对齐 B2 拆出的源码文件拆。

## 软线区间里尚未判断边界的文件（下次动它时补上边界）

> 拆分轮会把 500+ 的文件拆成两个 300+ 的文件，所以本区间的数量会随拆分**上升** ——
> 这是软线的正常形态（软线是判据，不是清理项）。下列文件还没写过拆分安排，
> 动到它们时按上面同款格式补一行「已看过的边界」。

- [ ] `server/internal/kanban/worktree_finish_stage_test.go`（499）
- [ ] `server/internal/kanban/worktree_finish_test.go`（484）
- [ ] `server/internal/api/usecase/session_prompt_test.go`（443）
- [ ] `server/internal/kanban/service_agent_stage_test.go`（424）
- [ ] `server/internal/api/usecase/external_sessions_test.go`（420）
- [ ] `server/internal/api/usecase/fs_git_test.go`（417）
- [ ] `server/internal/api/stream_hub.go`（413）
- [ ] `server/internal/agent/acp/process_lifecycle.go`（405）
- [ ] `server/internal/kanban/task_store.go`（402）
- [ ] `server/internal/agent/acp/session_event_test.go`（390）
- [ ] `server/internal/api/http_tasks.go`（388）
- [ ] `server/internal/agent/acp/elicitation_test.go`（385）
- [ ] `server/internal/api/ws_session_message.go`（382）
- [ ] `server/internal/api/usecase/session_command_shell.go`（381）
- [ ] `server/internal/kanban/service_stage_edit_test.go`（377）
- [ ] `server/internal/agent/claude/session_models_helpers.go`（353）
- [ ] `server/internal/kanban/service_run_moves_test.go`（349）
- [ ] `server/internal/api/http_tasks_begin_finish_test.go`（349）
- [ ] `server/internal/kanban/worktree_finish.go`（348）
- [ ] `server/internal/kanban/template_store_test.go`（345）
- [ ] `server/internal/api/usecase/session.go`（343）
- [ ] `server/internal/agent/claude/session_messages_tasks.go`（340）
- [ ] `server/internal/agent/claude/importer_transcript_test.go`（339）
- [ ] `server/internal/api/usecase/session_lifecycle_test.go`（334）
- [ ] `server/internal/api/usecase/candidates_file_skill.go`（334）
- [ ] `server/internal/scheduled/tasks_runner.go`（333）
- [ ] `cli/cmd/mindfs.go`（331）
- [ ] `server/internal/api/usecase/fs_git.go`（330）
- [ ] `server/internal/kanban/service_lifecycle_test.go`（320）
- [ ] `server/internal/kanban/service_worktree_test.go`（318）
- [ ] `server/internal/api/agent_api_provider_sync_ops.go`（317）
- [ ] `server/internal/gitview/gitview.go`（314）
- [ ] `server/internal/api/agent_api_provider_codex.go`（310）
- [ ] `server/internal/agent/claude/session_task_test.go`（309）
- [ ] `server/internal/api/usecase/fs_managed_dirs_helpers.go`（309）
- [ ] `server/internal/gitview/worktree_merge_test.go`（307）
- [ ] `server/internal/api/appcontext.go`（307）
- [ ] `server/internal/api/usecase/session_subagent_cancel_test.go`（307）
- [ ] `server/internal/kanban/service_session_link_test.go`（303）

## 论证过边界、本轮及以后都不再拆

（空 —— 2026-10-16 硬线清零轮后无此类条目。原先两条例外都已不再超线：

- `server/internal/agent/codex/importer_tool_calls.go` 850 → 442。当年的结论是「按**工具类型**
  拆会把同一个 switch 切成三份」，这条仍然成立，所以那个 switch 原样留着；拆的是**按工具**
  切出的三块互不调用的代码（JS 字面量解析 / patch 文本与输出解析），见 G-GK。
- `server/internal/kanban/worktree_finish_stage.go` 689 → 299，随 kanban 包拆分（B6）落地。

要再拆 `importer_tool_calls.go` 的前置仍是把 `normalizeXxx` 抽成表驱动，那是独立一轮；
现在它已在硬线内，不再有必须拆的压力。）

## 真 TODO（与拆文件无关，已清）

- [x] `server/internal/api/agent_config.go` 与同包 `*_env.go` / `*_backup.go` /
      `*_switch.go` 的身份校验/备份/切换错误。原本全部是 `errors.New("backup id required")`
      之类的英文裸 error，虽然部分已有 `apperr.Wrap` 用法，但校验/查找分支完全绕过了
      apperr 中文消息层 —— 原样进 `respondError` payload，用户看到英文原文。
      2026-10-16 走完 `apperrInvalid(...)` 助手，统一成
      `&apperr.Error{Code: "invalid_request", Message: <中文>}`，`errAgentConfigConflict`
      哨兵改成中文字符串；所有消息翻译成中文，并保持 `errors.Is` 语义不变（测试绿）。

长期约束见同目录规则文档；历史拆分记录保留在对应计划和提交记录中。
