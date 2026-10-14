# 后端开发规范 · 索引

> 范围：`server/` + `cli/`（Go）。前端见 `frontend/`。
> 原则：新代码与存量统一遵守规则；未完成的一次性事项记录在 `TODO.md`。
> 配套：`AGENTS.md` 的关键架构事实（单读 goroutine、Interrupt 超时、fork 三件套）是本规范的上游，
> 本规范只收"代码组织"类约束，不重复架构事实。

| 文件 | 主题 | 一句话 |
|---|---|---|
| `directory.md` | 目录划分 | api/appcontext/usecase/session/agent 各住什么 |
| `file-size.md` | 文件长度与拆分 | Go 文件统一长度要求与拆分方法 |
| `naming.md` | 命名规范 | 包/文件/导出/错误名 |
| `layering.md` | 分层与并发铁律 | ws→usecase→manager 单向，handler 异步派发 |
| `data-ownership.md` | 数据归属与迁移 | MetaDir/SharedMetaDir，只增不改 |
| `testing.md` | 测试规范 | `*_test.go` 同目录，`make test` |
| `reviewer-checklist.md` | reviewer 执行指南 | 按表打勾，红灯挡合并 |
| `TODO.md` | 短期待办 | 尚未完成的一次性事项（非长期规则） |

通用规则见 `../common/commit-and-customization.md`（提交 + fork 定制三件套）。
