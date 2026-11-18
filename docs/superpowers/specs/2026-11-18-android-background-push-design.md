# 安卓后台回复提醒：常驻通知可点 + 近实时推送

日期：2026-11-18　状态：已批准（2026-11-18）

## 意图与为什么

安卓壳的后台提醒靠 `ReplyPollerService` 每 10s 逐节点串行 GET `/api/replying-sessions`。
实际「任务结束 → 手机响」常在 10–30 秒后；而通知栏那条常驻「有会话正在回复」点了没有反应。

用户要两件事：

1. **常驻通知可点**：点「会话回复中」进 App，能直达正在回复的那条会话就直达。
2. **后台（近）实时推送**：状态一变就提醒，而不是等下一个 10s 轮询。

## 现状（代码事实）

| 位置 | 事实 |
|---|---|
| `ReplyPollerService.buildIdleNotification()` | 生成 `FOREGROUND_ID=750001` 常驻通知，**没有 `setContentIntent`** —— 点击无响应 |
| `ReplyPollerService.postAlert()` | 每会话完成/提问通知已带 `nodeId/rootId/sessionKey`，走 `MainActivity.dispatchReplySessionIntent` → `mindfs:open-reply-session` → `openReplySession()` 直达 |
| 轮询 | `ACTIVE_POLL_INTERVAL_SECONDS = IDLE_POLL_INTERVAL_SECONDS = 10`；`pollOnceSafely` 逐节点**串行** GET（连接 5s / 读 8s） |
| 服务端状态 | `StreamHub.pendingSessions` 内存里已有准确的 replying/awaiting；`ListReplyingSessions()` 直接可读 |
| 跨节点 | 浏览器/壳直接打各节点 URL（`appPath(..., nodeId)`），不经主节点代理；`/api/replying-sessions` 属数据面，worker 可用 |
| 已有推送 | `notifySessionDone` 是 Web Push（VAPID），Android WebView 不支持 `PushManager`，壳用不上 |

## 目标 / 非目标

**目标**

- 常驻通知可点：1 个会话在回复 → 直达该会话；多个 → 只打开 App（不替用户选）。
- 后台提醒延迟 < 1s（状态变化），同时空闲期请求数不高于现在。
- 旧服务端 + 新 App、新服务端 + 旧 App 都能工作。

**非目标**

- 不引入 FCM / APNs / 第三方推送通道。
- 不改前台页面的实时链路（WS + 5s 可见轮询不变）。
- 不做通知富交互（回复输入、标记已读等）。
- 不改 E2EE（本仓服务端没有 E2EE 中间件；客户端路径沿用现有请求头）。

## 方案对比（已由用户选定 B）

- **A 只提速轮询**（活跃 2–3s、并发拉取）：延迟仍 2–3s，请求量成倍上涨。不满足「近实时」。
- **B 服务端长轮询（选定）**：新增 `GET /api/replying-sessions/wait`，状态一变立刻回包，空闲超时（默认 20s）。
  延迟 <1s，空闲请求数反而下降；复用现有鉴权、缓存协商与请求构造。
- **C SSE 流**：延迟最低，但要处理 nginx 缓冲、逐事件分帧与客户端解析，复杂度最高，收益相对 B 很小。

选 B 的代价：改 Go 服务端，需 `deploy-all.sh` 分发；本机重启由用户执行。

## 设计

### 1. 服务端：`GET /api/replying-sessions/wait`

```
GET /api/replying-sessions/wait?timeout=25
200 {"sessions":[...]}      // 与 /api/replying-sessions 同一载荷
304                         // 带 If-None-Match 且载荷未变
```

返回时机（三者取先）：

1. **形状变化立刻返回**：会话集合 / `status` / `awaiting_answer` 任一变化（开始回复、完成、提问题、答完）。
2. **每 ≥5s 一次摘要刷新**：形状不变但 `summary` / 标题 / `updatedAt` 变了 —— 只为了让「完成」通知带上最后一段正文，
   且把频繁的 chunk 更新压在 5s 一档，避免变成请求风暴。
3. **超时**：`timeout` 秒（默认 20，夹在 5–30）。此时按载荷算 ETag，客户端带 `If-None-Match` 就回 304。

**首帧不等待**：不带 `If-None-Match` 的请求立即回 200 全量快照（客户端用它播种），
只有带 `If-None-Match` 的请求才进入等待循环。否则「进程启动后 25s 内开始又结束的一轮」
会既没被看见 replying、也没被看见完成，完成提醒静默丢失。

实现要点：

- 循环粒度 200ms，每轮只调 `ListReplyingSessions()`（纯内存 map 遍历，无 SQLite / 无 root 解析）。
- 昂贵的载荷构造（root 名、会话标题，走 `GetRoot` + `GetMeta`）**只在返回时做一次**；
  与 `/api/replying-sessions` 共用同一个构造函数。
- 请求方断开（`r.Context().Done()`）立即返回，不写响应。
- 路由不进控制面前缀表（`/api/replying-sessions` 本就是数据面），worker 可用。
- **确定性顺序**：`ListReplyingSessions()` 按 `sessionKey` 升序返回。现在它遍历 map、顺序随机，
  两个会话以上时 ETag 每次都不一样（304 永远不命中），等待循环的签名比对也会抖。排序对现有
  消费者无影响（前端按 key 合并、原生自己按 updatedAt 重排）。
- **在途上限**：全局最多 32 条等待（API 层按用户决策不鉴权，`fn` 还监听 `0.0.0.0`），
  超出回 503 + `Retry-After`，客户端按错误退避。
- **关停不拖部署**：`server.Shutdown` 现在无期限等待 handler。给 `http.Server` 设可取消的
  `BaseContext`，收到退出信号时先 cancel 再 Shutdown，等待循环立刻返回。
- 计时参数做成可注入（包级变量或 handler 字段），测试用毫秒级。

### 2. Android 服务：轮询改推送

- **每节点一条等待循环**（线程池大小 = 节点数）：`waitUrl` → 回来后立刻再发下一次；
  这就是「长轮询」，形态上仍是 HTTP，无需新协议。启动时先用一次不带 `If-None-Match` 的
  请求播种该节点快照，再进等待循环。
- **快照按节点保存**：`nodeSnapshots: nodeId → (key → ReplyState)`；现有 `states` 改为由它派生的合并视图。
  DONE 判定保持「只对本轮成功返回的节点判定」：节点失败时不把它的会话判成完成。
- **新鲜度**：一个节点的快照只在最近 90s 内成功过才算「可信」。不可信节点的会话不进
  live 集合、不参与「恰好 1 个 → 直达」的判断 —— 否则节点掉线后常驻通知会永远显示
  「有会话正在回复」，点进去是一条几小时前的会话。
- **剪枝**：已完成且不在任何节点快照里、也不处于 awaiting 的条目在提醒发完后移出 `states`，
  不再无限累积。
- **串行化应用**：所有循环在 `applyLock` 下合并快照、跑 `ReplyAlertPlanner`、刷新常驻通知；网络请求绝不持锁。
- **降级**：`waitUrl` 返回 404/405（旧服务端）→ 该节点退回普通 `/api/replying-sessions`
  10s 轮询；连续 3 次等待失败（连接被代理掐断、超时）→ 同样退回轮询，10 分钟后重试等待。
  其他错误 5s 退避。不追 3xx 跳转。
- **超时取值**：`timeout` 默认 **20s**（不是 25s —— 线上每个节点都隔着反向代理，见风险），
  客户端读超时 = timeout + 10s。
- **304**：保留上一条快照，直接重新发等待请求。
- **诊断**：`recordPollDebug` 增加每节点模式（push/poll）与最近一次结果，关于弹窗里能一眼看出走的是哪条路。
- 配置变化（节点表变更）用 generation 计数让旧循环退出、新循环启动。

### 3. 前台服务类型：`dataSync` → `specialUse`

`targetSdk = 36`：Android 15+ 对 `dataSync` 类型的前台服务有「24 小时内 6 小时」的运行额度，
到点系统回调 `onTimeout()`、必须停服。这恰好会让「推送跑了一夜，早上静默失效」。
side-load 应用不受 Play 类型审查约束，改用专门为「以上都不适用」准备的 `specialUse`
（`FOREGROUND_SERVICE_SPECIAL_USE` + `PROPERTY_SPECIAL_USE_FGS_SUBTYPE`），取消额度限制；
API 34 以下仍走不带类型的 `startForeground`。这条是既有隐患，顺手一并修掉。

### 4. 常驻通知可点

- `buildIdleNotification` 增加 `setContentIntent`：
  - 恰好 1 个 live 会话（replying 或 awaiting）→ 带 `nodeId/rootId/sessionKey` → 复用 `mindfs:open-reply-session` 直达；
  - 0 个或多个 → 只启动 App（不带 extras，落在当前页）。
- 空闲态通知（"MindFS"）同样可点，只打开 App。
- PendingIntent requestCode 用 `FOREGROUND_ID`，`FLAG_UPDATE_CURRENT | FLAG_IMMUTABLE`。
- 通知文案不变（仍不点名，隐私考量不变）。

### 5. 前端 `replyPoller.ts`

- 每个节点同时下发 `url`（普通轮询）与 `waitUrl`（长轮询）；单节点兜底地址同样给两条。
- 现有测试锚点 `absoluteNodeURL("/api/replying-sessions", id)` 必须保留（`web/tests/apk-self-update.test.mjs`），
  `waitUrl` 另起一行算。
- 其余契约（viewing、dismiss、takePendingSession）不变。

## 既有行为，明确不改

- **前台继续跑**：`onResume` 启动服务、`onStop` 清 viewing，不改。前台时原生提醒现在会跑在
  页面内 5s 轮询前面 —— 这正是要的「近实时」；「正在看的那条」仍由 viewing 抑制。
- `showReplyNotifications` / `showSingleReplyNotification` / `buildReplyGroupSummaryNotification` /
  `notificationMode` / `ACTION_CLEAR_COMPLETED` 是死代码，本次不动（真正活的只有
  `updateForegroundNotification` 与 `postAlert`）；单独清理另开一次改动。

## 兼容与上线顺序

1. 服务端先上（`deploy-all.sh`）：旧 App 完全不受影响（新路由多出来而已）。
2. App 后上（`apk-release`）：新 App 打旧服务端自动退回 10s 轮询。
3. 本机 system 单元重启由用户执行；worker 节点由脚本重启。

## 测试

**Go**（`http_sessions_list_wait_test.go`，用毫秒级计时注入）：

- 不带 `If-None-Match` 的首次请求 → 立即返回当前快照（播种），不等待。
- 形状变化（新增 replying / 置 awaiting / 完成消失）→ 立即返回，载荷正确。
- 无变化 → 超时返回；带 `If-None-Match` 匹配 → 304 且无正文。
- 仅 summary 变化 → 不早于刷新间隔返回，到点返回新 summary。
- 多会话时载荷顺序稳定（同状态两次请求字节一致 → ETag 一致）。
- 请求取消 → handler 立即退出。
- `timeout` 越界被夹到 5–30；在途超过 32 条 → 503。

**前端**（vitest）：`syncNativeReplyPoller` 下发的 payload 含每节点 `waitUrl` 且与 `url` 同源同账户参数。

**Android**：把「按节点合并 + DONE 判定 + 新鲜度」抽成纯 Java 类（仿 `ReplyAlertPlanner`），补 JVM 单测；
新增 `make test-android`（`./gradlew :app:testDebugUnitTest`），只做手动/定向门禁，不进 ship 快路径。
通知与线程部分靠 `assembleDebug` 编译 + 隔离实例联调（`scripts/mindfs-iso.sh`，端口 7431）验证；
线上再实测一次 20s 等待穿过各节点反向代理能否完整返回（这是代理超时的唯一真凭据）。

## 风险

- **代理超时**：每个节点都隔着反向代理，代理的 `proxy_read_timeout` 不在本仓库、本机也看不到 `/etc/nginx`。
  取值保守到 20s，并做「连续 3 次等待失败即退回轮询 + 10 分钟后重试」；上线验证时用真实等待计时确认。
- **匿名等待可被滥用**：API 层按用户决策不鉴权，`fn` 还监听 `0.0.0.0`。用全局 32 条在途上限兜底，
  超出 503，不做鉴权（不改变既有决策）。
- **耗电/流量**：空闲期由「每 10s 一次请求」变成「每 20s 一次」，更省；有会话在流式输出时
  每 5s 一次刷新请求（只为拿到提醒正文）。
- **系统省电**：部分厂商在深度待机下会挂起前台服务的网络；这是既有上限，普通轮询同样受影响。
- **并发线程**：4 节点 = 4 条常阻塞线程，均为前台服务进程内，数量固定、不随请求增长。
- **重启代价**：服务端关停原本无期限等待 handler，本设计加了可取消的 `BaseContext` 才不至于每次部署停 20s。

## 交给实施者决定

- 200ms / 5s / 20s / 90s 四个常量的最终取值（先按此，实测后再调）。
- 每节点循环用固定线程池还是逐节点 `Thread`（倾向固定线程池，便于统一关闭）。
- 抽出的合并类边界：`ReplyStateMerger` 输入输出用哪几个纯数据类型。