# 消息机制清点（第一步）

> 目标：把「会话消息」链路上的**全部机制**列出来，写清各自的作用、彼此的关系、
> 能否合并或替代（不能的话独特性在哪），并为每个机制建立测试。
>
> 范围：发送 / 接收 / 缓存 / 渲染 / 排序 / 生命周期 六条链路。
> 不含：看板任务、通知推送、文件浏览、多节点控制面 —— 它们不是「消息」。
>
> 状态：**进行中**。§1 已实测完成；§2–§5 待各链路清点回报后填入。

---

## 1. 测试基础设施实勘（先说这个，因为它决定后面每一步能否验证）

### 1.1 结论：仓库**没有** DOM 级测试框架，但**可以**跑真·单元测试

| 事实 | 证据 |
|---|---|
| 无 vitest / jest / jsdom / @testing-library | `web/package.json` 的 devDependencies 只有 `typescript` + `vite` + `@vitejs/plugin-react` + 几个类型包 |
| 无 playwright | `web/node_modules/playwright` 不存在；`tests/multi-node-root-routing.test.mjs` 靠 `try { await import("playwright") } catch { process.exit(0) }` 自跳过 |
| `npm test` = `node --test tests/*.test.mjs` | `web/package.json` scripts |
| 现有 72 个测试文件里，**只有 2 个**真的导入源码，其余 70 个是**读源码文本 + 正则** | `grep -l "await import\|require(" tests/*.test.mjs` |
| 但「直接 import 源码跑纯函数」**仓库已有先例** | `tests/markdown-outline.test.mjs` → `src/components/markdownOutline.ts`；`tests/task-stage-panel.test.mjs:329` → `src/app/appTask.ts` |

### 1.2 已打通：`node --test` 直接 import 带运行时依赖的 `.ts`

Node 22.18+ 原生剥离类型，但**要求说明符带扩展名**，而本仓库（Vite 打包）一律写
`from "./base"` —— 于是 `await import("../src/services/session.ts")` 会在它自己的
第一条相对导入上炸掉。

补上一个 ~15 行的解析 hook 即可解决，**不需要引入任何依赖**：

- `web/tests/ts-module-hook.mjs` —— 把无扩展名的相对说明符解析到 `.ts` / `.tsx` / `index.ts` / `.js`
- `web/tests/session-core-unit.test.mjs` —— 用它直接 import `src/services/session.ts` 的导出纯函数

实测可导入（`src/services/session.ts`，23 个导出，含消息核心全部函数）：
`isTransientExchange`、`dropTransientExchanges`、`mergeSessionExchanges`、
`composeLoadedExchanges`、`getSessionWindow`、`getSessionMinSeq` / `MaxSeq` …

**两个硬边界**（不是懒，是 Node 的能力边界）：

1. `.tsx` **导入不了** —— JSX 需要真正的转换，Node 只剥类型。因此判定逻辑住在组件里的
   模块（`SessionViewer.tsx`）现在**测不了**。
2. 没有写成 `import type` 的纯类型导入会在运行时找不到导出而抛 SyntaxError
   （`appSession.ts` 就是这样炸的）。

### 1.3 这张表本身是第一步最重要的发现之一

| 机制所在位置 | 现在能不能测 | 为什么 |
|---|---|---|
| `services/session.ts` 的纯函数 | ✅ 行为单元测试（已跑通） | 是 `.ts`、有导出、无 `.tsx` 依赖 |
| `hooks/useSessionStream.ts` 的排序（`persistedSeq` / `buildBaseTimeline`） | ❌ | **没有导出**（只有 hook 与 `TimelineItem` 类型导出），且其依赖图里有 `i18n/index.tsx` |
| `components/SessionViewer.tsx` 的 `tailOverlay` 等判定 | ❌ | 判定写在组件内的 `useMemo` 里，且 `.tsx` 无法导入 |
| 服务端 `StreamHub` 的重放/队列 | ✅ Go 单元测试（`ws_test.go` 已有先例） | Go 侧本来就是行为测试 |
| 端到端「切走切回画面不变」 | ⚠️ 可做，但要自己搭 | 无 playwright，但有 `~/.local/bin/chromium` + CDP（本会话已用它完成过真机复现） |

**要读出来的话**：机器里最难测的两层（渲染与排序）之所以难测，**不是因为没有测试框架，
而是因为判定逻辑住在 React 组件和未导出的私有函数里**。也就是说：

> 「把判定抽成纯函数」这一步，既是我要做的**统一**（两泳道），也是**让测试成为可能**的前提。
> 两者是同一件事，不是两件事。

### 1.4 已建立的测试层映射

| 层 | 载体 | 现状 |
|---|---|---|
| 单元（行为） | `node --test` + `tests/ts-module-hook.mjs` + `await import("../src/x.ts")` | **已跑通**，`session-core-unit.test.mjs` 13 例 |
| 单元（源码契约） | 现有 70 个读源码文本的 `*.test.mjs` | 既有，保留（钉写法） |
| 功能（浏览器行为） | CDP + `~/.local/bin/chromium`，按 `multi-node-root-routing.test.mjs` 的形状自跳过 | 待建 |
| 实例（隔离实例） | 独立 config-dir + 数据目录 + 端口，数据从线上**只读复制** | 待建 |

### 1.5 已埋下的第一条「一定会红」的测试

`tests/session-core-unit.test.mjs` 最后一条 `【红】seq=0 的用户行在两条退役路径上必须得到
同一个结论` —— **当前确认失败**：

```
同一条 seq=0 用户行：dropTransientExchanges 保留它，而 composeLoadedExchanges(inFlight=false) 丢弃它
    true !== false
```

它钉的不是写法，而是一条**不变量**：同一条行该不该留，系统里只能有一个答案。
现状是两处各判一次、判法相反：

| 机制 | 位置 | 对 seq=0 **用户行**的判决 |
|---|---|---|
| `dropTransientExchanges`（done / compact / reset 三处调用） | `services/session.ts:2119` 的 `isTransientExchange` | **保留** |
| `composeLoadedExchanges`（`inFlight=false`，即每次加载已落盘会话） | `services/session.ts:2029` 的 `mergeSessionExchanges` 只收 `seq>0` | **丢弃** |

后果：**「刚发出去的消息会不会消失」取决于先跑哪条路径** —— 先加载后 done 就留下，
先 done 后加载就消失。这是「同一份数据两个机制各判一次」的最小可证伪形态，
也是后面第三步要修的东西。

> ⚠️ **因此 `npm test` 现在是红的**（159 例 / 158 通过 / 1 失败，唯一失败的就是这一条，
> 按设计如此）。它会在第三步转绿。在那之前 `deploy-all.sh` 的门禁会拦下部署，这是刻意的。

---

## 1.6 隔离实例：已建成并实测（E2E 的地基）

`scripts/mindfs-iso.sh {start|verify|stop|status}` —— 起一个与线上完全隔离的实例，
**绝不碰线上**（本机 system 单元的重启只能由用户执行，所以 E2E 必须自带服务端）。

**隔离靠三个开关**（都是实测，不是推测）：

| 开关 | 作用 | 证据 |
|---|---|---|
| `XDG_CONFIG_HOME=/tmp/mindfs-iso/config` | 搬走账户表、项目注册表、偏好、看板模板、`e2ee.json`… | `MindFSConfigDir()` = `os.UserConfigDir()`/mindfs（`config/paths.go:10`）；实测 `UserConfigDir` 跟着走 |
| **不隔离 `HOME`** | agent 发现要读真实 `~/.local/bin`、`~/.claude` | `agent/discovery.go:61,168` 用 `UserHomeDir()` ⇒ 搬走 HOME 会让真实回合跑不起来 |
| `MINDFS_STATIC_DIR=<工作区>/web/dist` | E2E 验的是**工作区那版前端** | `server/app/server.go:35` |

`mkdir` 无关紧要，关键是**自证**：`verify` 跑 8 条断言，其中两条是硬的 ——

- `线上配置目录逐字节未变`（文件集+大小+mtime 指纹）
- **`隔离进程零句柄落在线上配置目录`**（查 `/proc/<pid>/fd`）—— 指纹会被「线上服务自己一直在写自己的
  配置目录」干扰，句柄不会：有句柄才是真的会写

**实测能力（这是关键，证明 E2E 可做）**：隔离实例里跑通了**真实 agent 回合** ——
WS 建会话 + 发消息 → `session.accepted` → `session.user_message` → `session.stream` → `session.done`，
agent 回了「收到」，盘上落成 `<隔离项目>/.mindfs/sessions/<key>.jsonl`：
`seq=1 role=user len=9`、`seq=2 role=agent len=2`。

**代价（必须记住，否则 E2E 会烧钱）**：一轮实测 `input_tokens=22421 / output_tokens=2`、
**$0.112/轮**。所以 E2E 用例必须**复用同一会话与同一轮**，绝不能每个断言都新起一轮。

### 踩过的四个坑（都会浪费下一次的时间）

1. **`/health` 要带部署前缀**：裸 `/health` 404、`/mindfs/health` 200。前缀来自构建期
   `-X mindfs/internal/deploy.Prefix`（默认 `/mindfs`）。
2. **项目列表按账户分（CLAUDE.md 事实 12）**：注册项目必须带 `user=<登录账户 id>`。
   不带就落进**主账户**，而「后建的账户」看不到它 —— 页面显示 `No projects yet` 且
   **连输入框都不渲染**，而 `/api/dirs` 明明 200。查证时同理必须带 `user=`。
3. **WS 握手必须带 `client_id`**，否则返回 `e2ee_proof_required` —— **这条报错是误导的**：
   隔离实例的 `e2ee.json` 是 `enabled:false`，真正的缺失参数是 `client_id`。
   （`ws.go:281` 的判据是 `clientID == "" || ts == "" || proof == ""` 三者合并报一个错。）
4. **输入框只在选中会话时渲染**：没有会话的项目页里 `[data-onboarding=message-input]` 数量为 0，
   而 `view=chat` 会被应用改写成 `view=workspace`。所以「建第一个会话」只能走**协议层**
   （WS `session.message` + `key=""`），DOM 层只能验**已存在**的会话。

另一处接口形状（省一次调试）：`GET /api/sessions/<key>?root=<root>&user=<uid>` 的回包是
**扁平**的 —— `exchanges` 在**顶层**，不在 `session` 字段下。

### E2E 原语（已可行的四条）

| 原语 | 实现 |
|---|---|
| 建会话 + 跑一轮 | WS `session.message`（`?client_id=&user=`），payload 见 `session.ts:951` |
| 读已落盘行 | `GET /api/sessions/<key>?root=&user=` → **顶层** `.exchanges` |
| **查库判据**（导入层 vs 渲染层） | 直接读 `<项目>/.mindfs/sessions/<key>.jsonl`，数某段正文出现几次 |
| DOM 断言 | CDP + `~/.local/bin/chromium`，时间线列 = `[data-mindfs-session-content-width]` 的首个子元素 |

---

> **移植注（2026-10-09）**：以下 §1.7 与文末 §7 来自 G-T（Canonical Turn/Event）线，
> 其代码**未**进 main。§1.7.2 说的「下一个必须做的动作」（让 `SetPendingUserAt` 把 turn 身份
> 回传给收尾点）在 main 上**已由 G-BE 以 `TurnGen uint64` 表示完成**，不是 `CanonicalTurnID`。
> 阅读时把 `canonical_turn.go` / `CanonicalTurnID` / 前端 `activeTurnId` 视为 **G-T 分支内部**的物；
> §1.7.1/§1.7.3 的**边界分析仍然成立**（`EventCursor` 是回合内游标而非 `event_id`；
> 「同一轮真的收两次 done」在当前生产路径上未确认）。完整对照见 `docs/message-mechanisms-plan.md` 顶部的移植说明。

### 1.7 第4阶段迁移记录：服务端 Canonical Turn/Event shadow projection

服务端在 `StreamHub` 的既有 pending/event 入口建立迁移期内部 adapter：`SetPendingUserAt` 开启新 turn，`AppendReplyEvent` 投影事件，`BroadcastSessionDone` 投影 terminal。adapter 只做内存 shadow reducer，不改变 `StreamEvent` JSON、`EventCursor`、Exchange/ExchangeAux、ReplyingList 或 pending 行为；Canonical 身份不是 wire/durable ID。重复、乱序和旧 turn 事件被 reducer 忽略，terminal 每 turn 只接受一次。

#### 1.7.1 本阶段查实的阻塞点：`session.done` 不携带 turn 身份

原本打算让 reducer 反向驱动 pending 状态（用 terminal 决定 `ClearSessionPending` 是否清）。**这条路走不通，且加了会引入比现状更糟的 bug**，原因不是 reducer 写错，而是输入端缺信息：

```text
BroadcastSessionDone(rootID, sessionKey, requestID)
                                  ↑ 没有「这条 done 属于哪一轮」
```

`terminal(sessionKey)` 只能认「当前那一轮」。于是**迟到的旧回合 done 会把新一轮标成 terminal**，`ClearSessionPending` 随即清掉新一轮的 pending —— 而那一轮实际还在跑。这就是「正在运行但灯不亮」的一个上游形态，且比现有的 replay 清理超时更难复现（它取决于跨回合的时序，不取决于单个回合内部）。

更糟的一点：这条守卫写上去后，若某个会话的 pending 不是经 `SetPendingUserAt` 建的（例如 importer、recovery 路径），adapter 里没有该会话的 turn，`IsTerminal` 恒为 false ⇒ **pending 永远清不掉**，把「灯不亮」从偶发变成必然。所以该改动已回滚，只保留红测试：

```text
server/internal/api/canonical_turn_shadow_test.go → TestTerminalIsScopedToItsOwnTurn
```

**结论（决定后续阶段的顺序）**：turn 身份不是「以后再补的元数据」，它是 terminal 语义可判定的前置条件。任何要按回合区分的状态（清 pending、结束计时器、切 running 灯、丢弃 transient）都必须等它到位。在此之前，服务端状态只能继续由现有的 pending/queue 路径承担，reducer 保持纯投影。

#### 1.7.2 turn 身份已加进 reducer，但**收尾帧的携带者仍未就位**

按上面的结论做了两处改动：

1. `terminal(sessionKey, turnID)` 显式接受 turn 身份，为空时回退当前 turn；
2. `TurnReducer.Apply` 放行**属于旧回合的 terminal**（此前一律 `return false` 丢弃），
   非 terminal 的旧回合事件仍然拒绝。`TestTerminalIsScopedToItsOwnTurn` 由此转绿。

**但这不是修复，是能力**。要读清楚两件事：

- reducer 现在**能**把 terminal 归到正确的回合，前提是调用方**告诉它**是哪一轮；
- 生产路径 `StreamHub.BroadcastSessionDone` 目前传的是**空 turn 身份**（走「回退当前那一轮」
  的分支），与改动前的行为完全一致。测试之所以绿，是因为测试**自己给出了**正确的
  turn ID，生产路径并没有这个信息源。

> 接线时**刻意**选了空身份而不是新增一个 `ActiveTurnID(sessionKey)` 读取器：后者会在
> `begin` 并发发生时分两次读 `turns[sessionKey]`（一次在读取器里、一次在 `terminal` 里），
> 得到 TOCTOU；而空身份在 `terminal` 的同一把锁内回退，语义相同、少一个方法、少一处竞态。

追了一遍现有调用方，**今天这条回退在主路径上是正确的**，但正确性来自别处而非身份本身：

| 路径 | turn 来源 | 空身份回退到「当前那一轮」是否可靠 |
|---|---|---|
| 主会话（`ws.go`） | `reserveOrQueueSessionMessage` 在 `state.Active` 时**入队**，`startNextQueuedSessionMessage` 在 `BroadcastSessionDone` **之后**才跑 | 可靠 —— N+1 不可能在 N 的 done 之前 begin |
| 子会话（`ws.go` / `appcontext.go`） | pending 由 `SetPendingReply` 建，**不走** `SetPendingUserAt` | adapter 里无该 key 的 turn ⇒ 拿到空 ⇒ terminal 成 no-op |
| 定时任务（`scheduled/tasks.go`） | 与主会话同一条 `BroadcastSessionUserMessageAt` 路径 | 同主会话 |

所以今天没有可复现的「迟到 done」—— **是队列串行化挡住的，不是身份挡住的**。这一点必须记下来：
一旦将来有任何路径允许两个回合在同一 session key 上重叠，`ActiveTurnID` 会立刻变成那个
「迟到 done 把新一轮标成 terminal」的 bug，而现在的代码没有任何东西会拦住它。

**下一个必须做的动作**：让 `SetPendingUserAt` 把新建的 turn ID 回传给调用方（现在只回 `*PendingUserMessage`），
调用方在 `BroadcastSessionDone` 时原样带回。在此之前 reducer 不得驱动 pending 状态。
（**main 注**：此动作在 main 上已由 G-BE 以 `TurnGen` 完成；但定时任务路径仍传 `0`，
是遗留缺口，见 `.omc/plans/gt-wip-reconcile.md` §1.7 与阶段 3。）

#### 1.7.3 其余已登记的边界（同一轮的排查结论）

- **重复 done 不等价于重复 terminal 帧**：`terminal()` 每次调用都分配**新的** `EventID`
  （`terminal-N` 用全局 seq），所以 `seen` 去重**不会**拦住第二次 done；真正拦住它的是
  `Apply` 里的 `state.Terminal` 短路。这条性质挂在那个短路分支上，而它正是「放行旧回合
  terminal」时最容易顺手改掉的分支。
  **但「同一轮真的收两次 done」在当前生产路径上未确认** —— 已排除的疑似来源：kanban 的
  `appcontext.go:389`（`OnSubSessionUpdate` 形参，子会话 key）与 `:399`（外层
  `exec.Run.SessionKey`，主会话 key）是两个不同会话；定时任务 `tasks.go:521`/`:537` 同理；
  `startNextQueuedSessionMessage`（`ws.go:1031`）起的是**新一轮**（`go h.runSessionMessage`），
  其 done 属于新 turn。所以 `TestDuplicateDoneCommitsTerminalOnce` 是**防御性护栏**，
  不是已复现的线上 bug —— 别把它当成后者引用。
- **子会话的 pending 没有 turn**：`SetPendingReply` 不调 `begin`，因此子会话的 done 在
  shadow 里是 no-op。要覆盖子会话，`begin` 需要下沉到「任何把 `state.Active` 置 true 的地方」。
- **`EventCursor` 不能冒充 `event_id`**：它形如 `baseSeq:eventSeq`，两个分量都**每回合归零**
  （`stream_hub.go:445/449`），所以它标识的是「**某一回合内的位置**」，不是全局事件身份。
  **常见路径下相邻回合并不碰撞** —— `baseExchangeSeq` 在 `persistUserTurnExchange` **之前**
  算出（`usecase/session.go:2472` vs `:2475`），本回合 user 行 seq 必大于它，故 N+1 的 base
  严格大于 N 的 base。真正会退化成碰撞的是两条路径：① `baseExchangeSeq` 不传时恒为 0
  （`formatEventCursor` 得 `0:1`/`0:2`…）—— 但 `SetPendingUser`/`BroadcastSessionUserMessage`
  这两个不传的包装**无生产调用方**，三个真实调用点都传了 `start.BaseExchangeSeq`；
  ② `persistUserTurnExchange` 命中判重短路（`:2339-2341` 返回旧 seq 且不新增行）时，
  base 不前进而 `eventSeq` 照常归零。
  **结论成立（它是回合内游标，不是 event_id），但不要用「每次重置所以必然碰撞」当理由** ——
  那个推导链是错的，会误导后来人加固或放松一个实际不存在的约束。canonical adapter 用独立的
  全局 `seq` 就是为了不踩这两个退化分支。

---

## 2. 机制清点

**读法**：每行一个机制。**验证标记**在最左列 ——
✅ = 我本人核对过代码；⚠️ = 来自分链路清点、我未逐行复核（结论可用，行号可能有 ±1 漂移）；
❌ = 分链路清点报错、已由我纠正。

统计（去重后）：**发送 34 · 接收 24 · 缓存 28 · 渲染与排序 26 = 112 个机制**。
其中 **41 个**在判「瞬时 vs 持久」、**9 个**在判「是否在回复」、**6 个**在判「该渲染哪一份」。

### 2.1 发送链路（浏览器 → 服务端）

| | 机制 | 位置 | 作用 | 存亡判据 |
|---|---|---|---|---|
| ⚠️ | `ActionBar.handleSend` | `components/ActionBar.tsx:405`（onClick :1003） | 发送入口：校验 + 拼正文 | 每次点击/回车；`sending` 期间禁用 |
| ⚠️ | 附件 → `[file: path]` token | `ActionBar.tsx:436-452` | 上传后把 token 拼进正文 | 上传成功即拼；`clearPendingAttachments` 退役 |
| ⚠️ | `/plan` 前缀短路 | `ActionBar.tsx:408-427` | 纯 `/plan` 不发消息，只切模式 | 命中前缀即走 |
| ⚠️ | `handleSendMessage` | `App.tsx:4708` | 发送编排器：乐观行、缓存、requestId、context | 每次发送一次 |
| ⚠️ | `isQueueSend` 判定 | `App.tsx:4768` | 判定本次是排队还是新回合 | 每次现算 |
| ⚠️ | 临时会话 key（`transient-`/`pending-*`） | `App.tsx:4830/4834` | 无 key 时占位，accepted 后替换 | accepted/失败时替换或删 |
| ✅ | `optimisticExchange` 乐观回声 | `App.tsx:4991-5001` | 本地先渲染 role=user 行，`pending_ack:true` | 建：每个新回合；退役：服务端回显把 `pending_ack` 置 false |
| ⚠️ | `pendingRequestRef` / `pendingDraftRef` | `App.tsx:5025/5060` | requestId → 待确认发送元数据 | 建：发送；删：accepted / error |
| ✅ | 本地 pending **三套** | `pendingBySessionRef` `App.tsx:261`、`markSessionPending` `:5339`、`multiProjectPendingByKey` `:327` | 三处各存一份「是否在回复」 | 见 §3 冲突 ⑤ |
| ⚠️ | `sessionService.sendMessage` | `services/session.ts:951` | 组 `session.message` 帧 | 每帧一次；进 `pendingMessages` |
| ⚠️ | `sendWSMessage` | `session.ts:872` | 序列化 + E2EE + send | **非 OPEN 直接丢帧**（靠 `pendingMessages` 重连补发） |
| ⚠️ | `pendingMessages` + `resendPendingMessages` | `session.ts:343/1064` | 未确认帧缓存，重连原样重发 | 删：accepted / error |
| ⚠️ | `compactContext` / `contextCache` | `session.ts:697` | selection 与上次相同则剔除 | 每会话一条 cacheKey |
| ⚠️ | 队列三操作 + `optimisticDequeuedIdsRef` | `session.ts:1096/1111/1127`；`App.tsx:265` | 删/改/立刻发 + 本地先隐藏 | 出队：服务端队列不再含该 id 时自删 |
| ⚠️ | `runSlashCommand` / `setPlanMode` / `cancelMessage` | `session.ts:1029/1006/1075` | slash、plan 二进制开关、中断 | 无缓存（plan/cancel） |
| ⚠️ | WS 单读分发 | `server/internal/api/ws.go:494` | 按 `req.Type` 分发；阻塞 handler 用 `go` | 每帧一次 |
| ⚠️ | `handleSessionMessage` | `ws.go:583` | 解析 + 建会话/worktree + 回 accepted + submit | 每帧一次 |
| ⚠️ | `reserveClientRequest` 去重 | `ws.go:613/1222` | 同 requestId 24h 只放行一次 | 建：首次；清：24h 过期 |
| ⚠️ | `reserveOrQueueSessionMessage` | `stream_hub.go:496` | 原子「占坑或入队」 | Active 则入队，否则 Active=true |
| ⚠️ | `runSessionMessage` / `startNextQueuedSessionMessage` | `ws.go:921/1010` | 跑回合；结束后 Pop 队首起新回合 | 队列空即停 |
| ⚠️ | `PopQueuedSessionMessage` | `stream_hub.go:597` | 取队首并置 Active | 非 Active 且非冻结 |
| ⚠️ | `Freeze`/`UnfreezeQueuedSessionMessages` | `stream_hub.go:564/579` | cancel 时冻结队列防自动续跑 | 冻结位 |
| ⚠️ | `handleSessionCancel` + `ActiveTurnID` | `ws.go:1081/516` | 读循环内**同步**取目标轮 id 再异步中断 | 每帧一次 |
| ✅ | `BroadcastSessionUserMessageAt` + `SetPendingUserAt` | `stream_hub.go:952/414` | 设 pendingUser 并广播 `session.user_message` | 每回合一次；覆盖写 |
| ✅ | `buildSessionUserMessageResponse`（seq 权威） | `stream_hub.go:208/243` | `userExchangeSeq>0` 才带 `seq` | 每帧 |
| ⚠️ | HTTP 旁路 `/api/sessions/{key}/message` | `http_session_message.go:14` | CLI 走的同一条提交路径 | 与 WS **共享**同一队列（非冲突） |

### 2.2 接收链路（服务端 → 浏览器 → React 状态）

| | 机制 | 位置 | 作用 | 存亡判据 |
|---|---|---|---|---|
| ⚠️ | `ws.onmessage` → `parseWSMessage` | `session.ts:504/858` | 收帧 → 解密 → `handleMessage` | 连接期；迟到帧按 `this.ws!==ws` 丢弃 |
| ⚠️ | e2ee `decodeWSMessage` | `session.ts:869` | 受保护帧解密 | `e2eeService.isRequired()` 时活 |
| ⚠️ | `handleMessage` | `session.ts:740` | 帧总入口：清 probe、拦 pong/e2ee.error、清 `pendingMessages` | 每帧一次 |
| ✅ | **批帧展开**（`emitDecrypted`） | `session.ts:776` | `events` 数组 → （若 `reset`）合成一帧 `session.stream.reset` + 逐条递归派发 | 每帧；展开后 return |
| ✅ | `emit`（全局 listeners） | `session.ts:894` | 广播给 `subscribeEvents` 监听者 | — |
| ✅ | `handlers`（按会话）+ `pendingStreams` | `session.ts:340/341/925` | 按 sessionKey 订阅；无 handler 时缓存事件 | **`pendingStreams` 无上限** |
| ✅ | `updateActiveStreamState` / `activeStreams` / `isSessionStreaming` | `session.ts:900/342/921` | 「本会话是否在流」状态机 | add：非 `message_done` 事件；delete：done/error |
| ✅ | `session.stream.reset`（合成帧） | `session.ts:803-807` | 供缓存侧清瞬时尾巴 | **只走 `emit`，不进 `handlers`** ⇒ `useSessionStream` 永远看不到 |
| ⚠️ | handler map（**28 个 key**） | `useRealtimeEvents.ts:998-2000` | 事件分发表 | 无死 handler（已交叉验证 28/28 有发送方或客户端合成） |
| ⚠️ | `handleSessionStream` + 9 个内层 case | `useRealtimeEvents.ts:458-678` | 单事件主处理器 | 每事件；`error` case 也调收尾（与 `session.error`/`session.done` 三条入口重叠） |
| ✅ | `handleSessionStreamDone` | `useRealtimeEvents.ts:357-456` | **回合收尾唯一出口** | 每回合一次；返回「是否真结束」 |
| ⚠️ | `reloadSessionForReplay` / `getReplayTargetsForRoot` / `replayTargetsForAllRoots` | `useRealtimeEvents.ts:252/280/312` | 重锚定的三件套 | 调用时活 |
| ✅ | 轮询兜底 + `multiProjectPendingByKey` | `App.tsx:3561/3609/327` | 5s visible-only 拉 `/api/replying-sessions` | 只重算成功节点的键 |
| ✅ | 服务端 `pendingSessions` / `ReplyingList` | `stream_hub.go:30/57` | 每会话在途回合状态 + 事件缓冲 | 建：`ensurePendingSessionLocked`；清：`ClearSessionPending`；**`ReplyingList` 无显式上限** |
| ⚠️ | `AppendReplyEvent` | `stream_hub.go:688` | 追加事件、分配 EventCursor、维护 Summary | 每事件 |
| ⚠️ | `replayStates` / `ReplayPending` / `collectReplayStep` / `replayStepToClient` | `stream_hub.go:31/827/1045/1087` | 重放→实时状态机与排空循环 | 建：`ReplayPending`；切 live：`nextReplayStepLocked` |
| ✅ | `ClearSessionPending` | `stream_hub.go:864` | 清回合状态（队列非空则保留） | 每回合结束 |
| ⚠️ | `BroadcastSessionStream` / `Done` / `UserMessage` / `QueueUpdated` | `stream_hub.go:905/926/933/981` | 四条广播 | 每事件/回合 |
| ⚠️ | `session.ready` → `ReplayPending` | `ws.go:1055` | 挂载即快照重建 | 每次挂载 |
| ✅ | **死帧 ×2** | `session.answer_question.accepted`（`ws.go:569`）、`session.repointed`（`http.go:1261`） | 服务端发出、**前端零引用** | 永不消费 |
| ⚠️ | 跨节点守卫 + `_nodeId` 注入 | `session.ts:781`；每个 handler 首行 | 节点不匹配即 return | 每帧 |

### 2.3 缓存与持久化

| | 机制 | 位置 | 作用 | 存亡判据 |
|---|---|---|---|---|
| ✅ | `sessionCacheRef` | `App.tsx:267` | 每会话的**权威内存缓存**（键 `node::root::key`） | 只增；仅根删除时按前缀清 |
| ⚠️ | `cacheVersion` / `bumpCacheVersion` / `…Debounced` | `App.tsx:455/1809/1812` | 手动失效信号 | 计数器；**直接与 30ms 防抖两条路径并存** |
| ⚠️ | `loadingSessionRef` | `App.tsx:269` | 同会话并发加载去重（in-flight Promise） | `finally` 清 |
| ❌ | ~~`loadedSessionRef` 是死字段~~ | `App.tsx:268` | 标记「已加载过」 | **分链路清点报错**：它在 `:3896/:4546/:4630/:7862` 被写、`:7846` 被读。**不是死字段** |
| ⚠️ | stale 标记四件套 | `App.tsx:270/2206/2218/2230` | 记录「缓存已过期需重拉」 | 建：`markSessionStale`；清：`clearSessionStale` |
| ✅ | `anchorSeqRef` / `_windowMeta` / `_anchoredAt` | `App.tsx:2243/2329`；`SessionViewer.tsx:1524` | 重锚定簿记：单调序号 + 窗口元信息 | **只在内存**；`stripAnchorBookkeeping` 保证不落 IDB |
| ⚠️ | `selected` / `drawer` / `bound` 三套 UI 状态 | `App.tsx:443/281/279` | 同一会话信息在三处各存一份 | 切根重建 / 显式清 |
| ✅ | pending **五处散落** | cache / drawer / selected / `pendingBySessionRef` / `multiProjectPendingRef` | 「是否在回复」 | `clearLocalPendingForSession`（`App.tsx:2137`）逐个清，漏一处即现「灯不灭/不亮」 |
| ⚠️ | IDB 单会话 store 六件套 | `session.ts:1726-1749/2184-2228` | 持久化会话快照 | **live 流式路径不写 IDB**（只有 sync/窗口回包才写） |
| ✅ | `toPersistentSession`（**唯一淘汰点**） | `session.ts:2347` | 写 IDB 前过滤 `seq>0`；超 500 条/200KB 时**从头部**淘汰 | 只在写入时 |
| ⚠️ | `stripAnchorBookkeeping` / `truncated` 标记 | `session.ts:2411/2396` | 落盘剥离锚点簿记；标记被截断 | 写 IDB 时 |
| ⚠️ | 列表快照六件套 | `session.ts:1848-1894` | 会话列表首帧种子 | **整体替换**（与单会话「只增」相反） |
| ✅ | `mergeSessionExchanges` | `session.ts:2029` | 按 seq 合并持久行，**丢弃 seq=0**，升序 | 只增不减 |
| ✅ | `composeLoadedExchanges`（**加载唯一组装规则**） | `session.ts:2068` | `inFlight=false` ⇒ 只返持久行；`true` ⇒ 接回 seq=0（只留最后一次 compact 之后） | 判据：`pending` |
| ✅ | `isTransientExchange` | `session.ts:2119` | 瞬时行 = `seq` 空**且非 user** | — |
| ✅ | `dropTransientExchanges` | `session.ts:2138` | 清瞬时行 | 事件驱动；**重锚定会话刻意不清** |
| ⚠️ | `appendSessionDelta` / `withSessionMeta` | `session.ts:2150/1968` | sync 增量；meta 字段回退链 | base 过滤 `seq>0` |
| ⚠️ | `syncSession` + `windowedViewKeys` | `session.ts:2472/2454` | 全量同步；窗口态标记（**内存标记，刷新即丢**） | — |
| ✅ | `restoreActiveSession` | `App.tsx:2245` | 窗口优先、失败回退 sync；**并入**缓存（非替换） | 结果写缓存 + `markSessionReady` |
| ⚠️ | JSONL 正文 + aux + SQLite meta | `manager.go:34/35/32` | 服务端持久化三分工 | 只在 DeleteSession 删；**无按体量淘汰** |
| ⚠️ | `addExchangeForAgentAt` / `nextSeq` | `manager.go:688/709` | 落盘一条 exchange；`seq = max+1` | 不按长度 |
| ⚠️ | `loadExchanges`（增量/全量） | `manager.go:2055` | 游标一致→过滤缓存；否则增量/全量 | **全量分支整体替换缓存** |
| ⚠️ | `pendingToolCalls`（纯内存） | `manager.go:404-487` | 在途工具卡 | **从不落盘、进程重启即丢** |
| ⚠️ | `getSessionWindowUnsafe` + `SessionWindowMeta` | `manager.go:1570/310` | 窗口切片（`latest>` / `beforeSeq>`） | 不写 `m.sessions` 缓存 |
| ⚠️ | `projectSessionExchangesForResponse` | `http.go:1413` | 响应边界折叠同期重复行 | **磁盘一行未删**（`MINDFS_SESSION_PROJECTION=off` 可关） |
| ⚠️ | `useSessionStreamCache` 的 6 个 `append*` | `useSessionStreamCache.ts:163/206/241/324/344/388` | 流式写入各角色行 | 只改内存（防抖 bump）；**不写 IDB** |

### 2.4 渲染与排序

| | 机制 | 位置 | 作用 | 判据（凭什么渲染/不渲染/排在哪） |
|---|---|---|---|---|
| ⚠️ | `visibleExchanges` | `SessionViewer.tsx:1138` | 窗口化视图的唯一持久源 | 只有 `seq>0` 入内 |
| ⚠️ | `visibleAux` | `:1141` | 窗口内 aux（工具卡/token 挂载点） | `Record<seq, aux[]>` |
| ✅ | `latestSeqState` / `noteLatestSeq` / `latestSeq` | `:1171/1174/1184` | 「会话最新持久化 seq」水位线 | **只增不减；只被 `applyWindow` 推进（全文件唯一调用点 `:1409`）** |
| ⚠️ | `visibleSeqSet` | `:1185` | 窗口内实际存在的 seq 集合 | 「窗口**有没有**」 |
| ⚠️ | `windowUserCounts` | `:1193` | 窗口内每个 user 正文的出现次数 | **按原始内容串计数**（不做空白归一） |
| ⚠️ | `windowToolCallIds` | `:1206` | 窗口 aux 已出现的 callId 集合 | `callId` 非空字符串 |
| ⚠️ | `windowTailTexts` | `:1216` | 窗口最新 **3** 条持久行的归一化正文 | `OVERLAY_TAIL_ROWS=3` |
| ⚠️ | `renderedPersistedTexts` | `:1235` | 「本帧已会被渲染的持久正文」全集 | 上面那份 + `seq>latestSeq` 的行 |
| ✅ | `tailOverlay`（**7 个分支**） | `:1227` | 缓存里「窗口未覆盖」的条目 | A1 `visibleSeqSet.has` → 丢；**A2 `seq<=latestSeq` → 丢**；A3 `seq>latestSeq` → 渲染；B tool 同 callId → 让位；C user 内容计数消抵；D 正文含于已渲染文本且 ≥32 字 → 让位；E 兜底渲染 |
| ⚠️ | `composedExchanges` | `:1300` | 窗口 + overlay 按对象同一性拼接 | 窗口在前（=保留持久那份） |
| ⚠️ | `applyWindow` | `:1374` | 把窗口写入 visible*，推进 `latestSeq` | `keepOlder` 时 `mergeSessionExchanges` 保历史 |
| ⚠️ | `loadMore` | `:1466` | 向上翻页（`beforeSeq` 前置合并） | 哨兵进入视口触发；**不推进 `latestSeq`** |
| ⚠️ | 锚定 effect | `:1527/1531` | 按 `_anchoredAt` 一次性换窗 | `anchorExchanges` 显式 `filter(seq>0)` |
| ⚠️ | init seed effect | `:1416` | 首帧种子 + 拉尾部窗口 | 种子只留 `seq>0` |
| ⚠️ | `targetSeq` 跨窗口拉取 | `:1811/1876` | 搜索跳转：不在窗口内就拉目标窗口 | **`setVisibleExchanges(winExchanges)` 整体替换；不推进 `latestSeq`** |
| ⚠️ | `dedupeToolCards` | `useSessionStream.ts:124` | 同一 callId 只留**首个** | 与 `windowToolCallIds` 是**同一 concern 的两处实现** |
| ✅ | `persistedSeq` | `useSessionStream.ts:358` | 取真 seq，没有就 **0**、绝不编造 | `>0` 才算持久 |
| ✅ | `buildBaseTimeline` | `useSessionStream.ts:363` | 展平成时间线，**数组顺序即时间序、不排序** | 顺序完全由上游决定 |
| ⚠️ | `buildAssistantTimeline` | `useSessionStream.ts:191` | 一条 assistant 行按 aux.line 切段并插辅助项 | aux 优先级 thought>plan>todo>compact>toolcall |
| ⚠️ | `stableTimelineID` | `useSessionStream.ts:84` | React key（内容稳定） | `prefix:index:ts:agent:hash` |
| ⚠️ | `settleRunningTools` | `useSessionStream.ts:140` | 把悬空 running 工具卡收成 complete | `ask_user`/`task` 豁免 |
| ✅ | `isStreaming` / `streamVersion` / `streamStatusText` | `useSessionStream.ts:503` | 流式三件套（驱动「正在生成」与底部跟随） | chunk 合并成 30ms 一次 |
| ⚠️ | `applySessionContextWindow` | `useSessionStream.ts:460` | 给末条 assistant 补 context_window | 与 `buildAssistantTimeline` 末尾**两处补同一字段** |
| ⚠️ | `activeAskUserCallId` | `:1976` | 找当前「等待回答」的 ask 卡 | 倒序扫；遇正文即停 |
| ⚠️ | **两个 `SessionViewer` 挂载点** | `App.tsx:8254`（主区）/`:9722`（抽屉） | 同一会话可同时挂两处 | **各自独立持有 `visibleExchanges`，共享同一 App 缓存** |
| ⚠️ | `normalizeOverlayText` + 两个阈值 | `:500/506/509` | overlay 对账的空白归一 | 尾行 3、短文本地板 32 字 |


## 3. 关系与「能否合并 / 替代」判定

### 3.1 主链与三条并行真值

```
发送  ActionBar → handleSendMessage → sendMessage ──(WS session.message)──┐
                                            │                              │
                          乐观回声 + 缓存 ──┘                              ▼
                                                              ws.go 单读分发 → reserveOrQueue
                                                                 │
   ┌─────────────────────────────────────────────────────────────┘
   ▼
 runSessionMessage → agent 事件 → AppendReplyEvent → pendingSessions[key].ReplyingList
                                          │
                    ┌─────────────────────┴─────────────────────┐
                    ▼                                           ▼
        BroadcastSessionStream（live 客户端）        ReplayPending（刚挂上的客户端）
                    │                                   整批 reset 快照
                    └────────────────┬──────────────────────────┘
                                     ▼
                    session.ts emitDecrypted（形状归一 + 状态机）
                                     │
                    ┌────────────────┴─────────────────┐
                    ▼                                  ▼
        emit → 全局 listeners                handlers → useSessionStream
      （useRealtimeEvents 的 28 个 handler）      （按会话的流式状态）
                    │
                    ▼
        sessionCacheRef（内存权威）←→ IndexedDB（持久，只增/头淘汰）
                    │
                    ▼
        SessionViewer: visibleExchanges（窗口） + tailOverlay（猜差集）
                    → composedExchanges → buildBaseTimeline（数组顺序=时间序）
```

图上可以看出问题所在：**「同一轮内容」在链路上有 6 个可渲染的落点**
（`ReplyingList` / 窗口 API / 内存缓存 / IDB / `visibleExchanges` / `tailOverlay`），
而**没有任何一个地方记录「这一轮属于哪一份」**。于是每一层都要自己判一次。

三条**平行的真值**（互不同步，各写各的）：

| 真值 | 存哪 | 谁写 | 谁清 |
|---|---|---|---|
| 「这个会话在不在跑」 | ① `activeStreams`（session.ts）② 内存缓存的 `pending` ③ `multiProjectPendingByKey` | ① stream 事件 ② 乐观发送/WS ③ 5s 轮询 + WS | ① done/error ② done ③ 轮询覆盖 |
| 「这一行是不是已持久化」 | 41 处各自判（`seq>0` ~14 处、内容比对 6 处、生命周期 4 处…） | — | — |
| 「该渲染窗口那份还是瞬时那份」 | ① `visibleExchanges` ② `tailOverlay`（内容比对） ③ `composedExchanges`（对象同一性） | — | — |

### 3.2 可以合并 / 替代的（判定：应当合并）

| # | 现状 | 合并成 | 依据 | 合并后能删掉 |
|---|---|---|---|---|
| ① | 「是否在回复」**9 个机制** | 1 个（服务端 `pendingSessions.Active` 为唯一源，前端只做展示缓存） | 三套真值判据与清理点全不同，且 CLAUDE.md §12 已记录过「蓝灯不亮」事故 | `activeStreams`+`isSessionStreaming`、缓存 `pending` 的判定职责、`multiProjectPendingRef` 的旁路推断、`setSelectedPendingByKey` / `markSessionPending` / `setMultiProjectSessionPending` 三件套 |
| ② | 「瞬时 vs 持久」**41 处判定** | 1 处（类别由**数据结构**表达，不再由谓词表达） | 见 §3.4 冲突①；红测试已钉住两把尺 | `isTransientExchange`（连同 user 例外）、`dropTransientExchanges` 的调用点、`mergeSessionExchanges`/`appendSessionDelta` 的 `seq>0` 过滤、`anchorExchanges.filter`、init seed 的 `filter`、`persistedSeq` 的兜底 |
| ③ | 工具卡去重**两处**：`windowToolCallIds`（渲染前让位）+ `dedupeToolCards`（时间线内保留首个） | 1 处 | 同一 concern、判据不同（callId 集合 vs 首个保留） | `windowToolCallIds` 一整块 |
| ④ | **内容比对让位 5 处**（`windowUserCounts`/`windowTailTexts`/`renderedPersistedTexts`/C 分支计数消抵/D 分支子串包含） | **0 处** | 它们是「不知道看哪一份」的替代品；②做完后不再需要猜 | 这 5 处 + `normalizeOverlayText` + 两个阈值常量（`OVERLAY_TAIL_ROWS` / `OVERLAY_DUP_MIN_CHARS`） |
| ⑤ | `windowMeta` **三个写者**口径不一（`applyWindow` 取 min；`loadMore`/`targetSeq` 整体覆盖） | 1 个 | 见 §3.4 冲突⑩ | 两处整体覆盖 |
| ⑥ | aux 合并**两套语义**（`restoreActiveSession` 按 seq 覆盖 vs `appendExchangeAuxDelta` 拼接） | **各自保留，但拼接必须幂等**（**2026-10-06 更正**：一度判定「统一成覆盖」是错的 —— 覆盖会让增量路径丢卡片，两者是不同操作：快照 vs 增量） | 两者只有一个调用方各自的语义需求；真正的缺陷是拼接不幂等（重投递即重复） | 无（拼接的去重已补上，见 §3.4 ⑦） |
| ⑦ | 附件**两套载体**（正文 `[file: path]` token vs `context.selection` 结构化） | 1 套 | 二者互不感知，同一次发送可能两处都带上 | 视产品语义择一 |
| ⑧ | plan mode **两通道**（文本 `/plan` 前缀三处解析 vs `session.plan_mode.set` 二进制） | 1 套 | `ActionBar`/`App`/服务端三处各解析一次前缀 | 文本前缀解析（或二进制通道） |
| ⑨ | `selection` 变没变的**两处**（`compactContext` 的 cache vs 每次 `buildClientContext` 重建） | 1 处 | 同一判定两处实现 | `contextCache` |
| ⑩ | `context_window` 补丁**两处**（`buildAssistantTimeline` 末尾 + `applySessionContextWindow`） | 1 处 | 同一字段两处补 | 后处理那一处 |
| ⑪ | `appendSessionDelta` 与 `mergeSessionExchanges` 的 `seq>0` 过滤**重复** | 1 处 | 同一件事写两遍 | `appendSessionDelta` 的过滤 |

### 3.3 真独立、不能合并的（独特性在哪）

| 机制 | 独特性（为什么不可替代） |
|---|---|
| 服务端 `ReplyingList`（在途事件缓冲） | **客户端挂载时必须有一份权威的「当前轮事件序列」**。落盘只在回合末发生，在途内容在持久层**根本不存在**。这是「快照重建」能成立的唯一前提，删掉它就只能靠猜（历史上正是这么错的） |
| 窗口 API（`?latest=` / `?beforeSeq=`）+ `_windowMeta` | 会话可达数千条，必须有**按 seq 寻址的分页**；跳转/搜索/向上翻页都依赖它。与「当前轮」正交（一个管历史、一个管在途） |
| IndexedDB | 冷启动首帧与离线可用；与内存缓存职责不同（它只存持久行、按预算淘汰）。**但它与内存缓存之间没有对账**，这是冲突⑧的来源 |
| `pendingStreams`（无 handler 时缓存事件） | 事件可能早于 React 订阅到达（`session.stream` 在订阅前就来了）。不可删，**但必须加上限** |
| e2ee 解密 / 跨节点守卫 / `_nodeId` 注入 | 安全边界与寻址边界，与消息语义无关 |
| 服务端队列（`Queue`/`Freeze`/`Promote`） | 「会话忙时排队」是产品语义；与 WS/HTTP 共享同一队列是**正确**的复用 |
| 导入判重（`seenUUIDs`/`live-owned`/读取侧投影） | 外部转录双写是**外部世界的现实**（CLI 会重复写文件），必须在写入侧与读取侧都设防。**但**：前端 `exchangeAlreadyRecorded` 与服务端投影是两套判据，这一层可再收敛 |
| 子会话 / fork / repoint | 会话的身份与转录搬家，是数据模型层，不在「渲染哪一份」的问题域里 |

### 3.4 冲突清单（均有代码证据）

> **状态列是清点时的快照**；每条的实际处置与证据见 **§6 第三步执行记录**（2026-10-06：23 条红全部转绿，含 4 条判定纠错）。

| # | 冲突 | 证据 | 后果 | 状态 |
|---|---|---|---|---|
| ① | 「瞬时行」**两把尺** | `isTransientExchange`（`session.ts:2119`，seq 空**且非 user**）vs `mergeSessionExchanges`（`:2029`，只收 `seq>0` ⇒ **丢掉 seq=0 的 user**） | 刚发出去的消息会不会消失，取决于先跑哪条路径 | ✅ 已核 · **已有红测试** |
| ② | `tailOverlay` **A2 分支**假定「`seq<=latestSeq` ⇒ 窗口会渲染」 | `SessionViewer.tsx:1251`；而 `noteLatestSeq` **全文件只有 `:1409` 一个调用点**，`targetSeq`（`:1893`）用 `setVisibleExchanges(winExchanges)` **整体替换**窗口且不推进 `latestSeq` | 跳到较早的消息后，窗口与 `latestSeq` 之间那段行**两边都不渲染**（渲染空洞）；切会话或再次重锚定才恢复 | ✅ 已核 · **已有红测试**（单元：`session-overlay-unit.test.mjs` 的 A2 用例；E2E 因搜索开关不在可驱动 URL 下渲染而带原因跳过） |
| ③ | 工具卡去重两处实现 | `windowToolCallIds`（`:1206`）vs `dedupeToolCards`（`useSessionStream.ts:124`） | 判据不同 ⇒ 边界情况下两侧各留一份或各丢一份（**aux 与窗口行脱节时卡片两边都不渲染**） | ✅ 已核 · **已有红测试**（单元 + 删除契约） |
| ④ | thought/plan/todo/compact 去重**只有弱判据** | 它们没有 callId，只能走 D 分支的「归一化文本包含 + ≥32 字 + 仅窗口最新 3 行」 | 短片段（<32 字）或更早的陈旧拷贝**认不出**，重复显示 —— **短回复（「收到」）落盘后显示两遍** | ✅ 已核 · **已有红测试**（单元） |
| ⑤ | 「是否在回复」**9 个机制、五处存储** | `activeStreams`、缓存 `pending`、`drawer.pending`、`selected.pending`、`pendingBySessionRef`、`multiProjectPendingRef`；`clearLocalPendingForSession`（`App.tsx:2137`）需逐个清 | 漏一处 ⇒ 小蓝灯不灭/不亮（CLAUDE.md §12 已记录过） | 已有红测试（结构） |
| ⑥ | 乐观行 `pending_ack` **三种匹配键** | `session.accepted` 按 `content+timestamp`；`session.user_message` 按 `role+content`；`clearLocalPendingForSession` 全量清 | 三种判据各自漏网 ⇒ 气泡一直「待确认」或提前转正 | 已有红测试（结构·自动推导：清 `pending_ack` 今天 7 处） |
| ⑦ | aux 合并覆盖 vs 拼接 | `App.tsx:2325-2328`（覆盖）vs `session.ts:2180`（拼接） | 工具卡重复渲染 | ✅ **已修（2026-10-06）**：拼接补上去重 ⇒ 幂等；两者语义各自保留。行为断言 `session-core-unit`【⑦b】、契约 `merges`【⑦】 |
| ⑧ | `mergeSessionExchanges` **只增不减** vs `toPersistentSession` **从头部淘汰** | `session.ts:2029` vs `:2347`（500 条/200KB） | 同一会话两种落盘体积；IDB 与内存长期不一致 | **已改写（见下）** |

**⑧ 的更正（写测试时发现，分链路清点报错了）**：`forceNoTruncate` **并不绕过上限**
（`session.ts:2396` 只决定写进 IDB 的 `truncated` 标记；`kept` 的切片两种调用完全相同）。
真正的缺陷更尖锐，且已写成红测试 ⑧b：

> **数据被砍了，标记却说没砍。** `truncated` 的语义是「下次加载必须全量回源」
> （`syncSession` 依此决定走全量还是增量），窗口态（`:2516`）把它置成 `false`
> ⇒ 被砍掉的头部**永远不补回来**。实测：600 行 → 砍到 500 行，标记仍是 `false`。
| ⑨ | **compact 术语/前提冲突** | 前端注释称「compact 是服务端历史的**重置点**，在它之前产生的瞬时行属于服务端已经丢掉的那段历史」（`session.ts:2088-2092`）；实际服务端交换 JSONL **append-only，全仓无截断/重写路径** —— `manager.go` 里唯一的 `os.Remove` 在两处 `DeleteSession`（`:1439/:1447`），compact 在服务端只写一条 `CompactNotice` aux（`usecase/session.go:2652`） | 前端按「服务端丢了那段」清理本地行，而服务端一行没丢。**能撞对的只是结论**（那批 seq=0 本就该由「不在跑就没在途内容」清掉），依据是错的 ⇒ 一旦服务端将来真的裁历史，两边会按不同前提各做一套 | ✅ **已核**：前提为假，注释需改，compact 边界规则在 §3.2 ② 完成后可能整条多余 |
| ⑩ | `windowMeta` 三写者口径不一 | `applyWindow` 取 min（`:1399-1406`）vs `loadMore`（`:1492`）/`targetSeq`（`:1895`）整体覆盖 | `targetSeq` 的 `inRange` 判定可能误判 | ⚠️ 分链路清点 |
| ⑪ | 两个挂载点**两套窗口态** | `App.tsx:8254` / `:9722`，各自 `visibleExchanges`，共享同一 App 缓存 | 同一会话主区与抽屉可显示不同窗口 | ⚠️ 分链路清点 |
| ⑫ | 两个**死帧** | `session.answer_question.accepted`（`ws.go:569`）、`session.repointed`（`http.go:1261`）：前端零引用 | 白发的帧；`session.repointed` 缺席意味着**前端不知道会话被搬家过** | ✅ 已核 |
| ⑬ | 两个**无上限缓冲** | `pendingStreams`（`session.ts:831-837` 只 push）、`ReplyingList`（`stream_hub.go:57`，仅 userShell 有 256KB 截断） | 长回合/无人订阅时内存无界增长 | ⚠️ 分链路清点 |

| ⑭ | 用户行的两条判据**口径不同**：C 分支用**原始 content** 计数，D 分支用**去空白** | `sessionOverlay.ts` 的 `windowUserCounts`（原 `SessionViewer.tsx:1197`）vs `normalizeOverlayText` 分支 | 只差空白的短用户消息：C 认不出、D 又因短于长度地板放行 ⇒ 显示两遍 | ✅ 已核 · **已有红测试**（单元） |
| ⑮ | 「同一件事写两遍」四组：附件载体 4 处 / plan 通道 5 处 / selection 判定 2 处 / context_window 补 2 处 | 见 §4.1 的删除契约（全部**自动推导**计数，不硬编码位置） | 改一处漏一处 | 已有红测试（结构 ×4） |

### 3.5 一句话结构根因

> **`exchanges` 是一个数组，装的却是两种本体不同的东西**（服务端持久行 / 客户端派生行），
> 而链路上有 6 个可渲染落点、3 条并行真值。数组不表达类别 ⇒ 每个消费者只能自己重新推断；
> 推断信息不足时（同内容重复发言、短片段、窗口被中段替换）就退化成**内容比对猜**。
> 上面 13 条冲突里，①②③④⑨⑩⑪ 都是这一个根因的不同投影。

## 4. 每个机制的测试

### 4.1 已完成

> 2026-10-06：红测试**已全部转绿**（见 §6）。它们在此之后的角色是**回归护栏**——
> 不再是「待修清单」。

| 载体 | 覆盖 | 例数 | 状态 |
|---|---|---|---|
| `tests/ts-module-hook.mjs` | （基础设施）让 `node --test` 能 import `web/src/**/*.ts` | — | ✅ |
| `tests/session-core-unit.test.mjs` | **行为**：`mergeSessionExchanges` / `composeLoadedExchanges` / `isTransientExchange` / `dropTransientExchanges` / `toPersistentSession` / `appendExchangeAuxDelta` | 16 | 12 绿 / **4 红** |
| `tests/session-overlay-unit.test.mjs` | **行为**：`computeTailOverlay`（overlay 尾巴的全部 7 个分支） | 6 | 2 绿 / **4 红** |
| `tests/message-mechanism-merges.test.mjs` | **删除契约**：§3.2 / §3.4 的每条合并与冲突各一条 | 15 | **15 红** |
| `tests/e2e-message-behavior.test.mjs` | **E2E**（隔离实例 + CDP）：冷启动窗口 / 完成瞬间交接 / 应用内切走切回 / 查库判据 | 5 | 4 绿 + 1 带原因跳过 |
| `scripts/mindfs-iso.sh` | 隔离实例（E2E 的地基，含 8 条隔离自证） | — | ✅ |

**红测试共 23 条**，覆盖 **14 个冲突（①②③④⑤⑥⑦⑧⑨⑪⑫⑬⑭⑮）的每一个**，分三层：

| 层 | 条数 | 说明 |
|---|---|---|
| 行为（单元） | 8 | ①⑨b⑦b⑧b（`session-core-unit`）+ ②③④⑭（`session-overlay-unit`） |
| 删除契约（源码结构） | 15 | ②③④⑤⑥⑦⑧⑨⑪⑫⑬⑮a⑮b⑮c⑮d —— 每条钉「重复机制已消失」 |
| E2E | 1 | ②（**带原因跳过**：搜索开关在可驱动的 URL 状态下不渲染，改由单元层覆盖） |

### 4.2 覆盖不到的（连同原因，不含糊）

| 缺口 | 原因 |
|---|---|
| 冲突⑪（两处挂载）只有结构断言 | **抽屉没有 UI 入口** —— 只由内部路径（related_files / worktree / 会话事件）设置，DOM 里点不出来（实测两次）。等窗口态提到挂载点之上后，它变成一条可断言的纯函数不变量。 |
| 冲突② 的 E2E | 搜索开关属于「会话列表」布局；可驱动的 URL（`?root=&session=&view=chat`）下侧栏是文件树，展开后仍是文件树。已在单元层覆盖（确定性更强）。 |
| 虚拟化阈值（>80 行）之后的渲染 | E2E 的 DOM 判据只在未虚拟化时成立；超过阈值要靠滚动验证，两条用例都显式断言了 `count <= 80` 并在不成立时报错。 |
| 导入判重链路（§3 回补的 6 个机制） | 只有「查库判据」工具（`countRows`）能把它与渲染层分开；针对这 6 个机制本身还没有红测试。 |

### 4.3 2026-10-06 的抽取：判定搬出组件

`tailOverlay` 的判定原来住在 `SessionViewer.tsx` 的 `useMemo` 里，**因此测不了**。测试只能各自绕：

- `tests/session-window.test.mjs:780` 写了一份「与 SessionViewer.tailOverlay **同构**」的副本，再对副本断言；
- `tests/session-window-overlay-dedup.test.mjs` 对 `composedExchanges` 那段源码做字符串手术（`useMemo(` → `return (`）再 `eval`。

两份绕法都是**会漂移的副本** —— 冲突②③④⑭ 就是被副本挡住的：副本只复刻了作者关心的分支。

现在：判定搬到 `src/components/sessionOverlay.ts`（逐字搬迁，两个阈值变成可选参数），
SessionViewer 只做接线。随之：

- 那份同构副本**已删除**，换成对真实现的薄适配器 —— `session-window.test.mjs` 里那 6 条
  断言（①窗口已含不重渲染 / ②流式不丢 / ③陈旧乐观拷贝让位 / ④真重复仍显示 /
  ⑤超出 latestSeq 保留 / ⑥未锚定不灌历史）从此跑在**真实现**上；
- 6 条源码结构断言重定向到新文件（写法层照旧钉住）；
- 新增 `session-overlay-unit.test.mjs`，把 ②③④⑭ 写成行为红测试。

**验证行为不变**：抽取后全量回到基线（177 例 / 158 通过 / 18 红），`tsc` 通过；
重建 `web/dist` 后 E2E 仍 **4 绿 + 1 跳过**（渲染路径改了，E2E 是唯一能查这件事的层）。

## 6. 第三步：执行记录（2026-10-06）

**起点 23 条红 → 终点 0 条红**（184 例 / 183 通过 / 1 跳过 = E2E 的带原因跳过）。
每一刀都是先红后绿，且**互相独立可回滚**。E2E 4 条绿作为渲染路径的安全网，每刀之后都重跑。

### 6.1 已修（按冲突编号）

| 冲突 | 改法 | 转绿的测试 |
|---|---|---|
| **⑧** `truncated` 标错 | 删掉 `forceNoTruncate` 参数，标记永远诚实（**读侧本来就能表达「窗口态不回补」**，写侧说谎导致刷新后读到「标记完整实则被砍过」的记录 → 只做增量 → 静默丢历史） | ⑧ + ⑧b |
| **⑨** compact 边界规则 | 删除（前提为假：服务端交换 JSONL append-only）+ 修正假注释 | ⑨ + ⑨b |
| **⑨c**（新发现） | compact 的**客户端清理点**也删了：其注释里「服务端历史重置了 / 此刻没有在途回合」两句都不成立，而 compact 是**轮内**事件 ⇒ 清一次就把本轮前半段连刚追加的通知一起抹掉 | ⑨c |
| **⑦** aux 拼接不幂等 | 补去重（保持拼接语义） | ⑦ + ⑦b |
| **②③④⑭** overlay | 见 §6.2 | 4 条行为 + 3 条契约 |
| **⑫** 两个死帧 | `answer_question.accepted` → **删**（客户端无需回执）；`session.repointed` → **接上**（服务端注释写明意图「强制刷新」，但从未有人消费） | ⑫（自动推导） |
| **⑬** `pendingStreams` 无上限 | 加 `PENDING_STREAM_LIMIT=200` 并按它裁剪（挂载时整轮快照重建兜底） | ⑬（含「只定义常量不算修」的反向断言） |
| **⑮a** 附件 token 4 处 | 收敛到 `services/upload.ts` 的 `formatFileToken` + `fileTokenPath` | ⑮a（自动推导计数） |
| **⑮b** `/plan` 前缀 5 处 | 收敛到 `inputTransforms.ts`：`PLAN_COMMAND` + `isPlanCommand` / `stripPlanCommandPrefix` / `withPlanPrefix` 同源 | ⑮b（自动推导计数） |
| **①/②** 瞬时行两把尺 | 去掉 `isTransientExchange` 的 user 行例外（两条路径都**丢弃** seq=0 行）；再把 `mergeSessionExchanges` / `composeLoadedExchanges` / SessionViewer 三处 / App 一处全部改走共用谓词，新增同伴 `isPersistedSeq` | 【红】+ ② |
| **⑤** 残缺清除 | 两处「只清 2/5 处」（发送取消 / 发送失败）改走清除编排者 `clearLocalPendingForSession` | ⑤（改判据为「清除必须覆盖五处」） |
| **⑥** `pending_ack` 7 处 | 抽出 `settlePendingAcks` + `isSamePendingEcho`，两处内联的局部 `clearPendingAck` 与两处认领谓词全部改走它们 | ⑥（改判据为「两条判据各只有一处实现」） |

### 6.2 overlay 那一刀（②③④⑭ 一起收）

| 现状 | 改法 | 删掉 |
|---|---|---|
| A1(`visibleSeqSet`) + A2(`seq<=latestSeq`) 两条 | 合成一条：`visibleSeqSet.has(seq)` 才跳过 | A2 的「窗口一定含它」假设 → **修②** |
| B 分支查 `windowToolCallIds` 让位 | 删掉，交给 `dedupeToolCards`（窗口在前 ⇒ 保留持久那份） | `windowToolCallIds` → **修③** |
| D 分支「归一化包含 + ≥32 字 + 只看最新 3 行」 | 语料改为**窗口里本轮**（`seq > 最后一条 user 行`）的持久行 | 长度地板与行数上限 → **修④** |
| C 分支用户行按**原始内容**计数 | 归一化后比较 | → **修⑭** |

**一处反转（写测试时被 fixture 证伪）**：曾判定「内容比对整类该消失」，但 fixture ② 与 ⑦
**形状完全相同**（窗口有本轮持久行 + 缓存有一条瞬时行），区别只在「瞬时行是不是那条持久行的
拷贝」—— 只能靠内容区分。所以内容比对是必要的，删的是它的三个补丁。

### 6.3 我自己判定错误的四条（都在动手时被测试/代码证伪）

| 编号 | 我先前说 | 核实后 |
|---|---|---|
| ⑦ | 「aux 两套语义该合并成按 seq 覆盖」 | **错**。覆盖会让增量路径丢卡片（同一 seq 会陆续追加新项）。真缺陷只是拼接不幂等 |
| ④ | 「内容比对整类消失」 | **错**。见 §6.2 的反转 |
| ⑮c | 「`contextCache` 与 `buildClientContext` 是同一判定的两处实现」 | **错**。是**构建 vs 去重**两种操作。改判据为「去重必须按会话、判等键必须含 file+行号+文本」 |
| ⑮d | 「`context_window` 两处补同一字段，删一处」 | **错**。**数据源不同**（按 exchange 的真值 vs 会话级兜底），互补。改判据为「兜底不得覆盖真值」 |
| ⑤ | 「9 个机制该合并成 1 个」 | **收窄**。该合并的是**清除路径**（漏清 = 灯不灭），存储多份作为投影可以接受；实测确有两处残缺清除 |
| ② | 「谓词该消失（类别由数据结构表达）」 | **收窄**到「判定只能有一处实现，两条路径都走它」——两泳道是架构目标，见 §6.4 |

> 这批纠错的共同形态：**清单里的「重复」有相当一部分是「看起来像同一件事、其实数据源或时机不同」**。
> 只有把每条都追到调用方与数据流才能分辨 —— 这也是为什么每条改动都必须先写红测试。

### 6.4 仍未做的（明确记账，不假装完成）

| 项 | 为什么没做 |
|---|---|
| **两泳道**（把瞬时时序从行数组搬出来） | 牵动乐观回声 / ask 卡 / 队列续轮 / 子会话 / 双挂载五处边界。当前已用「单一谓词 + 单一组装规则」把两把尺消掉，bug 不会因判据分歧而回归；结构升级留作目标形态 |
| **冲突⑪**（两处挂载各持窗口态） | 抽屉没有 UI 入口，E2E 驱动不了；结构断言已钉住目标形态（窗口态不得住在组件内） |
| **导入判重那 6 个机制** | 只有「查库判据」工具能把导入层与渲染层分开，机制本身还没有红测试 |
| 冲突② 的 E2E | 搜索开关在可驱动 URL 下不渲染（实测两次），已由单元层确定性覆盖 |

## 5. 第一步结论

1. **用户的怀疑成立**：「多个机制在打架」不是感觉，是 **13 条有代码证据的冲突**，
   背后是 **41 处各自判定「瞬时 vs 持久」**、**9 处各自判定「是否在回复」**。
2. **打架的根源是类型缺失**，不是补丁多：`exchanges` 一个数组装两种本体；
   链路上 6 个可渲染落点、3 条并行真值。补丁多只是它的症状。
3. **测试难做也是同一个根源**：判定逻辑住在 React 组件与未导出的私有函数里。
   所以「抽成纯函数」既是统一（两泳道）也是让测试可行的前提 —— 同一件事。
4. **不能合并的确实有**，且都有明确独特性（§3.3）：`ReplyingList`（在途内容的唯一权威）、
   窗口 API（按 seq 寻址）、IDB（冷启动）、`pendingStreams`（事件早于订阅）、
   队列、导入判重、安全/寻址守卫。**这七类要原样保留**，合并只能发生在 §3.2 的 11 组里。
5. 第一步**漏了一项**（依赖第二步回补）：§2 只覆盖了「会话消息」，
   而第二步的历史表暴露出一条**前置链路** —— 外部转录**导入判重**（`seenUUIDs` /
   `live-owned` / 读取侧投影），它产生的重复行会以「同一段正文两遍」的形式
   混进本节的渲染问题里。回补项：**M-I1…M-I6（导入判重链路）**，见第二步文档。


---

## 7. Canonical Turn/Event ADR（G-T 线，未进 main —— 提案）

> **移植注**：以下 ADR 来自 G-T 分支，其代码未进 main。目标是**未来统一事件身份**的设计参考；
> 阶段 5 的目标在 main 上已由 G-BE（`TurnGen`）达成，表示不同。§7.4/§7.6 提到的
> `session-core-unit.test.mjs` canonical adapter 护栏、`canonicalTurn.ts` 等均**不在 main**。

### 7.1 决策

将 Canonical Turn/Event 定义为现有消息链路的**语义模型和迁移目标**，而不是在第一阶段直接改写 WS 协议、StreamHub 或持久化格式。

- 一个 Turn 从一次被接受或排队的 `session.message` 开始，经 `session.user_message`、当前回合的 `session.stream` 事件，到该回合的 `session.done` 收尾。
- Canonical Event 是这些消息在未来统一模型中的语义事件；当前阶段不新增 wire 字段，也不把 `EventCursor` 提升为公开身份。
- `Exchange/ExchangeAux`、`ReplyingList`、WS 帧和 React timeline 暂时继续作为各自 projection，现有行为先由测试守护。

### 7.2 目标模型（后续阶段）

Canonical Event 目标上应携带：

```text
event_id + turn_id + event_seq + generation + version + timestamp + payload
```

其中 `event_id` 负责幂等，`turn_id` 绑定一轮用户提交及其输出，`event_seq` 保证回合内顺序，`generation/version` 防止旧回合或旧快照覆盖新状态。`durable_seq` 未来表示它是否已经进入历史投影，但不能替代 `event_seq`、`agent_ctx_seq` 或 Exchange.Seq。

### 7.3 现有协议映射

| 当前机制 | 第一阶段语义 | 当前边界 |
|---|---|---|
| `session.message` / `session.accepted` | Turn 请求进入系统 | 客户端提交与服务端接收，不是持久事件日志 |
| `session.user_message` | Turn 的用户输入投影 | 用户 Exchange 已先落盘，广播携带真实 seq |
| `session.stream` | Turn 的实时事件投影 | `ReplyingList` 是当前进程内存缓冲，不是 durable log |
| `session.stream{reset:true,events:[...]}` | 当前 Turn 快照重建 | reset 是快照语义，续投帧不得复用 reset |
| `session.done` | Turn terminal 投影 | 当前只带 root/session，不含独立 turn 身份 |
| `Exchange/ExchangeAux` | 历史消息投影 | Exchange.Seq、aux seq 与内部 EventCursor 不是同一序列 |

### 7.4 第一阶段必须守住的不变量

1. 相同的 Turn 快照重复应用，结果必须相同。
2. replay 快照必须先于该 Turn 的 terminal 语义；不能依赖 `WriteJSON` 互斥锁猜顺序。
3. `reset` 只清理旧的实时投影，续投事件不能再次 reset。
4. 旧的客户端 transient 不能凭内容、数组位置或时间窗口被错误认领到当前 Turn。
5. `session.done` 只能由一个收尾编排路径决定；列表灯和计时器不得各自推断另一套运行状态。

第 1、4 条已在 `web/tests/session-core-unit.test.mjs` 登记为护栏测试（**G-T 分支**）；**in main 上第 5 条由 G-BE 的单一终结器 `EndSessionTurn` 保证。**

### 7.5 迁移边界

**本阶段允许：**

- 记录术语、现有协议映射和未来 projection 边界；
- 增加针对当前缺口的红测试；
- 将现有 replay、done、时间戳和窗口测试归入 Canonical Turn/Event 护栏。

**本阶段禁止：**

- 改 `server/internal/api/ws.go`、`stream_hub.go`、`agent/types` 的 **wire 或事件结构**
  （即 `StreamEvent` 字段、WS 帧 JSON、`EventCursor` 格式）；
- 新增或宣称已实现 wire 上的 `turn_id/event_id/version`；
- 把所有流式 chunk 直接写成 Exchange；
- 恢复客户端 event cursor 续传；
- 删除 `Exchange/ExchangeAux`、窗口分页、IndexedDB、importer、repoint cursor；
- 用新的 pending/轮询补丁代替状态模型。

### 7.6 后续迁移阶段

1. **Envelope**：在服务端事件产生处建立兼容的 canonical adapter，旧 WS/Exchange 继续作为 projection。
   （**main 注**：等价目标已由 G-BE 的 `TurnGen` 达成，不引入 adapter。）
2. **Turn reducer**：统一 live、import、recovery 的幂等写入口和 terminal 状态提交。
3. **Ordered transport**：每个 WS 连接单一 FIFO writer，统一 replay、live、terminal 顺序。
   （**main 注**：待办，见 `.omc/plans/gt-wip-reconcile.md` 阶段 4。）
4. **Frontend reducer**：WS、HTTP snapshot、window、sync、reset、done 全部进入单一 session reducer，React 只消费 projection。
   （**main 注**：其症状面 7a 已由 G-AY 达成。）
5. **删除补丁**：影子对账通过后，再收缩 `tailOverlay`、内容猜重、`seq=0` 混合数组和旧 importer projection。

### 7.7 ADR 后果与明确不做事项

该决策短期不会修复任何线上状态错序；它的价值是先建立一个不会继续漂移的边界和验收标准。Canonical 模型不等于把每个 token 持久化：实时事件日志与 `Exchange/ExchangeAux` durable projection 可以并存。第一阶段也不部署、不重启、不提交生产协议变更；真实断线时序仍需在隔离实例用 observability probe 验证。
