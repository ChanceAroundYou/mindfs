# 后端数据归属与迁移（长期规则）

- **归属**：会话库/任务库按账户分（`MetaDir()`），上传/批注共享（`SharedMetaDir()`，
  不看账户）。主账户两者相同。改路径代码别用错解析器。
- **Schema**：只增不改 + 惰性迁移（与前端 database.md 同源，约束后端实现侧）。
  `sessions.pinned_at` 已退役只留列，不再读写——退役列只留不删是既定做法。
- **提交要求**：碰 `manager.go` / `registry.go` / SQLite schema 的改动，提交信息注明库文件与回滚方式。
- **多账户实例**：共享用同一份实例；看板/定时任务每账户一个实例，用本账户 session manager
 （`scheduled/tasks.go` 的 `registry.GetSessionManager`）。
