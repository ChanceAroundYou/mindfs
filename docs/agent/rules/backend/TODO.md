# 后端规范 · 短期待办（非长期规则，分批还）

## 必拆文件（>1500 非测试，只列不改，每次 1 个 + `make test`）

```
usecase/session.go 4602 · session/manager.go 3905 · agent/claude/session.go 3074
api/http.go 3024 · api/agent_api_provider.go 2484 · kanban/service.go 1989
agent/codex/importer.go 1887 · agent/codex/session.go 1586 · gitview/gitview.go 1575
api/ws.go 1403 · agent/claude/importer.go 1398 · api/appcontext.go 1378
agent/acp/process.go 1321 · agent/probe.go 1288 · api/stream_hub.go 1224
update/service.go 1171 · usecase/fs.go 1158 · fs/fs.go 1135
cli/cmd/mindfs.go 1059 · api/http_tasks.go 1048 · gitview/worktree_finish.go 1040
```

## 已知约束缺口

- [ ] `usecase/session.go` 4892 行是头号拆分对象（按 session CRUD/消息/同步/归档拆）。
- [ ] `api/http.go` 3024 行：路由注册与 handler 实现混装，handler 下沉到 `http_*.go`。
- [ ] `cli/cmd` 与 `server/cmd/mindfs-server` 双入口：发布走前者，后者仅 dev 用——
      修启动逻辑只修 `cli/cmd`（历史踩坑：修错入口导致真机不生效）。
