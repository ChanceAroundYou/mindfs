# 后端存量事项

> 规则见 [file-size.md](file-size.md)：软 300 / 硬 500，覆盖 `server/` 与 `cli/` 的所有手写
> Go 文件（**含测试**）。本文件是存量的唯一清单；新代码不进来 —— 新代码两条线立即适用。
> 每清掉一批，在这里勾掉，不留「反正还有更差的」的借口。

## 超硬线（>500）—— 2026-10-16 首轮清零（历史记录）

`backend-file-size-300-500.md` 的 B1–B10 拆完全部存量；当天晚些的硬线清零轮又把最后两个
例外也拆了。那一轮结束时全仓无手写 Go 文件超过 500 行。**这个状态没有保持住**——
2026-11-22 复核见下方小节，当前的超线清单以那里为准。历史去向：

| 文件 | 改造前 | 现在 | 去向 |
|---|---|---|---|
| `cli/cmd/mindfs.go` | 1059 | 331 | B10 拆成同包职责文件 |
| `server/internal/gitview/worktree_finish.go` | 1040 | 78 | B10 → `worktree_merge.go` 等 |
| `server/internal/gitview/gitview.go` | 933 | 314 | B10 |
| `server/internal/scheduled/tasks.go` | 661 | 96 | B10 → `tasks_runner.go` |
| `server/internal/gitview/gitview_history.go` | 653 | 1 | B10（只剩包声明） |
| `server/internal/fs/shared_watcher.go` | 630 | 165 | B10 |
| `server/internal/gitview/worktree_finish_test.go` | 779 | 50 | B10 |
| `server/internal/gitview/gitview_test.go` | 517 | — | B10 已并入其它测试文件（该文件现已不存在） |
| `server/internal/agent/codex/importer_tool_calls.go` | 850 | 442 | 硬线清零轮拆成 `importer_tool_call_javascript.go` / `importer_tool_call_output.go` |
| `server/internal/kanban/worktree_finish_stage.go` | 689 | 299 | 随 kanban 包拆分（B6）落地；文件后来已整体删除 |

维护方式：新超线文件直接进本表并当批清掉，不要重建「批次」列 —— 批次是那轮改造的临时刻度。

### 2026-11-22 复核：硬线再次被突破

`kanban` 与 `/api` 在 11-14 之后的扩张重新越过了 500 行（这几轮都没登记进本表）。复核时全仓
有 21 个手写 Go 文件（含测试）超线；本轮维护拆掉 4 个最大的：

| 文件 | 拆前 | 现在 | 去向 |
|---|---|---|---|
| `server/internal/kanban/activity_service_test.go` | 998 | 493 | `activity_service_summary_test.go`（177）/ `activity_service_direct_test.go`（180）/ `activity_service_status_test.go`（181） |
| `server/internal/kanban/task_store_convert_stages.go` | 888 | 454 | `task_store_convert_legacy_graph.go`（448） |
| `server/internal/kanban/activity_route_apply.go` | 672 | 485 | `activity_route_reply.go`（200） |
| `server/internal/kanban/router_brief.go` | 643 | 292 | `router_brief_views.go`（361） |

拆法全部是纯移动（无行为改动），`go build ./...` 通过，`go test ./server/internal/kanban/`
全包绿。仍超线的 16 个（按行数倒序，下一轮照这个顺序拆）：

- [ ] `server/internal/kanban/activity_store.go`（640）
- [ ] `server/internal/kanban/router_tools.go`（627）
- [ ] `server/internal/api/usecase/session_prompt_test.go`（589）
- [x] `server/internal/kanban/task_store_workflow_runs.go`（573→439，2026-11-23 把建表/补列/历史重建拆到 `task_store_workflow_run_schema.go`）
- [ ] `server/internal/kanban/activity_service.go`（563）
- [ ] `server/internal/kanban/router_coordinate_turn.go`（560）
- [ ] `cli/cmd/startup.go`（560）
- [ ] `server/internal/kanban/router_route_test.go`（559）
- [ ] `server/internal/kanban/activity_store_test.go`（551）
- [ ] `server/internal/kanban/router_event_sources_test.go`（544）
- [ ] `server/internal/kanban/service_completion_verifier.go`（541）
- [ ] `server/cmd/mindfs-server/main.go`（538）
- [ ] `server/internal/kanban/router_event_store.go`（533）
- [ ] `server/internal/kanban/workflow_node_executor.go`（525）
- [ ] `server/internal/api/usecase/session_send.go`（503）— 发送主路径，与同包 `session.go` 双向耦合，须同批拆（原先按软线登记，现已超硬线）。
- [ ] `server/internal/update/service_test.go`（502）

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
- [ ] `server/internal/agent/config.go`（334，B2 已拆一轮）— 剩装载/合并一条线，可按「装载」「合并」再拆。
- [ ] `server/internal/agent/pool.go`（493，B2 已拆一轮）— 剩获取/关闭主路径。
- [ ] `server/internal/agent/probe_run.go`（333，B2 已拆一轮）— 探测执行：可按 configured / installed 两条线拆。
- [ ] `server/internal/agent/proxy/live_compact_wire_test.go`（336）— Phase A 的解析/哈希测试；可按「解析与配对」「drift 哈希」两条线拆。
- [ ] `server/internal/agent/proxy/live_compact_proxy_test.go`（454）— 代理层集成测试；可按「模式与透传」「队列/并发」两条线拆。
- [ ] `server/internal/agent/proxy/proxy.go`（461）— 会话代理主体；同一批改动里顺带时，可按「路由/handler」与「会话注册」拆。
- [x] `server/internal/agent/probe_status_query.go`（232 行，已达标）
- [x] `server/internal/api/agent_api_provider_files.go` — JSON family (read/writeJSONObject + normalize* + ensureJSONObject) 拆出 `agent_api_provider_json.go`；YAML/env/model/URL/managed-block 留原文件。
- [ ] `server/internal/api/usecase/git_worktree.go` — worktree 生命周期（建/删/收尾）。
- [x] `server/internal/api/usecase/session_fork.go`（170 行，已达标）
- [x] `server/internal/api/usecase/session_subagents.go`（235 行，已达标）
- [x] `server/internal/api/usecase/session_turn_identity.go`（172 行，已达标）
- [ ] `server/internal/commandexec/process_windows.go` — Windows 进程树；平台相关，凑巧超线。
- [x] `server/internal/commandexec/runner.go`（154 行，已达标）
- [x] `server/internal/fs/registry.go`（258 行，已达标）
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

> 快照：2026-11-22 复核。拆分轮会把 500+ 的文件拆成两个 300+ 的文件，所以本区间的数量会随拆分**上升** ——
> 这是软线的正常形态（软线是判据，不是清理项）。下列文件还没写过拆分安排，
> 动到它们时按上面同款格式补一行「已看过的边界」。

- [ ] `server/internal/api/http_workflows_test.go`（499）
- [ ] `server/internal/channels/feishu/intake_store.go`（341）— 去重账本与活跃目标共用一个 JSON 文件与同一把锁；下次动它时按「去重」（Seen/Claim/Record/SetStatus）与「活跃目标」（Set/Active/Clear ActiveTarget）两组拆，`saveLocked`/`pruneLocked` 留其中一处由另一文件调用。
- [ ] `server/internal/agent/fold/engine.go`（496）
- [ ] `server/internal/kanban/router_coordinate_recovery_test.go`（491）
- [ ] `server/internal/kanban/workflow_recovery.go`（485）
- [ ] `server/internal/kanban/router_coordinate_test.go`（481）
- [ ] `server/internal/kanban/service.go`（479）
- [ ] `server/internal/kanban/router_event_brief_test.go`（478）
- [ ] `server/internal/kanban/workflow_review_fixes_test.go`（474）
- [ ] `server/internal/kanban/workflow_store.go`（474）
- [ ] `server/internal/api/http_workflows.go`（473）
- [ ] `server/internal/kanban/workflow_runtime.go`（473）
- [ ] `server/internal/api/usecase/session.go`（470）
- [ ] `server/internal/kanban/workflow_runtime_test.go`（468）
- [ ] `server/internal/kanban/router_event_sources.go`（464）
- [ ] `server/internal/kanban/router_shadow_test.go`（463）
- [ ] `server/internal/kanban/router_event_store_test.go`（461）
- [ ] `server/internal/kanban/router_service.go`（457）
- [ ] `server/internal/agent/proxy/proxy_test.go`（456）
- [ ] `server/internal/api/appcontext_workflow_test.go`（455）
- [ ] `server/internal/kanban/workflow_worktree_lifecycle_test.go`（455）
- [ ] `server/internal/kanban/router_workflow_publish_test.go`（450）
- [ ] `server/internal/kanban/workflow_executor.go`（449）
- [ ] `server/internal/kanban/task_store_convert_legacy_graph.go`（448）
- [ ] `server/internal/kanban/task_store_workflow_comments.go`（448）
- [ ] `server/internal/kanban/router_event_brief.go`（443）
- [ ] `server/internal/kanban/service_activities.go`（443）
- [ ] `server/internal/kanban/router_coordinate.go`（440）
- [ ] `server/internal/api/stream_hub.go`（437）
- [ ] `server/internal/kanban/service_activities_summary.go`（437）
- [ ] `server/internal/kanban/service_archive_test.go`（434）
- [ ] `server/internal/kanban/workflow_worktree_merge_test.go`（430）
- [ ] `server/internal/api/http_workflow_agent.go`（429）
- [ ] `server/internal/kanban/router_types.go`（429）
- [ ] `server/internal/kanban/worktree_finish_test.go`（425）
- [ ] `server/internal/api/usecase/external_sessions_test.go`（420）
- [ ] `server/internal/api/usecase/fs_git_test.go`（417）
- [ ] `server/internal/kanban/task_store_workflow_node_runs.go`（414）
- [ ] `server/internal/api/agent_api_provider_sync_ops.go`（409）
- [ ] `server/internal/agent/acp/process_lifecycle.go`（405）
- [ ] `server/internal/kanban/router_r0_audit_test.go`（402）
- [ ] `server/internal/kanban/task_store.go`（402）
- [ ] `server/internal/api/usecase/session_list.go`（401）
- [ ] `server/internal/gitview/worktree_merge.go`（400）
- [ ] `server/internal/kanban/workflow_engine.go`（398）
- [ ] `server/internal/api/appcontext_task_prompt_test.go`（396）
- [ ] `server/internal/kanban/service_outbox.go`（395）
- [ ] `server/internal/kanban/workflow_loop_test.go`（392）
- [ ] `server/internal/agent/acp/session_event_test.go`（390）
- [ ] `server/internal/api/ws_session_message.go`（390）
- [ ] `server/internal/kanban/router_store.go`（390）
- [ ] `server/internal/kanban/router_supervision.go`（388）
- [ ] `server/internal/agent/conformance/probe.go`（387）
- [ ] `server/internal/agent/acp/elicitation_test.go`（385）
- [ ] `server/internal/api/appcontext_workflow.go`（385）
- [ ] `server/internal/api/http_sessions_list.go`（385）
- [ ] `server/internal/kanban/worktree_finish.go`（384）
- [ ] `server/internal/api/usecase/session_hygiene.go`（382）
- [ ] `server/internal/api/usecase/session_command_shell.go`（381）
- [ ] `server/internal/kanban/workflow_agent_ops.go`（381）
- [ ] `server/internal/api/router_runtime.go`（378）
- [ ] `server/internal/api/ws.go`（376）
- [ ] `server/internal/kanban/workflow_completion_test.go`（373）
- [ ] `server/internal/kanban/workflow_driver_loop.go`（373）
- [ ] `server/internal/api/usecase/session_subagent_stream_test.go`（371）
- [ ] `server/internal/kanban/router_supervision_direct_test.go`（370）
- [ ] `server/internal/kanban/router_node_admin_test.go`（369）
- [ ] `server/internal/kanban/workflow_agent_ops_test.go`（368）
- [ ] `server/internal/api/http_tasks.go`（362）
- [ ] `server/internal/agent/claude/session_models_helpers.go`（361）
- [ ] `server/internal/channels/feishu/service.go`（361）
- [ ] `server/internal/kanban/router_brief_views.go`（361）
- [ ] `server/internal/api/appcontext_activity_test.go`（360）
- [ ] `server/internal/kanban/workflow_lifecycle_guards_test.go`（357）
- [ ] `cli/cmd/startup_config_test.go`（356）
- [ ] `server/internal/api/http_router.go`（354）
- [ ] `server/internal/api/http_workflow_authoring.go`（354）
- [ ] `server/internal/agent/claude/session_task_test.go`（353）
- [ ] `server/internal/kanban/workflow_task_worktree_test.go`（351）
- [ ] `server/internal/agent/fold/anthropic/equivalence_test.go`（350）
- [ ] `server/internal/agent/claude/session_messages_tasks.go`（345）
- [ ] `server/internal/kanban/types.go`（343）
- [ ] `server/internal/api/ws_test.go`（341）
- [ ] `server/internal/agent/claude/importer_transcript_test.go`（339）
- [ ] `server/internal/kanban/workflow_validate.go`（337）
- [ ] `server/internal/agent/proxy/live_compact_fold_request.go`（336）
- [ ] `server/internal/agent/pool_open.go`（334）
- [ ] `server/internal/api/usecase/candidates_file_skill.go`（334）
- [ ] `server/internal/api/usecase/session_lifecycle_test.go`（334）
- [ ] `server/internal/scheduled/tasks_runner.go`（333）
- [ ] `server/internal/api/agent_api_provider_codex.go`（332）
- [ ] `server/internal/kanban/service_activity_summary_test.go`（332）
- [ ] `server/internal/kanban/task_store_migrate.go`（332）
- [ ] `server/internal/agent/fold/ledger_test.go`（331）
- [ ] `server/internal/agent/fold/ledger.go`（330）
- [ ] `server/internal/api/usecase/fs_git.go`（330）
- [ ] `server/internal/api/usecase/session_hygiene_wire_test.go`（330）
- [ ] `server/internal/kanban/activity_service_turns.go`（330）
- [ ] `server/internal/agent/fold/engine_test.go`（329）
- [ ] `server/internal/api/usecase/session_worktree_ownership_test.go`（328）
- [ ] `server/internal/kanban/router_supervision_tools.go`（327）
- [ ] `server/internal/kanban/service_completion.go`（325）
- [ ] `server/internal/agent/conformance/conformance.go`（323）
- [ ] `server/internal/api/usecase/session_hygiene_test.go`（323）
- [ ] `server/internal/api/agent_config_shared_test.go`（319）
- [ ] `server/internal/session/manager_meta.go`（318）
- [ ] `server/internal/kanban/workflow_comments_test.go`（317）
- [ ] `server/internal/agent/fold/config.go`（312）
- [ ] `server/internal/kanban/workflow_recovery_test.go`（309）
- [ ] `server/internal/api/http_workflow_agent_test.go`（308）
- [ ] `server/internal/api/usecase/external_sessions_live_tail_test.go`（308）
- [ ] `server/internal/api/usecase/fs_managed_dirs_helpers.go`（308）
- [ ] `server/internal/api/usecase/session_subagent_cancel_test.go`（307）
- [ ] `server/internal/gitview/worktree_merge_test.go`（307）
- [ ] `server/internal/session/manager_pending_test.go`（307）
- [ ] `server/internal/agent/claude/importer_transcript.go`（304）
- [ ] `server/internal/kanban/workflow_test.go`（304）
- [ ] `server/internal/kanban/acceptance_items_test.go`（302）
- [ ] `server/internal/session/manager_session_list.go`（302）
- [ ] `server/internal/kanban/task_store_comments.go`（301）

## 论证过边界、本轮及以后都不再拆

（空 —— 2026-10-16 硬线清零轮后无此类条目。原先两条例外都已不再超线：

- `server/internal/agent/codex/importer_tool_calls.go` 850 → 442（后续又拆到 194）。当年的结论是「按**工具类型**
  拆会把同一个 switch 切成三份」，这条仍然成立，所以那个 switch 原样留着；拆的是**按工具**
  切出的三块互不调用的代码（JS 字面量解析 / patch 文本与输出解析），见 G-GK。
- `server/internal/kanban/worktree_finish_stage.go` 689 → 299，随 kanban 包拆分（B6）落地；该文件后来已整体删除。

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
