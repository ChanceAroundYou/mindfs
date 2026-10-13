# Agent 中立规范体系与 task-38 会话 repoint

> **状态（2026-10-14）：已被用户指示 supersede。** Agent 中立规则入口与计划索引已交付；task-38 repoint 不再属于当前目标，不要等待或请求服务重启。以下记录保留作历史审计，不代表仍需执行。

## 目标

建立所有 agent 可发现的规范入口与唯一规则/计划正文，并安全迁移 MindFS 会话
`1791854764-fa16e56ead9c` 绑定的 Claude 转录，清除已经删除的 `task-38` worktree 归属。

## 已完成

- 只读确认该 MindFS session 同时有 Claude 和 Codex bindings：Claude id 为
  `bd0d68f4-d09d-448e-adbd-81aa4aa4a16d`，Codex id 为当前 thread id。
- Claude JSONL 在 `~/.claude/projects/*--worktree-task-38/`，主 checkout slug 下没有同 id 文件；转录游标字段为空，`agent_ctx_seq` 为 62。
- 修正 repoint：显式 Claude `agent_session_id` 选择匹配 binding，不再被最新 Codex exchange 覆盖；回归测试验证 Claude binding/cursor 迁移、Codex binding 不变、ctx seq 保留。
- 规范搬到 `docs/agent/rules/`；添加 AGENTS/Claude/Gemini 共享入口与 `docs/plans/README.md`。
- `web/tests/agent-docs.test.mjs` 覆盖入口、相对链接与计划分类。
- G-BM/G-BN 提交已推送；ship 的 Go tests、tsc、web tests（230 项）、构建安装和 WSL/fn 部署均通过。

## 待完成

1. 等用户执行 `sudo systemctl restart mindfs`。Agent 不得重启本机 systemd 单元。
2. 重启后调用 `wt-finish.sh repoint --resolved --session bd0d68f4-d09d-448e-adbd-81aa4aa4a16d`，并检查
   `/tmp/wt-repoint-bd0d68f4.status` 显示真实成功；不能以命令退出码替代状态内容。
3. 运行 `wt-finish.sh verify`，必须得到 `verify: OK`。若失败，保留原 session/transcript，按状态查因，不手改数据库。
4. 更新本计划为完成记录、将其从 Active 移到已完成索引，再按 ship skill 完成最终提交/部署门禁。

## 验收

- 新 transcript 在 MindFS 主 checkout Claude slug；旧 transcript 保留作回滚。
- Claude binding 指向新 id，`external_source_path`/offset/committed offset 指向新文件 EOF，`agent_ctx_seq` 原值保留；Codex binding 未改变。
- session `related_worktree` 已持久清除，`wt-finish.sh verify` 通过。
- Agent 入口/规范索引门禁和 `check-upstream` 通过，所有 `docs/plans/*.md` 在唯一索引中有状态。
- 最终 `git status` 只保留用户原有且未提交的 `package-lock.json`；提交不得覆盖用户文件。

## 停止条件

- 服务端返回与预期不符、目标 Claude binding/session key 不匹配、或目标 transcript 不可读时停止，不清理 worktree ownership。
- 绑定/游标无法通过服务层事务迁移时停止，不直接更新 SQLite。
