# Live Compact 折叠择时、计量口径与前缀安全 · 设计（2026-11-20）

> 状态：**待用户批准**。未写任何生产代码。
> 上游：[原生活压缩设计](2026-11-18-mindfs-native-live-compact-design.md)（§6 门禁、§12.4–§12.9 实测）、
> [B3-A 文本卡片评估](2026-11-20-live-compact-b3-text-cards-design.md)（结论 A2：不做 B3）。
> 本文件只处理 `mode=on` 首日暴露的三件事：**账算错了**、**改写可能撞模型前缀校验**、**折叠不看时机**。

---

## 1. 范围

| 组 | 改什么 | 为什么现在做 | 默认 | 改变折叠内容？ |
|---|---|---|---|---|
| **A 计量口径** | 账本与日志按 `input` / `cache_creation` / `cache_read` 三分记录；`rewrite_tokens = input + cache_creation` | 线上每次折叠记录 260 / 897 / 1354 / −1，真实重算约 125k，成本结论目前不可信 | 常开 | 否 |
| **B 前缀校验闸门** | 从请求体读 `model`，命中 preserved-thinking 家族时**不折** | 代理改写 = 客户端编辑，Anthropic 前缀校验比的是"你发的"而不是"模型读的"，会让后续 thinking 块失效 → 400 或陈旧思考被计费 | 开 | 否（只减少折叠） |
| **C 择时折叠** | 识别"冷缓存"时刻（刚压缩 / 空闲超过 TTL / 上一拍零命中），此时允许在低于 rail 时排空积压 | 折叠唯一硬代价是缓存重建；缓存已经冷了，重写就免费，而当前策略在冷启动时仍然不动 | 开（`cold_rail=60000`） | 是（更早折） |

三者互不依赖，可分别回滚。

### 非目标（本文件明确不做）

1. **不做 B3 文本卡片**——B3-A 已证明没有质量（旧轮 assistant 文本 509 块、最大 162 token，全部低于 `min_block_tokens=2000`）。维持 A2。
2. **不接 Anthropic 原生 `context_management` / `compact-2026-09-04`**——本仓库走的是"代理侧改写 + 会话本体不变"的路子，原生编辑只对直连 Anthropic 有效，且与我们"原文可 Read"的承诺冲突。作为独立方案另议。
3. **不动 `rail`/`target`/`keep_rounds`/`keep_tail_tokens` 默认值**——§12.8 回放已把它们定在 100k/20k/3/32k，本轮只增加"何时允许折"，不增加"折多少"。
4. **不修 llmux 侧 `cache_creation=0`**——这是上游计量口径，不在本仓库。

---

## 2. 证据（2026-11-20 重启后 30 分钟，`mode=on`）

| 观测 | 数值 | 说明 |
|---|---|---|
| `[live-compact]` 行 / 折叠事件 / 会话 | 620 / 42 / 6 | 折叠真实发生，`archive_errors=0`，203 KB 存档 |
| `cache_creation_input_tokens` | **恒为 0**（353 样本） | 上游不报缓存写入，A 组不能把成本完全寄托在它上面 |
| `cache_read` vs `input` 均值 | 111,577 vs 4,088 | ≈96.5% 命中，缓存是有效的 |
| `input > 20k` 的样本 | 14 条，峰值 `input=142,538` / `cache_read=0` | 折叠后第一次请求确实全价重算 |
| 账本 `rewrite_tokens` | 260 / 897 / 1,354 / **−1** | 与上面 125k 差两个数量级 → 口径缺陷 |
| `est_in` / `actual_in` 峰值 | 186,988 / 185,517（`gate_est=194,711`） | 离线回放的余量在生产里是**负的**，`epoch=2` 已出现 |

三条结论：

1. `-1` 不是"没有用量"，而是**用量被丢了**（`observeLiveCompact` 返回 nil 时仍写了空 usage 的账）。
2. 折叠后的重算代价真实存在，且**比账本记录的大 100 倍**。
3. `epoch` 递增说明 CLI 自动压缩在生产会发生；压缩之后的第一拍是**冷缓存**，也是唯一"折叠免费"的窗口。

---

## 3. A 组：计量口径

### 3.1 现状与缺陷位置

- `server/internal/agent/proxy/live_compact_ledger.go`：`rewrite := totals.InputTokens`——漏掉 `CacheCreationInputTokens`；且 `usageTotals{}`（`Seen=false`）也照写一行，产生 `-1` 且无法与"真零"区分。
- `server/internal/agent/proxy/proxy.go`：`observeLiveCompact` 返回 nil（body < `MinBodyBytes` 或队列满）时走 `writeFoldLedger(token, ledger, usageTotals{})`，这是 `-1` 的来源。
- `usageTotals` 三个计数器本来就都在（`usage.go`），只是没落账。

### 3.2 新字段

`LedgerEntry` 新增（JSON 与 Go 同名）：

| 字段 | 含义 |
|---|---|
| `usage_input` | 折叠请求的 `input_tokens` |
| `usage_cache_creation` | `cache_creation_input_tokens` |
| `usage_cache_read` | `cache_read_input_tokens` |
| `usage_source` | `"response"` / `"missing"`；替代"靠 −1 猜" |

`rewrite_tokens` 语义改为 **`input + cache_creation`**（1× 与 1.25×/2× 部分），**不再用 −1 表示缺失**：缺失时 `rewrite_tokens = -1` 保留（向后兼容读取方），但同时 `usage_source="missing"`，聚合时按 `usage_source` 排除而不是按负数排除。

> 语义变更日期写进字段注释：2026-11-20 之前 `rewrite_tokens` 只含 `input_tokens`，历史行的数值偏小，不得与新行直接相加做趋势。

### 3.3 日志

`[live-compact] fold` 行追加 `in=.. create=.. read=..`（`scanWireRequest` 已有成本，不新增解析）。
`[live-compact] on` / `shadow` 行追加 `cold=.. reason=..`（C 组用）。

### 3.4 API 与前端

`ContextSaving` 增加 `total_rewrite_tokens` 已有；新增 `rewrite_samples`（已知样本数）与 `usage_missing`（缺失数），让"0 个样本"与"真的 0"可分。
会话页"上下文"胶囊：本轮数字旁补一句"重算（含缓存写入）"，只在 `usage_source="response"` 时显示；`missing` 时显示"—"。**不改布局，只改文案与取值。**

### 3.5 测试（RED 先行，逐条变异验证）

| 测试 | 钉住什么 | 变异 |
|---|---|---|
| `TestLedgerRecordsThreeUsageCounters` | 三分量落行 | 删任一字段 → 红 |
| `TestLedgerRewriteTokensIncludesCacheCreation` | `input=100,create=5000 → 5100` | 退回只加 `InputTokens` → 红 |
| `TestLedgerMarksMissingUsageExplicitly` | `Seen=false → source=missing, rewrite=-1` | 把 missing 写成 0 → 红 |
| `TestFoldLogLineCarriesUsageCounters` | 日志格式 | 删 `create=` → 红 |
| `TestContextSavingCountsSamplesAndMissing` | 聚合口径 | 按负数排除 → 红 |

---

## 4. B 组：前缀校验闸门

### 4.1 官方语义（作为设计前提，不是猜测）

Anthropic 的 preserved-thinking 前缀校验带指纹，**比较的是请求里发出去的内容**：

> "The prefix check compares what you sent, not what Claude read. Compaction at a token threshold and context editing change only what Claude reads, so they never count."

代理改写请求体 = 客户端编辑 → 通过代理发出的历史与模型上次产出 thinking 时看到的不一致 → 后续 thinking 块失效。已列明 preserved-thinking 的模型：Fable 5.2 / Mythos 5.2 / Fable 5.1 / Mythos 5.1 / Opus 5.5 / Sonnet 5.5 / Haiku 5.5（新一代直接 400，上一代可能是 400 或"陈旧 thinking 被计费"）。

**这条与 live compact 的核心机制正面冲突**，所以必须有一道闸门，而不是靠"目前用的是 `ok` 别名"这种巧合。

### 4.2 判定

1. `scanWireRequest` 增加顶层 `model` 抓取（复用 `scanEachMember`，约 15 行；`scanRequest` 目前没有 `model` 字段）。
2. 规范化：小写、去掉 `-20\d{6}` 日期后缀、去掉 `claude-` 前缀。
3. 命中内置家族（前缀匹配，避免误伤 `opus-5`）：`fable-5-2`、`mythos-5-2`、`fable-5-1`、`mythos-5-1`、`opus-5-5`、`sonnet-5-5`、`haiku-5-5`。
4. 命中配置项 `live_compact.prefix_unsafe_models`（字符串数组，给别名用，例如 llmux 的 `ok`）同样视为不安全。

### 4.3 行为（fail-closed，但只关折叠）

| 情况 | 行为 | 日志 |
|---|---|---|
| 命中内置或配置列表 | **不折**（shadow 只报 `would_fold`） | `gate=prefix-guard model=<原值>` |
| `model` 未知（别名 / 缺失 / 解析失败） | **照常折**，每会话**一次**告警 | `[live-compact] prefix-guard: unknown model %q, folding allowed` |
| 已知安全（如 `claude-opus-5`、`ok` 且未被列入） | 照常折 | 无额外输出 |

未知放行的理由：默认拦会把现有生产（llmux `ok`）直接停掉，而 `ok` 是否映射到 preserved 模型本仓库无法判定；把它变成**运营配置**（列进 `prefix_unsafe_models`）比猜安全。告警每会话一次，避免日志洪水。

### 4.4 配置

`live_compact.prefix_guard`（bool，默认 `true`）、`live_compact.prefix_unsafe_models`（`[]string`，默认空）。
两个键都要同时进 `server/cmd/mindfs-server/main.go` 的两处重复解析与 `server/internal/agent/pool.go` 的 `PoolOptions → proxy.LiveCompactConfig` 映射。`prefix_guard=false` 是应急开关，不建议常关。

### 4.5 测试

| 测试 | 钉住什么 | 变异 |
|---|---|---|
| `TestPrefixGuardBlocksPreservedThinkingFamilies` | 表驱动 7 个家族 + 带日期后缀 | 去掉后缀剥离 → 红 |
| `TestPrefixGuardAllowsOlderOpus` | `claude-opus-5` 不被误伤 | 前缀改 `opus-5` → 红 |
| `TestPrefixGuardAllowsUnknownAliasWithWarn` | `ok` 放行 + 只告警一次 | 改成一见未知就拦 → 红 |
| `TestPrefixGuardConfigListBlocksAlias` | `prefix_unsafe_models=["ok"]` → 拦 | 忽略配置 → 红 |
| `TestPrefixGuardShadowReportsWithoutRewriting` | shadow 只报不折 | shadow 也折 → 红 |
| `TestScanRequestCapturesModel` | 顶层 `model` 解析（含转义、缺失） | 写成子串搜索 → 红（`messages` 里出现 `"model"` 的用例） |

---

## 5. C 组：择时折叠

### 5.1 判据

在 `foldContext` 增加 `cold bool` / `coldReason string`，由三个信号决定（优先级从高到低）：

| 信号 | 判定 | `cold_reason` |
|---|---|---|
| 刚压缩 | `handleCompactSignal` 置 `pendingColdFold=true`，下一次决策消费一次 | `compact` |
| 空闲超过 TTL | `now - lastUsageAt ≥ cache_ttl`（默认 300s，Anthropic 5m） | `idle` |
| 上一拍零命中 | 上一拍 `cache_read == 0` 且 `ContextTokens() ≥ cold_rail`；首拍也算 | `no-read` |

`hot` 时完全走今天的行为（rail 100k）。

### 5.2 冷时策略

```
cold && est ≥ cold_rail(default 60000)  → 允许折，rail 视为满足
cold && est <  cold_rail                → 不折（gate=below-rail，reason 保留在日志里）
hot                                     → 现状（rail 100k）
```

其余约束**一条都不放松**：`keep_rounds` / `keep_tail_tokens` 保护窗口、`min_block_tokens`、`min_fold_bytes`、`max_fold_bytes`（单块）、`foldMaxChunksPerEvent=8`、`foldCommits` 单调性、`verifyFold` 结构校验。

选 60k 的理由：低于 60k 时距 CLI 自动压缩阈值（~167k）还有 100k 余量，折叠不解决任何窗口压力，只损失连续性；高于 60k 时冷启动折一次可以让后续几十轮都跑在更低水位上。这是**唯一**一个需要用户确认的魔法数。

### 5.3 不变式

1. **候选集合不变**：冷只影响"是否允许提交"，不改变哪些块可折（同样的 body，冷时 `would_fold` ⊇ 热时 `would_fold`，且新的部分必须都是原本被 rail 挡住的）。
2. **已提交集合不变**：I8/I9 照旧——`foldCommits` 里的块无条件折回，折叠区逐字节稳定。
3. **消费一次**：`pendingColdFold` 在完成一次决策（无论折或不折）后清除，避免整个 epoch 都当冷。
4. **shadow 同步预演**：shadow 也用同一判据，只多打 `cold=` / `reason=` / `would_fold_tokens=`，不渲染、不写存档。

### 5.4 配置

`live_compact.cold_fold`（bool，默认 `true`）、`live_compact.cold_rail`（int，默认 `60000`）、`live_compact.cache_ttl_seconds`（int，默认 `300`）。同样走两处 main.go + pool.go。

### 5.5 测试

| 测试 | 钉住什么 | 变异 |
|---|---|---|
| `TestColdFoldAfterCompactSignalFoldsBelowRail` | est 70k + pending → `gate=fold reason=compact` | 删 pending 分支 → 红 |
| `TestColdFoldIgnoredBelowColdRail` | est 50k → 不折 | 忽略 `cold_rail` → 红 |
| `TestColdFoldSignalConsumedOnce` | 第二拍回 `below-rail` | 不清 pending → 红 |
| `TestIdleBeyondTTLFoldsBelowRail` | 空闲 301s → cold | TTL 比较写反 → 红 |
| `TestHotCacheDoesNotTriggerColdFold` | 上一拍 `read` 大 → hot | 把 `read==0` 写成 `read>=0` → 红 |
| `TestColdFoldKeepsCandidateEligibility` | 冷热候选集合包含关系 | 冷时放宽保护窗口 → 红 |
| `TestColdFoldRespectsFoldBudget` | `min_fold_bytes` / `max_fold_chunks` 仍生效 | 冷时跳过预算 → 红 |

---

## 6. 参数总表（新增项）

| 键 | 默认 | 组 | 说明 |
|---|---|---|---|
| `live_compact.prefix_guard` | `true` | B | 总开关 |
| `live_compact.prefix_unsafe_models` | `[]` | B | 别名黑名单 |
| `live_compact.cold_fold` | `true` | C | 择时总开关 |
| `live_compact.cold_rail` | `60000` | C | 冷时视为满足的估算水位 |
| `live_compact.cache_ttl_seconds` | `300` | C | 空闲判定 |

只走 `config.json`（与 B4 的 5 个键一致，不加 flag）。

---

## 7. 失败模式与回滚

| 失败 | 表现 | 处置 |
|---|---|---|
| 前缀闸门误伤 | 折叠数掉到 0，日志全是 `gate=prefix-guard` | `prefix_guard:false` 或把该模型从黑名单删掉（需重启） |
| 冷判定过激 | `[live-compact] on` 行大量 `cold=...`、链式重建 | `cold_fold:false` |
| 冷判定不触发 | 只有 `cold=hot` | 查 `handleCompactSignal` 日志与 `lastUsageAt` |
| 计量仍不可信 | `usage_source=missing` 占比高 | 查 `observeLiveCompact` 的 nil 分支（队列满 / body 过小） |
| 整体异常 | 折叠写坏请求 | 落 `<config-dir>/context/DISABLED`（1s 内停折，不重启） |

所有配置项都是**启动读一次**，改配置要重启；急停文件是唯一免重启开关。

---

## 8. 上线与验收

1. 本轮：只改代码 + 测试，本机 `make build && make install`，**不部署其他节点**。
2. 本机 `sudo systemctl restart mindfs`（仅用户执行）后观察 24h：
   - `usage_source=missing` 占比应 < 5%；
   - `rewrite_tokens` 与新日志的 `in=.. create=..` 一致，且出现 ≥100k 量级的样本；
   - `cold=` 分布：`compact` 应在每次 `epoch` 递增后出现一次；
   - `gate=prefix-guard` 计数：`ok` 未知 → 应为 0，且每会话恰好一条 warn。
3. 24h 无异常后 `bash scripts/deploy-all.sh` 分发 wsl/fn/pc-win，逐端核对 health。
4. 门禁：`go test -count=1 ./...`、`go build ./server/...`、`tsc`+`vitest`（改胶囊文案才需要）、干净 worktree 上 `make check-upstream`；`docs/upstream-customizations.yaml` 补 G-MN 新锚点，提交信息 `Scope: G-MN`。

---

## 9. 待用户确认

1. **`cold_rail=60000`** 是否接受（这是唯一凭经验定的数）。
2. **未知模型放行 + 每会话一次告警**，而不是"未知就拦"——后者会立刻停掉 llmux `ok` 上的折叠。
3. 胶囊文案改为"重算（含缓存写入）"、缺失样本显示"—"是否接受。
4. 是否需要把 `ok` 预置进 `prefix_unsafe_models`（取决于 llmux 后端实际模型；本仓库无法判定）。
