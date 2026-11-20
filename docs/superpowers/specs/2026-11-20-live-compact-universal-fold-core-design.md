# Live Compact 通用内核 + Harness 适配器 · 设计（2026-11-20）

> 状态：**待用户批准**。未写任何生产代码。
> 上游：[原生活压缩设计](2026-11-18-mindfs-native-live-compact-design.md)、
> [折叠择时与计量设计](2026-11-20-live-compact-fold-timing-and-accounting-design.md)。
> 用户 2026-11-20 决定：**A. 抽内核 + 适配器**，本轮**只做 Codex（Responses）**。

---

## 1. 结论先行

1. Codex 可以接，且比 Claude 少两个部件：不需要 HTTP hook、不需要 URL token。
2. 现有 `proxy` 包里真正与协议相关的只有三块：**扫描/定位字节区间、拼接改写、SSE 用量提取**。其余（rail/target/keep/冷缓存策略、存档、指针、承诺集、账本、急停、估算）本来就与协议无关，只是与 Anthropic 的 `wireMessage/wireBlock` 长在了一起。
3. 通用形态 = **协议无关内核 `fold` + 每个 harness 一个 `Adapter`**；`proxy` 退化为传输层。
4. 迁移采用「纯移动 → 等价接缝 → 新适配器」三段式，每一步都保持既有 200+ 用例全绿。

---

## 2. 探针证据（2026-11-20，隔离 `CODEX_HOME`，`gpt-6-luna`）

隔离目录 `/tmp/codex-probe`，本地捕获代理只记录请求体、原样转发到 llmux；**未触碰 `~/.codex`**。

| # | 结论 | 证据 |
|---|---|---|
| 1 | Codex 每轮重发完整历史 | 请求体 `store:false`、无 `previous_response_id`；第 2 个请求 `input` 由 7 项增至 10 项，含上一轮 `reasoning` / `custom_tool_call` / `custom_tool_call_output` |
| 2 | 工具结果有两种形态 | `custom_tool_call_output.output = [{type:"input_text",text}]`（`exec` 自定义工具，真实会话 1635 次）；`function_call_output.output = "字符串"`（函数工具，148 次） |
| 3 | 改写历史不被 reasoning 校验打断 | 把 452 B 工具输出换成指针文本重发 → `200`；追问「上次工具输出是什么」→ 模型**逐字引用指针** |
| 4 | 用量口径与 Anthropic 相反 | `input_tokens=13582` 已含 `cached_tokens=13246` 与 `cache_write_tokens=333` |
| 5 | 不改用户 config 即可接入 | `codex app-server -c model_providers.<name>.base_url=http://127.0.0.1:PORT/v1` 生效，请求确实落在代理上 |

附带事实：`gpt-6-luna` 窗口 272,000（effective 95%）；Codex 自带 `remote_compaction_v2`，压缩事件已由 app-server 的 `contextCompaction` 映射为 `types.EventTypeCompact`（`codex/session_events.go:150`）；请求头自带 `session-id` / `prompt_cache_key`（均为 codex thread id）。

---

## 3. 目标与非目标

**目标**

1. 把与协议无关的折叠能力抽成 `server/internal/agent/fold`，`proxy` 只剩传输与每会话状态机。
2. 新写 `fold/responses` 适配器，让 Codex 会话在 `mode=on` 下与 Claude 获得同等折叠、账本与胶囊。
3. 保持 `mode=off` 零成本、`shadow` 可预演、`DISABLED` 急停、隔离 ConfigDir 只观测这四条既有语义。
4. 第三个协议（如 chat-completions）将来只需新增一个适配器，不改内核。

**非目标**

1. 不做 ACP agent 适配（各家是否认 `*_BASE_URL`、说什么协议要逐个验证，不能承诺「全部」）。
2. 不做 OpenAI 兼容 `/chat/completions` 适配器（本轮只做 Responses）。
3. 不接管 Codex 的 `remote_compaction_v2`；两者正交，我们的折叠是请求历史编辑。
4. 不代理 ChatGPT 登录模式（`default` profile）：它的路由由 auth 方式决定，`model_providers.openai` 能否覆盖未验证，显式记 `gate=unsupported`。
5. 不改变 Anthropic 侧任何阈值与行为；P1/P2 是纯移动与等价接缝。

---

## 4. 架构

```
server/internal/agent/
  fold/                       协议无关内核
    config.go                 rail/target/keep/cold/prefix、Normalized()
    estimate.go               字节 → token 估算
    types.go                  Span / Result / Round / Conversation / Replacement / Usage / Verdict
    adapter.go                Adapter 接口
    gate.go                   decideFoldBudget、foldContext
    cold.go                   冷缓存三信号（纯函数）
    plan.go                   候选资格、预算、commit 集
    render.go                 指针渲染
    archive.go                内容寻址存档
    pointer_cache.go          渲染缓存
    commits.go / commit_log.go  单调承诺集与持久化
    ledger.go                 每会话账本
    engine.go                 Engine.Fold：扫描 → 决策 → 渲染 → 存档 → 拼接 → 校验
  fold/anthropic/             Messages 适配器（从 proxy 迁入）
  fold/responses/             Responses 适配器（新）
  proxy/                      传输层
    proxy.go                  路由、转发、SSE 旁路、压缩信号
    live_compact_fold_request.go  每会话状态机 + 调 Engine
    live_compact_turn.go      请求/响应配对
    live_compact_worker.go    异步观测队列
```

`proxy` 里用**类型别名**保持现有调用点不变：

```go
type LiveCompactConfig = fold.Config
type ContextSaving      = fold.ContextSaving
```

`pool.go` / `startup.go` / `main.go` 的配置传递一行都不用改。

---

## 5. Adapter 接口

```go
package fold

type Span struct{ Start, End int }

// Result 是一个已配对的工具结果。只有 Parts 恰好一个时才可原地折叠。
type Result struct {
    CallID       string
    ToolName     string
    InputSummary string   // tool_use 的参数摘要，进指针
    Parts        []Span   // 文本字面量的字节区间
    Bytes        int
    Tokens       int
    ErrorText    bool     // 成功但文本像失败报告
    Failed       bool     // is_error
    NonText      bool     // 结果里含图片/文档/未知块
}

// Round 是一次工具调用及其全部结果；开放轮次（有调用无结果）不可折。
type Round struct {
    Results []Result
}

type Conversation struct {
    Model      string
    Rounds     []Round
    Estimated  int
}

type Replacement struct {
    Span    Span
    Literal string
}

type Usage struct {
    ContextTokens int // 本次请求的完整上下文
    CacheRead     int
    CacheWrite    int
    Output        int
    RewriteTokens int // 折叠后按全价重算的部分
    Source        string // "response" | "missing"
}

type GuardVerdict struct {
    Block  bool
    Reason string
}

type Adapter interface {
    Name() string
    // Scan 解析请求体，产出按线序排列的轮次。未知/畸形结构必须返回错误，
    // 由 Engine 回退原 body（与今天 I7 相同）。
    Scan(body []byte) (*Conversation, error)
    // Splice 只替换给定区间，其余字节逐字节保留。
    Splice(body []byte, reps []Replacement) ([]byte, error)
    // Usage 从响应（SSE 或整包）提取并归一化用量。
    Usage(resp []byte) (Usage, bool)
    // Guard 决定该模型是否允许代理侧改写。
    Guard(model string, cfg Config) GuardVerdict
    // Window 返回模型上下文窗口；0 表示未知，内核退回配置值。
    Window(model string) int
}
```

内核不 import 任何具体适配器；适配器不 import `proxy`。这条边界由一个 import 守卫测试钉住。

---

## 6. 内核迁移清单（P1/P2 纯移动）

| 现文件（`proxy/`） | 去向 | 处理 |
|---|---|---|
| `estimate.go` | `fold/estimate.go` | 纯移动 |
| `live_compact_archive.go` | `fold/archive.go` | 纯移动（已是独立类型） |
| `live_compact_pointer.go`、`pointer_cache.go` | `fold/` | 纯移动 |
| `live_compact_commits.go`、`commit_log.go` | `fold/` | 纯移动 |
| `live_compact_ledger.go` | `fold/ledger.go` | 纯移动，JSON 形状不变 |
| `live_compact_fold_gate.go` | `fold/gate.go` | 输入由 `scanMessage` 换成中性 `Round/Result` |
| `live_compact_cold.go` | `fold/cold.go` | 改成纯函数，吃状态快照而非 `*Server` |
| `live_compact_fold_plan.go`、`fold_render.go` | `fold/plan.go`、`render.go` | 候选资格与预算改为吃 `[]Round` |
| `live_compact.go` 的配置部分 | `fold/config.go` | 其余（会话状态）留在 proxy |
| `live_compact_scan.go`、`scan_request.go` | `fold/anthropic/scan.go` | 协议专属 |
| `live_compact_wire.go`、`wire_analysis.go`、`wire_drift.go` | `fold/anthropic/wire.go` | 协议专属 |
| `live_compact_fold_splice.go` | `fold/anthropic/splice.go` | 协议专属 |
| `usage.go` | `fold/anthropic/usage.go` + `fold/usage.go` | 解析留下，归一化上移 |
| `proxy.go`、`fold_request.go`、`turn.go`、`worker.go`、`log.go` | 留在 `proxy/` | 传输与会话状态 |

工具轮次配对（`pairToolRounds`）是协议专属，留在 `fold/anthropic/`；**资格判断**（keep 窗口、大块放行、预算、承诺集）上移内核。

---

## 7. Responses 适配器

### 7.1 结构

`input` 是数组，元素 `type` 决定角色：

| 元素 | 处理 |
|---|---|
| `additional_tools` / `message` | 不可折，原样保留 |
| `reasoning` | 不可折，**且绝不改写**（`encrypted_content` 原样回传） |
| `custom_tool_call` / `function_call` | 开启一轮，按 `call_id` 记工具名与参数摘要 |
| `custom_tool_call_output` | 关闭对应调用；`output` 是 `[{type:"input_text",text}]`，折其中的 `text` |
| `function_call_output` | 关闭对应调用；`output` 是字符串，折字符串字面量 |

配对规则与 Anthropic 一致：**开放轮次（有调用无结果）永不可折**；一个轮次只有在所有 `call_id` 都有结果后才关闭。

### 7.2 折叠前提

- `store` 必须为 `false` 且顶层无 `previous_response_id`；否则 `gate=unsupported`（有状态续接看不到历史，无法改写）。
- 只替换 `*_output` 内部的文本区间；`reasoning`、`*_tool_call`、`additional_tools` 的字节逐字保留。
- 改写后重新 `Scan` 校验：轮次数、每个 `call_id` 的配对关系、非目标元素的字节全部不变；任一不符回退原 body。

### 7.3 前缀安全

探针第 3 条已证明改写不会触发 reasoning 校验失败，模型也确实读到了指针。因此 Responses 适配器的 `Guard` 恒返回放行；接口保留，将来若出现校验再加。

### 7.4 阈值

`gpt-6-luna` 窗口 272,000。Responses 默认：`rail=200000`、`target=60000`、`cold_rail=120000`、`keep_rounds=3`、`keep_tail=32000`，其余继承全局。窗口由 `Window(model)` 提供（先内置已知表，未知回退 200k）。

---

## 8. 注入与会话身份

### 8.1 注入

- 启动 codex app-server 时读取 `config.toml` 的 `[model_providers]`，对**每个显式声明的 provider** 追加：
  `-c model_providers.<name>.base_url=http://127.0.0.1:{port}/c/{name}/v1`
- 只有显式声明的 provider（如 `llmux`）会被代理；内置 `openai`（ChatGPT 登录）不在表里，保持直连。
- 代理路由 `/c/{provider}/v1/...` → 转发到该 provider 原始 `base_url` + 剩余路径。原始地址在注册时从同一份配置读取，只存内存，不写日志、不落共享配置。

### 8.2 会话身份

- Responses 请求头自带 `session-id`（= codex thread id）；缺失时退回 `prompt_cache_key`。
- `codex.OpenSession` 返回后，pool 调 `proxy.RegisterThread(threadID, mindfsSessionKey)`；账本、胶囊、`/api/sessions/{key}/context-saving` 都按这条映射解析。
- thread 未知时按「只观测」处理，不折叠（与隔离 ConfigDir 的规则一致）。

### 8.3 网络失败

代理转发失败必须原样把上游错误回给客户端，不吞、不伪造 200；与 Claude 路径同一套透明转发逻辑。

---

## 9. 用量归一化

| | Anthropic | Responses |
|---|---|---|
| 上下文 | `input + cache_creation + cache_read` | `input_tokens`（**已含**缓存） |
| 缓存读 | `cache_read_input_tokens` | `input_tokens_details.cached_tokens` |
| 缓存写 | `cache_creation_input_tokens` | `input_tokens_details.cache_write_tokens` |
| 全价重算 | `input + cache_creation` | `input - cached_tokens` |

`Usage.RewriteTokens` 由适配器填，内核与账本不再自己加。`usage_source="missing"` 语义不变。

---

## 10. 压缩信号与冷缓存

- Claude：`PostCompact` → `POST /compact-signal {"token":...}`（不变）。
- Codex：`EventTypeCompact` 已由 app-server 事件产生；pool 收到后调 `proxy.SignalCompact(threadID)`，代理置 `pendingColdFold`，下一拍消费一次。
- 冷判据（`compact` / `idle` / `no-read`）完全复用现有实现，只把状态读取改成中性输入。

---

## 11. 配置

新增按 harness 覆盖：

```json
"live_compact": {
  "mode": "on",
  "rail_tokens": 100000,
  "harness": {
    "codex": {
      "enabled": true,
      "rail_tokens": 200000,
      "target_tokens": 60000,
      "cold_rail_tokens": 120000
    }
  }
}
```

- `enabled=false` 时该 harness 只观测，不折叠（应急开关）。
- 只走 `config.json`，不加 flag；与既有 5 个 B7 键一致。
- 未命中覆盖的键回落全局值。

---

## 12. 分阶段实施（每阶段独立提交、门禁全绿）

**P1 内核抽包（纯移动）**
新建 `fold` 包，迁入第 6 节表格中「纯移动」的文件与测试；`proxy` 加类型别名。行为零变化。
验收：`go test ./server/internal/agent/...` 全绿；`fold` 不 import `proxy` 的守卫测试。

**P2 适配器接缝**
定义 `Adapter` 与中性类型；把 Anthropic 扫描/拼接/配对迁入 `fold/anthropic`；`Engine.Fold` 接管「扫描 → 决策 → 渲染 → 存档 → 拼接 → 校验」。
验收：同一批真实请求体，新旧两条路径输出**逐字节相同**（等价测试）；既有用例全绿。

**P3 Responses 适配器**
新写 `fold/responses`：Scan / Splice / Usage / Window；离线夹具来自探针请求体（脱敏：删 `encrypted_content`、替换 prompt 文本、截断）。
验收：配对、两种 output 形态、开放轮次、`store=true` 拒绝、改写后校验、用量归一化，各一组表驱动用例 + 变异验证。

**P4 传输与注入**
`/c/{provider}/v1/` 路由；provider 发现与 `-c` 注入；`RegisterThread`；代理侧按 `session-id` 建会话状态。
验收：隔离实例 + 真实 codex + `gpt-6-luna` 跑多轮工具调用，日志出现 `[live-compact] fold`，账本有记录，回答正确；关掉 `mode` 后零折叠。

**P5 收尾**
Codex 压缩信号接入冷折叠；胶囊与 `context-saving` 对 codex 会话可用；配置项贯通 `pool.go` / `startup.go` / 两个 `main.go`；登记 `docs/upstream-customizations.yaml`（G-MN）；计划与索引记录。

---

## 13. 测试与变异

| 层 | 用例 | 变异 |
|---|---|---|
| 边界 | `fold` 不 import `proxy`；`fold/responses` 不 import `proxy` | 加一行 import → 红 |
| 等价 | Anthropic 新路径 vs 旧路径字节相同 | 改指针渲染一个字符 → 红 |
| 配对 | 两种 output 形态、开放轮次、多结果轮次 | 允许开放轮次 → 红 |
| 前提 | `store=true` / `previous_response_id` → 不折 | 去掉检查 → 红 |
| 改写 | 只动 output 文本，reasoning/call/其它元素逐字不变 | 顺手重编码 reasoning → 红 |
| 校验 | 折后重扫，轮次/配对/非目标字节一致 | 跳过 verify → 红 |
| 用量 | `input` 已含缓存的归一化 | 按 Anthropic 公式相加 → 红 |
| 会话 | `session-id` 缺失退回 `prompt_cache_key` | 只读一个头 → 红 |
| 冷折叠 | `EventTypeCompact` → 下一拍 `cold=compact` | 不消费 pending → 红 |

真实环境验收：隔离实例（独立 `CODEX_HOME`、独立端口）+ 真实 `gpt-6-luna`，两轮工具调用产生至少一次折叠；随后用带指针的历史追问内容，答案与折叠前一致。

---

## 14. 风险与回滚

| 风险 | 处置 |
|---|---|
| 迁移引入回归 | P1/P2 是纯移动 + 等价测试，任何差异立即打红 |
| Codex 改写导致上游 5xx | `live_compact.harness.codex.enabled=false`（重启生效）；或 `DISABLED` 文件（1 秒生效） |
| 注入影响未预期的 provider | 只覆盖 `[model_providers]` 显式声明的条目；ChatGPT 登录不在其中 |
| 存档/账本路径冲突 | 复用同一 `<config-dir>/context`，按 harness 前缀分目录 |
| 夹具泄露用户内容 | 夹具必须脱敏后才进仓库 |

所有新配置启动读一次；急停文件仍是唯一免重启开关。

---

## 15. 未覆盖（明确记录）

1. ACP agent 适配：需要逐个探针验证协议与 `*_BASE_URL` 支持，不在本轮。
2. `/chat/completions` 适配器：不在本轮。
3. ChatGPT 登录模式（`default` profile）：显式 `gate=unsupported`。
4. `store=true` / `previous_response_id` 续接模式：无法改写，显式拒绝。
5. Codex `remote_compaction_v2` 的内部行为：只消费其事件，不试图替代。
