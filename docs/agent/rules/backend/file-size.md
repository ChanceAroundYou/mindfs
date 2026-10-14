# 后端文件长度与拆分（长期规则）

- **口径**：物理行 `wc -l`，与前端一致。
- **红线**：软 800 / 硬 1500（Go 文件含测试倾向更长，红线放宽；`_test.go` 不计入）。
- **豁免**：生成物、`testdata/`、`*_test.go`。

## 超了怎么拆（判据优先级）

1. 按 handler/用例拆：`http_*.go` 一域一文件（现状已是）；`usecase/*.go` 一主题一文件。
2. 大 service 拆子逻辑：`manager.go` 按游标/同步/绑定/搜索拆 `manager_*.go`（已有先例，继续）。
3. 纯类型/常量下沉：接口放 `types/`，错误放 `apperr`，常量放文件头 `const` 块。

## 必拆清单（>1500 非测试，按当前工作区实测）

```
server/internal/agent/codex/importer.go 1887
server/internal/agent/codex/session.go 1586
server/internal/gitview/gitview.go 1575
```

执行原则：每次 1 个 + `make test` 全绿；SQLite 相关先备回滚说明。
