# 输入区徽标带流内化与 Codex 双限额展示

**状态**：Implemented（2026-10-22），已提交（`Scope: G-JQ`）并快进合入 `main`；待 ship（构建安装 + 分发 WSL/fn）。

## 目标

三件事一起解：

1. 输入区的「内存」徽标不再压住「排队中」消息右侧的 ✎/🗑/⏎ 操作按钮 —— 徽标左置，
   并且**被排队消息顶起来**（排队消息变多时把徽标带推上去）。
2. Claude Code 等**所有** agent 也显示内存徽标（此前只有 Codex 有），同样左置。
3. Codex 额外显示 **5 小时限额**；每个限额的**剩余量与重置时间合并**成一条展示。

## 现状与结论

- **遮挡的根因不是「右置」而是「浮层压流内元素」**：`ComposerTopChips` 是贴在输入框上沿
  外侧 4px 的 `position:absolute` 浮层，而 `QueuedMessagesList` 是同一列里的流内元素，
  两者共用输入框上方的同一条竖直带 ⇒ 浮层必然盖住最后一条排队行；徽标带右对齐 ⇒ 压住的
  正是排队行右侧的三个 28px 按钮。
- **「被顶起来」只能靠参与列内流**：徽标带改为 `ActionBar` 列里的常规流内一行，排在
  排队列表**之前**；排队消息变多时它被推上去，重叠在结构上不再可能。挪到左边缘或给排队区
  留间隙都只是把遮挡从右边挪到左边。
- **内存徽标此前根本没挂载**：`ComposerTopChips` 的早退守卫（plan 模式 / 无绑定会话且是
  git 仓库 / agent=codex）与 `ActionBar` 里的同款外层守卫一起挡掉了它。
- **内存统计是节点本地行为**：`/api/agents/memory` 与 `/api/agents/release-idle` 报的是
  该节点自己的 agent 进程；原来 `appPath` 不带 `nodeId` ⇒ 静默打当前激活节点。
- **5 小时限额在后端就被丢掉了**：codex app-server 的 `account/rateLimits/read` 返回
  `primary`/`secondary` 两个窗口（5h=300min、weekly=10080min），后端 `selectWeeklyWindow`
  只按周限时长挑一个，5 小时窗在归一化时被丢弃。

## 落地

### 后端

| 文件 | 作用 |
|---|---|
| `server/internal/agent/codex/rate_limits.go` | `RateLimitStatus` 新增 `five_hour`（omitempty）；新增 `selectFiveHourWindow` 与共用的 `isWindowDuration`；`selectWeeklyWindow` 的旧回退收紧（secondary 是 5 小时窗时不再当周限） |

窗口挑选规则：按 `window_duration_mins` 匹配标称值 ±3 分钟；**匹配不到就不显示**，
宁可少一个窗口，也不要把别的窗口当 5 小时限额展示。

### 前端

| 文件 | 作用 |
|---|---|
| `web/src/components/action/ActionBar.tsx` | 徽标带移出输入框内部，作为列内流内一行排在 `QueuedMessagesList` 之前；删掉外层守卫 |
| `web/src/components/action/ComposerTopChips.tsx` | 浮层改流内一行（`minHeight:24px`，选择器弹层打开时隐藏内容但保留行高）；单组靠左，顺序 `[内存][Plan][worktree][5h][周限][重置]`；内存徽标传 `nodeId` |
| `web/src/components/agent/useCodexRateLimit.ts` | 新增 `limits` 视图（每个窗口的 label/remaining/resetText/resetDetail）；`remainingPercent` 泛化到窗口；`formatClockTime` + `formatRemaining` 按窗口给出两种重置文案 |
| `web/src/components/agent/CodexRateLimitIndicator.tsx` | 每个限额一个胶囊，`{名称} {剩余}% · {重置时间} 重置`；Limit Reset 按钮只在可用次数 > 0（或仍在加载）时出现 |
| `web/src/components/agent/useAgentMemory.ts` | 接受 `nodeId`，取数与释放都带节点；切节点重取 |
| `web/src/services/agent/agentMemory.ts` | `fetchAgentMemory(nodeId?)` / `releaseAgentSessions(nodeId?)` |
| `web/src/services/agent/codexRateLimits.ts` | 类型新增 `five_hour` |
| `web/src/i18n/locales/{zh-CN,en-US}-agents.ts` | 新增 `codexLimit.fiveHour` / `limitValue` / `limitValueWithReset` / `limitTitle` / `resetAtTime` / `resetInRemaining` / `resetInRemainingShort`；删除只服务旧周限胶囊的 `weeklyTitle*` |

### 测试

| 测试 | 钉住什么 |
|---|---|
| `server/internal/agent/codex/rate_limits_test.go` | 5 小时窗按时长被挑出；没有就不出现；5 小时窗不会被当周限 |
| `web/tests/composer-chips-layout.test.mjs` | 徽标带在 `ActionBar` 里排在排队列表之前且离开输入框；不再有 absolute/早退守卫；内存徽标左置并带 `nodeId`；限额徽标同属左组（`justify-content: flex-start`，无右组） |
| `web/tests/codex-rate-limit-windows.test.mjs` | 后端有 `five_hour`；前端类型与 `limits` 视图含 5 小时窗；每个限额合并剩余与重置时间；可用重置次数为 0 时不渲染重置徽标；文案两种语言成对 |

## 兼容策略

- `five_hour` 是 `omitempty` 的**增量字段**：旧前端忽略未知字段；新前端读到旧 worker
  （没有该字段）时只是不显示 5 小时胶囊，不报错。
- `/api/agents/codex/rate-limits`、`/api/agents/memory` 的路径、方法、请求参数都不变；
  只是 `appPath` 多传一个既有语义的 `nodeId`。
- 调用方唯一：`useCodexRateLimit` 只被 `CodexRateLimitIndicator` 用，`ComposerTopChips` 只被
  `ActionBar` 用，`fetchAgentMemory`/`releaseAgentSessions` 只被 `useAgentMemory` 用 ⇒
  改签名不外溢。
- **无数据迁移**：不落盘、不改 schema。
- 可见变化：输入框上方多一条 24px 的行（Plan 芯片从「贴输入框上沿」变成「独立一行」）；
  所有 agent 每次挂载输入区会多一次 `/api/agents/memory` 请求（只读、失败显示 `—`）；
  Limit Reset 按钮不再显示倒计时（重置时间已在限额胶囊里，倒计时计时器随之删除）。

## 复测与修正（2026-10-22 第二次）

用户在真机看到「只有周限、限额没靠左、重置 0 仍显示」，逐条查证：

- **5 小时限额缺失 = 后端进程未重启**，不是代码问题。直连 app-server 取原始响应，
  `primary.windowDurationMins=300`（used 20%）、`secondary.windowDurationMins=10080`（used 69%），
  两个窗口都在；旧二进制只输出 `weekly`。进程重启后 `/api/agents/codex/rate-limits`
  同时返回 `five_hour` 与 `weekly`。
- **限额没靠左**：徽标带内层用 `justify-content: space-between` 把限额甩到右端。
  改成单一左组（`flex-start`），顺序 `[内存][Plan][worktree][5h][周限][重置]`。
- **「重置 0」**：可用次数为 0 时不渲染重置徽标（仍在加载时保留占位）。
- **重置时间按窗口语义分开写**：5 小时窗写准确时刻「16:56 重置」，周限写剩余时长
  「剩 4d 23h 重置」（`nowMs` 每分钟走一次，避免首帧算一次就冻住）。

## 遗留

- 窄屏下两个限额胶囊较长，需要靠 `white-space: nowrap` + 容器压缩处理，暂未做专门适配。
- 「释放空闲会话」现在作用于当前项目所在节点（此前是当前激活节点），多节点下语义更一致。
