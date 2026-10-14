# 后端规范 · 短期待办（非长期规则，分批还）

## 必拆文件（>1500 非测试，只列不改；每次 1 个 + `make test`）

```
agent/codex/session.go 1586 · gitview/gitview.go 1575
```

## 已知约束缺口

- [x] `usecase/session.go` 已按列表、fork、读取、生命周期、prompt、命名、runtime、turn identity、SendMessage、subagent、stream event、agent event/path、model validation 拆分；当前 1455 行，低于 1500 行硬线（`wc -l`）。
- [x] `session/manager.go` 的 search、exchange/aux、binding、SQLite、list、alias、meta 行读写、related-files、exchange identity、lifecycle 与窗口读取职责已分模块；当前 749 行，低于 800 行软线和 1500 行硬线（`wc -l`）。
- [x] `api/http.go` 的 Git、files/directories、session handlers 与 static serving 已按域迁出；`handleSessionRepoint` 留在原文件且未改。当前 658 行，低于 800 行软线。
- [ ] `cli/cmd` 与 `server/cmd/mindfs-server` 双入口：发布走前者，后者仅 dev 用——
      修启动逻辑只修 `cli/cmd`（历史踩坑：修错入口导致真机不生效）。
