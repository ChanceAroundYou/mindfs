# MindFS 开发规范

> 长期规则（会被反复执行的约束）。一次性待办在各自的 `TODO.md`，做完即销。
> 规范解释的是「怎么写代码」，架构事实（单读 goroutine、多账户分区、控制面/数据面）在
> 仓库根 `AGENTS.md`——那是本规范的上游，冲突时以 `AGENTS.md` 为准。

| 目录 | 范围 | 索引 |
|---|---|---|
| `common/` | 两端通用（提交规范、fork 定制三件套） | 见下方 |
| `frontend/` | `web/src` + `web/tests` | [README](frontend/README.md) |
| `backend/` | `server/` + `cli/`（Go） | [README](backend/README.md) |

## common

- [提交与 fork 定制](common/commit-and-customization.md) — Conventional Commits（中文）、
  `Scope: G-X`、`docs/upstream-customizations.md` 三件套。**本项目最硬的约束。**

## 执行方式

- **新代码**：强制遵守；命名检查 + 复用门槛 + 测试要求进 CI/自查。
- **存量**：按各 `TODO.md` 分批还，每批「2–3 个 + 一次构建/测试」，零行为变化。
- **reviewer**：按各 `reviewer-checklist.md` 打勾，红灯挡合并。
