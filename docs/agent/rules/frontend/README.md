# 前端开发规范 · 索引

> 范围：`web/src` + `web/tests`。后端另开第二轮。
> 原则：零行为变化优先；新代码与存量统一遵守规则。一次性工作完成后从 `TODO.md` 移除。
> 配套：`docs/upstream-customizations.yaml` 登记制度不变（fork 定制三件套照旧）。

| 文件 | 主题 | 一句话 |
|---|---|---|
| `directory.md` | 目录划分 | 源目录职责与可执行判据 |
| `file-size.md` | 文件长度与拆分 | 软 300 / 硬 500（`wc -l`），统一适用，拆法优先级 |
| `naming.md` | 命名规范 | 按职责命名，后缀即语义 |
| `layering.md` | 视图与功能分离 | view/logic/service/presenter 四层 + 三铁律 |
| `data-flow.md` | 单向数据流 | 服务端为准，WS 增量合并 |
| `reuse.md` | 复用原则 | 2 次就抽，就近优先 |
| `testing.md` | 测试规范 | 新复用件必须带测试，切 Vitest |
| `database.md` | 数据库与数据结构 | 只增不改 + 惰性迁移 |
| `i18n.md` | 国际化 | 双语键集不许漂移，禁硬编码文案 |
| `reviewer-checklist.md` | reviewer 执行指南 | 按表打勾，红灯挡合并 |
| `TODO.md` | 短期待办 | 尚未完成的一次性事项（非长期规则） |

通用规则见 `../common/commit-and-customization.md`（提交 + fork 定制三件套）。

执行节奏：新代码和存量都遵守同一规则；拆分工作按小批完成，每批运行相关检查。
