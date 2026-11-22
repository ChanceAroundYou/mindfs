# 会话流式记录 A+：未收尾回合的 provisional 块流与中断恢复

Plan 2026-11-22。用户先要求定位并修复「会话运行中重启丢聊天记录」，再在隔离实例验收中明确：**工具执行中途 `kill -9` 才是要解决的场景**。本计划只做 A+ 目标：**已经流式出现过的聊天内容不丢**；不做进程级回合续跑（目标 B）。

## 取证结论

1. `external_sessions_live_tail.go:17-23` 自己记着当前设计的根因：实时路径把整轮正文攒在内存里，只在回合收尾时写块流；重启后的恢复只能从 agent 转录倒推。
2. `session_send.go:158/320-330/471`：`responseText` 是内存变量，`persistTurnBlocks` 在回合结束后才写。
3. `importer_transcript.go:47-52,192-205`：恢复复用的 importer 只认「最后一条助手消息有正文且无 `tool_use`」的闭合尾；开放 `tool_use` 尾整条丢弃。
4. 真隔离实例 kill 探针：在 `tool_use(sleep 25)` 出现时 kill，转录尾没有助手正文；重启 Full 后恢复出来的「正文」其实是重复的用户 prompt。绑定已被 `9506584f` 学到，但 importer 无内容可导。
5. 因此只放开 `claudeTranscriptTailClosed` 不够：开放尾里往往还没有正文。要让已有内容不丢，必须在流式路径上增加 provisional 落盘。

## 目标 / 非目标

**目标：**

- 每个 `MessageChunk` 到达时，把该段文本以 provisional block 追加进块流；正常渲染继续忽略 `Step < 0` 的未封口版本。
- 工具调用发起时，也把调用卡以 provisional block 追加；恢复时若没有对应结果，封成「中断」态。
- 进程在工具执行中崩溃/被 kill 后，重启 + Full/delta 同步：用户行保留，kill 前已流出的助手文本变成可见助手行，工具卡明确标记中断，不重复用户行、不伪造最终回复。
- 正常回合仍只显示一份最终正文，provisional 块不泄漏到界面。

**非目标：**

- 不让续跑 agent 子进程、不接管回环代理、不等待工具结果、不生成工具结束后的最终回答。这是目标 B，另立计划。
- 不改 importer 的闭合尾约束；现有闭合尾恢复继续处理「转录里已有完整回复但 MindFS 没落库」的场景。
- 不改所有权守卫和自动增量同步语义。

## 设计

### 1. 复用现有块流的 unsealed / revision 机制

`session.Block` 已有 `Step == -1`（步序未定，读取侧忽略）与 `Revision`（同 `(seq,pos)` 取高版本）。不需要新文件：

- 流式文本：每个 chunk 追加一条 `Step=-1` 的 `text` block，`Pos` 为 chunk 序号，`MindFSID=turnID`。
- 流式工具调用：每条 tool call 追加一条 `Step=-1` 的 `tool_call` block，`MindFSID=turnID`，状态 `running`。
- 正常收尾：`persistTurnBlocks` 仍写最终 blocks（`Step >= 0`），provisional 块继续留在文件里但被读取侧忽略。
- 崩溃恢复：读取该 `turnID` 的全部 unsealed blocks，拼接文本、把未完成 tool call 标成 interrupted，追加高 revision 的 sealed block，并补一条同 turnID 的 `Partial=true` 助手 exchange 行。

### 2. 写入点

`session_send.go` 的 `OnUpdate` 回调里：

- `EventTypeMessageChunk`：在现有内存累积之外，调用 `persistProvisionalTurnChunk(...)`；失败只记日志，绝不打断流式回复。
- `EventTypeToolCall`：同样追加 provisional tool_call block。

新 helper 放在 `server/internal/api/usecase/session_stream_provisional.go`，只依赖 `session.Manager` 的小接口，便于单测。

### 3. 恢复点

在 `recoverInterruptedLiveTurn` 最前面尝试 `recoverProvisionalTurnBlocks`：

- 用 `liveTailIndex` 找到最后一个 `user` 行和是否存在已收尾的助手行；
- 若尾部已收尾或本进程正在跑，直接返回（沿用现有守卫）；
- 若该 user 行的 `MindFSID` 有一组 unsealed blocks：
  - 若已有同 turnID 的 agent exchange 行：视为已恢复，返回；
  - 否则补 agent exchange 行（`Partial=true`、`MindFSID=turnID`、`TurnSeq=userSeq`），封口 blocks，更新 agent state；
  - 记一行 `[session/recovery] provisional_recover ...`；
  - 返回，不再走 importer 尾部恢复。
- 没有 provisional 内容时，原 importer 恢复路径完全不变。

Full 通用导入的 floor 在恢复后按最新 exchange 行重算，避免把同一轮 prompt 作为新 exchange 再导一次。

### 4. 中断态

工具卡状态用 `interrupted`；前端把 `interrupted` 归到失败态展示，标题写明「服务端重启，工具未返回」。文本块保持原样，不追加「这是部分内容」以外的伪正文。

## 测试与验收

### 单元 / 契约

- `session` 包：unsealed block 写入、按 turnID 读取、sealed revision 覆盖、正常窗口读取看不到 unsealed。
- `usecase` 包：MessageChunk → provisional block；恢复幂等；已收尾回合不恢复；tool call 封成 interrupted；Full floor 重算。
- web：`ToolCallGroupCard` 的 interrupted 归为 failed。

### 真实隔离 e2e

用 `scripts/mindfs-iso.sh` 起独立端口/独立目录，生产 7331 不碰：

1. 发送「先输出一段文字，再调用 `sleep 25` 工具」的回合；
2. 客户端收到助手文本、转录出现 `tool_use` 时 kill -9 服务端；
3. 重启该实例并触发 Full；
4. 断言：用户行 1、助手行 1、文本锚点命中、工具卡 interrupted、重复 Full/delta 行数不变；
5. 清理只限本次创建的临时实例/进程/转录目录。

## 实施顺序

1. 写失败测试（session block helper + usecase recovery + web status）。
2. 实现 session 层 provisional block API。
3. 接流式写入与恢复。
4. 前端中断态。
5. 全量门禁 + 真实隔离 kill e2e。
6. 按逻辑单元本地提交；更新 G-MD 定制登记；不 push、不部署、不重启生产。

## 执行记录（2026-11-22）

### 实现与提交

分支 `fix/session-stream-journal`（从 `a2f82597` 切出的独立 worktree `/tmp/mindfs-stream-journal-src`），六个逻辑单元均已本地提交、未 push：

- `4d457724` 计划文档
- `8dc3aa16` session 层 provisional block API（先 RED 后 GREEN）
- `ea3a2460` 流式写入 + 崩溃恢复 + Full floor 重算 + 源码守卫
- `b01f56f6` 前端 interrupted 失败态
- `efd2f0ac` / `5f9498fa` G-MD 定制登记与计划路径补登
- `bac00966` 中断工具卡显示「服务端重启，工具未返回」

### 真实隔离 kill A/B（非生产）

控制组用基线提交 `a2f82597` 的独立 worktree，实验组用本分支；两个实例都通过 `scripts/mindfs-iso.sh` 起在独立端口/目录/DB（`7437` / `7438`，均 ≠ 7331），`verify` 的句柄断言确认隔离进程零句柄落在线上配置目录。生产 7331 未被 kill/restart；隔离脚本报告线上配置指纹变化是生产服务自身写入，句柄检查才是硬证据。

两条真实 claude 回合都先收到 `message_chunk` 锚点 `ANCHOR_A_PLUS_7F3C9D2B`，再收到 `status=running` 的 `tool_call`（`sleep 25`）；随即在工具仍运行时 `kill -9` 隔离服务端，并只清理由该服务端直接 spawn 的 agent 子进程：

| | 控制组（基线 `a2f82597`） | 实验组（A+） |
|---|---|---|
| 会话 key | `1795342333-347658540eb8` | `1795342177-12f01e60bf12` |
| kill 前块文件 | 1 块，仅用户 prompt | 3 块：用户 prompt + `step=-1` 锚点文本 + `step=-1` running tool call |
| 重启 + Full 后 exchange | 2 行，第二行仍是 **user**（重复 prompt） | 2 行：user + `agent(partial=true)` |
| 恢复正文 | 只有重复的用户 prompt | `ANCHOR_A_PLUS_7F3C9D2B` 原文命中 |
| 工具卡 | 无 | 唯一一张，`status=interrupted`，标题带「服务端重启，工具未返回」 |
| 连续 3 次 Full + GET | — | 行数/块数/载荷 sha256 三次完全一致（2 行 / 3 块 / `7c702723…`） |

恢复日志：`[session/recovery] provisional_recover session=1795342177-12f01e60bf12 agent=claude user_seq=1 blocks=2`。隔离证据留在 `/tmp/mindfs-a-plus-e2e/` 与 `/tmp/mindfs-a-plus-control/`，清理时只删这两个临时目录。

### 门禁

- `go build ./...` 通过；`go test -count=1 ./server/internal/session ./server/internal/api/usecase` 通过。
- `npx tsc -b --noEmit` 通过；`npx vitest run`：265 文件 / 885 通过 / 6 跳过。
- `make check-upstream`：2398 delta 文件被 363 组完整覆盖，通过。
- 全量 `go test -count=1 ./...` 有三条红，均与本组改动无关：`TestOverviewProjectionCoversEveryFieldTheSharedCardReads` 在基线 `a2f82597` 的干净 worktree 上同样失败（投影多 `archived_at` 的既有 parity 漂移）；`TestWorkspaceWorkerDerivesAccountID` 与 `TestCoordinatorTickReclaimsStaleClaims` 都是单独重跑也能复现的 `TempDir RemoveAll cleanup: directory not empty` 竞态。本组涉及的 `session` / `usecase` 包全绿。

未 push、未部署、未重启任何节点；本机生产 7331 全程未碰。
