# 后端规范 · 短期待办（非长期规则，分批还）

## 必拆文件（>1500 非测试，只列不改；每次 1 个 + `make test`）

当前没有超线文件；按工作区 `wc -l` 核对。

## 已知约束缺口

- [x] `usecase/session.go` 已按列表、fork、读取、生命周期、prompt、命名、runtime、turn identity、SendMessage、subagent、stream event、agent event/path、model validation 拆分；当前 1455 行，低于 1500 行硬线（`wc -l`）。
- [x] `session/manager.go` 的 search、exchange/aux、binding、SQLite、list、alias、meta 行读写、related-files、exchange identity、lifecycle 与窗口读取职责已分模块；当前 749 行，低于 800 行软线和 1500 行硬线（`wc -l`）。
- [x] `api/http.go` 的 Git、files/directories、session handlers 与 static serving 已按域迁出；`handleSessionRepoint` 留在原文件且未改。当前 658 行，低于 800 行软线。
- [x] `cli/cmd` 与 `server/cmd/mindfs-server` 双入口已审计：`Makefile build`、`scripts/build-all.sh` 发布走 `cli/cmd`，`dev-backend` 仅跑 `server/cmd/mindfs-server`；`startup_config_test.go` 门禁锁定该边界和缺省配置行为。
