# Live Compact 通用内核 + Codex 适配器 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 live compact 抽成协议无关的 `fold` 内核 + 每 harness 一个 `Adapter`，并新写 Responses 适配器让 Codex 会话获得与 Claude 同等的折叠、账本与胶囊。

**Architecture:** `server/internal/agent/fold` 持有配置、估算、候选资格、预算、渲染、存档、承诺集、账本与冷缓存判据；`fold/anthropic` 与 `fold/responses` 各自实现 Scan/Text/Splice/Usage/Guard/Window；`proxy` 退化为传输层与每会话状态机。迁移按「纯移动 → 等价接缝 → 新适配器 → 注入 → 收尾」五段推进，每段独立提交且门禁全绿。

**Tech Stack:** Go 1.26（`$HOME/.local/share/go`），标准库 `net/http`、`encoding/json`，`github.com/pelletier/go-toml/v2`（已在 `internal/codexsync` 使用），`github.com/openai/codex-sdk-go` 经 `server/internal/agent/codex` 封装。

**Spec:** `docs/superpowers/specs/2026-11-20-live-compact-universal-fold-core-design.md`

## Global Constraints

- 门禁：`go test -count=1 ./...`、`go build ./server/...` 全绿；改动前端才需要 `tsc` + `vitest`。
- 每个提交信息用中文 Conventional Commits，并带 `Scope: G-MN`。
- 任何 Go 文件（含测试）不得超过 **500 行**；超线按职责纯移动拆分。
- 每个新行为必须先写失败测试（RED），再实现（GREEN），并对关键守卫做一次变异验证（改坏 → 测试变红 → 还原）。
- 不静默吞错：解析失败、转发失败、存档失败都必须有返回值或日志，不允许 `_ = err`。
- 工作区被其他会话共享：只 `git add` 本任务点名的路径，不 `git checkout`，不提交他人 WIP。
- 本机 `sudo systemctl restart mindfs` **只能由用户执行**；WSL/fn 用户单元可由 Agent 重启；不在 worker 上编译。
- 凭据只从 `~/.config/mindfs/api-providers.json` / 子进程环境读取，绝不打印、绝不写进共享配置或日志。
- `mode=off` 必须零解析成本；`DISABLED` 急停文件 1 秒内生效；隔离 ConfigDir 会话只观测不折叠——三条语义在任何阶段都不得改变。
- Anthropic 侧阈值与行为在 P1/P2 零变化；Codex 默认阈值 `rail=200000 / target=60000 / cold_rail=120000 / keep_rounds=3 / keep_tail=32000`。

## Review Focus

以下几类输入在 spec 中没有被任何既有测试覆盖，但最可能让使用者踩坑。每条都在对应任务里补了钉住它的测试：

1. **畸形/截断的请求体**：代理改写不能把上游原本能处理的请求变成 400 —— 任何 Scan/Splice 错误都必须回退原始 body 且逐字节转发。
2. **`reasoning` 项的 `encrypted_content`**：Codex 会原样回传；任何"顺手重编码"都会让上游拒绝整轮。
3. **`custom_tool_call_output.output` 为数组、`function_call_output.output` 为字符串**：两种形态混在同一历史里，只认一种会漏折或写坏。
4. **上一轮工具调用尚无结果（开放轮次）**：折叠它等于删掉模型正要用的调用，必须永不折。
5. **`store:true` 或带 `previous_response_id` 的请求**：历史不在 body 里，改写会静默失效，必须显式拒绝而不是折一半。

---

## 文件结构

| 路径 | 职责 |
|---|---|
| `server/internal/agent/fold/config.go` | `Config`（原 `LiveCompactConfig`）、`Normalized`、开关判定 |
| `server/internal/agent/fold/types.go` | `Span/Result/Round/Conversation/Replacement/Usage/GuardVerdict/Adapter` |
| `server/internal/agent/fold/walker.go` | 通用字节级 JSON 遍历（两个适配器共用） |
| `server/internal/agent/fold/estimate.go` | 字节→token 估算 |
| `server/internal/agent/fold/archive.go` | 内容寻址存档与回收 |
| `server/internal/agent/fold/pointer_cache.go` | 渲染缓存 |
| `server/internal/agent/fold/pointer.go` | 指针渲染 |
| `server/internal/agent/fold/commits.go` / `commit_log.go` | 单调承诺集与持久化 |
| `server/internal/agent/fold/ledger.go` | 每会话账本与 `ContextSaving` |
| `server/internal/agent/fold/cold.go` | 冷缓存三信号（纯函数） |
| `server/internal/agent/fold/gate.go` | rail/target/预算决策 |
| `server/internal/agent/fold/plan.go` | 候选资格、commit 集、事件预算 |
| `server/internal/agent/fold/engine.go` | `Engine.Fold`：Scan→决策→渲染→存档→Splice→校验 |
| `server/internal/agent/fold/anthropic/adapter.go` | Messages 适配器入口 |
| `server/internal/agent/fold/anthropic/scan.go` / `wire.go` / `splice.go` | 字节扫描、引用解析、拼接 |
| `server/internal/agent/fold/anthropic/pair.go` | 工具轮次配对 |
| `server/internal/agent/fold/responses/adapter.go` | Responses 适配器入口 |
| `server/internal/agent/fold/responses/scan.go` | `input[]` 扫描与配对 |
| `server/internal/agent/fold/responses/splice.go` | 只替换 `*_output` 文本 |
| `server/internal/agent/fold/responses/usage.go` | 用量归一化 |
| `server/internal/agent/proxy/proxy.go` | 传输、路由 `/p/` 与 `/c/`、转发 |
| `server/internal/agent/proxy/live_compact_fold_request.go` | 每会话状态机，调 `fold.Engine` |
| `server/internal/agent/proxy/live_compact_turn.go` / `worker.go` | 请求/响应配对、异步观测队列 |
| `server/internal/agent/pool_codex_proxy.go`（新） | provider 发现、`-c` 注入、thread 注册、compact 信号 |
| `server/internal/agent/pool_open.go` | Codex 分支接上以上 |

`proxy` 通过类型别名保持既有调用点不变：

```go
type LiveCompactConfig = fold.Config
type ContextSaving     = fold.ContextSaving
type LedgerEntry       = fold.LedgerEntry
```

---

### Task 0: 折叠覆盖率修复（生产证据驱动，先于重构）

**背景（2026-11-20 线上取证）**：`mode=on` 重启后 82 个请求，gate 分布 `below-rail` 43 /
`no-backlog` 33 / `fold` 6；每请求 `cand_blocks` 20–50，实际只折 2–4 块，`skipped` 19–45。
会话 token `07edc43f` 4 分钟内被 CLI 自己压缩 3 次（246,567→195,603、254,684→203,765、
267,325→216,616）；胶囊 `latest.saved_bytes=7821`。三个缺陷：

1. **每块门槛把中块全挡了**。`planFoldCommit` 第 2 遍（`live_compact_fold_plan.go:140`）用
   `rendered.tokens < cfg.MinBlockTokens` 过滤，默认 2000 ≈ 8 KB。真实会话绝大多数工具结果是
   1–7 KB，于是 rail 触发时一块都过不了，`newBytes=0 < min_fold_bytes` 把事件降级成
   `no-backlog`（33 次），上下文继续涨到 CLI 自动压缩。
2. **候选为空时 `gate_projected` 恒为 0**。`planFoldCommit` / `previewFold` 在
   `len(candidates)==0` 时提前 return，`decideFoldBudget` 从未被调用；日志里 240k 上下文却写
   `gate=below-rail gate_projected=0`，排障时会被误判成 gate 坏了。
3. **PostCompact 冷信号被空请求吃掉**。`coldFoldContext` 无条件清 `pendingColdFold`，而紧跟
   压缩的那个请求常常没有可折块（或整条没被观测），信号直接丢失 —— 全量日志只有 1 次
   `cold=true`。

**Files:**
- Modify: `server/internal/agent/proxy/live_compact_fold_render.go`
- Modify: `server/internal/agent/proxy/live_compact_fold_plan.go`
- Modify: `server/internal/agent/proxy/live_compact_fold_gate.go`
- Modify: `server/internal/agent/proxy/live_compact_fold_request.go`
- Modify: `server/internal/agent/proxy/live_compact_cold.go`
- Modify: `server/internal/agent/proxy/live_compact.go`
- Modify: `server/internal/agent/proxy/proxy.go`
- Create: `server/internal/agent/proxy/live_compact_coverage_test.go`
- Modify: `server/internal/agent/proxy/live_compact_cold_test.go`

**Interfaces:**
- Produces: `renderedFold.savedBytes int`（`spanLen - len(literal)`）；
  `liveCompactGate.candidates int`；`const foldMinBlockSavings = 1024`；
  `(*Server).peekColdFold(token string, cfg LiveCompactConfig, now time.Time) (bool, string)`；
  `(*Server).consumeColdFold(token string, used bool)`；
  `liveCompactState.pendingColdFoldAt time.Time`。
- 配置项不变：不新增 `config.json` 键。`min_block_tokens` 的语义收窄为「失败结果（`is_error`）
  的候选门槛」，其默认值 2000 保持不动；普通结果的每块门槛改为按**实际节省字节**判定。

- [ ] **Step 1: 写失败测试**

`live_compact_coverage_test.go`：

```go
// 生产回归：1–7 KB 的工具结果必须能折，而不是被 2000-token 门槛挡掉。
func TestFoldCoversBlocksBelowLegacyTokenFloor(t *testing.T) {
	dir := t.TempDir()
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	cfg := gateTestConfig(dir)
	cfg.MinBlockTokens = 2000 // 生产默认值

	// 4096 B：指针本体约 1.9 KB（1600 B 摘录 + 头），净省 ~2.2 KB，可折。
	body := distinctFoldBody(t, 3, 4096)
	result, gate := foldWithCommits(t, body, cfg, store, newPointerCache(),
		newFoldCommits(), foldContext{est: 1 << 20})
	if gate.decision != gateFold {
		t.Fatalf("decision = %s, want fold", gate.decision)
	}
	if result.newBlocks != 2 {
		t.Fatalf("newBlocks = %d, want the two closed rounds", result.newBlocks)
	}
}

// 指针只省几百字节的块不值得一次存档与一行指针，必须继续跳过。
func TestFoldSkipsBlockWhosePointerSavesTooLittle(t *testing.T) {
	dir := t.TempDir()
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	cfg := gateTestConfig(dir) // MinBlockTokens = 10：旧门槛不会挡它

	// 2400 B：比指针本体（约 1.9 KB）只大几百字节，净省 < 1024 B，必须跳过。
	body := distinctFoldBody(t, 3, 2400)
	result, _ := foldWithCommits(t, body, cfg, store, newPointerCache(),
		newFoldCommits(), foldContext{est: 1 << 20})
	if result.newBlocks != 0 {
		t.Fatalf("newBlocks = %d, want 0: a 2400-byte result saves too little", result.newBlocks)
	}
}

// 没有候选时也必须把 rail 算术写进日志：240k 上下文报 projected=0 会被读成 gate 故障。
func TestGateProjectionReportedWithoutCandidates(t *testing.T) {
	dir := t.TempDir()
	cfg := gateTestConfig(dir)
	body := wireBody(t, []any{userText("hi")})

	_, gate := foldWithCommits(t, body, cfg, newArchiveStore(dir, time.Hour, archiveMaxBytes),
		newPointerCache(), newFoldCommits(), foldContext{est: 200000})
	if gate.projected != 200000 {
		t.Fatalf("projected = %d, want 200000", gate.projected)
	}
	if gate.decision != gateNoBacklog {
		t.Fatalf("decision = %s, want %s (rail fired, nothing to fold)", gate.decision, gateNoBacklog)
	}
}

// 空请求不能消费 PostCompact 信号：真正跟着压缩来的那个请求才该拿到 cold。
func TestColdSignalNotConsumedByRequestWithoutCandidates(t *testing.T) {
	dir := t.TempDir()
	cfg := gateTestConfig(dir)
	cfg.RailTokens = 1 << 30 // 只有 cold rail 能开闸
	cfg.ColdRailTokens = 1
	s := &Server{
		sessions:     map[string]*sessionState{"tok": {foldAllowed: true}},
		liveCompact:  cfg,
		archive:      newArchiveStore(dir, time.Hour, archiveMaxBytes),
		pointerCache: newPointerCache(),
	}
	s.sessions["tok"].live.pendingColdFold = true
	s.sessions["tok"].live.pendingColdFoldAt = time.Now()

	s.foldRequestLedger("tok", wireBody(t, []any{userText("hi")}))
	if !s.sessions["tok"].live.pendingColdFold {
		t.Fatal("a request with no candidates must not consume the compact signal")
	}

	_, _, _, gate := s.foldRequestLedger("tok", distinctFoldBody(t, 3, 8001))
	if !gate.cold || gate.decision != gateFold {
		t.Fatalf("gate = %+v, want a cold fold on the next foldable request", gate)
	}
}
```

`live_compact_cold_test.go` 改写 `TestColdFoldSignalConsumedOnce` 为 peek/consume 语义，并补
过期用例：

```go
func TestColdFoldSignalPeekThenConsume(t *testing.T) {
	cfg := coldTestConfig()
	s := &Server{sessions: map[string]*sessionState{"tok": {foldAllowed: true}}}
	now := time.Now()
	s.sessions["tok"].live.pendingColdFold = true
	s.sessions["tok"].live.pendingColdFoldAt = now

	for i := 0; i < 2; i++ {
		if cold, reason := s.peekColdFold("tok", cfg, now); !cold || reason != coldReasonCompact {
			t.Fatalf("peek %d cold=%v reason=%q, want true/compact", i, cold, reason)
		}
	}
	s.consumeColdFold("tok", true)
	if cold, _ := s.peekColdFold("tok", cfg, now); cold {
		t.Fatal("a consumed signal must not mark a second decision cold")
	}
}

// 信号不是永久有效：超过缓存 TTL 后缓存本来就冷了，idle 规则会接管。
func TestColdFoldSignalExpiresAfterCacheTTL(t *testing.T) {
	cfg := coldTestConfig()
	s := &Server{sessions: map[string]*sessionState{"tok": {foldAllowed: true}}}
	now := time.Now()
	s.sessions["tok"].live.pendingColdFold = true
	s.sessions["tok"].live.pendingColdFoldAt = now.Add(-301 * time.Second)

	if cold, _ := s.peekColdFold("tok", cfg, now); cold {
		t.Fatal("a 301s-old compact signal must be expired")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./server/internal/agent/proxy/ -run 'TestFoldCovers|TestFoldSkipsBlockWhosePointer|TestGateProjectionReported|TestColdSignal' -count=1`
Expected: 四条新用例 FAIL（`newBlocks = 0 want 2`、`newBlocks = 2 want 0`、`projected = 0 want 200000`、
`a request with no candidates must not consume...`），冷信号两条因 `peekColdFold` 未定义而编译失败。

- [ ] **Step 3: 实现**

1. `live_compact_fold_render.go`：`renderedFold` 增加 `savedBytes int`，两条返回路径都写
   `spanLen - len(literal)`（缓存命中路径用 `spanLen - len(entry.literal)`）。
2. `live_compact_fold_plan.go`：
   - 第 2 遍把 `if rendered.tokens < cfg.MinBlockTokens { continue }` 换成
     `if rendered.savedBytes < foldMinBlockSavings { continue }`，常量
     `foldMinBlockSavings = 1024` 定义在同文件顶部，注释写明「事件级门槛是 `min_fold_bytes`，
     这里是每块是否值得单独存档一行指针」。
   - `len(candidates)==0` 的提前返回改为先算投影：`current := fc.est`，`fc.actual > 0` 时取
     `fc.actual`，调 `decideFoldBudget(cfg, fc, current, false)`；decision 为 `gateFold` 时改写
     成 `gateNoBacklog`（rail 到了但没料可折），然后返回。
   - 两处设置 `gate.candidates = len(candidates)`。
3. `live_compact_fold_gate.go`：`liveCompactGate` 增加 `candidates int`；`previewFold` 做与
   `planFoldCommit` 完全相同的空候选投影与 `savedBytes` 过滤（shadow 必须预演同一套策略）。
   `previewFold` 的候选没有渲染，节省字节用 `width` 与 `foldMinBlockSavings` 比较：只有
   `width >= foldMinBlockSavings` 才计入（渲染后仍可能被 `renderPointer` 的
   `foldSkipTooBig` 挡掉，这是既有差异）。
4. `live_compact_cold.go`：`coldFoldContext` 改名为 `peekColdFold`，**不清** `pendingColdFold`；
   新增 `consumeColdFold(token string, used bool)`，`used` 为真才清。`peekColdFold` 先判过期：
   `pendingColdFoldAt` 非零且 `now.Sub(...) >= CacheTTLSeconds` 时视为已过期（并清掉）。
5. `live_compact.go`：`liveCompactState` 增加 `pendingColdFoldAt time.Time`；`MinBlockTokens`
   注释改为「失败结果的候选门槛」。
6. `proxy.go`：`handleCompactSignal` 置 `pendingColdFold = true` 时一并写
   `pendingColdFoldAt = time.Now()`。
7. `live_compact_fold_request.go`：`fc.cold, fc.coldReason = s.peekColdFold(...)`；在
   `previewFold` / `planFoldCommit` 返回后调 `s.consumeColdFold(token, gate.candidates > 0)`；
   `verifyFold` 失败与 `changed()` 为假的分支同样消费（决策已经做过）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./server/internal/agent/proxy/ -count=1`
Expected: PASS（含全部既有折叠用例）。

- [ ] **Step 5: 变异验证**

逐个改坏再还原，确认对应用例转红：
1. 把 `savedBytes < foldMinBlockSavings` 改回 `rendered.tokens < cfg.MinBlockTokens` →
   `TestFoldCoversBlocksBelowLegacyTokenFloor` 红。
2. 去掉 `savedBytes` 判断 → `TestFoldSkipsBlockWhosePointerSavesTooLittle` 红。
3. 恢复空候选提前返回 → `TestGateProjectionReportedWithoutCandidates` 红。
4. `consumeColdFold` 无视 `used` 一律清 → `TestColdSignalNotConsumedByRequestWithoutCandidates` 红。
5. `peekColdFold` 不判过期 → `TestColdFoldSignalExpiresAfterCacheTTL` 红。

- [ ] **Step 6: 全量门禁与提交**

Run: `go test -count=1 ./... && go build ./server/...`
提交信息：`fix(proxy): 折叠覆盖率——按节省字节判块、空候选也报投影、冷信号不再被空请求吃掉（Scope: G-MN）`。
本机装二进制后**待用户** `sudo systemctl restart mindfs`；四端 `config.json` 都显式钉了
`min_block_tokens: 2000`，本任务不改配置语义所以无需改配置，但 rollout 时不要把它当生效门槛。

**执行结果（2026-11-20）**：三项全部落地。① 每块门槛改为按指针实际节省字节
（`foldMinBlockSavings = 1024`；指针本身约 1.9 KB，所以实际可折下限约 3 KB），
`min_block_tokens` 收窄为失败结果（`is_error`）的候选门槛，默认值与配置键都不变；② 候选为空时
仍执行 `decideFoldBudget` 并把投影写进 `gate_projected`，decision 记 `no-backlog`（shadow 的
`previewFold` 同步）；③ 冷信号拆成 `peekColdFold` / `consumeColdFold`：只有本次决策确实有候选才
花掉它，且超过 `cache_ttl_seconds` 自动过期。新增 5 条用例（覆盖率 2、空候选投影 1、冷信号 2），
`TestColdFoldSignalConsumedOnce` 改写为 peek/consume 语义；五处变异（回退 token 门槛、删节省判断、
恢复空候选提前返回、无条件消费、去掉过期）各自打红对应用例后还原。门禁：proxy 包绿、
`go test -count=1 ./...` 全绿（首轮 kanban 4 条在负载下偶发红，单跑与复跑均绿，且该包当时有并发
会话在飞改动）。

---

### Task 1: `fold` 包骨架与配置迁移

**Files:**
- Create: `server/internal/agent/fold/config.go`
- Create: `server/internal/agent/fold/config_test.go`
- Create: `server/internal/agent/proxy/fold_alias.go`
- Modify: `server/internal/agent/proxy/live_compact.go`（删除 `LiveCompactConfig` 结构体、`Normalized`、`PrefixGuardOn`、`ColdFoldOn`，改为别名）
- Create: `server/internal/agent/fold/imports_test.go`

**Interfaces:**
- Consumes: 无
- Produces: `fold.Config`（字段与 `proxy.LiveCompactConfig` 逐字段一致）、`fold.Config.Normalized() fold.Config`、`fold.Config.Observes() bool`、`fold.Config.PrefixGuardOn() bool`、`fold.Config.ColdFoldOn() bool`、常量 `fold.ModeOff/ModeShadow/ModeOn`

- [ ] **Step 1: 写失败测试**

`fold/config_test.go`：

```go
func TestConfigNormalizedDefaults(t *testing.T) {
    got := Config{Mode: "ON"}.Normalized()
    if got.Mode != ModeOn { t.Fatalf("mode = %q", got.Mode) }
    if got.KeepRounds != 3 || got.KeepTailTokens != 32000 || got.MinBlockTokens != 2000 { t.Fatalf("keep defaults: %+v", got) }
    if got.RailTokens != 100000 || got.TargetTokens != 20000 { t.Fatalf("rail defaults: %+v", got) }
    if got.MinFoldBytes != 4096 || got.MaxFoldBytes != 262144 || got.LargeBlockTokens != 32000 { t.Fatalf("fold defaults: %+v", got) }
    if got.ColdRailTokens != 60000 || got.CacheTTLSeconds != 300 { t.Fatalf("cold defaults: %+v", got) }
}

func TestConfigZeroValueIsOff(t *testing.T) {
    var c Config
    if c.Normalized().Mode != ModeOff { t.Fatal("zero config must normalize to off") }
    if c.Observes() { t.Fatal("off must not observe") }
}

func TestConfigTriStateDefaultsOn(t *testing.T) {
    var c Config
    if !c.PrefixGuardOn() || !c.ColdFoldOn() { t.Fatal("nil tri-state must mean on") }
    off := false
    c.PrefixGuard, c.ColdFold = &off, &off
    if c.PrefixGuardOn() || c.ColdFoldOn() { t.Fatal("explicit false must mean off") }
}
```

`fold/imports_test.go`：

```go
func TestFoldDoesNotImportProxy(t *testing.T) {
    out, err := exec.Command("go", "list", "-deps", "./...").Output()
    if err != nil { t.Fatal(err) }
    for _, line := range strings.Split(string(out), "\n") {
        if strings.HasSuffix(strings.TrimSpace(line), "/agent/proxy") {
            t.Fatalf("fold must not depend on proxy: %s", line)
        }
    }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./server/internal/agent/fold/ -run 'TestConfig|TestFoldDoesNot' -v`
Expected: FAIL — `undefined: Config`（包不存在）

- [ ] **Step 3: 实现 `fold/config.go` 并改 proxy 用别名**

把 `server/internal/agent/proxy/live_compact.go` 中 `LiveCompactConfig` 结构体与 `Normalized`/`PrefixGuardOn`/`ColdFoldOn` 的定义**整段剪切**到 `fold/config.go`，仅把类型名改为 `Config`、常量改为 `ModeOff/ModeShadow/ModeOn`。字段默认值与注释逐字保留（它们是阈值决策的唯一记录）。

`proxy/fold_alias.go`：

```go
package proxy

import "mindfs/server/internal/agent/fold"

type LiveCompactConfig = fold.Config

const (
    LiveCompactOff    = fold.ModeOff
    LiveCompactShadow = fold.ModeShadow
    LiveCompactOn     = fold.ModeOn
)
```

`proxy/live_compact.go` 只保留 `LiveCompactMode`、`liveCompactState`、`liveCompactObservation`、`isMessagesPath`、`liveCompactDisabled`；`Observes()` 随结构体一起搬到 `fold.Config` 上，经别名自动可见，proxy 侧不再定义。

- [ ] **Step 4: 跑测试确认通过**

Run: `go build ./server/... && go test ./server/internal/agent/fold/ ./server/internal/agent/proxy/`
Expected: PASS（既有 proxy 用例一条不改、全绿）

- [ ] **Step 5: 变异验证**

在 `fold/config.go` 把 `ColdRailTokens` 默认值临时改成 `50000`，`go test ./server/internal/agent/fold/` 必须红；还原。

- [ ] **Step 6: 提交**

```bash
git add server/internal/agent/fold/config.go server/internal/agent/fold/config_test.go server/internal/agent/fold/imports_test.go server/internal/agent/proxy/fold_alias.go server/internal/agent/proxy/live_compact.go
git commit -m "refactor(live-compact): 抽出 fold 包并把配置迁入内核（Scope: G-MN）"
```

**执行结果（2026-11-20）**：`fold/config.go` 承接 `Config` / `Normalized` / `Observes` /
`PrefixGuardOn` / `ColdFoldOn` 与 `ModeOff/ModeShadow/ModeOn`，字段与注释逐字保留；archive 的两个
默认值（7 天 / 2 GiB）随 `Normalized` 一起搬到 `fold.DefaultRetention` /
`fold.DefaultMaxArchiveBytes`，proxy 侧的 `archiveRetention` / `archiveMaxBytes` 改为引用它们（否则
内核无法在自己的默认值里用这两个数）。`proxy/fold_alias.go` 用类型别名 + 常量别名让既有调用点零改动；
`fold/imports_test.go` 用 `go list -deps` 钉住「内核不得依赖任何 harness 传输」。新增 4 条配置用例
（默认值、零值 off、三态默认开、无 archive 目录即关存档），变异（冷折 rail 60000 → 50000）打红后还原；
`go build ./server/...` 与两个包的测试全绿。

---

### Task 2: 存档与渲染缓存迁入内核

**Files:**
- Create: `server/internal/agent/fold/archive.go`（自 `proxy/live_compact_archive.go` 纯移动）
- Create: `server/internal/agent/fold/pointer_cache.go`（自 `proxy/live_compact_pointer_cache.go` 纯移动）
- Create: `server/internal/agent/fold/archive_test.go`、`fold/pointer_cache_test.go`（对应测试纯移动）
- Modify: `server/internal/agent/proxy/*.go`（导出名改为 `fold.ArchiveStore` 等，删除旧文件）

**Interfaces:**
- Consumes: `fold.Config`
- Produces:
  - `fold.NewArchiveStore(dir string, retention time.Duration, maxBytes int64) *ArchiveStore`
  - `(*ArchiveStore).Write(text string) (string, error)`
  - `(*ArchiveStore).RecordFailure(text string)`、`(*ArchiveStore).Sweep(now time.Time)`、`(*ArchiveStore).ProtectedNames() map[string]struct{}`（若现有方法是 `sweep/recordFailure/protectedNames` 则改为导出；名字以现有实现为准，只做大小写变更）
  - `fold.PointerCacheKey`（原 `pointerCacheKey`，字段与 maphash 计算方式不变）
  - `fold.NewPointerCacheKey(text string) PointerCacheKey`
  - `fold.NewPointerCache() *PointerCache`、`(*PointerCache).Get(PointerCacheKey) (string, bool)`、`(*PointerCache).Put(PointerCacheKey, string)`（同上只改可见性）

- [ ] **Step 1: 先跑既有测试确认基线**

Run: `go test ./server/internal/agent/proxy/ -run 'Archive|PointerCache' -v`
Expected: PASS（记录基线；这些用例将随文件移动）

- [ ] **Step 2: 纯移动文件与测试**

用 `git mv server/internal/agent/proxy/live_compact_archive.go server/internal/agent/fold/archive.go`，改 `package fold`，把所有小写方法名改为导出名；调用点用 `gofmt -r` 或手工替换。测试文件同样 `git mv` 到 `fold/`，`package fold`。

- [ ] **Step 3: 跑测试确认通过**

Run: `go build ./server/... && go test ./server/internal/agent/fold/ ./server/internal/agent/proxy/`
Expected: PASS

- [ ] **Step 4: 变异验证**

把 `ArchiveStore.Write` 里的 `0600` 权限临时改成 `0644`，`go test ./server/internal/agent/fold/` 必须红；还原。

- [ ] **Step 5: 提交**

```bash
git add server/internal/agent/fold server/internal/agent/proxy
git commit -m "refactor(live-compact): 存档与渲染缓存迁入 fold 内核（Scope: G-MN）"
```

> 纯移动任务的 `git add` 只点名本次真正改动的文件；不要 `git add -A`（工作区有他人 WIP）。

---

### Task 3: 承诺集与持久化迁入内核

**Files:**
- Create: `server/internal/agent/fold/commits.go`、`fold/commit_log.go`（自 `proxy/live_compact_commits.go`、`proxy/live_compact_commit_log.go` 纯移动）
- Create: `fold/commits_test.go`、`fold/commit_log_test.go`
- Modify: `server/internal/agent/proxy/live_compact_fold_request.go`（改用 `fold.Commits`）

**Interfaces:**
- Consumes: `fold.PointerCacheKey`（原 `pointerCacheKey`）、`fold.ArchiveStore`
- Produces:
  - `fold.NewCommits() *Commits`
  - `fold.NewCommitsFor(log *CommitLog) *Commits`
  - `(*Commits).Has(key PointerCacheKey) bool`
  - `(*Commits).Add(key PointerCacheKey)`
  - `(*Commits).Commit(key PointerCacheKey, name string)`
  - `(*Commits).ProtectedNames() map[string]struct{}`
  - `(*Commits).Adopt(name string, key PointerCacheKey) bool`
  - `fold.NewCommitLog(dir string) *CommitLog`

- [ ] **Step 1: 纯移动并改可见性**

方法名与签名逐字保留，只把类型名首字母大写并在 proxy 侧更新调用点。`restored` 的 adopt 语义、`log` 为 nil 时不落盘的规则**不得改动**。

- [ ] **Step 2: 跑测试确认通过**

Run: `go build ./server/... && go test ./server/internal/agent/fold/ ./server/internal/agent/proxy/`
Expected: PASS

- [ ] **Step 3: 变异验证**

把 `Has` 在 `c == nil` 时改成返回 `true`，`go test ./server/internal/agent/fold/ -run Commit` 必须红；还原。

- [ ] **Step 4: 提交**

```bash
git add server/internal/agent/fold server/internal/agent/proxy
git commit -m "refactor(live-compact): 承诺集与提交日志迁入 fold 内核（Scope: G-MN）"
```

**执行结果（2026-11-20，Task 2 与 Task 3 合并执行）**：两者不能按计划顺序拆开 ——
`ArchiveStore.sweepProtected` 的保护集来自持久化提交日志（`referencedAfter` 读的正是
`commit_log.go` 的文件），先移存档就会让内核反向依赖 proxy，撞上 `imports_test.go` 的守卫。
所以存档、渲染缓存、承诺集、提交日志四个文件一起 `git mv` 进 `fold/`：`ArchiveStore`
（`NewArchiveStore` / `Write` / `RecordFailure` / `Sweep` / `SweepProtected` / `Dir`）、
`PointerCache`（`NewPointerCache` / `Get` / `Put` / `NewPointerCacheKey` /
`NewPointerCacheKeyVariant` / `PointerCacheLimit` / `PointerVariantFailed` / `CachedPointer`
四字段导出）、`Commits`（`NewCommits` / `NewCommitsFor` / `Has` / `Add` / `CommitAll` /
`Adopt` / `RestoredCount` / `DropUnrestored` / `ProtectedNames` / `Len`）、`CommitLog`
（`NewCommitLog` / `Append` / `Load` / `CommitRecord` / `CommitLogDirName`）。会话文件名哈希
（原 `ledgerSessionName`）抽成 `fold.SessionFileName`，账本先委托给它，Task 4 搬账本时直接复用。
`proxy/fold_alias.go` 追加类型别名与构造函数别名，调用点只改方法名大小写（`.Get`/`.Put`/
`.Has`/`.Add`/`.Adopt`/`.CommitAll`/`.Append`/`.Load`/`.SweepProtected`）。

新增 `fold/commits_test.go` 3 条（nil 集合、Add/CommitAll、只认已恢复名字的 adopt）；
两处变异各自打红：`ArchiveStore.Write` 的 `0600` → `0644` 被 `TestArchiveFilesArePrivate` 抓住，
`Commits.Has` 在 nil 时返回 true 被 `TestCommitsNilSetHasNothing` 抓住，还原后 `fold` 与 `proxy`
全绿；`go test ./server/internal/agent/...` 全绿。登记侧：G-MN 的四个文件条目改为 `fold/` 前缀、
三个测试路径跟着搬、14 处锚点重指到新符号。

---

### Task 4: 账本与 `ContextSaving` 迁入内核

**Files:**
- Create: `server/internal/agent/fold/ledger.go`（自 `proxy/live_compact_ledger.go` 纯移动）
- Create: `fold/ledger_test.go`、`fold/ledger_rewrite_test.go`、`fold/ledger_usage_test.go`
- Create: `server/internal/agent/proxy/fold_alias.go` 追加 `type ContextSaving = fold.ContextSaving`、`type LedgerEntry = fold.LedgerEntry`
- Modify: `packages/api` 侧读取 `ContextSaving` 的调用点（若有显式类型名，改为别名即可，无需改字段）

**Interfaces:**
- Consumes: 无
- Produces:
  - `fold.NewLedgerStore(dir string) *LedgerStore`
  - `(*LedgerStore).Write(sessionKey string, entry LedgerEntry) error`
  - `(*LedgerStore).Read(sessionKey string) []LedgerEntry`
  - `fold.SummarizeLedger(entries []LedgerEntry) ContextSaving`
  - `fold.LedgerEntry`、`fold.ContextSaving`（JSON 字段名逐字不变）

- [ ] **Step 1: 纯移动**

`ledgerDirName`、`ledgerMaxBytes`、`ledgerKeepLines`、`ledgerReadLimit`、轮转逻辑、`usage_source` 语义全部逐字保留。`Server.SessionContextSaving` 留在 proxy，内部改为 `fold.SummarizeLedger(s.ledger.Read(key))`。

- [ ] **Step 2: 跑测试确认通过**

Run: `go build ./server/... && go test ./server/internal/agent/fold/ ./server/internal/agent/proxy/ ./server/internal/api/ -run 'Ledger|ContextSaving'`
Expected: PASS

- [ ] **Step 3: 变异验证**

把 `SummarizeLedger` 的 `UsageMissing` 统计条件改成永远不命中，`go test ./server/internal/agent/fold/ -run Summarize` 必须红；还原。

- [ ] **Step 4: 提交**

```bash
git add server/internal/agent/fold server/internal/agent/proxy server/internal/api
git commit -m "refactor(live-compact): 账本与上下文节省汇总迁入 fold 内核（Scope: G-MN）"
```

**执行结果（2026-11-20）**：`fold/ledger.go` 承接 `LedgerStore` / `LedgerEntry` /
`ContextSaving` / `SummarizeLedger` 与 `UsageSourceResponse` / `UsageSourceMissing`，常量
`LedgerDirName` / `ledgerMaxBytes` / `ledgerKeepLines` / `ledgerReadLimit` 逐字保留；文件命名
直接用 Task 2 抽出的 `fold.SessionFileName`。与计划的唯一偏离：`Write` 返回 `error` 而不是
静默返回（计划 Interfaces 写的就是 `error`）——调用方 `writeFoldLedger` 显式 `_ =` 忽略并保留
“诊断写失败不阻断请求”的注释，轮转（compaction）仍按尽力而为处理，失败只留下可读的超大文件。

proxy 侧只剩 `foldLedgerPending` / `newFoldLedgerPending` / `writeFoldLedger` /
`SessionContextSaving`（88 行），`proxy.go` 的字段改为 `*fold.LedgerStore`、构造改走
`fold.NewLedgerStore(filepath.Join(cfg.ArchiveDir, fold.LedgerDirName))`；`fold_alias.go` 追加
`ContextSaving` / `LedgerEntry` 类型别名与 `usageSource*` 常量别名，旧测试读法不变。
`Pool.SessionContextSaving` 与 API usecase 直接引用 `fold.ContextSaving`（不再经 proxy 转发），
JSON 字段名一个没动，胶囊契约不变。

测试按“store 归内核、handler 归 proxy”拆开：`fold/ledger_test.go` 9 条（append/read、按会话
隔离、哈希文件名、0600/0700、截断行、容量轮转、summary、nil store、时间戳）+
`fold/ledger_usage_test.go` 2 条（legacy 已知样本、只加已知样本），proxy 保留 3 条真 handler
用例（`TestProxyFoldWritesLedger` 等）。变异验证：`SummarizeLedger` 的 `UsageMissing++` 改成
不计数，`go test ./server/internal/agent/fold/ -run LedgerSummary` 在
`TestLedgerSummaryKeepsLegacyKnownSamples` 打红（`missing=0, want 2/1`），还原复绿。
`go test ./server/internal/agent/... ./server/internal/api/...` 全绿。登记侧：G-MN 的两个
`rewrite_tokens` 锚点与 `ledgerMaxBytes` 重指 `fold/ledger.go`，`SessionContextSaving` 两处
签名跟着返回类型改，测试清单补两条 fold 用例。

---

### Task 5: 估算、指针渲染与冷缓存判据迁入内核

**Files:**
- Create: `fold/estimate.go`、`fold/pointer.go`、`fold/cold.go`（自 `proxy/estimate.go`、`proxy/live_compact_pointer.go`、`proxy/live_compact_fold_render.go`、`proxy/live_compact_cold.go`）
- Create: `fold/walker.go`（`proxy/live_compact_scan.go` 中的通用字节 walker 部分）
- Create: `fold/estimate_test.go`、`fold/pointer_test.go`、`fold/cold_test.go`、`fold/walker_test.go`
- Modify: `server/internal/agent/proxy/live_compact.go`（`coldFoldContext` 改为读快照）

**Interfaces:**
- Consumes: `fold.Config`、`fold.Span`
- Produces:
  - `fold.Span`（`struct{ Start, End int }`，字节区间；两个适配器与内核共用）
  - `fold.ErrScanMalformed`
  - `fold.ScanEachMember(body []byte, open int, visit func(key []byte, vs, ve int) (bool, error)) error`
  - `fold.ScanEachElement(body []byte, open int, visit func(vs, ve int) error) error`
  - `fold.MemberKeyIs(key []byte, name string) bool`
  - `fold.JSONStringAt(body []byte, vs, ve int) (string, error)`
  - `fold.ScanValueEnd(body []byte, i, depth int) (int, error)`、`fold.ScanSkipSpace(body []byte, i int) int`
  - `fold.EstimateTokens(string) int`、`fold.EstimateByteTokens([]byte) int`
  - `fold.SummarizeToolInput(body []byte, obj Span) string`
  - `fold.RenderPointer(in PointerInput) string`，其中
    ```go
    type PointerInput struct {
        ToolName     string
        InputSummary string
        Text         string
        ArchivePath  string
        Failed       bool
        ErrorText    bool
    }
    ```
  - `fold.ColdDecision(s ColdSnapshot, cfg Config, now time.Time) (bool, string)`，其中
    ```go
    type ColdSnapshot struct {
        PendingCompact bool
        LastUsageAt    time.Time
        NoReadStreak   int
        LastContext    int
    }
    ```
    返回 `fold.ColdReasonCompact/Idle/NoRead` 之一或空串

- [ ] **Step 1: 写失败测试**

`fold/cold_test.go`：

```go
func TestColdDecisionCompactConsumesOnce(t *testing.T) {
    cfg := Config{}.Normalized()
    s := ColdSnapshot{PendingCompact: true}
    if cold, reason := ColdDecision(s, cfg, time.Now()); !cold || reason != ColdReasonCompact { t.Fatalf("got %v %q", cold, reason) }
    s.PendingCompact = false
    if cold, _ := ColdDecision(s, cfg, time.Now()); cold { t.Fatal("second decision must not be cold") }
}

func TestColdDecisionIdleBeyondTTL(t *testing.T) {
    cfg := Config{}.Normalized()
    s := ColdSnapshot{LastUsageAt: time.Now().Add(-301 * time.Second)}
    if cold, reason := ColdDecision(s, cfg, time.Now()); !cold || reason != ColdReasonIdle { t.Fatalf("got %v %q", cold, reason) }
}

func TestColdDecisionNoReadNeedsRail(t *testing.T) {
    cfg := Config{}.Normalized()
    if cold, _ := ColdDecision(ColdSnapshot{NoReadStreak: 2, LastContext: 59999}, cfg, time.Now()); cold { t.Fatal("below cold rail must not fold") }
    if cold, reason := ColdDecision(ColdSnapshot{NoReadStreak: 2, LastContext: 60000}, cfg, time.Now()); !cold || reason != ColdReasonNoRead { t.Fatalf("got %v %q", cold, reason) }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./server/internal/agent/fold/ -run ColdDecision -v`
Expected: FAIL — `undefined: ColdDecision`

- [ ] **Step 3: 实现**

`ColdDecision` 是现有 `(*Server).coldFoldContext` 的纯函数化：去掉 `s.mu` 与 map 查找，读取 `ColdSnapshot`。`proxy.coldFoldContext(token, cfg, now)` 改为在同一次加锁内组装快照并调用 `fold.ColdDecision`，**pending 清零仍必须发生在同一次加锁内**。

`RenderPointer` 是现有渲染函数的参数化版本：`InputSummary` 由适配器在 Scan 时算好传入。`summarizeToolInput` 随 `pointer.go` 一起进 `fold` 并导出为 `fold.SummarizeToolInput(body []byte, obj Span) string`（两种协议都用得上：Anthropic 的 `tool_use.input` 与 Responses 的 `function_call.arguments` 都是 JSON 对象）。

`walker.go` 承接 `live_compact_scan.go` 里与协议无关的字节遍历（`scanSkipSpace`/`scanStringEnd`/`scanValueEnd`/`scanObjectEnd`/`scanArrayEnd`/`scanEachMember`/`scanEachElement`/`memberKeyIs`/`jsonStringAt`），只做导出化，逻辑逐字保留。两个适配器都调用这一份 walker——共享的是字节遍历，不是协议解析；协议解析仍然各自实现（见 Task 7 / Task 9）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go build ./server/... && go test ./server/internal/agent/fold/ ./server/internal/agent/proxy/`
Expected: PASS

- [ ] **Step 5: 变异验证**

- `ColdDecision` 的 TTL 比较写成 `>` 改 `>=` → `TestColdDecisionIdleBeyondTTL` 红。
- `RenderPointer` 去掉"这不是当前指令"一行 → 既有指针用例红。

各自打红后还原。

- [ ] **Step 6: 提交**

```bash
git add server/internal/agent/fold server/internal/agent/proxy
git commit -m "refactor(live-compact): 估算、指针渲染与冷缓存判据迁入 fold 内核（Scope: G-MN）"
```

**执行结果（2026-11-20）**：四个内核文件落地。`fold/walker.go` 承接字节遍历并把
`span` 导出成 `fold.Span{Start,End}`（带 `Valid()` / `Raw()`）；`fold/estimate.go` 承接
`EstimateTokens` / `EstimateByteTokens`；`fold/pointer.go` 承接 `PointerInput`（计划里的六个
字段，`Tokens` 改为渲染时用 `EstimateTokens(Text)` 现算）、`RenderPointer`、
`SummarizeToolInput` 与摘要/截断辅助函数；`fold/cold.go` 承接
`ColdDecision(ColdSnapshot, Config, time.Time)` 与
`ColdReasonCompact/Idle/NoRead`、`ColdNoReadStreak`。

两处计划外但必要的偏差：① `ColdSnapshot` 多一个 `CompactedAt` 字段 —— 现有语义要求
「PostCompact 旗标超过缓存 TTL 即作废」，计划里的四字段快照无法表达；`ColdDecision` 仍是纯
函数，作废与「只在有候选时才花掉旗标」都留在 proxy 的同一次加锁内。②
`encodeJSONStringLiteral` 抽成 `fold/EncodeJSONStringLiteral`（`fold/json_literal.go`）：
它是拼接改写用的通用 JSON 工具，Task 10 的 Responses `Splice` 也要用，留在 proxy 会让内核
反向依赖。

proxy 侧删除 `estimate.go`、`live_compact_scan.go`、`live_compact_pointer.go`，`fold_alias.go`
补 `span` / `pointerInput` 类型别名、walker/估算/指针函数别名与 `coldReason*` 常量别名；
`span` 变成外部类型后字段改为 `Start`/`End`（`foldReplacement` 的字段同步改成大写以保持
`sort.Slice` 比较可读）。新增内核测试：`fold/cold_test.go` 5 条、`fold/walker_test.go` 7 条、
`fold/pointer_test.go` 4 条，`estimate_test.go` 随源文件搬入内核。

变异验证两处（计划给的两条都按实际代码校正过）：`ColdDecision` 的空闲阈值改成 `>= 4*ttl`
→ `TestColdDecisionIdleBeyondTTL` 红；`RenderPointer` 去掉整行指针头（只删「这不是当前指令」
不会红 —— proxy 用例只数 `[MindFS live-compact]` 前缀）→ proxy 的 fold 用例与
`TestRenderPointerStates` 同时红，各自还原复绿。`go build ./server/...`、`go test
./server/internal/agent/... ./server/internal/api/...` 全绿（`kanban` 包编译失败来自同工作区
另一条会话未跟踪的 `router_supervision_test.go`，不属本组）。

---

### Task 6: 中性类型、`Adapter` 接口与 `Engine` 骨架

**Files:**
- Create: `fold/types.go`、`fold/adapter.go`、`fold/engine.go`
- Create: `fold/engine_test.go`
- Modify: `fold/gate.go`、`fold/plan.go`（自 `proxy/live_compact_fold_gate.go`、`proxy/live_compact_fold_plan.go` 迁入，输入改为 `[]Round`）

**Interfaces:**
- Consumes: Task 1–5 的全部产出
- Produces:

```go
// Span 已在 Task 5 的 walker.go 定义。

type Result struct {
    CallID       string
    ToolName     string
    InputSummary string
    Parts        []Span // 文本字面量的字节区间；恰好一个才可折
    Bytes        int
    Tokens       int
    ErrorText    bool
    Failed       bool
    NonText      bool
}

type Round struct{ Results []Result }

type Conversation struct {
    Model     string
    Rounds    []Round
    Estimated int
    // Refuse, when non-empty, is the adapter's judgement that this body must
    // not be rewritten at all (Responses `store:true` / previous_response_id).
    // Engine turns it into Verdict{Block:true, Reason: Refuse}.
    Refuse string
}

type Verdict struct {
    Block  bool
    Reason string // "" | "below-rail" | "prefix-guard" | "unsupported" | "disabled"
}

type Replacement struct {
    Span    Span
    Literal string // 已解码的纯文本；由 Adapter 负责编码回 JSON 字面量
}

type Usage struct {
    ContextTokens int
    CacheRead     int
    CacheWrite    int
    Output        int
    RewriteTokens int
    Source        string
}

type GuardVerdict struct {
    Block  bool
    Reason string
}

type Adapter interface {
    Name() string
    Scan(body []byte) (*Conversation, error)
    Text(body []byte, r Result) (string, bool)
    Splice(body []byte, reps []Replacement) ([]byte, error)
    Usage(resp []byte) (Usage, bool)
    Guard(model string, cfg Config) GuardVerdict
    Window(model string) int
}
```

> spec §5 列了 5 个方法，这里多一个 `Text`：内核只在**真正要渲染**的候选上取文本，避免对每个候选都做 JSON 解码（8 MB body 上实测差 400 ms）。这是对 spec 的细化，不是偏离。

```go
type Session struct {
    Commits         *Commits
    PendingColdFold bool
    LastUsageAt     time.Time
    NoReadStreak    int
    LastContext     int
    PrefixWarned    bool
}

type Request struct {
    Body            []byte
    Session         *Session
    Now             time.Time
    FoldAllowed     bool
    LastEstimate    int
    LastActual      int
    LastForwardEst  int
}

type Outcome struct {
    Body            []byte
    Folded          bool
    FoldedBlocks    int
    FoldedTokens    int
    FoldedBytes     int
    StickyBlocks    int
    NewBlocks       int
    Skipped         int
    ArchiveFailures int
    Verdict         Verdict
    Estimated       int
    Projected       int
    Cold            bool
    ColdReason      string
}

type Engine struct { /* cfg, adapter, archive, cache */ }

func NewEngine(cfg Config, a Adapter, archive *ArchiveStore, cache *PointerCache) *Engine
func (e *Engine) Fold(req Request) (Outcome, error)
```

- [ ] **Step 1: 写失败测试**

`fold/engine_test.go` 用一个 30 行的假适配器（把 body 当作 JSON `{"items":["a","b"]}`，可折项是长度 > min 的字符串）钉住：

```go
func TestEngineFoldsCommittedBlocksAfterFirstPass(t *testing.T) {
    e := newTestEngine(t)
    req := Request{Body: []byte(`{"items":["` + strings.Repeat("x", 20000) + `"]}`), Session: NewTestSession(), Now: time.Now(), FoldAllowed: true}
    first, err := e.Fold(req)
    if err != nil { t.Fatal(err) }
    if !first.Folded || first.NewBlocks != 1 { t.Fatalf("first fold: %+v", first) }
    second, err := e.Fold(req) // 同一个原始 body 再来一次
    if err != nil { t.Fatal(err) }
    if !second.Folded || second.StickyBlocks != 1 || second.NewBlocks != 0 { t.Fatalf("second fold: %+v", second) }
    if string(second.Body) != string(first.Body) { t.Fatal("committed fold must render the same bytes") }
}

func TestEngineFallsBackOnSpliceError(t *testing.T) {
    e := newTestEngineWithSpliceError(t)
    req := Request{Body: []byte(`{"items":["big"]}`), Session: NewTestSession(), FoldAllowed: true}
    out, err := e.Fold(req)
    if err == nil { t.Fatal("splice error must surface") }
    if string(out.Body) != string(req.Body) { t.Fatal("error path must return the original body") }
}

func TestEngineFallsBackOnScanError(t *testing.T) {
    e := newTestEngine(t)
    body := []byte(`{"items":[`)
    out, err := e.Fold(Request{Body: body, Session: NewTestSession(), FoldAllowed: true})
    if err == nil { t.Fatal("scan error must surface") }
    if string(out.Body) != string(body) { t.Fatal("malformed body must be forwarded verbatim") }
}

func TestEngineHonoursAdapterRefusal(t *testing.T) {
    e := newTestEngineRefusing(t, "unsupported")
    out, err := e.Fold(Request{Body: []byte(`{"items":["big"]}`), Session: NewTestSession(), FoldAllowed: true})
    if err != nil { t.Fatal(err) }
    if out.Folded || !out.Verdict.Block || out.Verdict.Reason != "unsupported" { t.Fatalf("%+v", out) }
}

func TestEngineRefusesOpenRound(t *testing.T) { /* 适配器返回一个只有 call 没有 output 的 Round */ }
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./server/internal/agent/fold/ -run TestEngine -v`
Expected: FAIL — `undefined: Engine`

- [ ] **Step 3: 实现 `Engine.Fold`**

顺序固定，任一步失败都返回原 body：

1. `conv, err := e.adapter.Scan(req.Body)`；出错 → 返回 `Outcome{Body: req.Body}` 与原错误。
2. 若 `conv.Refuse != ""` → 返回原 body 与 `Verdict{Block:true, Reason: conv.Refuse}`。
3. 若 `req.Session == nil` → 只观测，不折。
4. `Guard(conv.Model, e.cfg)`；`Block` → 返回 `Verdict{Reason: "prefix-guard"}`。
5. `cold, reason := ColdDecision(...)`；消费 `Session.PendingColdFold`（由调用方在持锁时写入快照，见 Task 8）。
6. `Verdict := DecideBudget(conv, cfg, cold, projected)`；未过 → 返回原 body。
7. 选候选：`PlanCandidates(conv.Rounds, cfg, commits)` → `[]Result`（已提交集合无条件入选；新块受 keep 窗口、min/max、large-block 约束）。
8. 对每个候选 `text, ok := adapter.Text(body, r)`；`!ok` → 跳过该候选并计入 `Skipped`。
9. `archive.Write(text)` 得名字（已提交块先查渲染缓存）；失败计入 `ArchiveFailures` 并跳过。
10. `RenderPointer(...)` 得字面量，组装 `[]Replacement`。
11. `out, err := adapter.Splice(body, reps)`；出错 → 返回原 body 与原错误。
12. 结构校验：重新 `Scan(out)`，要求 `len(Rounds)` 相同、每个 `CallID` 配对关系相同；不符 → 返回原 body 与 `ErrVerifyFailed`。
13. 全部成功才 `commits.Commit(...)`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go build ./server/... && go test ./server/internal/agent/fold/`
Expected: PASS

- [ ] **Step 5: 变异验证**

- 第 12 步的校验改成直接 `return` 成功 → `TestEngineFallsBackOnSpliceError` 之外的校验用例必须红（在 Step 1 额外加 `TestEngineRejectsPairingChange`）。
- 第 7 步去掉"已提交集合无条件入选" → `TestEngineFoldsCommittedBlocksAfterFirstPass` 第二次断言红。
- 第 2 步删掉 `conv.Refuse` 检查 → `TestEngineHonoursAdapterRefusal` 红。

- [ ] **Step 6: 提交**

```bash
git add server/internal/agent/fold
git commit -m "feat(live-compact): fold 内核增加 Adapter 接口与折叠引擎（Scope: G-MN）"
```

**执行结果（2026-11-20）**：六个内核文件落地 —— `fold/types.go`（`Result` /
`Round` / `Conversation` / `Verdict` / `Replacement` / `Usage` / `GuardVerdict`）、
`fold/adapter.go`（`Adapter` / `Session` / `Request`）、`fold/engine.go`（`Engine` /
`Outcome` / `ErrVerifyFailed` / `Fold` / `verifyConversation` / `abandon` /
`render`）、`fold/gate.go`（`DecideBudget` 与中性的 `Estimate` / `Decision` /
`Budget`，gate 字符串与 proxy 现有日志逐字一致）、`fold/plan.go`（`PlanCandidates`
与 `Candidate`，`FoldMinBlockSavings` / `MaxChunksPerEvent` 原值搬入）、
`fold/error_text.go`（`LooksLikeErrorText` 自 `proxy/live_compact_wire_drift.go`
原样迁出，proxy 通过 `fold_alias.go` 的 `looksLikeErrorText` 别名继续使用）。
`Fold` 按计划的 13 步实现，唯一顺序调整是**承诺集只在 splice + 校验都通过后才
`CommitAll`**（比旧 planner 在计划期提交更严：校验失败的块绝不能记成已折）。

两处对计划的偏离，均为实现中暴露的必要细化：

1. **`Adapter` 多一个 `Encode(text string) string`**，且 `Replacement.Literal` 是
   **已按线格式编码好的字面量**（计划原文写的是「已解码的纯文本」）。理由：旧
   proxy 的渲染缓存与「指针必须比原文小」的判定都在**线字节**上做 —— JSON 转义
   能把一次折叠变成膨胀，按解码后长度比较会放过这种块。适配器负责编码后，内核
   的节省算术与缓存语义与旧路径逐字节等价，Task 8 的等价替换才有可比性。
2. **`fold/gate.go` 与 `fold/plan.go` 与 proxy 的 `live_compact_fold_gate.go` /
   `live_compact_fold_plan.go` 暂时并存**（计划把「迁入」写在本任务）。内核版本
   输入是中性类型，proxy 版本仍用 `scanRequest`；在一个提交里同时改内核与调用方
   会让等价性无法分辨。proxy 的那几份在 Task 8 的删除清单里，本任务只保证命名
   与判定逐条对齐（含 `roundOversized` 不施加 failed/min-block 门槛这一既有行为）。

测试 `engine_test.go` 7 条（上文 5 条 + `TestEngineGuardBlocksRewrite` +
`TestEngineObservesWithoutPermission`）、`plan_test.go` 6 条、`gate_test.go` 6 条。
计划指定的三处变异各自打红后还原：跳过 `verifyConversation` →
`TestEngineRejectsPairingChange` 红（`pairing change was not rejected: <nil>`）；
pass 1 的入选条件改成无条件跳过（`if true { continue }`，等于取消「已提交集合
无条件入选」）→ `TestEngineFoldsCommittedBlocksAfterFirstPass` 第二次断言红
（`second fold: … StickyBlocks:0 NewBlocks:1`）；删掉 `conv.Refuse` 检查 →
`TestEngineHonoursAdapterRefusal` 红。
`gofmt` / `go build ./server/...` / `go vet ./server/internal/agent/...` /
`go test ./server/internal/agent/...`（fold、proxy、claude、codex、acp、types、
agent）全绿。

---

### Task 7: Anthropic 适配器

**Files:**
- Create: `fold/anthropic/adapter.go`、`fold/anthropic/scan.go`、`fold/anthropic/wire.go`、`fold/anthropic/splice.go`、`fold/anthropic/pair.go`、`fold/anthropic/usage.go`
- 迁入来源：`proxy/live_compact_scan_request.go` + `live_compact_scan.go` 中的协议专属部分（`scanWireRequest`/`scanMessagePayload`/`scanBlockPayload`/`scanToolResultContent`）、`live_compact_wire.go`、`live_compact_wire_analysis.go`、`live_compact_wire_drift.go`、`live_compact_fold_splice.go`、`live_compact_prefix_guard.go`、`usage.go`，以及工具轮次配对逻辑（`pairToolRounds`，现在 `live_compact_fold.go` / `live_compact_fold_gate.go` 中）。通用 walker 已由 Task 5 迁入 `fold/walker.go`，这里只 import。
- Create: 对应 `_test.go`（自 proxy 侧测试迁入）
- Create: `fold/anthropic/imports_test.go`（不 import proxy）

**Interfaces:**
- Consumes: `fold.Adapter` 与 Task 6 的全部中性类型
- Produces: `anthropic.New() fold.Adapter`

- [ ] **Step 1: 迁移并实现 `Scan`**

把 `proxy/live_compact_scan.go`、`live_compact_scan_request.go`、`live_compact_wire.go`、`live_compact_wire_analysis.go`、`live_compact_wire_drift.go` 迁入 `fold/anthropic`（`package anthropic`），保留全部字节级扫描实现。`Scan` 输出：

- `Conversation.Model` = 顶层 `model`
- 每个 `Round` 对应一次 `tool_use` → 其全部 `tool_result`
- `Result.Parts` = `tool_result` 的文本字面量 span（含引号，原样）
- `Result.ToolName` / `InputSummary` = 由 `tool_use` 的 `name` 与 `summarizeToolInput` 得到
- `Result.Bytes` / `Tokens` = 现有估算函数
- `NonText` = `tool_result` 含图片/文档/未知块

- [ ] **Step 2: 实现 `Text` / `Splice` / `Usage` / `Guard` / `Window`**

- `Text`：`toolResultText` 的解码路径；仅当 `len(Parts)==1 && !NonText` 返回 `ok=true`。
- `Splice`：把 `Literal` 编码成 JSON 字符串（`json.Marshal`），替换 `Parts[0]` 的整段（含引号），其余字节逐字拷贝；返回新 body。
- `Usage`：现有 SSE `usage` 解析 + 归一化（`input` 不含缓存，见 spec §9 左列）。
- `Guard`：迁入 `live_compact_prefix_guard.go`，行为不变。
- `Window`：先返回 0（未知 → 内核用配置值）；Anthropic 侧目前不需要。

- [ ] **Step 3: 写等价性测试（关键）**

`fold/anthropic/equivalence_test.go`：读取 `testdata/` 下 5 个真实请求体（从 `/tmp/codex-probe` 之外的现有 proxy 测试夹具或线上脱敏样本取得），对每个 body：

```go
func TestAdapterScanSpliceRoundTrip(t *testing.T) {
    a := New()
    for _, body := range loadTestdata(t) {
        out, err := a.Splice(body, nil) // 空替换必须逐字节相同
        if err != nil { t.Fatal(err) }
        if !bytes.Equal(out, body) { t.Fatalf("empty splice changed bytes") }
        conv, err := a.Scan(body)
        if err != nil { t.Fatal(err) }
        reparsed, err := a.Scan(out)
        if err != nil { t.Fatal(err) }
        if len(conv.Rounds) != len(reparsed.Rounds) { t.Fatal("round count changed") }
    }
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go build ./server/... && go test ./server/internal/agent/fold/anthropic/`
Expected: PASS

- [ ] **Step 5: 变异验证**

- `Text` 改成总是返回 `ok=true` → 含 `NonText` 的用例红。
- `Splice` 用 `strconv.Quote` 代替 `json.Marshal` → 含 `<`/`>`/非 ASCII 的用例红（`json.Marshal` 会转义 `<`，`strconv.Quote` 不会；两者都必须与 CLI 的读取端兼容——以既有 `live_compact_fold_splice.go` 的实现为准）。

- [ ] **Step 6: 提交**

```bash
git add server/internal/agent/fold/anthropic
git commit -m "feat(live-compact): 新增 Anthropic Messages 适配器（Scope: G-MN）"
```

**执行结果（2026-11-20）**：`fold/anthropic` 落地，`anthropic.New()` 返回
`fold.Adapter`。`scan.go` 承接 `live_compact_scan_request.go` 的字节扫描（全部
walk 函数改为 `fold.Scan*`），`pair.go` 把 assistant 消息的 `tool_use` 组配成
`fold.Round`（开放轮次输出零 `Results`），`adapter.go` 实现 `Scan` / `Text` /
`Encode` / `Window`，`splice.go` 只替换 span 内字节，`usage.go` 承接 SSE 扫描
（导出 `UsageScanner` / `UsageTotals`，新增 `Adapter.Usage` 读整段 JSON 或整段
SSE），`guard.go` 承接前缀守卫。

三处对计划的偏离：

1. **没有单独的 `wire.go`**：字节扫描器（`scanRequest`/`scanMessage`/
   `scanBlock`）留在 `scan.go`，`pair.go` 只做配对。拆一个纯类型文件不会减少
   任何一处的理解成本。
2. **`Guard` 需要把「未知别名」带回调用方**：`warnUnknownModelOnce` 的每会话
   去重住在 proxy 的 session 表里，适配器拿不到。`fold.GuardVerdict` 新增
   `UnknownModel`、`fold.Outcome` 新增 `GuardUnknownModel`，由 Task 8 的调用方
   打日志；守卫本身仍不拦截别名。
3. **`Adapter.Usage` 的归一化口径与账本一致**：`ContextTokens = input +
   cache_creation + cache_read`、`RewriteTokens = input + cache_creation`、
   `Source = response`，与 `writeFoldLedger` 现有算法逐项相同，Task 8 切换后账本
   数字不变。

测试：`equivalence_test.go`（5 个 `testdata/*.json` 夹具：单结果、多轮多调用、
`is_error`、图片块、开放轮次）钉住空 splice 逐字节不变、span 扫描与
`encoding/json` 参考解析器逐轮逐结果一致、开放轮次零结果、`Text` 拒绝非文本、
`Encode` 合法 JSON 且不转义 HTML、单 span splice 前后字节不变、真引擎折叠
2 × 20 KB 结果并重扫通过、未知别名从 `Outcome.GuardUnknownModel` 浮出；
`usage_test.go` 5 条（SSE 合并 message_start/message_delta、整段 JSON、回放
SSE、缺失、超大行丢弃）；`guard_test.go` 4 条；`imports_test.go` 钉住适配器不
import proxy。两处变异各自打红后还原：`Text` 去掉 `NonText` 判断 →
`TestAdapterTextRejectsNonText` 红；`Encode` 换成 `strconv.Quote` →
`TestAdapterEncodeRoundTripsAndKeepsAngles` 红（`\x01` 不是合法 JSON 转义；
注意 `<` 在两条路径下都不转义，因为 `EncodeJSONStringLiteral` 设了
`SetEscapeHTML(false)`，所以鉴别点是控制字符而不是尖括号）。
`go vet ./server/internal/agent/...` 与 `go test ./server/internal/agent/...`
全绿。proxy 侧的 `usage.go` 与 `live_compact_prefix_guard.go` 暂时保留，与内核
副本在 Task 8 一起删除。

---

### Task 8: proxy 切到 `Engine`（等价替换）

**Files:**
- Modify: `server/internal/agent/proxy/live_compact_fold_request.go`
- Modify: `server/internal/agent/proxy/live_compact.go`、`proxy.go`（构造 `Engine`）
- Delete: `proxy/live_compact_scan.go`、`live_compact_scan_request.go`、`live_compact_wire*.go`、`live_compact_fold_splice.go`、`live_compact_fold_plan.go`、`live_compact_fold_render.go`、`live_compact_fold_gate.go`、`live_compact_cold.go`、`live_compact_pointer.go`、`live_compact_commits.go`、`live_compact_commit_log.go`、`live_compact_archive.go`、`live_compact_pointer_cache.go`、`live_compact_ledger.go`、`estimate.go`（均已迁出）
- Create: `proxy/fold_golden_test.go`

**Interfaces:**
- Consumes: `fold.NewEngine`、`fold/anthropic.New`
- Produces: `Server.engines map[string]*fold.Engine`（本任务只填 `"anthropic"`；Task 15 填 `"codex"`）

- [ ] **Step 1: 先落 golden**

在删除旧实现**之前**，新增 `proxy/fold_golden_test.go`，用 5 个固定 body（含：单个大 `tool_result`、多轮、`is_error`、含图片块、开放轮次）跑 `foldRequestLedger`，把输出 body 的 sha256 写进 `testdata/fold_golden.txt`。golden 与测试一起在 Step 5 提交。

Run: `go test ./server/internal/agent/proxy/ -run TestFoldGolden -v`
Expected: PASS，且 `testdata/fold_golden.txt` 生成

- [ ] **Step 2: 切换实现**

`live_compact_fold_request.go` 只保留：急停、`MinBodyBytes`、`foldAllowed`、校准、seq 派发、调用 `s.engine.Fold(...)`、账本写入。所有 scan/plan/render/splice/verify 都改由 `Engine` 完成。

`Server` 新增 `engines map[string]*fold.Engine`，在 `NewServerWithLiveCompact` 中当 `archive != nil` 时构造 `"anthropic"` 一项（`fold.NewEngine(cfg, anthropic.New(), archive, pointerCache)`）；`mode=shadow` 传 `FoldAllowed=false` 但保留观测。

- [ ] **Step 3: 跑 golden 与全量测试**

Run: `go test ./server/internal/agent/proxy/ -run TestFoldGolden -v && go test ./server/internal/agent/...`
Expected: PASS —— golden 哈希与切换前逐字节一致

- [ ] **Step 4: 变异验证**

把 `fold/anthropic` 的指针渲染改一个字符（"原文见" → "原文在"），`go test ./server/internal/agent/proxy/ -run TestFoldGolden` 必须红；还原。

- [ ] **Step 5: 提交**

```bash
git add server/internal/agent/proxy server/internal/agent/fold
git commit -m "refactor(live-compact): proxy 改用 fold 引擎，行为逐字节不变（Scope: G-MN）"
```

**执行结果（2026-11-20）**：等价替换落地 —— `testdata/fold_golden.txt` 五个 body 的转发 sha256 在切换前后逐字节一致，`decision` / `new` / `sticky` / `skipped` 计数也一致。

- **内核新增 `Request.Preview` + `fold/preview.go`（6 条用例）**：shadow 不能只靠 `FoldAllowed=false`，否则门禁一进内核就直接 observe-only、什么都不预演。`Preview` 让内核照常规划、报告决策与 would-fold 节省，并把「将会提交」的键记进**非持久** `Commits`（下一次预演看到同一条单调边界），但不渲染指针、不写存档、不碰渲染缓存、不改 body。`Outcome` 同时补 `Candidates`（零候选是「冷信号留给下一请求」的判据）与 `Model`（未知别名告警用）。
- **proxy 只剩请求路径**：`live_compact_fold_request.go` 保留急停、`MinBodyBytes`、校准、seq 派发、`Fold` 调用与账本写入；`foldEngine()` 按 adapter 名懒建并缓存引擎（`mode=off`/无存档的实例永不构造适配器），`foldSession()` / `applyFoldSession()` 先取承诺集再拼 `fold.Session` 视图，避开 `s.mu` 与承诺集锁的重入。`live_compact_gate_view.go` 承接原来的日志形状与 gate 常量（日志字符串一字未改，Phase A 工具链不受影响）。
- **删除**：生产侧 `live_compact_fold.go`、`_fold_gate.go`、`_fold_plan.go`、`_fold_render.go`、`_fold_splice.go`、`_scan_request.go`、`_cold.go`、`_prefix_guard.go` 八个文件；测试侧九个文件（`_fold_test`、`_fold_cache_test`、`_fold_gate_test`、`_fold_gate_norm_test`、`_shadow_preview_test`、`_cold_test`、`_prefix_guard_test`、`_error_fold_test`、`_scan_test`）—— 逐条核对过内核 `fold` / `fold/anthropic` 已有等价用例，行为覆盖没有减少。新增 `live_compact_testhelpers_test.go`，`live_compact_coverage_test.go` 改为从请求路径断言（不再直接调 planner）。
- **偏差一**：`live_compact_wire.go` / `_wire_analysis.go` / `_wire_drift.go` **没有删**。它们是 Phase A 观测管线，只读 body 喂 `[live-compact]` 日志、不参与折叠决策，删掉会连带丢掉线上观测与 `wire_test`；计划里把它们列进删除清单是当时按「太长就删」写的。
- **偏差二**：`fold_alias.go` 没有整体消失，而是缩到只留 proxy 自己命名的东西（配置/模式常量、`Server` 字段的存储类型、wire 分析用的估算助手）。其余 planner 时代的别名（scan 助手、pointer 键、冷判据常量等）随旧实现一起删除，测试改用 `fold.` 原名。
- **变异验证**：计划里写的 `原文见`→`原文在` 在适配器里并不存在（实际页脚是「需要原文时用 Read 打开 archive」），改为 `需要原文时用`→`需要原文用`，五个 golden 哈希全红；还原后复绿，证明 golden 真的钉住了指针渲染。
- **门禁**：`go build ./...` clean；`go test -count=1 ./server/internal/agent/...` 全绿（含 `-race` 跑 proxy / fold / fold/anthropic）；随后全仓 `go test -count=1 ./...` 结果见下方提交记录。

---

### Task 9: Responses 适配器 —— 扫描与配对

**Files:**
- Create: `fold/responses/adapter.go`、`fold/responses/scan.go`
- Create: `fold/responses/scan_test.go`、`fold/responses/testdata/README.md`
- Create: `fold/responses/testdata/request_two_turns.json`（脱敏夹具）

**Interfaces:**
- Consumes: `fold.Adapter`、`fold.Result/Round/Conversation/Span`
- Produces: `responses.New() fold.Adapter`；`Scan` 的具体规则见下

- [ ] **Step 1: 落夹具**

从 `/tmp/codex-probe/requests/002.json` 生成 `testdata/request_two_turns.json`，规则：

- 保留 `model`、`input` 数组结构、每一项的 `type`
- `reasoning.encrypted_content` 置为 `""`，`summary` 置为 `[]`
- 所有 `message` 的 `content[].text` 替换为 `"user-1"` / `"assistant-1"` 等占位
- `custom_tool_call.input` 保留类型但截断到 200 字节
- `custom_tool_call_output.output[0].text` 换成 `strings.Repeat("A", 30000)` 的等价物（写成一个 3 万字符的固定串）
- `function_call_output.output` 另造一项字符串形态
- 删除 `additional_tools`（或用 3 个假工具定义代替），避免 24 KB 无关内容

`testdata/README.md` 写明来源、脱敏规则、为什么可以进仓库（无用户内容、无凭据）。

- [ ] **Step 2: 写失败测试**

```go
func TestScanPairsCustomAndFunctionOutputs(t *testing.T) {
    conv, err := New().Scan(readFixture(t, "request_two_turns.json"))
    if err != nil { t.Fatal(err) }
    if len(conv.Rounds) != 2 { t.Fatalf("rounds = %d", len(conv.Rounds)) }
    if conv.Rounds[0].Results[0].CallID == "" || conv.Rounds[0].Results[0].ToolName != "exec" { t.Fatalf("custom round: %+v", conv.Rounds[0]) }
    if conv.Rounds[1].Results[0].ToolName != "shell" { t.Fatalf("function round: %+v", conv.Rounds[1]) }
}

func TestScanMarksOpenRound(t *testing.T) {
    body := []byte(`{"model":"gpt-6-luna","input":[{"type":"custom_tool_call","call_id":"c1","name":"exec","input":"x"}]}`)
    conv, err := New().Scan(body)
    if err != nil { t.Fatal(err) }
    if len(conv.Rounds) != 0 { t.Fatalf("open round must not be foldable: %+v", conv.Rounds) }
}

func TestScanRefusesStatefulRequests(t *testing.T) {
    for _, body := range [][]byte{
        []byte(`{"model":"gpt-6-luna","store":true,"input":[]}`),
        []byte(`{"model":"gpt-6-luna","store":false,"previous_response_id":"resp_1","input":[]}`),
    } {
        conv, err := New().Scan(body)
        if err != nil { t.Fatal(err) }
        if conv.Refuse != "unsupported" { t.Fatalf("body %s: refuse = %q", body, conv.Refuse) }
    }
}

func TestScanAllowsStatelessRequest(t *testing.T) {
    conv, err := New().Scan([]byte(`{"model":"gpt-6-luna","store":false,"input":[]}`))
    if err != nil { t.Fatal(err) }
    if conv.Refuse != "" { t.Fatalf("refuse = %q", conv.Refuse) }
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./server/internal/agent/fold/responses/ -run TestScan -v`
Expected: FAIL — 包不存在 / `undefined: New`

- [ ] **Step 4: 实现 Scan**

用 Task 5 的 `fold.ScanEachMember` / `fold.ScanEachElement` / `fold.JSONStringAt` 做字节遍历（**不 import `fold/anthropic`**：两个协议的解析规则会分叉，共享解析层会让一次修改变成两处风险；共享的只有字节 walker）。规则：

| 元素 | 处理 |
|---|---|
| `additional_tools` / `message` / `reasoning` | 跳过，不产生 Result |
| `custom_tool_call` / `function_call` | 记 `call_id`、`name`、参数摘要；开启 pending |
| `custom_tool_call_output` | `output` 是数组，取唯一的 `{type:"input_text",text}` 的 text span；关闭对应 pending |
| `function_call_output` | `output` 是字符串字面量；关闭对应 pending |
| 有调用无结果 | 不产生 Round（开放轮次不可折） |
| 顶层 `store:true` 或存在 `previous_response_id` | `Conversation.Refuse = "unsupported"`，不做任何改写（历史不在 body 里） |

`Result.InputSummary`：`custom_tool_call.input` 是自由 JS 字符串，取前 300 字符；`function_call.arguments` 是对象，复用指针的 key 偏好表（`file_path/command/...`）。

- [ ] **Step 5: 跑测试确认通过**

Run: `go build ./server/... && go test ./server/internal/agent/fold/responses/`
Expected: PASS

- [ ] **Step 6: 变异验证**

允许开放轮次进入 `Rounds` → `TestScanMarksOpenRound` 红；去掉 `store`/`previous_response_id` 检查 → `TestScanRefusesStatefulRequests` 红。各自还原。

- [ ] **Step 7: 提交**

```bash
git add server/internal/agent/fold/responses
git commit -m "feat(live-compact): Responses 适配器扫描与工具轮次配对（Scope: G-MN）"
```

**执行结果（2026-11-20）**：`fold/responses` 落地（`adapter.go` / `scan.go` / `pair.go`），提交 `45ef4bb2`。

- 抓包现场（`/tmp/codex-probe/requests/00{1,2,3}.json`）三个真实 Codex 请求都是 `store:false`、无 `previous_response_id`，且工具只用 `custom_tool_call`（Codex 把一切工具收在一个 `exec` 里）—— 所以 `function_call` 那半是按 Responses 公开形状**构造**进夹具的，并在 `testdata/README.md` 里写明。
- 实际输出比计划里假设的更宽：`custom_tool_call_output.output` 是**多个** `input_text` 块（真实抓包是两个）。适配器照实记录全部 text span，`Parts` 多于一块时引擎本来就不折（`Text` 只接受单 part），这保持了「一次只换一个字符串字面量」的不变量。
- 计划里写「`function_call.arguments` 是对象」不准确：线上是**编码成字符串的**对象。摘要因此先解字符串再复用 `fold.SummarizeToolInput` 的 key 偏好表；解不成对象就截 300 字符。
- 用例 7 条（计划 4 条 + 多块输出 / NonText 输出 / 非工具项三条），两处变异各自打红后还原。Step 2/3 的 RED 没有单独留证：实现与测试同一轮写出，红色证据来自 Step 6 的变异。

**并发事故（同一工作区另一条会话）**：Task 8 的 17 个删除在提交前被 `515fc827`「恢复 G-MN 二十个 proxy 文件」还原，`a009965d` 只带走了修改没带走删除，推送出去的 HEAD 因此 `go build ./...` 直接失败（`gateDisabled` 等重复声明）。`a525caf5` 重新删除并复验 `go build`，随后 `docs(custom)` 提交清掉 G-MN 里已删文件与锚点。教训：**删除类提交要先 `git show --stat HEAD` 确认删除真的进了提交，再从干净 worktree 跑 `go build`**（`check-upstream` 不编译，过了也不代表能构建）。

---

### Task 10: Responses 适配器 —— `Text`、`Splice` 与改写校验

**Files:**
- Create: `fold/responses/splice.go`、`fold/responses/splice_test.go`
- Modify: `fold/responses/adapter.go`（补 `Text`/`Splice`/`Window`）

**Interfaces:**
- Consumes: Task 9 的 `Scan`
- Produces: 完整可用的 `fold.Adapter`（`Usage` 除外，见 Task 11）

- [ ] **Step 1: 写失败测试**

```go
func TestSpliceOnlyReplacesOutputText(t *testing.T) {
    a := New()
    body := readFixture(t, "request_two_turns.json")
    conv, _ := a.Scan(body)
    r := conv.Rounds[0].Results[0]
    text, ok := a.Text(body, r)
    if !ok || len(text) < 1000 { t.Fatalf("text extraction failed: ok=%v len=%d", ok, len(text)) }
    out, err := a.Splice(body, []fold.Replacement{{Span: r.Parts[0], Literal: "[folded]"}})
    if err != nil { t.Fatal(err) }
    if !bytes.Contains(out, []byte("[folded]")) { t.Fatal("replacement missing") }
    // 非目标字节逐字不变：把替换点之外的两段切片比较
    if !bytes.Equal(out[:r.Parts[0].Start], body[:r.Parts[0].Start]) { t.Fatal("prefix changed") }
    if !bytes.Contains(out, []byte(`"encrypted_content":""`)) { t.Fatal("reasoning rewritten") }
}

func TestSpliceRejectsOpenOrMultiPart(t *testing.T) {
    a := New()
    body := []byte(`{"input":[{"type":"function_call_output","call_id":"c","output":"x"}]}`)
    _, err := a.Splice(body, []fold.Replacement{{Span: fold.Span{Start: 0, End: 0}, Literal: "y"}})
    if err == nil { t.Fatal("zero span must be rejected") }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./server/internal/agent/fold/responses/ -run TestSplice -v`
Expected: FAIL — `undefined` / 行为不符

- [ ] **Step 3: 实现**

`Text`：按 span 解码 JSON 字符串字面量（与 Anthropic 相同规则：无转义走快路径）。

`Splice`：按 span 定位并替换，其余字节逐字拷贝。两种形态统一处理——span 记录的是 `output` 内部**文本字面量**的位置，与它是"数组里的 text 字段"还是"整个 output 字符串"无关。

`Window`：内置表 `{"gpt-6-luna": 272000}`，未知返回 0。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./server/internal/agent/fold/responses/`
Expected: PASS

- [ ] **Step 5: 变异验证**

`Splice` 顺手重新编码整个 `input` 数组（`json.Marshal` 整包）→ `TestSpliceOnlyReplacesOutputText` 的 `encrypted_content` 断言与字节前缀断言同时红；还原。

- [ ] **Step 6: 提交**

```bash
git add server/internal/agent/fold/responses
git commit -m "feat(live-compact): Responses 适配器按字节区间改写工具输出（Scope: G-MN）"
```

**执行结果（2026-11-20）**：`fold/responses/splice.go` + `splice_test.go` 落地，提交 `0dbd6216`。

- **偏离计划：拼接抽到内核**。`Splice` 只是「按 span 写 literal、其余字节逐字拷贝」的字节手术，与协议无关；Responses 版会与 Anthropic 版逐字重复，所以新增 `fold/splice.go::SpliceReplacements`，两个适配器的 `Splice` 都退化成一行转发。Anthropic 侧零行为变化由 proxy 的五个 golden 哈希钉住（提交后仍在 `proxy` 包全绿）。
- **偏离计划：没有改 `adapter.go`**。`Text`/`Encode`/`Splice`/`Window` 单独放 `splice.go`，`adapter.go` 保持只有 `Name`/`Scan`——一个文件一件事，也避免 Task 11 补 `Usage`/`Guard` 时把两个职责塞回同一个文件。
- 用例 6 条：前缀/后缀逐字不变 + `encrypted_content` 未动 + 折叠后体积真的变小、坏 span 三种（零长 / 反向 / 越界）、空替换是 no-op、`Text` 拒绝多 part 与 NonText、`Encode` → `Splice` → `Scan` → `Text` 往返、`Window` 已知与未知（未知返回 0，由引擎回落到配置值）。
- 两处测试侧修正：往返用例最初的 body 只有 `function_call_output` 没有配对 call，`Scan` 不产轮次直接 panic——补上配对的 `function_call`（也顺带覆盖了「arguments 是字符串」的真实形状）；`TestSpliceOnlyReplacesOutputText` 最初断言「原文 A 一个不剩」不成立，因为夹具第二轮本身也有 30,000 个 A，改为断言折叠后体积至少少 20 KB。
- 变异：`Splice` 顺手 `json.Marshal` 整包 → `TestSpliceOnlyReplacesOutputText` 在 `prefix changed` 处红，还原后全绿。

---

### Task 11: Responses 用量归一化与适配器守卫

**Files:**
- Create: `fold/responses/usage.go`、`fold/responses/usage_test.go`、`fold/responses/guard_test.go`
- Modify: `fold/responses/adapter.go`（补 `Usage`/`Guard`/`Name`）

**Interfaces:**
- Consumes: `fold.Usage`、`fold.GuardVerdict`
- Produces: `responses.New()` 完整实现

- [ ] **Step 1: 写失败测试**

```go
func TestUsageNormalization(t *testing.T) {
    resp := []byte(`{"usage":{"input_tokens":13582,"input_tokens_details":{"cached_tokens":13246,"cache_write_tokens":333},"output_tokens":25}}`)
    got, ok := New().Usage(resp)
    if !ok { t.Fatal("usage missing") }
    if got.ContextTokens != 13582 { t.Fatalf("context = %d", got.ContextTokens) }
    if got.CacheRead != 13246 || got.CacheWrite != 333 { t.Fatalf("cache = %+v", got) }
    if got.RewriteTokens != 336 { t.Fatalf("rewrite = %d, want input-cached = 336", got.RewriteTokens) }
    if got.Source != "response" { t.Fatalf("source = %q", got.Source) }
}

func TestUsageMissingSource(t *testing.T) {
    got, ok := New().Usage([]byte(`{"id":"x"}`))
    if ok { t.Fatal("no usage block must report !ok") }
    if got.Source != "missing" || got.RewriteTokens != -1 { t.Fatalf("got %+v", got) }
}

func TestGuardAlwaysAllows(t *testing.T) {
    if v := New().Guard("gpt-6-luna", fold.Config{}.Normalized()); v.Block { t.Fatalf("responses must not block: %+v", v) }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./server/internal/agent/fold/responses/ -run 'TestUsage|TestGuard' -v`
Expected: FAIL

- [ ] **Step 3: 实现**

`Usage` 接受两种响应形态：整包 JSON 与 SSE（逐行 `data: ` 累积，取最后一个含 `usage` 的事件）。归一化按 spec §9 右列：`ContextTokens = input_tokens`（已含缓存），`RewriteTokens = input_tokens - cached_tokens`（不小于 0）。无 usage 时返回 `Usage{Source:"missing", RewriteTokens:-1}, false`。

`Guard` 恒 `{Block:false}`；`Window` 表（Task 10 已加）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./server/internal/agent/fold/responses/`
Expected: PASS

- [ ] **Step 5: 变异验证**

`RewriteTokens` 改成 `input + cached` → `TestUsageNormalization` 红；还原。

- [ ] **Step 6: 提交**

```bash
git add server/internal/agent/fold/responses
git commit -m "feat(live-compact): Responses 用量归一化与适配器守卫（Scope: G-MN）"
```

**执行结果（2026-11-20）**：`fold/responses/usage.go` + `usage_test.go` + `guard_test.go` 落地；`adapter.go` 补一行 `var _ fold.Adapter = Adapter{}`，从此 Responses 适配器是编译期完整的 `fold.Adapter`。

- 口径按 spec §9 右列：`ContextTokens = input_tokens`（Responses 的 `input_tokens` 已经**含**缓存部分，与 Messages 的三个互斥计数器相反），`RewriteTokens = input_tokens - cached_tokens` 且夹到不小于 0（防 detail 块落后于 `input_tokens` 时把重算算成负数）。`cache_write_tokens` 只在分项计费缓存写入的供应商上出现，缺失即 0。
- `Usage` 一份解析覆盖两种形态：整包 JSON（非流式，usage 在顶层或 `response` 里）与 SSE（逐行 `data:`，取最后一个含 usage 的块，跳过 `response.created` 里的 `usage:null`）。逐行扫描用索引切分而不是 `bytes.Split`，8 MB 的响应体不会为每行分配一个切片；单行超过 1 MiB 直接跳过（usage 事件很小，超长的不是它）。
- 无用量时显式 `Source=missing, RewriteTokens=-1` 并返回 `false`——与 Anthropic 侧返回零值不同，账本按 `UsageSourceMissing` 单独计数，不能让「没有数」被读成「省了 0」。
- `Guard` 恒放行：前缀闸门保护的是「改写会让 preserved thinking 失效」，而 Responses 的推理是 `encrypted_content` 不透明项、折叠只换工具输出，没有可保护的对象。
- 用例 6 条（整包归一化、SSE 末块、`response` 包一层、缺失显式、负重算夹零、守卫对三个模型名都放行）。RED 以临时移走 `usage.go` 取证（`Guard`/`Usage` undefined）。变异两处：`RewriteTokens` 改 `input + cached` → 归一化用例红（26828 ≠ 336）；缺失分支退回零值 → 缺失用例红；各自还原后全绿。

---

### Task 12: 代理路由 `/c/{provider}/v1` 与 provider 发现

**Files:**
- Create: `server/internal/agent/proxy/provider_route.go`、`proxy/provider_route_test.go`
- Create: `server/internal/agent/pool_codex_proxy.go`、`pool_codex_proxy_test.go`
- Modify: `server/internal/agent/proxy/proxy.go`（注册路由与 `RegisterProvider`）

**Interfaces:**
- Consumes: `fold.Engine`（已在 Server 上）
- Produces:
  - `(*Server).RegisterProvider(name, realBaseURL string)`
  - `(*Server).ProviderProxyURL(name string) string` → `http://127.0.0.1:{port}/c/{name}/v1`
  - `codexProviderBaseURLs() (map[string]string, error)`：读 `<CODEX_HOME>/config.toml` 的 `[model_providers]`，跳过 `base_url` 为空的条目
  - `codexProxyArgs(providers map[string]string, s *proxy.Server) []string` → 形如 `["-c","model_providers.llmux.base_url=http://127.0.0.1:PORT/c/llmux/v1"]`

- [ ] **Step 1: 写失败测试**

`proxy/provider_route_test.go`：

```go
func TestProviderRouteForwardsToRegisteredBaseURL(t *testing.T) {
    var gotPath string
    upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        gotPath = r.URL.Path
        w.Write([]byte(`{"ok":true}`))
    }))
    defer upstream.Close()
    s, err := NewServerWithLiveCompact(LiveCompactConfig{})
    if err != nil { t.Fatal(err) }
    defer s.Close()
    s.RegisterProvider("llmux", upstream.URL+"/v1")
    resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/c/llmux/v1/responses", s.Port()), "application/json", strings.NewReader(`{}`))
    if err != nil { t.Fatal(err) }
    defer resp.Body.Close()
    if resp.StatusCode != 200 || gotPath != "/v1/responses" { t.Fatalf("status=%d path=%q", resp.StatusCode, gotPath) }
}

func TestProviderRouteUnknownNameIs404(t *testing.T) { /* /c/nope/v1/responses → 404 */ }

func TestProviderRoutePassesUpstreamErrorThrough(t *testing.T) {
    upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusBadGateway)
        w.Write([]byte(`{"error":{"message":"upstream boom"}}`))
    }))
    defer upstream.Close()
    s, _ := NewServerWithLiveCompact(LiveCompactConfig{})
    defer s.Close()
    s.RegisterProvider("llmux", upstream.URL+"/v1")
    resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/c/llmux/v1/responses", s.Port()), "application/json", strings.NewReader(`{}`))
    if err != nil { t.Fatal(err) }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusBadGateway { t.Fatalf("status = %d", resp.StatusCode) }
    body, _ := io.ReadAll(resp.Body)
    if !bytes.Contains(body, []byte("upstream boom")) { t.Fatalf("body = %s", body) }
}
```

`agent/pool_codex_proxy_test.go`：

```go
func TestCodexProviderBaseURLsReadsTOML(t *testing.T) {
    dir := t.TempDir()
    t.Setenv("CODEX_HOME", dir)
    os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[model_providers.llmux]\nbase_url = \"https://up.example/llmux/v1\"\nwire_api = \"responses\"\n[model_providers.broken]\n"), 0o600)
    got, err := codexProviderBaseURLs()
    if err != nil { t.Fatal(err) }
    if got["llmux"] != "https://up.example/llmux/v1" { t.Fatalf("got %+v", got) }
    if _, ok := got["broken"]; ok { t.Fatal("provider without base_url must be skipped") }
}

func TestCodexProxyArgsRedirectBaseURL(t *testing.T) {
    args := codexProxyArgs(map[string]string{"llmux": "https://up.example/llmux/v1"}, "http://127.0.0.1:9999/c/llmux/v1")
    if len(args) != 2 || args[0] != "-c" || args[1] != "model_providers.llmux.base_url=http://127.0.0.1:9999/c/llmux/v1" { t.Fatalf("%v", args) }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./server/internal/agent/ -run 'TestCodexProvider|TestCodexProxyArgs' -v && go test ./server/internal/agent/proxy/ -run TestProviderRoute -v`
Expected: FAIL

- [ ] **Step 3: 实现**

`provider_route.go`：路由 `/c/`，`SplitN(TrimPrefix(path,"/c/"), "/", 2)` 得 `name` 与 `rest`；`RegisterProvider` 把 `realBaseURL` 存进 `map[string]string`（带 mutex）。转发：目标 = `realBaseURL` + `"/"` + `rest` 的路径部分，沿用 `handleProxied` 的 header 与 body 原样转发、SSE 旁路逻辑。未知 provider → 404（不是 502，避免误以为是上游故障）。

`pool_codex_proxy.go`：用 `toml.Unmarshal` 读 `[model_providers]` 为 `map[string]map[string]any`，只取字符串 `base_url`。`codexProxyArgs` 按 provider 名排序生成参数（Go map 迭代无序，排序让参数可复现）。**绝不在日志里打印 base_url**（可能含 token 查询参数），只打印 provider 名。

转发必须透传上游状态码与响应体：`handleProxied` 已有的透明转发逻辑直接复用，不重新包装错误（否则 4xx/5xx 会被伪造成 200 或统一成 502）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go build ./server/... && go test ./server/internal/agent/...`
Expected: PASS

- [ ] **Step 5: 变异验证**

`RegisterProvider` 不存 baseURL（直接转发到原路径）→ `TestProviderRouteForwardsToRegisteredBaseURL` 红；`codexProviderBaseURLs` 不跳过空 `base_url` → 第二个用例红。

- [ ] **Step 6: 提交**

```bash
git add server/internal/agent/proxy server/internal/agent/pool_codex_proxy.go server/internal/agent/pool_codex_proxy_test.go
git commit -m "feat(live-compact): 代理新增按 provider 的转发路由与 Codex provider 发现（Scope: G-MN）"
```

**执行结果（2026-11-20）**：`proxy/provider_route.go` + `provider_route_test.go`、`pool_codex_proxy.go` + `pool_codex_proxy_test.go` 落地，`proxy.go` 注册 `/c/` 路由并加 `providerMu`/`providers` 两个字段。

- 路由语义：`/c/{name}/{tail}` → `normalizeProviderBase(base) + "/" + tail`。注册时把 base 末尾的 `/` 与 `/v1` 去掉，因为路由自己已经带 `/v1/...`；不去重就会出现 `/v1/v1/responses`（变异验证的正是这一条）。未知 provider 回 404 而不是 502：这是我们的接线错误，502 会把排查引到上游。
- 转发是透明的：方法、query、头（除 `Host`）、体、状态码、响应体原样过去，SSE 走同一个 `flushWriter` 逐块下发。上游错误不重新包装，CLI 才能读到 provider 自己的报错文本。
- **偏离计划：`codexProxyArgs` 的签名与取值**。签名改成 `codexProxyArgs(providers map[string]string, proxyURL func(name string) string) []string`——按 provider 名分发地址，比传一个已经拼好的 URL 更贴合调用点；右值写成**带引号的 TOML 字符串**（`model_providers.llmux.base_url="http://..."`），因为 `-c` 按 TOML 解析右值，裸 URL 不是合法 TOML 值。provider 名按字典序排序，argv 可复现。
- `codexProviderBaseURLs` 只认**裸键**名（`[A-Za-z0-9_-]`）：名字要拼进 `-c` 的 TOML 键，带点或引号的名字需要另一套转义规则，宁可跳过也不要生成 Codex 解析不了的覆盖项。缺 `config.toml` 返回空表且不报错（ChatGPT 登录的配置本来就没有 `[model_providers]`），`base_url` 为空的条目跳过（这就是 Codex 表达「该 provider 走登录」的方式）。
- 用例 7 条（转发路径/query/头/体、未知 404、上游错误透传、`ProviderProxyURL`、TOML 读取含跳过规则、缺文件、`-c` 参数排序与引号）。变异两处：去掉 `/v1` 归一化 → 转发用例红（`/v1/v1/responses`）；不跳过空 `base_url` → TOML 用例红；各自还原后全绿。

---

### Task 13: Codex 会话注入与 thread 身份

**Files:**
- Modify: `server/internal/agent/pool_open.go`（Codex 分支）
- Modify: `server/internal/agent/pool.go`（`compactProxy` 已是字段；新增 provider 注册在构造函数）
- Modify: `server/internal/agent/proxy/proxy.go`（`RegisterThread`、`/c/` 请求按 `session-id` 建状态）
- Create: `server/internal/agent/proxy/thread_test.go`

**Interfaces:**
- Consumes: Task 12 的 `RegisterProvider`/`ProviderProxyURL`/`codexProviderBaseURLs`/`codexProxyArgs`
- Produces:
  - `(*Server).RegisterThread(threadID, sessionKey string)`
  - `(*Server).UnregisterThread(threadID string)`
  - Codex 分支的 `Args` 与 `Env` 改造

- [ ] **Step 1: 写失败测试**

```go
func TestCodexRequestUsesRegisteredThreadSession(t *testing.T) {
    s, _ := NewServerWithLiveCompact(LiveCompactConfig{})
    defer s.Close()
    s.RegisterProvider("llmux", upstreamURL)
    s.RegisterThread("thread-1", "mindfs-session-key")
    // 发一个带 session-id: thread-1 的 /c/llmux/v1/responses
    // 断言：s.SessionContextSaving("mindfs-session-key") 能读到账本记录
}

func TestUnknownThreadIsObserveOnly(t *testing.T) {
    // session-id: thread-unknown，mode=on
    // 断言：上游收到的 body 与原始逐字节相同（未折叠），且日志出现一次 unknown-thread
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./server/internal/agent/proxy/ -run 'TestCodexRequest|TestUnknownThread' -v`
Expected: FAIL

- [ ] **Step 3: 实现**

- `RegisterThread` 建立 `threadID → sessionKey` 映射，并为该 thread 建 `sessionState`（`foldAllowed=true`、`realBaseURL` 由 provider 路由决定、`commits = fold.NewCommitsFor(...)`）。
- `/c/` 请求：先读 `session-id` 头，缺失读 `prompt_cache_key`；都缺失 → 透明转发、只观测。
- `pool_open.go` Codex 分支：

```go
providers, err := codexProviderBaseURLs()
if err != nil { log.Printf("[proxy] codex providers: %v", err) } // 不中断会话
args := append([]string{}, def.Args...)
if p.compactProxy != nil && len(providers) > 0 {
    for name, base := range providers { p.compactProxy.RegisterProvider(name, base) }
    args = append(args, codexProxyArgs(providers, p.compactProxy)...)
}
sess, err := p.codex.OpenSession(ctx, codex.OpenOptions{..., Args: args, OnCompact: p.compactProxy.SignalCompact})
if err == nil && p.compactProxy != nil && sess.SessionID() != "" {
    p.compactProxy.RegisterThread(sess.SessionID(), in.SessionKey)
}
```

- **不修改** `~/.codex/config.toml`；`-c` 只在子进程参数里生效（探针已证）。
- ChatGPT 登录模式（`default` profile）没有 `[model_providers]`，`providers` 为空 → 不注入、不注册，行为与今天完全一致。

- [ ] **Step 4: 跑测试确认通过**

Run: `go build ./server/... && go test ./server/internal/agent/...`
Expected: PASS

- [ ] **Step 5: 变异验证**

`session-id` 缺失时不看 `prompt_cache_key` → `TestCodexRequestUsesRegisteredThreadSession` 的 fallback 变体红（Step 1 再加一条用例）；还原。

- [ ] **Step 6: 提交**

```bash
git add server/internal/agent/proxy server/internal/agent/pool_open.go server/internal/agent/pool.go
git commit -m "feat(live-compact): Codex 会话注入代理并登记 thread 身份（Scope: G-MN）"
```

**执行结果（2026-11-20）**：`proxy/thread.go` + `thread_test.go` 落地；`provider_route.go` 接上折叠与账本；`pool_open.go` 注入 provider 覆盖；`pool.go` 登记/清理 thread。

- **thread 身份以真实抓包为准**：三条请求都带 `session-id` / `thread-id` / `x-client-request-id`（三者同值），**没有** `prompt_cache_key` 头。取值顺序改为 `session-id` → `thread-id` → `x-client-request-id`，全缺才不回退（计划里写的 `prompt_cache_key` 作废）。
- thread 状态**复用 `sessions` 表**，键是 `thread:{id}`：父进程所有「按 key 取会话状态」的辅助函数（校准对、承诺集、冷信号、账本落盘）因此一行不改就能服务两种传输；前缀键也保证 thread id 不会撞上生成的 token。未登记的 thread 只转发不改字节，并对每个 thread 打**一行** `unknown codex thread`。
- `foldEngine()` 变成 `foldEngineFor(adapter)`（每个线格式一个引擎），`foldRequestLedger` 变成 `foldRequestLedgerFor(key, adapter, body)`；`/c/` 固定用 `responses`。账本落盘改为接收归一化后的 `fold.Usage`（`writeFoldLedgerUsage`），Anthropic 侧由 `usageTotals` 转过去，写出的字段与数值跟改前一致（既有账本用例全绿）。
- Responses 流式用量：新增 `responses.UsageScanner`（逐行、跨 chunk 边界、单行 1 MiB 上限），`/c/` 在折叠当轮用 `io.TeeReader` 旁路扫描，流结束后把 `RewriteTokens` 写进账本；带 `Content-Encoding` 的响应不扫，记 `missing` 而不是编一个 0。
- **偏离计划：注册点在 `GetOrCreate` 而不是 `openSession`**。只有赢下建会话竞争的 entry 才登记，输掉的那份不会留下映射；`closeSessions` 按 entry 上新增的 `threadID` 注销。provider 覆盖仍在 `openSession` 的 Codex 分支里加（`codexProviderBaseURLs` 失败只记日志，不阻断会话；`[model_providers]` 为空时完全不加参数，ChatGPT 登录配置行为不变）。
- 用例 5 条（注册线程后真实折叠 + 账本含 500000 重算 token + 最新轮逐字保留、未登记线程逐字转发、三个头名回退、注销即失效）；`responses` 侧另加 2 条扫描器用例（跨 chunk 分片、无 usage 事件报 missing）。变异三处：`/c/` 不解析 thread → 未折叠用例红；`RegisterThread` 的 `foldAllowed` 改 false → 同一用例红；流式用量不读 → 账本 `UsageSource=missing` 断言红。各自还原后全绿。

---

### Task 14: 压缩信号接入冷折叠

**Files:**
- Modify: `server/internal/agent/codex/session_open.go`（`OpenOptions.OnCompact func(threadID string)`，事件处调用）
- Modify: `server/internal/agent/codex/session_events.go`（三处 `EventTypeCompact` 前调 `s.onCompact(s.SessionID())`）
- Modify: `server/internal/agent/proxy/proxy.go`（`SignalCompact(threadID string)`）
- Create: `server/internal/agent/codex/session_compact_signal_test.go`、`proxy/compact_signal_test.go`

**Interfaces:**
- Consumes: Task 13 的 thread 状态
- Produces: `(*Server).SignalCompact(threadID string)`；`codex.OpenOptions.OnCompact`

- [ ] **Step 1: 写失败测试**

```go
func TestSignalCompactMarksNextDecisionCold(t *testing.T) {
    s := newTestServerWithThread(t, "thread-1", "key")
    s.SignalCompact("thread-1")
    // 触发一次 fold 决策；断言 Outcome.Cold==true && ColdReason=="compact"
    // 第二次决策断言 Cold==false
}

func TestCodexCompactEventInvokesOnCompact(t *testing.T) {
    var got string
    sess := newSessionWithOnCompact(func(id string) { got = id })
    sess.handleItem(&codexsdk.CompactedItem{})
    if got != sess.SessionID() { t.Fatalf("got %q", got) }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./server/internal/agent/proxy/ -run TestSignalCompact -v && go test ./server/internal/agent/codex/ -run TestCodexCompactEvent -v`
Expected: FAIL

- [ ] **Step 3: 实现**

`SignalCompact` 在 `s.mu` 内把对应 thread 的 `live.pendingColdFold = true`；未知 thread 静默忽略（日志一行，不报错——信号可能早于第一请求到达）。

`codex` 侧：`OpenOptions.OnCompact` 存进 `session`，`emitCompact` 统一封装三处调用点，`nil` 安全。

- [ ] **Step 4: 跑测试确认通过**

Run: `go build ./server/... && go test ./server/internal/agent/...`
Expected: PASS

- [ ] **Step 5: 变异验证**

`SignalCompact` 不清 `pendingColdFold`（消费一次语义丢失）→ 第二个断言红；还原。

- [ ] **Step 6: 提交**

```bash
git add server/internal/agent/proxy server/internal/agent/codex
git commit -m "feat(live-compact): Codex 压缩事件接入冷折叠信号（Scope: G-MN）"
```

**执行结果（2026-11-20）**：`SignalCompact` 落在 `proxy/thread.go`（与本任务新增的 thread 状态同文件），在锁内给 `thread:{id}` 的 `live.pendingColdFold` 置位并记时间；空 id 与未登记 thread 直接返回、不建状态（压缩事件可能早于第一条请求到达，这不是错误）。`codex` 侧 `OpenOptions.OnCompact` → `session.onCompact`，三处 `EventTypeCompact`（`CompactedItem`、`UnknownItem` 的 `contextCompaction`、按 `GetType()` 兜底的 `contextCompaction`）全部改走 `emitCompact`，先发 UI 事件再调 hook，nil 安全；`pool_open.go` 在 `compactProxy != nil` 时把 `SignalCompact` 接上。

用例：proxy 两条 —— 冷决策命中 `Cold==true / ColdReason=="compact"`，第二次决策必须不再冷（消费一次），以及未知/空 thread 不产生状态；codex 三条 —— 三种事件形态各触发一次且携带真实 thread id、未装 hook 时照常发事件不 panic。变异两处：`applyFoldSession` 不写回 `PendingColdFold` → 「第二次仍冷」红；`emitCompact` 去掉 hook 调用 → 三个子用例全红。各自还原后 `go build ./server/...` 与 `go test -count=1 ./server/internal/agent/...` 全绿。

---

### Task 15: 配置覆盖、端到端验收与登记

**Files:**
- Modify: `server/internal/agent/pool.go`（`PoolOptions.LiveCompactHarnesses map[string]HarnessOverride`）
- Modify: `server/cmd/mindfs-server/main.go`、`cli/cmd/startup.go`（解析 `live_compact.harness.codex.*`）
- Create: `server/internal/agent/proxy/harness_config_test.go`
- Modify: `docs/upstream-customizations.yaml`（G-MN 新锚点）
- Modify: `docs/superpowers/plans/2026-11-20-live-compact-universal-fold-core.md`（执行记录）

**Interfaces:**
- Consumes: 全部前置任务
- Produces: `fold.Config.ForHarness(name string) Config`（未命中覆盖时返回自身）

- [ ] **Step 1: 写失败测试**

```go
func TestHarnessOverrideAppliesCodexDefaults(t *testing.T) {
    c := Config{Mode: "on", RailTokens: 100000, TargetTokens: 20000}.Normalized()
    c.Harnesses = map[string]HarnessOverride{"codex": {Enabled: boolPtr(true), RailTokens: 200000, TargetTokens: 60000, ColdRailTokens: 120000}}
    got := c.ForHarness("codex")
    if got.RailTokens != 200000 || got.TargetTokens != 60000 || got.ColdRailTokens != 120000 { t.Fatalf("%+v", got) }
    if c.ForHarness("claude").RailTokens != 100000 { t.Fatal("claude must keep global rail") }
}

func TestHarnessDisabledObservesOnly(t *testing.T) {
    c := Config{Mode: "on"}.Normalized()
    c.Harnesses = map[string]HarnessOverride{"codex": {Enabled: boolPtr(false)}}
    if c.ForHarness("codex").Folds() { t.Fatal("disabled harness must not fold") }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./server/internal/agent/fold/ -run TestHarness -v`
Expected: FAIL

- [ ] **Step 3: 实现配置贯通**

`fold.Config` 增加 `Harnesses map[string]HarnessOverride`；`Config.ForHarness(name)` 在 `Normalized()` 之后应用覆盖（覆盖为 0 的字段回落全局值）；`Enabled=false` 时把 `Mode` 降为 `shadow`。

`pool.go` 构造 proxy 时按 harness 不能用同一份 Config——`Server` 改为持有 `map[string]fold.Config`，`/c/{provider}` 请求按 provider → harness 映射（`llmux` 是 Responses，`anthropic` 直连是 Anthropic）取配置；`/p/` 请求固定用 anthropic 配置。用 `harnessForProvider(name string) string` 单点决定，默认 `codex`（Responses 路由只服务 Codex）。

两处 `main.go` 的 JSON 结构体加 `Harness map[string]...`，把 4 个键原样透传。

Codex 引擎复用同一个 `<config-dir>/context`，但存档与账本落在 `context/codex/` 与 `context/codex/ledger/`：两个 harness 的 commit log 与账本各自独立，互不覆盖（spec §14「存档/账本路径冲突」）。

`Window(model)` 目前只是适配器自述能力，内核阈值用的是显式 token 数（`RailTokens` 等），不依赖它；未知模型返回 0，保留给将来的自动阈值。

- [ ] **Step 4: 跑单元测试**

Run: `go build ./server/... && go test ./server/internal/agent/...`
Expected: PASS

- [ ] **Step 5: 端到端验收（隔离实例，真实 codex + gpt-6-luna）**

```bash
# 1. 独立 CODEX_HOME 与独立端口，不碰 ~/.codex 与 7331
export CODEX_HOME=/tmp/mindfs-lc-codex/home
mkdir -p "$CODEX_HOME" && cp /tmp/codex-probe/home/config.toml "$CODEX_HOME/config.toml"
# 2. 起隔离 mindfs（复用 scripts/mindfs-iso.sh 的模式，端口 7460）
# 3. 在隔离实例里建 codex 会话，让模型连续做 3 次大文件 Read（每次 > 40 KB）
# 4. 断言：
#    - 日志出现 [live-compact] fold 且 mode=on
#    - GET /api/sessions/{key}/context-saving 的 requests >= 1
#    - 最后追问「第一次读到的文件前 3 行是什么」，答案与文件内容一致（指针可读回）
# 5. 把 live_compact.harness.codex.enabled 改成 false 重启，重跑同样负载，日志零 fold
```

记录实际数值（折叠块数、saved_bytes、请求数）到本计划文末「执行记录」。

- [ ] **Step 6: 登记与门禁**

- `docs/upstream-customizations.yaml` 的 G-MN 组补新锚点：`fold/types.go`、`fold/engine.go`、`fold/responses/*`、`proxy/provider_route.go`、`pool_codex_proxy.go`。
- `go test -count=1 ./...`、`go build ./server/...` 全绿。
- 干净 worktree 上 `make check-upstream` 全绿。

- [ ] **Step 7: 提交**

```bash
git add server/internal/agent server/cmd/mindfs-server/main.go cli/cmd/startup.go docs/upstream-customizations.yaml docs/superpowers/plans/2026-11-20-live-compact-universal-fold-core.md
git commit -m "feat(live-compact): Codex 按 harness 覆盖配置并完成端到端验收（Scope: G-MN）"
```

**执行结果（2026-11-20）**：配置贯通落地 —— `fold.Config` 新增 `Harnesses map[string]HarnessOverride`
与 `ForHarness(name)`（覆盖为 0 的字段回落节点值，`enabled=false` 把 `mode=on` 降为 `shadow`）；两处启动解析
（`cli/cmd/startup.go`、`server/cmd/mindfs-server/main.go`）把 `live_compact.harness.<name>.*` 原样透传到
`PoolOptions.LiveCompactHarnesses`；`proxy.Server` 按 harness 缓存运行时（`harnessRuntime`：各自的 config /
archive / ledger / engine），Codex 的存档与账本落在 `<config-dir>/context/codex/` 与 `.../codex/ledger/`。

**端到端验收第一次就把真缺陷打出来了**：隔离实例（端口 7460、真实 `codex-cli` 0.169.1 + `gpt-6-luna` 经 llmux、
独立 `XDG_CONFIG_HOME`/`CODEX_HOME`）连读 4 个 49 KB 文件，日志里**一条 fold 都没有**，每条请求都是
`candidates=0 gate=no-backlog`。抓下真实请求体（347,093 B）用内核回放才看清：Codex 的每个
`custom_tool_call_output` 都是**两个** `input_text` 块（`Script completed / Wall time` 头 ≈47 B + 载荷 ≈40 KB，
7/7 个输出全如此），而 `PlanCandidates` 当时要求 `len(result.Parts) == 1`，于是所有 Codex 结果都被跳过 ——
上下文照涨，折叠永远不触发，正是用户报告的「只省了很少的 token」。修复（`fold/plan.go`）：
多块结果改为「只折最大的那一块」（`largestPart`），候选资格不再看块数；`Text`/`Splice` 的「一次只换一个字面量」
不变量不变。同时补两处守卫：① `fold/engine.go` 遇到渲染结果以 `PointerMarker` 开头直接跳过
（`renderSkipFolded`）——否则「头 + 指针」这类多块结果的最大块就是上一轮的指针，会一轮包一层；
② provider 路由此前**没有任何每请求观测行**（诊断时只能靠抓包），新增
`[live-compact] provider=responses token=… gate=… candidates=… folded=… blocks=… saved_bytes=…`；
`ShortToken` 对 `thread:<uuid>` 保留尾 8 位（此前每个 Codex 会话都打成 `token=thread:0`）。

**验收数值（修复后，同一隔离实例）**：

- 重负载会话 `1795174185-992e5137eb18`：`[live-compact] fold token=…# blocks=1 new_blocks=1 … gate=fold` 起，
  随后 sticky/`no-backlog`；胶囊 `GET /api/sessions/<key>/context-saving?root=proj`：`requests: 9`、
  `max_saved_bytes: 48746`、`blocks: 3`，末轮 `req_bytes=242653 → folded_bytes=193907`。回放同一 347 KB 真实
  body：`candidates=6 blocks=6 tokens_est=45070 bytes=180257 outlen=172225`。
- 追问「第一次读到的文件前 3 行是什么」，答案 `alpha-1-0x01 / beta-1-0x007 / gamma-1-0x00d` 与文件一致（指针可读回）。
- 短负载复跑（`1795174712-c6c48c0d4ce2`，4 个文件各读首行）：`requests: 7`、`saved_bytes: 5889`、`blocks: 2`；
  每轮新增的 Python 小输出进候选后 `skipped` 递增（指针比原文大），是预期行为；四条回复全部命中文件首行。
- 反向验收：`live_compact.harness.codex.enabled=false` 重启后换新会话（`1795174335-20d0ef5dfa6c`）重跑同样负载，
  日志零 fold 行、胶囊 `requests: 0`；改回后立刻恢复折叠。
- 变异验证：多块收窄改回「块数必须为 1」→ 3 条用例 RED；去掉指针守卫 → 内核用例 RED；`ShortToken` 退回前缀 →
  日志用例 RED（重现 `token=thread:0`）；`provider=responses` 字段缺失 → 观测行用例 RED。各自还原后复绿。

**门禁**：`go test ./server/internal/agent/...`（fold / fold/anthropic / fold/responses / proxy）全绿、
`go build ./server/...` 成功；全仓 `go test -count=1 ./...`、干净 worktree 的 `make check-upstream` 见提交记录。

**HEAD 复验（2026-11-20 22:20–22:55，用户指令「执行隔离测试，用 luna」）**：在干净 HEAD `e27f8a84`
上重建隔离实例（端口 7460、`/tmp/lc-run`、真实 `codex-cli` + `gpt-6-luna` 经 llmux、独立
`XDG_CONFIG_HOME`/`CODEX_HOME`），先确认二进制含 B10 单行页脚。重负载会话
`1795185602-1c03d6fe9fc5`（两次完整 `cat` + 分段 `sed`，116 次工具调用）：日志 19 次块提交
（合计 `tokens_est=68147 bytes=271438`），此后每请求 `sticky_blocks=19 new_blocks=0
gate=no-backlog`，`skipped=0 archive_errors=0`；胶囊 `requests=46`、`max_saved_bytes=244126`、
`total_rewrite_tokens=892482`、`usage_missing=0`，末轮 `req_bytes 1263754 → folded 1019628`；
模型自己在回复里说明「补读区间也有少数响应被工具再次折叠」，按指针改用更小 `sed` 区间。
**指针可读回（强版）**：三个 fixture 全部移走后追问 `charlie.txt` 第 0050 行，模型只发起 1 次
工具调用、按指针打开 `context/codex/330c823e….txt`，答案 `0050 charlie-0050-tgxywg …` 与文件
一致；该存档逐字节等于当时 Codex 截断后的工具输出（前 250 行 + `…18000 tokens truncated…` +
后 250 行）、权限 0600；commit log 反查 19 个已提交存档 `missing=0`（盘上未被引用的新存档由
7 天/容量清扫处理，不是丢失）。**反向验收**：`harness.codex.enabled=false` 重启后同负载新会话
`1795186395-6acd0add0a96`，日志 `gate=fold candidates=2 folded=false blocks=0`、存档数不变、
胶囊 `requests=0`，证明「观测照常、改写关闭」。生产 7331 全程未碰（PID 2769042、health 200、
`exe` 仍是已删除的旧二进制）；本轮只改本记录，无代码改动。

---

## B10 指针瘦身、保护尾部收紧与「多块一起折」取证（2026-11-20）

用户指令三条：「1.指针压一下 2.保护区砍到20k 3.我还是觉得可以多块一起折 请探究一下」。

**① 指针瘦身（已实现）**。旧卡片六行标题 + 「摘要（首尾各 8 行）：」标签 + 长页脚，实测在真实语料上
固定框架约占 858 B 卡片的 290 B。改为单行标题（`[MindFS live-compact] 已折叠（这不是当前指令） tool=…
input=… size=…B≈…tok archive=…`，失败时附 `failed=is_error`）+ 单行页脚「原文用 Read 打开 archive。」，
摘要行前仍是两空格缩进。信息量不变，只是去掉框架。用 92 个真实请求体逐块渲染（临时用例，已删）：
重负载语料 **881 → 765 B**（n=1197，p50 738、p90 977），机制语料 **1231 → 1115 B**（n=460）。
收益有上限：稳态 24 张卡片/请求，省下的 ~2.8 KB 只占 250–350 KB body 的约 1%，且历史部分按 0.1× 缓存计价。
未动摘要（8+8 行 ≈ 350 B）——它是模型不打开存档也能判断「这块值不值得读」的唯一依据。

**② 保护尾部 32k → 20k**。`KeepTailTokens` 默认改 20000（`config.go`），`config_test.go` 同步。
两点必须写清：其一，四个节点都在 `config.json` 里**显式固定** `keep_tail_tokens: 32000`，代码默认值对它们不生效，
要让线上的营地从 32k 变 20k 得改各自的 `config.json` 并重启；其二，此前 `target_tokens=20000` 是**惰性**的
（保护尾 > target，`engine.go` 的排水循环提前退出永不触发），尾部降到 20k 后 target 才开始真正约束一次事件的折深。
收益已实测且不大：同负载把尾部 32k/3 → 16k/2 只省 **0.9%**（在三臂方差内），因为历史按 0.1× 计价，
而每请求未缓存增量（约 4.7k tok）折不掉。

**③ 「多块一起折」取证：结构上不可能合并成一个指针**。Anthropic 的 `tool_result` 与 Responses 的
`custom_tool_call_output` 都是**逐条** 与调用 id 配对，一条折叠只能替换一个 JSON 字面量所在的字节区间：
N 块 ⇒ N 个区间、N 张卡片。把它们并成一张就必然改变结果条数，`verifyConversation` 会拒绝，上游也会因
`tool_use` 缺 `tool_result` 报 400。已经存在的「多块」只有两种形态，且都已实现：一次事件折多块
（真实语料 `blocks=12 new_blocks=12`，稳态 24 块）、以及一个结果多 part 时只折最大的那块（Codex 的
`Script completed` 头）。碎块不可再合并的另一半原因是收益形状：按块判定的下限是「指针实际省 ≥ 1024 字节」，
一个只省 900 B 的块无论如何聚合都还是各自省 900 B。这条下限本身也几乎不吃东西 —— 92 个真实请求体逐块渲染
（同一临时用例）：73 个候选块里只有 **2 块**落在下限之下，合计 **772 字节**；机制语料 41 个候选里 4 块、
2230 字节。也就是说「把碎块也折掉」最多再省一个请求的 0.09%，真正的上限由可折字节总量决定，不由粒度决定。

**门禁**：`go test ./server/internal/agent/fold/...`、`proxy` 全绿；`fold_golden.txt` 因指针字节变化重新生成，
五条用例的 `tokens/new/sticky/decision` 全部不变（只有 sha256 变），说明折叠决策与账本口径零变化。

---

## 执行顺序与依赖

```
Task 1 → 2 → 3 → 4 → 5 → 6 → 7 → 8   （P1/P2：内核与 Anthropic，行为零变化）
                                  9 → 10 → 11   （P3：Responses 适配器）
                                              12 → 13 → 14   （P4：注入与信号）
                                                          15   （P5：配置与验收）
```

Task 2/3/4/5 之间无代码依赖，可按任意顺序执行；但都在 Task 6 之前完成，否则 Task 6 的 `Engine` 无法落地。

## 明确不做

- ACP agent 适配（需逐个探针验证协议与 `*_BASE_URL` 支持）。
- `/chat/completions` 适配器。
- 代理 ChatGPT 登录模式（`default` profile）：无 `[model_providers]` 条目，保持直连。
- 接管 Codex 自有 `remote_compaction_v2`：只消费其 `EventTypeCompact` 事件。

## 成本 A/B 与官方登录复验（2026-11-20 深夜，隔离实例 7460）

同一负载（`prompt-ab2.txt`：6 条 `sed -n` 各读 300 行、每条单独一次调用，最后答 charlie 第 0001 行），
三臂各自全新会话：

| 臂 | 请求 | input | cached | cache write | output | 墙钟 |
|---|---|---|---|---|---|---|
| control（自定义 provider 走代理，rail=1,000,000 即不折） | 7 | 282,082 | 228,299 | 53,762 | 974 | 38.7s |
| fold（rail=20,000 / keep_tail=4,000 / keep_rounds=1，强制短会话也折） | 8 | 179,885 | 109,610 | 70,251 | 1,203 | 44.5s |
| official（ChatGPT 登录，代理无法插入） | 7 | 286,061 | 232,960 | 0（记为 uncached 53k） | 373 | 34.5s |

- **结构生效**：fold 臂第 3→4 请求之间首次折叠（块 6 个、末轮 `saved_bytes=120,792`），此后每轮 input 增量从
  ~8.7k 降到 ~0.65k，cached 读钉在 13.6k；总 input **−36%**。
- **短负载不省钱**：省下的全是 0.1× 的缓存读（−118.7k），花掉的是全价的缓存写（+16.5k）——按 OpenAI 口径
  （读 0.1×、写 1×）折算 81.2k vs 76.6k ≈ **+6%**，按 Anthropic 口径（写 1.25×）+9.7%。原因是折叠发生在第 4 个
  请求，只剩 4 轮摊薄。每轮成本此时已打平（折后 ~14–15k/轮且平坦；不折第 7 轮 14.8k 并随上下文线性涨），
  交叉点就在这个长度附近，与 Claude 侧长回放 0.88–0.93× 同向。生产默认 rail=100k 时 70k 的会话根本不会触发，
  这条测量是「强制提前折」的下限情形。
- **官方登录拿不到折叠**：`-c model_providers.openai.base_url=…` 被 codex 拒绝（`model_providers contains
  reserved built-in provider IDs: openai`），所以「不代理 ChatGPT 登录」是能力边界而非取舍。该臂 0 条
  `live-compact` 日志、胶囊 `requests 0`、用量与 control 同形。本机 `agents-config-sync/active-profile` 当前是
  `default`，即生产上 Codex 侧只有在切到 llmux profile 时才会走代理折叠。
- **副作用已恢复**：隔离实例第一次启动时把 llmux profile 渲染进真实 `~/.codex/config.toml`（写入了
  `model_provider` 与 `[model_providers]`），已还原为测试前 513 字节内容；之后改用独立 `CODEX_HOME` +
  symlink `auth.json` 复验，真实文件 mtime 停在还原时刻。生产 7331 全程 200，隔离实例已停、端口已关。

### 官方登录取道反代 spike（2026-11-20 23:30–23:45，隔离实例 7460，真实 ChatGPT 登录，产品代码零改动）

做法：在隔离 `CODEX_HOME` 的本地覆盖段（`# >>> sync-codex local` 之间）声明
`[model_providers.chatgpt]`（`base_url` 指向本地 shim、`wire_api="responses"`、`requires_openai_auth=true`）
并设 `model_provider='chatgpt'`。MindFS 的 provider 发现照旧把 `base_url` 换成自己的 `/c/chatgpt/v1`，
`Authorization` / `chatgpt-account-id` 由 `req.Header.Clone()` 逐字透传。

- **探针**：自定义 provider + `requires_openai_auth=true` 时，codex 会把 ChatGPT Bearer（JWT，非 API key）
  与 `chatgpt-account-id` 打到自定义 `base_url`，body 带 `store:false`、无 `previous_response_id`——即
  Responses 适配器的可折条件成立。裸反代（`/v1/responses` → `chatgpt.com/backend-api/codex/responses`）
  跑通一次真实对话（14,688 tok，回答正确）。
- **12 次单命令 `sed`（3400 行）A/B**：控制臂（rail=1,000,000 不折）input 857,842 / 缓存读 739,840 /
  末轮上下文 115,920 / 99.7s；折叠臂（rail=20,000）input **357,557（−58%）** / 缓存读 190,464 /
  末轮上下文 **33,211** / 11 块、`saved_bytes=241,000`、`archive_errors=0` / 104.9s。两次都是 12 次工具
  调用、答案逐字正确、零错误。订阅制下省的是窗口与自动压缩次数，不是账单。
- **产品化缺口两个**：① `codexsync` 拒绝共享 profile 里的 `[model_providers]`
  （`unsupported shared default key "model_providers"`），provider 只能来自本地覆盖段或 MindFS 自己注入；
  ② provider 路由固定插 `/v1` 段，而 ChatGPT 后端是 `/backend-api/codex/responses`（无 `/v1`），spike 用
  一次性 shim 改写路径，产品化需要路径形状例外（或把上游拆成 base + 固定后缀）。
- **风险**：`requires_openai_auth` / `-c model_providers.*` 是 codex 内部契约（0.169.1 实测），升级要回归；
  认证头与工作区头必须逐字透传，401 要原样回给 codex 让它自己刷新；代理不可达必须 fail-open 直连。
- 真实 `~/.codex/config.toml` 与 `auth.json` 全程未被修改（隔离用独立 `CODEX_HOME` + `auth.json` 软链）；
  隔离实例与 shim 已停，生产 7331 全程 200。

### 官方登录反代 3 轮验收（2026-11-20 23:47–00:05，生产形态配置）

配置：节点 `live_compact` 用生产值（`mode=on` / rail 100k / target 20k / keep_rounds 3 / keep_tail 20k），
隔离实例 + 真实 ChatGPT 登录 + 本地 shim 修路径。负载：3 轮 × 每轮 8 条单命令 `sed`（共 24 次读、
约 7200 行），第 3 轮末尾追问 `delta.txt` 第 0400 行——该块在第 1 轮就被折叠。

| 臂 | 工具调用 | 折叠 | 累计 input | 末轮上下文 | 答案 |
|---|---|---|---|---|---|
| 不折（rail=1M） | 24 | 0 | 1,646,131 | **216,397 / 258,400（84%）** | 正确 |
| 折（生产默认） | 28 | 18 次事件 / 20 块 / saved 438,200 B / archive_errors 0 | **1,279,137（−22%）** | **109,718（−49%）** | 先答「无法确认」，被明确要求后才正确 |

- **上游全程零错误**，折叠后的请求（含 20 个指针块、每轮 sticky 折回）被官方后端正常接受，SSE 正常。
- **指针可见且可读**：模型在追问里直接引用了卡片上的归档文件名（`context/codex/23cd92….txt`），
  按提示 `sed` 读到第 0400 行并答对；沙箱不拦。所以首答失败是**模型行为**——它宁愿说「无法从当前可见
  内容确认」，也不主动打开卡片里的 archive。产品侧应把提示语写得更硬（例如「不确定时必须先打开
  archive 再回答」），或把「读归档」做成一次工具调用。
- 折叠臂多出的 4 次工具调用是 3 次重复 `sed bravo 901-1200` 加 1 次读归档，属模型绕路，不影响结论。
- 生产形态下 24 次读不折也只到 84% 窗口，说明 258k 窗口的 Codex 自动压缩来得更晚；折叠的价值体现在
  窗口余量（216k → 110k）与后续轮次的增长斜率，而不是「避免立刻压缩」。

### B11 官方登录反代产品化（2026-11-21，`Scope: G-MN`）

spike 证明机制可行，这一轮把它做成产品行为，四处改动，全部先 RED 后 GREEN，并逐项做变异验证。

**1. ChatGPT 登录自动注入 provider**（`server/internal/agent/pool_codex_proxy.go`、`server/internal/agent/pool_open.go`）。
`codex login` 的账号走内置 `openai` provider，config.toml 里**没有**任何 `[model_providers]` 条目可重定向，
原先 `codexProviderBaseURLs()` 返回空 → 不加任何 `-c` → 官方登录完全没有折叠。现在：
`readCodexProviderConfig()` 同时读出 `model_provider` 与各 provider 的 `base_url`，交给纯函数
`planCodexRouting(cfg, chatGPTLogin, proxyURL)` 决策：

- 用户配置了 provider → 照旧逐个重定向 `base_url`（行为不变）；
- 选中项是内置 `openai`（未写或显式写 `openai`）**且** `codexChatGPTLogin()` 为真 → 合成
  `mindfs-chatgpt` 并改选：`-c model_provider=…` / `.name` / `.base_url` / `.wire_api=responses` /
  `.requires_openai_auth=true`；
- 选中项是别人 → 只重定向，绝不改选。

`codexChatGPTLogin()` 只看 `auth.json` 的形状（`tokens.access_token` 非空且 `OPENAI_API_KEY` 为空），
凭据值不出函数、不落日志。API key 登录因此完全不受影响（它需要 `/v1` 形状与 bearer key）。

**2. 路径形状跟随上游**（`server/internal/agent/proxy/provider_route.go`）。原先代理地址恒为
`/c/{name}/v1`，而 ChatGPT 后端是 `/backend-api/codex`、没有版本段 —— spike 里靠一次性 shim 改写路径绕过，
产品化必须自己解决。`RegisterProvider` 现在记录“配置的 base_url 是否以 `/v1` 结尾”，
`ProviderProxyURL` 据此给出带或不带 `/v1` 的地址；转发仍是 `base + "/" + tail`，路由代码零改动。
`/v1` 结尾的上游行为逐字不变（既有 `TestProviderProxyURLPointsAtThisServer` 仍绿）。

**3. 失败开放**。注入是唯一可能让用户彻底失去 Codex 的一步，所以真正下发覆盖前做一次 250ms TCP 探活
（`proxyListening`）；探不通就整体放弃覆盖、打一行日志，Codex 直连上游。此前“代理没起来”只意味着没有折叠，
注入之后它会意味着会话全挂，这道闸门是那次风险变化的对价。

**4. 指针页脚写硬**（`server/internal/agent/fold/pointer.go`）。3 轮验收里模型读完卡片先答
「无法从当前可见内容确认」并重跑了原命令，被明确要求后才去读归档。页脚从「原文用 Read 打开 archive。」
改为「需要原文时用 Read 打开上面的 archive 路径；不要重复原命令，也不要凭猜测作答。」。
只降低概率、不保证；代价是每张卡多几十字节，与 B10 省下的框架字节对冲。

**验证**：新增 11 条用例（配置解析 2 / 登录判定 6 个子例 / 路由决策 3 / 探活 1 / 路径形状 2 / 页脚 1），
五处变异各自打红后还原 —— `v1Suffix` 恒真、允许覆盖用户显式选中的 provider、无视 `OPENAI_API_KEY`、
探活恒真、页脚退回旧文案。`fold_golden.txt` 因页脚字节变化重生成，五条用例的 folded/new/sticky/tokens/decision
逐列不变，只有 body sha 变。`go test ./server/internal/agent/...` 全绿（`TestCodexHarnessOverrideSelectsItsOwnRail`
首轮出现一次 `TempDir RemoveAll ... directory not empty` 的清理竞态，随后两棵树各连跑 3 轮全绿，属既有 flake）。
`go build ./server/...`、`go vet ./server/internal/agent/...` 干净。

**未验证**：真实 ChatGPT 登录 + 新代码的端到端（spike 用的是手写 config.toml 等价形态，注入路径本身没有在
真机跑过）；`requires_openai_auth` 在 0.169.1 之外的版本行为。上线顺序建议：本机先跑一条官方登录会话，
日志出现 `codex official-login provider injected` 且胶囊有数字，再分发其他节点。

**B11 修一：真实会话第一跳就 404（2026-11-21）**。按上面写完先跑了隔离实例的真实官方登录会话
（`CODEX_HOME` 只放 `auth.json` 软链与两行 model 配置，无任何 provider 表），日志正确打出
`codex official-login provider injected base=https://chatgpt.com/backend-api/codex`，但第一条请求回来
`unexpected status 404 Not Found ... url: http://127.0.0.1:44197/c/mindfs-chatgpt/v1/responses` ——
**注入的地址还是带 `/v1`**。根因：`planCodexRouting` 在生成 `-c` 参数时调用 `ProviderProxyURL`，
而那一刻 provider 还没 `RegisterProvider`，查不到注册形状就退回历史的 `/v1` 默认值。
修法是把「决策」与「生成参数」拆开：`planCodexRouting(cfg, login)` 只产出 `Providers` / `Redirect` /
`Injected`，`codexRoutingArgs(routing, proxyURL)` 在注册之后才生成 argv。新增回归用例
`TestCodexRoutingArgsCarryRegisteredPathShape`（真实 proxy server，先注册后取地址），
把顺序反过来即打红。教训写进类型注释：**依赖注册表的纯函数不能在注册之前被调用** ——
这一条单元测试在旧 API 下根本写不出来，只有真实请求会暴露。
