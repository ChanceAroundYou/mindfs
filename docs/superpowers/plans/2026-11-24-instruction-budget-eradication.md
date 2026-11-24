# 指令注入预算根除：AGENTS.md / plans 索引瘦身 + 口径校准

> Scope IDs: G-QZ（claimed 2026-11-24T11:42Z）

## Context

Claude CLI 在 `session_start` 展开 `AGENTS.md` 的 `@docs/plans/README.md`，把 146,783 chars 注入；本地拒发线约 177k（200k − 20k 输出预留 − 3k），上次成功 usage 145,117 + ~73k 注入越线，191 ms 合成 `Prompt is too long`。Codex 不展开 @import；ACP 无 MindFS 注入逻辑。根因在自动发现入口过大，修复必须跨 harness 通用且防回归。

## Global Constraints

- 保持 200k 默认窗口，不改 `[1m]`、不开 1M、不改 blocking override。
- 不新增 MindFS 侧按请求大小自动压缩策略。
- 不 push / 部署 / 发布 / 重启任何节点。
- 工作区多会话共享；提交用显式 pathspec，只碰本计划文件。
- TDD：先看失败测试再实现；变异必须因断言失败变红；`go test -count=1`。
- 三件事：登记 `docs/upstream-customizations.yaml`、针对性测试入表、提交标 Scope。

## Task 1 — 静态预算闸门与源头瘦身（RED→GREEN）

- 先写失败测试：AGENTS.md ≤8 KiB；每个 `@import` 目标 ≤16k chars；展开总量 ≤32k；plans README 不含部署史段落且有 `DEPLOY_HISTORY.md` 指针。
- 运行测试，确认因断言失败变红。
- 改 `AGENTS.md`：删除 `@docs/plans/README.md`，保留 rules import，补按需读取指针。
- 瘦身 `docs/plans/README.md`：保留 Active + Plan rules，历史部署记录原文搬到 `docs/plans/DEPLOY_HISTORY.md`，顶部加指针。
- 重跑测试转绿；变异：加回 @import / 塞回历史段 → 断言失败变红后还原。

## Task 2 — 胶囊口径校准（RED→GREEN）

- 先写失败测试：胶囊同时显示“上次成功 usage”与“本地预检估算（估算）”，缺估算标记即失败。
- 改前端胶囊组件/文案，使两种口径分开且估算标注为估算。
- 重跑测试转绿；变异：删估算标记 → 断言失败变红后还原。

## Task 3 — 登记、门禁与复现记录

- 填 `docs/upstream-customizations.yaml` 的 G-X 条目（files/tests/anchors 完整）。
- 计划文档写执行记录、变异、门禁结果。
- 跑 `go test -count=1 ./...`、`npx tsc -b --noEmit`、`npx vitest run`、`make check-upstream`。
- 复现路径：用失败会话 `:14659` 前快照在隔离实例重放；若环境不允许，明确写“未实跑”，不声称修好。
- 显式 pathspec 提交。

## Review Focus

- 是否仍有任何自动注入路径把大体量文件塞进 Claude 的 `session_start`。
- Codex 32 KiB 上限是否被 AGENTS.md 变化破坏。
- UI 估算是否误导为真实 usage。

## 执行记录（2026-11-24，Scope: G-QZ）

### 改动

- `AGENTS.md`：去掉 `@docs/plans/README.md`，只保留规则索引；plans / 部署史 / 计划执行记录改成按需读取的链接。
- `docs/plans/README.md`：从 147,399 chars 瘦到约 5.6k chars，只留 Active 链接 + Plan rules；47 条 Active 条目的完整执行记录与 Completed 表原文搬到 `docs/plans/PLAN_HISTORY.md`，部署 / 发布状态原文搬到 `docs/plans/DEPLOY_HISTORY.md`。
- `web/tests/agent-docs.test.mjs`：新增文档预算闸门（AGENTS.md ≤8 KiB、单个 @import 目标 ≤16k chars、展开总量 ≤32k chars、plans 索引不留部署史段且有归档指针）。
- `web/src/components/agent/ContextSavingIndicator.tsx` + zh/en locale + `web/tests/context-saving-chip.test.mjs`：胶囊区分「上次真实 usage」与「本地预检线（估算）」，估算口径 = 窗口 − 20k 输出预留 − 3k 余量，不冒充上游计量。
- `docs/upstream-customizations.yaml`：G-QZ 完整登记；G-BY 的 plans 索引锚点随历史搬移改指 `PLAN_HISTORY.md`。

### 针对性测试

| 测试 | 钉住什么 |
|---|---|
| `web/tests/agent-docs.test.mjs > instruction injection stays under every harness budget` | AGENTS.md 不再内联 plans 索引；文档注入总量留在 Claude/Codex 预算内；历史段已搬走 |
| `web/tests/context-saving-chip.test.mjs > 上次真实 usage 与本地预检线分开显示，预检线标为估算` | 两种口径分列且估算明确标注，不会把本地预检当真实 usage |

### TDD 与变异

- 先写上述两条测试并实跑：分别因断言失败变红（不是编译失败）。
- 变异 1：把 `@docs/plans/README.md` 加回 AGENTS.md → 预算用例断言失败，已还原。
- 变异 2：删掉 zh locale 的「估算」标记 → 胶囊用例断言失败，已还原。

### 门禁

- `go test -count=1 ./...`：首轮因既有 `server/internal/agent/proxy` TempDir 清理 flake 退出 1；单跑该包 `ok`，整轮重跑退出 0。
- `(cd web && npx tsc -b --noEmit)`：退出 0（首轮因隔离 worktree 缺 node_modules，链接已有依赖后通过；组件里 rows 的 tuple 类型错误已修）。
- `(cd web && npx vitest run)`：274 文件 / 969 通过 / 6 跳过，退出 0。
- `go run ./scripts/scope-id check`：399 组唯一、计划引用全部已登记。
- `make check-upstream`：提交前因新文件尚未进入 delta 且两处旧锚点随历史搬移失配而红；修 G-BY 锚点、补齐被误删的既有测试、显式 pathspec 提交后重跑（结果见下方提交后记录）。

### 复现路径与证据边界

- 实跑：在修好的 worktree 用真实 Claude CLI（`ok`）新开会话，instruction attachment 里项目 AGENTS.md 为 5,035 chars、`docs/agent/rules/README.md` 为 558 chars，**不再出现 `docs/plans/README.md`**；该请求有真实 usage（input 22,049），没有 synthetic `Prompt is too long`。
- 未实跑：没有在隔离实例里复刻「145k 基线 + 主动发第二条」的完整老会话路径（当前老 transcript 已随会话迁移到 worktree 项目目录，直接 resume 会写活会话）。因此这里只声称「注入源已实测从 146,783 chars 降到 5,593 chars 且不再含 plans 索引」，不声称完整老路径已再次实跑通过。
