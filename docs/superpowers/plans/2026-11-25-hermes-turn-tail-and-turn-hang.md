# Hermes 会话：回合边界后的续跑落盘 · 卡住回合的空闲看门狗

> Scope IDs: G-RU / G-RV（claimed 2026-11-25T12:47Z）

> Plan 2026-11-25。Scope: G-RU（续跑落盘）/ G-RV（空闲看门狗）。

## 背景（用户报告的三条）

用户在实际工程里报三件事，本计划只覆盖其中两条代码缺陷（第三条是 `~/.hermes/config.yaml` 的
配置清理，不涉及本仓库代码）：

1. 工具调用 / 工具结果堆在正文后面，与文本顺序错乱，后半段正文消失。
2. 会话已完成，界面仍显示运行中。
3. `~/.hermes/config.yaml` 里有一批用户从未配置的 Google 模型，默认上下文与模型不匹配。

## 根因（只读取证，2026-11-25）

受影响会话 `1795570207-89e3aba017b9` 块流 + aux + Hermes 转录三方对账：

- **ACP `Prompt` 在「人类发起的那一轮」结束时就返回**，Hermes 之后的自发续跑
  （后台任务结果回注）继续通过同一个 `OnUpdate` 送进 `session_send.go`。
- `usecase.SendMessage` 在 `runtime.SendMessage` 返回处**一次性** `persistTurnBlocks`，
  之后迟到的 `auxBuffer` / `responseText` 只进 provisional 流水（`step=-1`，读取侧丢弃）。
  实测 seq 39：落盘 71 块（36 工具、2 段正文），provisional 里有 62 个工具、
  还有两段未落盘的正文；seq 35 丢 13 个工具、seq 41/37 各丢正文与工具、seq 45 丢两段正文。
- 幸存的那段正文被 `BuildTurnBlocks` 按「所有 aux 都在同一行」压成一个**块**贴在最后 ——
  用户看到的「工具堆在正文后面」就是这个布局。
- 现有的补救是 `markSessionContinuationTail` → 下一次增量同步的
  `recoverLiveContinuationTail`，它把续跑逐条导入成**新的 exchange**（实测 seq 50–73 共
  25 条），既没有工具卡，也不是同一轮 —— 是「一堆空助手气泡」的另一个坏法。
- 第二条：`sessionMessageContext()` 没有死线，`conn.Prompt` 也没有死线；ACP 回合卡住时
  pending 灯与 `replying-sessions` 会一直亮。实测 `/compact` 那一轮 19:49:07 发出、
  20:00:08 才落盘（11 分钟），期间状态一直是「运行中」。

## 修复

### S1 · 回合收尾后的增量落盘（Scope: 待 claim）

新增 `server/internal/api/usecase/session_turn_tail.go`：

- `turnStreamState`：把 `responseText` / `lastResponseUpdateType` / `auxBuffer` 收进一把
  互斥锁后面（`appendChunk` / `assistantLine` / `addAux` / `snapshot`），因为落盘之后
  回调线程仍会写、缓存刷新线程会读。
- `turnTailWriter`：首轮 `persistTurnBlocks` 之后安装。每次迟到更新调用 `note()`，
  每 1.5 s（及 1.5 s 静默后的尾刷）把游标之后的**增量**编译成块，位置从
  `len(initialBlocks)` 起顺延追加：
  - `content[cursor:]` + 平移行号后的增量 aux 走同一个 `session.BuildTurnBlocks`，
    行号平移量 = 已落盘前缀的行数（`currentAssistantLine(prefix)`，前缀不以 `\n` 结尾时
    减一），保证与一次性布局同序。
  - 已落盘的工具卡（按 callId 认领）不再新开卡，改为在**原 pos** 上 `revision+1`
    覆盖状态（running→complete），这正是 revision 的语义。
- 回调里原先「回合已结束 → `markSessionContinuationTail`」改为：有 tail writer 时实时补落
  （并先 `flushThought`），**不再**标记转录补录 —— 否则同一条续跑会既进块流又被导入成
  新 exchange，正文出现两遍。
- `persistTurnBlocks` 返回落下的块（给 writer 建 callId→pos 索引），其他调用点不变。

### S2 · 回合空闲看门狗（Scope: 待 claim）

`turnUpdateTracker` 增加 `IdleFor()`（距最后一次 OnUpdate 的时长）。`runSessionMessage`
在等待 `SendMessage` 期间起一个 30 s 轮询：连续空闲超过阈值（默认 30 分钟，Hermes 自身
工具超时是 420 s）就取消回合 ctx 并打日志 —— 走既有的 cancel 收尾路径清掉 pending，
而不是无限等一条永不返回的 `Prompt`。

## 验证

- `go test -count=1 ./server/internal/api/usecase/ ./server/internal/api/ ./server/internal/session/`
- 新增用例：增量追加落在首轮块之后且顺序正确；重复工具状态按 revision+1 覆盖原 pos；
  行号平移在「前缀以 / 不以换行结尾」两种情形下一致；看门狗在静默超阈值时取消、活跃时不取消。
- `make check-upstream`（改的是上游文件，需登记 `docs/upstream-customizations.yaml`）。
- 端到端：已落盘的历史轮次无法回补；验证只对**新回合**成立，用隔离实例起 ACP 会话复现。

## 不做

- 不重写历史块（seq 35/37/39/41/45 的丢失与 seq 50–73 的碎气泡保留，属于既有数据）。
- 不做方案 §3.3 的「按投递单元切 step」（S2c 的完整形态），本计划只保证**不丢**且**有序**。
