# 后端文件拆分到「软 300 / 硬 500」+ 同包内重组

> 状态：Active。日期：2026-10-16。
> 目标：后端 Go 文件（含 `*_test.go`）统一套用 **软 300 / 硬 500** 行限制（与前端口径一致），
> 并在同包内重组文件夹结构；单向依赖可抽子包的另行评估。
> 约定验收：`make test` 全绿 · `go run ./scripts/check-upstream` 全绿 · 零行为变化（同包内移动）·
> fork 三件套（登记 + 针对性测试 + `Scope: G-X`）。

## 一、为什么要改规则口径

后端原口径是软 2000 / 硬 4000，前端是软 300 / 硬 500。两条规则服务同一个目的
（「一个文件一个职责，改哪读哪」），数值差 6 倍没有技术理由 —— 后端已经有
`server/internal/api/usecase/session.go` 1496 行、`appcontext.go` 1465 行这类
「单个文件里 4–5 个不相干职责」的形态。本次把后端对齐到前端口径。

存量测量（非测试 161 文件 / 测试 134 文件）：

| 口径 | 源码 | 测试 |
|---|---|---|
| >500 | **43** | **18** |
| 300–500 | 27 | 9 |
| 200–300 | 26 | 20 |
| ≤300 | 65 | 87 |

## 二、方案的边界（为什么同包内拆，不切子包）

`server/internal/api` 的符号流是**双向的**，不是预设的「handler → helper」单向：

- `http.go` 反向引用 20+ 个 handler 文件，同时被 15 个文件反向引用；
- `agent_api_provider.go ↔ _apply.go ↔ _files.go` 是**三向环**（29/7/20 个 ident）；
- `stream_hub.go ↔ appcontext.go ↔ http_sessions.go` 也是环；
- `http_session_message.go` 反向依赖 `ws.go`。

`api/usecase` 同构（`session_send.go` 用 `session.go` 17 个 ident，反向 4 个）。

切子包要先打断这些环（接口提取 + 参数重构），那是**行为风险**，不是移动风险。
因此本轮只做**同包内移动**；单向依赖抽子包单独评估后**放弃**（见 §五）。

## 三、批次

| 批次 | 内容 | 风险 |
|---|---|---|
| B1 | 规则文档 + TODO + 本计划 | 零 |
| B2 | 叶包：`agent/types`、`update`、`webpush`、`auth`、`fs`、`commandexec` | 零 |
| B3 | `session` 包源 + 测试 | 低 |
| B4 | `api/usecase` 源 | 中 |
| B5 | `api/usecase` 测试 | 低 |
| B6 | `kanban` 源 + 测试 | 中 |
| B7 | agent 运行时源（probe/config/pool/claude/codex/acp） | 中 |
| B8 | agent 导入器 + 测试 | 低 |
| B9 | `api` 源 + 测试（单读 goroutine 铁律，逐文件提交） | 高 |
| B10 | 收尾：全量门禁 + TODO 勾项 + 索引标 Completed | 零 |

执行原则：每批独立提交，批内可多文件；B9 内部**逐文件**提交并单独跑
`go test ./server/internal/api/...`。

## 四、兼容策略

- 全部为同包内移动或同包内新增文件：**无签名变化、无 type 位置变化（跨文件同包可见）、
  无 import 路径变化**。
- 不碰 SQLite schema、`registry.json`、任何落盘格式；零数据迁移。
- 现有调用方零改动；Go 编译器当场校验符号解析，`make test` 绿即移动正确。
- fork 定制清单 `docs/upstream-customizations.yaml`：每批为被拆分的**已登记**文件
  追加新文件条目（旧文件仍在 delta 中，断言 2 不报；追加是满足断言 1 的必要条件）。
- 记账粒度按「一批一组」，`boundary` 列多个 `X→Z` 映射，避免 55 个新组把清单
  信息密度冲垮（G-GJ..G-GM 一格一组是因为它们各自单一主题；本方案是批量重组）。

## 五、原方案的调整及理由（实现期发现）

1. **`agent/probe` 与 `agent/config` 不抽子包，改为同包拆文件。**
   实测 `probe.go` 反向依赖 `agent.Pool`/`Config`/`Definition`/`Protocol`，抽子包必须引入
   接口重构（`RuntimeOpener`/`RuntimeCloser`）才能打破包间环 —— 那是行为风险。
   `config.go` 虽无反向依赖（`agent` 包内 0 处其它文件用它的未导出符号），但被
   **9 个外部文件**以 `agent.Config`/`agent.Definition` 引用，抽走要改 9 处 import 与
   232 组清单的锚点，收益（少一层目录）远小于代价。故两处均改为同包内拆文件。
2. **`agent/codex/importer_tool_calls.go`（850）与 `kanban/worktree_finish_stage.go`（689）
   本轮不拆。** 二者分别是 G-GK / G-GI 刚拆过、边界已论证过的产物，再拆等于推翻
   前一轮结论。登记到软线待办。

## 六、验收

```bash
make test                            # go test ./... 全绿
go run ./scripts/check-upstream      # 定制清单全绿
```

每批改完跑一遍；B9 每文件跑 `go test ./server/internal/api/...`。
