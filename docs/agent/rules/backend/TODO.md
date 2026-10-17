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
- [x] `server/internal/api/agent_api_provider_files.go` — JSON family (read/writeJSONObject + normalize* + ensureJSONObject) 拆出 `agent_api_provider_json.go`；YAML/env/model/URL/managed-block 留原文件。
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
