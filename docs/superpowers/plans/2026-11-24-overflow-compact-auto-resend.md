# 上下文溢出压缩成功后的自动重发（复用原回合，有界一次）

> Scope IDs: G-QT（claimed 2026-11-24T06:47Z）

> Plan 2026-11-24。只本地提交：不 push、不部署、不重启任何节点。

## 背景

2026-11-24 线上复现确认：同一个长会话第一次发送必失败，报
`Prompt is too long（terminal_reason=blocking_limit）`，随后（`G-QO`）补了一次有界
`/compact`，界面提示「已开始压缩，完成后请重新发送」—— 用户必须手动再发一次。

根因是两条事实叠在一起：

1. **MindFS 把 Claude 钉在 200k**。`session_models_helpers.go` 只在模型名带 `[1m]` 时
   放开 1M，否则设 `CLAUDE_CODE_DISABLE_1M_CONTEXT`。llmux 把 `os` 宣传成 1,000,000，
   CLI 实际按 `contextWindow: 200000 / maxOutputTokens: 32000` 判，可用发送上限约 168k。
2. 用户裁定**不改 1M、保持 200k 默认**，改走「压缩成功后由 MindFS 自动重发一次」。

用户指令：「我不同意改成1m 我希望默认用200k 请走B」。

## 设计

### 状态归属

待办重发挂在 `HygieneTracker` 的每会话记录上（`hygieneScope(rootID, sessionKey)`），
字段是一份 `OverflowResumeSpec`。选择理由：溢出压缩本来就由 tracker 的
`compactPending` 记账，成功/失败的唯一收口点是 `finishHygieneCompact`；把待办放在同一
处，消费条件与压缩结论天然同源。

**只在内存**：压缩期间 MindFS 重启会丢掉待办，用户看到旧错误、手动重发即可 —— 与今天
一致，不新增持久化面。

### 生命周期（五个判据）

1. `ensureOverflowCompact` 判定「这一轮确实是终端溢出、且压缩在途（刚排上或原本就在跑）」
   时登记待办，内容取自**这条失败的回合**（content / model / mode / effort /
   fastService / planMode / clientCtx / runtimeRoot / turnSeq / turnID）。
2. `SendMessage` 起手：任何**用户发起**的回合（`!hygieneCompact && !overflowResume`）
   清掉待办 —— 用户自己又说了话，重发旧内容已经没有意义。
3. `finishHygieneCompact`：
   - 失败 → 丢弃待办（不重发）。
   - 成功 → 若会话此刻空闲、且队列里没有别的消息 → 交给 Registry 的可选能力
     `ResumeSessionOverflowTurn(spec)` 排进会话队列；否则丢弃。
4. 重发回合 `OverflowResume=true`：`ensureOverflowCompact` 直接返回 false，
   **不会再排压缩、也不会再产生待办** —— 一次为限，压完仍溢出就按普通终端失败收尾。
5. 重发复用原回合身份：`SendMessageInput.ReuseUserTurn={Seq,TurnID}`，跳过
   `persistUserTurnExchange`（不新增 user 行），WS 侧 OnStart 不广播 `user_message`
   气泡、改用 `SetSessionPendingReply` 续上同一个 turnID。用户看到的是「原消息 + 继续
   回复」，不是同一条消息出现两遍。

### 为什么必须异步

`SendMessage` 整个回合持 `getSessionSendLock(key)`，压缩本身也是一次 `SendMessage`。
在失败的调用里同步等压缩再重发会直接死锁；因此重发走会话队列，由回合结束时
`startNextQueuedSessionMessage` 拉起（这也是「用户已经排在前面」时排序正确的唯一办法）。

### 为什么重发要经过 API 层

`SendMessage` 的实时 UI（正文/工具卡/子会话/任务状态跟随）全部由 WS handler 装配。
后台回合若绕过它，用户会看到一个不动的界面。因此 Registry（`*api.AppContext`）实现
可选能力，把 spec 变成一次普通的 `sessionMessageJob` 提交。

## 任务

| # | 内容 | 测试 |
|---|---|---|
| T1 | tracker：`MarkOverflowResume` / `TakeOverflowResume` / `DropOverflowResume` / `HasOverflowResume` | 登记-取出-再取为空；不同会话互不串 |
| T2 | `ensureOverflowCompact` 登记待办 + `OverflowResume` 递归闸门 | 终端溢出登记 spec（含原 content/seq/turnID）；`OverflowResume` 回合不登记、不排压缩 |
| T3 | `SendMessage` 用户回合清待办 | 用户新回合后待办为空；`hygieneCompact` / `OverflowResume` 不清 |
| T4 | `finishHygieneCompact` 消费/丢弃 | 成功 + 空闲 → runner 收到一次；失败 → 丢弃；忙（有活跃回合）→ 丢弃 |
| T5 | API 层：`sessionMessageJob.OverflowResume` + `ReuseUserTurn`、OnStart 分支、队列闸门 | 复用身份时不再落 user 行；OnStart 不广播 user_message；有待排队消息时不提交 |

## 验收

- 每个任务先写测试（RED）再实现（GREEN）。
- 每个任务至少一处变异，必须因**正确原因**变红（断言失败，不是编译失败）。
- 定向：`go test -count=1 ./server/internal/api/usecase/ ./server/internal/api/`。
- 提交信息标 `Scope: G-QT`；登记 `docs/upstream-customizations.yaml` 并跑
  `make check-upstream`。

## 边界

- 不做 1M 窗口、不改 `os` 模型别名。
- 不新增「按大小自动压缩」策略：重发只消费已经因为撞墙而排上的那一次压缩。
- 不在压缩失败时重发、不在重发再次溢出时二次压缩。
- 重启丢待办（内存态），文档写明。

## 执行记录（2026-11-24）

**提交**：`851c455b feat(session): 上下文溢出压缩成功后自动重发原消息（Scope: G-QT）`，
14 文件 +931/−2。**未 push、未部署、未重启任何节点。**

**测试顺序的实际情况（不粉饰）**：实现在先、测试在后，没有逐任务走严格的
RED→GREEN；测试有效性由下面的 11 处变异证明 —— 每处都是**断言失败**打红
（不是编译失败），还原后复绿。

**测试**：
- `server/internal/api/usecase/session_overflow_resume_test.go` 8 条：登记原回合
  完整输入、重发回合不再登记/不排压缩、用户回合作废而压缩/重发回合保留、成功消费
  一次、忙丢弃、runner 拒绝也丢弃、压缩失败丢弃、复用身份不落库。
- `server/internal/api/overflow_resume_test.go` 6 条：队列非空拒绝、缺回合身份拒绝、
  空 AppContext 拒绝、spec→job 逐项搬运且标记 `OverflowResume`、身份不完整不复用、
  `HasQueuedSessionMessage`/`overflowResumeAcceptable`、OnStart 源码契约（重发分支
  只置 pending 且位于普通广播之前）。

```
go test -count=1 ./server/internal/api/usecase/ ./server/internal/api/
ok  mindfs/server/internal/api/usecase  25.244s
ok  mindfs/server/internal/api         38.511s
go build ./...                         BUILD_OK
make check-upstream                    ✓ 2692 个 delta 文件被 398 组完整覆盖
```

**变异验证（11 处，全部断言失败打红后还原）**：

| # | 变异 | 打红的用例 |
|---|---|---|
| M1 | `ensureOverflowCompact` 递归闸门去掉 `in.OverflowResume` | `TestOverflowResumeTurnDoesNotArmAgain` |
| M2 | 删掉 `s.markOverflowResume(in)` | `TestOverflowFailureArmsResumeWithOriginalTurn` |
| M3 | 用户回合清待办的 flag 闸门改成 `if false` | `TestUserTurnDropsPendingResume` |
| M4 | 会话忙丢弃分支改成永不触发 | `TestCompactSuccessDropsResumeWhenSessionBusy` |
| M5 | 成功路径改成 `dropOverflowResume` | `TestCompactSuccessPathConsumesResume` |
| M6 | 失败路径去掉 `dropOverflowResume` | `TestCompactFailureDropsResume` |
| M7 | `reuseUserTurnIdentity` 恒不复用 | `TestReuseUserTurnIdentity` |
| M8 | job 不标记 `OverflowResume` | `TestOverflowResumeJobCarriesTurnIdentity` |
| M9 | job 在缺 TurnSeq 时也设复用身份 | `TestOverflowResumeJobOmitsIdentityWhenIncomplete` |
| M10 | `overflowResumeAcceptable` 恒接受 | `TestOverflowResumeAcceptableReflectsQueue` |
| M11 | OnStart 的重发分支改成 `if false` | `TestOverflowResumeStartHookSkipsUserBubble` |

**实现期偏离计划的三处（都必要）**：

1. **新增队列闸门**：排队中的消息对 `SendMessage` 不可见，所以「用户已经排了话」
   只能由 API 层用 `StreamHub.HasQueuedSessionMessage` 判定；否则重发会插到用户
   消息前面。
2. **不能用「最后一条 user 行是否还是溢出的那条」做陈旧判断**：`/compact` 本身也落
   user 行，这条判据会把待办误判成陈旧。改为「用户发起新回合即作废」+ 队列闸门。
3. **判据抽成纯函数 `overflowResumeAcceptable`**：直接在 `ResumeSessionOverflowTurn`
   上做变异会让门禁失效时真的提交任务、起一个真实 agent，测试现场不可控；抽成纯
   函数后变异只影响返回值。

**未验证 / 剩余风险**：
- 真实长会话上的端到端闭环未跑（需要线上流量与重启，属用户执行范围）；发布继续搁置，
  `Prompt is too long` 是否彻底消失要等上线后取真实窗口。
- `go vet ./server/internal/api/...` 有一条既有告警
  （`http_feishu_relay_test.go:174` 复制含 `sync.Once` 的值），非本组引入。
- 压缩期间 MindFS 重启会丢掉待办（设计如此，与今天的「手动重发」体验一致）。
## 追加修正（2026-11-24，deploy-all 门禁抓出）

首次跑 `bash scripts/deploy-all.sh` 时 Go 与 tsc 均绿，**Vitest 红 1 条**：
`web/tests/user-message-persist-timing.test.mjs` 要求 `persistUserTurnExchange(` 字面出现在
`SendMessage` 函数体内且先于 `runtime.SendMessage(` —— 原实现把落盘包进了
`persistTurnUserRow` 包装函数，语义没错但源码顺序契约看不穿这层间接。

处理：**不放宽守卫**。把身份复用分支内联回 `SendMessage`（`persistUserTurnExchange(` 回到
函数体），包装函数缩成纯判定 `reuseUserTurnIdentity(in) (int, string, bool)`；测试改为直接
钉这个纯函数（含「seq 为零」「turnID 空白」两条负例）。11 处变异全部重跑，M7 改为让
`reuseUserTurnIdentity` 恒返回 false，仍因断言失败打红。

```
go build ./...                                             BUILD_OK
go test -count=1 ./server/internal/api/usecase/            ok
npx vitest run tests/user-message-persist-timing.test.mjs  1 passed
python3 /tmp/gqt_mutate.py                                 ALL MUTATIONS RED FOR THE RIGHT REASON
```
