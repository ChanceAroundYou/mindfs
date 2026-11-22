# Live compact 收尾：压缩竞态 · 计费口径 · 校准护栏 · 诊断字段

Plan 2026-11-22。用户确认上一轮 live-compact 现场核查（「我觉得基本上没有问题」）后，
指定这一轮范围 **P0 + P1 A/B + P2**，交付到 **代码 + 测试 + 本地提交**：
不部署、不重启任何节点、不 push、不发版。

Scope: G-PR。

## 背景与证据

上一轮只读核查在三个节点上取证，结论是折叠本身在生效，但四条链路各有一个可修缺口：

1. **压缩竞态**（`session_hygiene_wire.go`）：压缩回合已经结束、harness 的压缩事件
   还没到时，自动压缩会被记成失败（`harness 没有报告压缩事件`），而实际上压缩已经
   发生。Codex 的 `compacted` 事件与流结束是两个独立回调，谁先到不确定。
2. **胶囊口径**（`ContextSavingIndicator.tsx`）：Codex 的请求体里 89–95% 是推理密文，
   不计入计费上下文。按字节算，一次真实的 39% 节省会读成 3%；按估算 token 算，
   分母里仍混着密文估算。只有 `usage_input + usage_cache_creation + usage_cache_read`
   与同一批请求的折叠内容是同一量纲。
3. **校准对**（`live_compact_fold_request.go`）：rail 用 `(lastActual, lastForwardEst)`
   把估算折算成真实 token。两半此前分别写入：估算在请求路径写、真实值在响应路径写。
   任何「只写一半」的请求（小体积旁路请求、响应没有 usage 的请求）都会让两半来自
   不同请求，比例失真且不会被任何断言发现。
4. **诊断字段**：`plan_*` 七项无法回答「请求很大但没得折」；`actual=-1` 无法区分
   「压缩响应」与「流提前结束」。两者都是只读日志字段，改动零行为。

## P0-1 压缩竞态

`session_hygiene_wire.go`：`onCompactSeen` 改为关闭一个 channel；压缩回合结束后
最多等 `hygieneCompactGrace`（3s）看事件是否到达。流报错但事件已到 → 记成功
（`reason=stream_error_after_compact`）；流正常但事件没到 → 仍记失败（这是真没压）。
测试 `TestCompactEventArrivingAfterStreamErrorIsSuccess`：把 grace 改 0 → 用例红，
恢复 → 绿。既有表驱动用例把 grace 覆写成 20ms，避免每条都真等 3s。

## P0-2 计费口径

- 账本汇总新增 `total_context_tokens` / `total_folded_tokens` / `context_token_samples`：
  只统计「有 usage 样本」且「有折叠 token 估算」的行；缺样本或缺估算的行不进分母，
  不拿两种量纲混算。
- 胶囊：有 usage 样本时，本轮节省率 = 折叠 tokens / (计费上下文 + 折叠 tokens)，
  「累计节省」走同一口径；没有样本时退回原估算口径（旧行为）。
- 测试：`TestSummarizeLedgerBillingContextTotals`（280k 上下文 / 162k 折叠 / 2 样本、
  36.6%），前端契约 70 条通过、`tsc` clean。

## P0-3 校准护栏

`sessionState` 新增 `pendingForwardEst`、`calibrationReset`：

- `noteForwardEstimate` 只写 pending；`noteActualContext` 接受样本时把
  `(actual, pendingForwardEst)` **成对**提交，两半永远描述同一个请求。
- 小体积旁路请求（本请求估算 < 已校准估算的 1/4）即使上报 usage 也不接受：
  它测的不是这段对话。护栏按「请求体本身很小」判定，而不是按实际值大小猜。
- 压缩信号（`SignalCompact` 与 PostCompact hook 两条路径）置 `calibrationReset`：
  压缩后的第一个样本本来就该小，必须放行。
- 测试四条，三处变异各自打红：去掉护栏 → 旁路样本污染；退回半对写入 →
  未采样请求的估算留在 pair 里；去掉 reset → 压缩后样本被拒。

## P1 A/B：`min_fold_bytes` 4 KiB vs 16 KiB

`lcsmoke` 新增 `-min-fold-bytes` / `-keep-tail` / `-rail` 三个 flag，并打印
`billing: context_tokens=… folded_tokens=… samples=… share=…` —— A/B 只比计费口径，
不比字节。实验用固定夹具（六个仓库文件复制到 `/tmp/lc-ab-fixture`），两臂同负载、
同一真实 CLI 会话顺序执行，各跑一次 `mode=on`。

（结果见文末「执行记录」。）

## P2 诊断字段

- `PlanStats.UnaccountedBytes`：`BodyBytes - ResultBytes`，即任何折叠策略都触不到的
  部分（system prompt、工具定义、对话正文、JSON 结构）。观测行与 provider 行统一由
  `planStatsFields` 输出，`plan_unaccounted_bytes=` 与其余 `plan_*` 并列。
- `fold.Usage.MissingReason`：`upstream_unreachable` / `content_encoding` /
  `not_observed` / `no_usage_block`。provider 行新增 `usage_missing=<reason|->`；
  账本行新增 `usage_missing_reason`（旧行读回为空 = 未知，不是诊断）。
- 变异各自打红：Unaccounted 写成 ResultBytes；provider 行恒打 `unknown`；
  账本不写 reason。

## 门禁

- `go test ./server/internal/agent/... ./server/internal/api/usecase/` 全绿；
  代理与内核包全量 `-count=1` 全绿。
- `npx vitest run web/tests/context-saving-chip.test.mjs` 70 通过；
  `cd web && npx tsc --noEmit -p tsconfig.json` clean。
- 干净 worktree 上 `make check-upstream` 覆盖 G-PR 登记。

## 边界

不改任何阈值默认值（P1 若结论是维持 4 KiB，则配置零改动），不改折叠策略，
不部署、不重启、不 push。

## 执行记录

P0-1 / P0-2 / P0-3 / P2 全部实现，变异验证见上。

**P1 结果**（固定夹具：六个仓库文件复制到 `/tmp/lc-ab-fixture`，真实 CLI、模型 `ok`、
`mode=on`、`rail=15000`、`keep_tail=4000`，三臂顺序执行、同一负载）：

| 臂 | `min_fold_bytes` | 折叠事件 | requests | rewrite_total | 计费节省率 |
|---|---|---|---|---|---|
| A | 4096 | 6 | 6 | 29536 | 40.3% |
| B | 16384 | 6 | 6 | 29536 | 40.3% |
| C | 65536 | 2 | 2 | 11448 | 43.4% |

读数：

- A 与 B 逐字节相同：这个负载里每个折叠事件本身就有 16.6 KB，已在 16 KiB 门槛之上，
  4 → 16 KiB 完全不构成差别。也就是说 `min_fold_bytes` 在现网默认值附近不是杠杆。
- C（64 KiB）把折叠事件从 6 次压到 2 次、重算成本 −61%，计费节省率反而略高；
  但样本只有 2 条、只有一个夹具、各跑一次，不足以支撑改生产默认值，且它与
  B4 的「触发晚、一刀深」结论方向一致但证据强度不同。
- 结论：**不改任何阈值默认值**。先让 P2 的 `plan_unaccounted_bytes` / `usage_missing`
  进 24h 只读审计，按真实构成再决定是否调门槛。

夹具与日志：`/tmp/lc-ab-fixture`、`/tmp/lc-ab-{a,b,c}.log`（一次性，不入库）。
