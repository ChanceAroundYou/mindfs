# 后端存量事项

> 规则见 [file-size.md](file-size.md)：软 300 / 硬 500，覆盖 `server/` 与 `cli/` 的所有手写
> Go 文件（**含测试**）。本文件是存量的唯一清单；新代码不进来 —— 新代码两条线立即适用。
> 执行批次见 [Active 计划](../../../plans/backend-file-size-300-500.md)，每批清掉的文件在
> 这里勾掉，不留「反正还有更差的」的借口。

## 超硬线（>500）—— 按计划批次清

| 文件 | 行数 | 批次 |
|---|---|---|
| `server/internal/api/appcontext.go` | 1465 | B9 |
| `server/internal/api/ws.go` | 1408 | B9 |
| `server/internal/api/stream_hub.go` | 1302 | B9 |
| `server/internal/api/agent_api_provider.go` | 1291 | B9 |
| `server/internal/api/http_sessions.go` | 1192 | B9 |
| `cli/cmd/mindfs.go` | 1059 | B10 |
| `server/internal/api/http_tasks.go` | 1048 | B9 |
| `server/internal/gitview/worktree_finish.go` | 1040 | B10 |
| `server/internal/gitview/gitview.go` | 933 | B10 |
| `server/internal/session/manager.go` | 797 | B3 |
| `server/internal/api/agent_config.go` | 786 | B9 |
| `server/internal/api/agent_api_provider_apply.go` | 769 | B9 |
| `server/internal/session/blocks.go` | 720 | B3 |
| `server/internal/scheduled/tasks.go` | 661 | B10 |
| `server/internal/gitview/gitview_history.go` | 653 | B10 |
| `server/internal/api/http.go` | 652 | B9 |
| `server/internal/fs/shared_watcher.go` | 630 | B10 |
| `server/internal/api/http_files.go` | 575 | B9 |
| `server/internal/session/types.go` | 568 | B3 |

| 测试文件 | 行数 | 批次 |
|---|---|---|
| `server/internal/session/manager_test.go` | 1531 | B3 |
| `server/internal/api/http_tasks_begin_finish_test.go` | 888 | B9 |
| `server/internal/gitview/worktree_finish_test.go` | 779 | B10 |
| `server/internal/api/http_window_test.go` | 705 | B9 |
| `server/internal/fs/fs_test.go` | 570 | B3 |
| `server/internal/api/ws_test.go` | 563 | B9 |
| `server/internal/gitview/gitview_test.go` | 517 | B10 |

## 软线区间（300–500）—— 下次动它时顺手拆

不要求一次清完。判据是「超过软线就判断职责边界并记录拆分安排」，
所以每项都附上**已看过的边界**，下次动这个文件时照着拆，不用重新想。

- [ ] `cli/cmd/autostart.go` — 启动/自引导一条线，可按平台（systemd / launchd / windows）拆。
- [ ] `server/app/server.go` — 组装 server：路由装配、静态目录、优雅关停三段。
- [ ] `server/app/workspace.go` — 每账户 AppContext 的惰性构建与缓存，可按「构建」「缓存淘汰」拆。
- [ ] `server/internal/agent/acp/elicitation.go` — AskUserQuestion 的 ACP 解冻与回填。
- [ ] `server/internal/agent/acp/importer.go` — 转录导入：读文件、解析、写库三段。
- [ ] `server/internal/agent/claude/session_models.go` — 模型列表拉取与规范化。
- [ ] `server/internal/agent/claude/session_tool_results.go` — 工具结果回填。
- [ ] `server/internal/agent/claude/session_tool_summaries.go` — 工具摘要渲染。
- [ ] `server/internal/agent/codex/session_tools.go` — 工具调用分发（按工具类型分文件）。
- [ ] `server/internal/agent/config.go`（335，B2 已拆一轮）— 剩装载/合并一条线，可按「装载」「合并」再拆。
- [ ] `server/internal/agent/pool.go`（303，B2 已拆一轮）— 剩获取/关闭主路径。
- [ ] `server/internal/agent/probe_run.go`（440，B2 已拆一轮）— 探测执行：可按 configured / installed 两条线拆。
- [ ] `server/internal/agent/probe_status_query.go`（357，B2 已拆一轮）— 状态查询与广播。
- [ ] `server/internal/api/agent_api_provider_files.go` — 供应商配置文件的读写备份。
- [ ] `server/internal/api/http_git.go` — git 只读端点集合，按端点分文件。
- [ ] `server/internal/api/usecase/git_worktree.go` — worktree 生命周期（建/删/收尾）。
- [ ] `server/internal/api/usecase/session_fork.go`（426）— fork 与 repoint 周边逻辑。
- [ ] `server/internal/api/usecase/session_send.go` — 发送主路径（与 session.go 双向耦合，须同批拆）。
- [ ] `server/internal/api/usecase/session_subagents.go` — 子代理会话装配。
- [ ] `server/internal/api/usecase/session_turn_identity.go` — 回合身份写入。
- [ ] `server/internal/commandexec/process_windows.go` — Windows 进程树；平台相关，凑巧超线。
- [ ] `server/internal/commandexec/runner.go` — 一次性进程执行（与 long_shell 的三份文件分开）。
- [ ] `server/internal/fs/registry.go` — 项目注册表：结构 / 持久化 / 查询三段。
- [ ] `server/internal/kanban/service_execution.go` — 阶段执行与调度守卫。
- [ ] `server/internal/kanban/template_store.go` — 模板库持久化。
- [ ] `server/internal/pins/store.go` — 置顶表（每账户一份，落 session_pins.json）。
- [ ] `server/internal/preferences/store.go` — 偏好存储（全账户共享）。
- [ ] `server/internal/session/manager_aux_reads.go` — aux 表读路径。
- [ ] `server/internal/session/manager_exchanges.go` — exchange 表读写。
- [ ] `server/internal/session/manager_search.go` — 正文搜索。
- [ ] `server/internal/session/manager_sqlite.go` — schema 与迁移。
- [ ] `server/internal/update/release_fetch.go`（338，B2 已拆一轮）— 发布信息拉取。

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

## 论证过边界、本轮及以后都不再拆

- [~] `server/internal/agent/codex/importer_tool_calls.go`（850）—— G-GK 刚拆过：工具卡与
      ask_user_question 的解析是一条**共享的**归一化链，按工具类型拆会把同一个 switch 切成
      三份，改一种工具要同时动三个文件。硬线超了但再拆会推翻前一轮结论。
      真要拆的前置是先把 `normalizeXxx` 抽成表驱动，那是独立一轮。
- [~] `server/internal/kanban/worktree_finish_stage.go`（689）—— G-GI 刚拆过：阶段收尾的
      「推进 → 清理 → 汇报」三步之间有隐含的顺序依赖（推进失败就不该清理），按步骤拆会把
      guard 条件复制到两个文件。同上，超硬线但不再拆。

## 顺带发现的待办（与拆文件无关）

- [ ] `server/internal/api/agent_config.go` 裸 error 绕过 `apperr` 中文消息层：`errors.New("backup id required")`、`"backup not found"`、`errAgentConfigConflict` 等业务文案匹配不上 `apperr.Classify` 的关键字兜底，原样进 `respondError` payload，用户看到英文原文。修法：包一层 `apperr`（参考同文件已有的 `apperr.Wrap` 用法）。下次动 agent_config 相关代码时顺手修（2026-10-15 后端体检发现，LOW）。

长期约束见同目录规则文档；历史拆分记录保留在对应计划和提交记录中。
