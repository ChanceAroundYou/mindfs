# 硬线清零：web/tests 与 codex 工具卡导入器

> 状态：**已完成**（2026-10-16）。
> 验收：`wc -l` 下全仓手写源文件无一超 500；`make test` / `tsc --noEmit` / `npm test` /
> `go run ./scripts/check-upstream` 全绿；fork 三件套（登记 + 针对性测试 + `Scope: G-X`）齐备。
> 结果见文末「五、执行结果」。

## 一、问题：硬线其实没清干净

`backend-file-size-300-500.md`（B1–B10，同日完成）把后端清到了 1 个例外。但前端只清了
`web/src` —— **`web/tests` 从来没进过 `file-size.md` 的适用范围**。规则索引
（`frontend/README.md`）写着范围是 `web/src` + `web/tests`，`file-size.md` 却只写了
`web/src/`。这条自相矛盾让三个超线文件一直没人管：

| 文件 | 行数 |
|---|---|
| `web/tests/session-window.test.mjs` | 787 |
| `web/tests/task-panel-unification.test.mjs` | 665 |
| `web/tests/source-map.mjs` | 655 |

后端还剩一个「论证过边界、本轮及以后都不再拆」的例外（`codex/importer_tool_calls.go` 850），
本轮一并拆掉 —— 用户 2026-10-16 明确指示「不需要什么论证，强拆」。

`web/src` 本身硬线为 0（最大 498），本轮不动；软线区 77 个文件留待下一轮。

## 二、拆法

### 2.1 `codex/importer_tool_calls.go` 850 → 3 个文件

同包内移动，无签名变化、无 import 路径变化、零数据迁移。切的是**三块互不调用的工具**，
不是那个被论证过的「按工具类型切 switch」（那个 switch 原样留在 A 里）：

| 文件 | 内容 | 行数 |
|---|---|---|
| `importer_tool_calls.go` | 入口与归一化：`parseImportedCodexToolCall` → `importedCodexWrappedTool` | ~431 |
| `importer_tool_call_javascript.go` | JS 字面量解析：`importedCodexToolCallIndex` → `isImportedJavaScriptIdentifierByte` | ~175 |
| `importer_tool_call_output.go` | patch 文本与输出解析：`quoteImportedShellCommand` → `importedCodexAssistantLine` | ~232 |

`quoteImportedShellCommand` 是 shell 引号，不是 JS 解析，归 C。

### 2.2 `web/tests/source-map.mjs` 655 → 1 + 3 个文件

`MODULES` 是一张 152 条、555 行的纯数据表。按**逻辑键的顶层目录**分片，主文件合并后
仍导出同一个 `MODULES`（消费方一行不用改）：

- `source-map-modules-components.mjs`（78 条）
- `source-map-modules-services.mjs`（63 条）
- `source-map-modules-app.mjs`（app / hooks / shared / i18n / 入口，11 条）
- `source-map.mjs` 只剩头部说明、合并与 5 个函数

### 2.3 `web/tests/session-window.test.mjs` 787 → 3 个文件

按文件里既有的 `// ── 小节 ──` 边界切，每个文件自带走自己的读源前言：

| 文件 | 小节 |
|---|---|
| `session-window.test.mjs` | 窗口/overlay 契约（session 域导出、SessionViewer 窗口态、后端 manager/http 契约、纯逻辑、方案 C） |
| `session-window-merge.test.mjs` | 合并与清理（切会话闪、composeLoadedExchanges、重放适配层、dropTransientExchanges） |
| `session-window-cache.test.mjs` | 淘汰与实时契约（缓存淘汰方向、R4 probe、F2/F3/F4、窗口尺寸、身份 overlay） |

### 2.4 `web/tests/task-panel-unification.test.mjs` 665 → 3 个文件

| 文件 | 小节 |
|---|---|
| `task-panel-unification.test.mjs` | 三面板共用 StageEditor/PanelShell、角色切换、新建任务的 agent/模型与「立即执行」 |
| `task-panel-options.test.mjs` | 字段说明、任务名同排、下拉自绘、阶段选项常驻、关闭确认、底部空白、移动端键盘 |
| `task-card-status.test.mjs` | 已执行阶段只读、任务状态不绑会话/worktree、SW 缓存策略、状态色单源 |

## 三、规则缺口（顺手修）

- `docs/agent/rules/frontend/file-size.md` 的适用范围补成 `web/src/` + `web/tests/`，
  与 `README.md` 一致；并写明测试文件同样适用（对齐后端 `file-size.md` 的「含测试」）。
- `docs/agent/rules/backend/TODO.md` 的「超硬线」表已过期（B10 拆完，8 行全部失效，
  `gitview_test.go` 已不存在），表头还指向已 Completed 的计划。清掉，改为指向本节结论。

## 四、验收

```bash
find web/src web/tests server cli -name '*.ts' -o -name '*.tsx' -o -name '*.go' | xargs wc -l | awk '$1>500'
make test && (cd web && npx tsc --noEmit && npm test)
go run ./scripts/check-upstream
```

拆完每个文件跑一次；全绿后走 `bash scripts/deploy-all.sh` 收尾。

## 五、执行结果（2026-10-16）

| 文件 | 前 | 后 |
|---|---|---|
| `server/internal/agent/codex/importer_tool_calls.go` | 850 | 442 |
| ↳ `importer_tool_call_javascript.go` / `importer_tool_call_output.go` | — | 181 / 241 |
| `web/tests/source-map.mjs` | 655 | 108 |
| ↳ `source-map-modules-{components,services,app}.mjs` | — | 238 / 126 / 211 |
| `web/tests/session-window.test.mjs` | 787 | 265 |
| ↳ `session-window-merge.test.mjs` / `session-window-cache.test.mjs` | — | 296 / 219 |
| ↳ `session-window-sources.mjs`（三文件共用读源前言） | — | 76 |
| `web/tests/task-panel-unification.test.mjs` | 665 | 246 |
| ↳ `task-panel-options.test.mjs` / `task-card-status.test.mjs` | — | 285 / 190 |

`web/src` 与 `web/tests` 现在**零个**超过 500 行的手写文件（最大 498）。

**规则缺口已闭环**：`file-size.md` 补上适用范围，`reviewer-architecture-rules.test.mjs` 的
500 行门禁从 `web/src`（`.ts/.tsx`）扩到 `web/tests`（含 `.mjs`）—— 用 501 行的探针文件
变异验证过会红。

**行为不变的证明**：三个测试文件的拆分逐行比对过原始 body 的非注释行多重集，
「只在原文件」为空（新增的只有各文件自带的读源前言与跨文件共用的两个切片常量）；
`source-map` 的 152 条键值对 `git show HEAD:` 逐字一致；Go 侧比对过 `^func ` 签名集合与
全部非 import/空行代码行，只多一行 `agenttypes` import。

**门禁**：`make test` 绿；`tsc --noEmit` 0 错；`npm test` 173 文件 / 353 例通过（+4 文件 +4 例，
即新增的四个测试文件）；`go run ./scripts/check-upstream` 绿。

**未做（明确留给下一轮）**：`web/src` 软线区（301–500）的 77 个文件。
