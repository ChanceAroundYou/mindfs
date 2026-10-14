# 后端文件长度与拆分（长期规则）

- **口径**：物理行 `wc -l`，与前端一致。
- **红线**：软 800 / 硬 1500（Go 文件含测试倾向更长，红线放宽；`_test.go` 不计入）。
- **豁免**：生成物、`testdata/`、`*_test.go`。

## 超了怎么拆（判据优先级）

1. 按 handler/用例拆：`http_*.go` 一域一文件（现状已是）；`usecase/*.go` 一主题一文件。
2. 大 service 拆子逻辑：`manager.go` 按游标/同步/绑定/搜索拆 `manager_*.go`（已有先例，继续）。
3. 纯类型/常量下沉：接口放 `types/`，错误放 `apperr`，常量放文件头 `const` 块。

## 必拆清单（>1500 非测试，实测；只列不改）

```
usecase/session.go 1782 · session/manager.go 3905 · agent/claude/session.go 3074
api/http.go 3024 · api/agent_api_provider.go 2484 · kanban/service.go 1989
agent/codex/importer.go 1887 · agent/codex/session.go 1586 · gitview/gitview.go 1575
api/ws.go 1403 · agent/claude/importer.go 1398 · api/appcontext.go 1378
agent/acp/process.go 1321 · agent/probe.go 1288 · api/stream_hub.go 1224
update/service.go 1171 · usecase/fs.go 1158 · fs/fs.go 1135
cli/cmd/mindfs.go 1059 · api/http_tasks.go 1048 · gitview/worktree_finish.go 1040
```

执行原则：每次 1 个 + `make test` 全绿；SQLite 相关先备回滚说明。
