# Web Push 只在页面还开着时投递（关闭页面后不再弹桌面通知）

> Scope IDs: G-RA（claimed 2026-11-24T10:40Z 为 G-QU；合回 main 时撞上 6f34fd4c 已登记的 G-QU，按「先引入者保留、合入方改号」改为 G-RA）

- 日期：2026-11-24
- Scope: G-RA（原 claim 号 G-QU 撞号后避让，见文首说明）
- 用户诉求：PWA 在页面/界面关闭后仍持续弹桌面通知；多台设备关掉页面后通知还在往外蹦，实际没有必要。
  要求定位根因（SW / 推送订阅 / 后台定时通知），关闭页面后停止投递，并重跑复现路径验证。

## 1. 取证（只读，全部来自现场）

### 1.1 订阅表：两条长期存活的 Windows 订阅

`<config-dir>/web-push-subscriptions.json` 里两条 `wns2-*.notify.windows.com` 订阅，`created_at`
2026-11-19 / 11-20，`enabled=true`，此后从未被删除。订阅表没有任何「页面是否还开着」的字段，
也没有过期语义 —— 一次授权 + 一次 `pushManager.subscribe()` 就是永久投递地址。

### 1.2 服务端扇出：所有 enabled 订阅无条件全发

`server/internal/webpush/send.go::sendToItems` 的循环只有两个判断：`item.Enabled` 为假跳过、
410/404 删除。`send()` 直接 `store.List()` 全量。`shouldSend(eventID)` 只做 30 分钟同事件去重，
与「谁的页面还开着」无关。→ **关掉页面这个事实在服务端完全不可见。**

### 1.3 触发源：每一轮会话结束 / 提问 / 定时任务

`notifySessionDone` / `notifyAskUserIfNeeded` / `notifyScheduled` → `NotifyPayload` → `sendAsync`。
worker 节点经 `/api/web-push/relay` 把载荷转给主节点代发，主节点用自己的 VAPID 与订阅表扇出 ——
所以「WSL / fn 上跑完的会话」也会打到桌面浏览器的 WNS 订阅上。用户在桌面关掉页面后，
后台 agent 仍在跑，每跑完一轮就是一条通知，来源与页面生命周期无关。

### 1.4 已排除的三个猜测

| 猜测 | 证据 |
|---|---|
| 页面内的定时器在发通知 | 全仓没有 `new Notification(` / `showNotification(` 的页面侧调用；页面侧只有 WebAudio 提示音（`useNotificationSound`），不产生桌面通知 |
| 事件被重复投递 | 每个事件有唯一 eventID + 30 分钟去重；现场通知内容是不同回合的完成提醒，不是同一条重放 |
| SW 里用 `clients.matchAll()` 静默忽略即可 | `userVisibleOnly: true` 的订阅下，push 处理器不显示通知时 Chrome 会自己弹「此网站已在后台更新」；抑制只能搬到服务端 |

## 2. 根因

**Web Push 是「与页面生命周期无关」的通道，而 MindFS 只利用了这一点，从未补上「页面已关闭就别投递」
的判定。** 订阅是永久的、扇出是全量的、事件源是后台 agent —— 三者相乘就是「关掉页面通知照弹」。

## 3. 方案：订阅级在场（presence）+ 关闭页面立即注销

### 3.1 数据模型

- `Subscription` 新增 `PresenceCapable bool`（`presence_capable` JSON）：客户端在本版首次上报订阅时
  带 `presence: true`，Upsert 里**粘性置真**（后续请求不带也不会回落），升级前落库的旧订阅保持 false。
- 新增 `server/internal/webpush/presence.go`：进程内 presence 表，key = endpoint，
  value = 该订阅下活跃 `client_id` 集合 + 每客户端最后心跳时刻。
  - `Touch(endpoint, clientID, now)`：刷新心跳；客户端数超过上限时淘汰最旧的。
  - `Leave(endpoint, clientID)`：页面关闭时立刻摘掉（`pagehide` beacon）。
  - `Live(endpoint, now) bool`：任一客户端心跳在 TTL 内即为真。
  - 定期清理过期条目（复用 `send` 时的 now 扫描，不额外起 goroutine）。

### 3.2 扇出过滤

`sendToItems` 增加一道闸门：`item.PresenceCapable && !presence.Live(...)` → 跳过（不计 error、不删订阅）。
`SendTestToEndpoint`（用户在通知面板点「发送测试」）显式豁免 —— 那是用户当场发起的动作，与页面
是否还开着无关。全部被过滤时 `send` 返回 nil（这不是失败，是预期内的静默）。

旧订阅（`PresenceCapable=false`）保持原行为：不是所有客户端都会在升级瞬间上报，这里 fail-open，
宁可多弹一条也不让升级用户彻底收不到提醒。等客户端上报一次 `presence: true` 后自动切换到新语义。

### 3.3 客户端

- 订阅上报（`saveSubscription`）带 `presence: true` + 当前 tab 的 `client_id`（sessionStorage 级，
  同 tab 刷新不变、关 tab 即失效）。
- 心跳：页面可见时每 30s `POST /api/web-push/presence`；`visibilitychange` 回前台立刻补一次。
- `pagehide`：`navigator.sendBeacon` 发 `{endpoint, client_id, leave: true}`（`text/plain` Blob，
  避免 preflight —— beacon 不等待响应，带 JSON content-type 会触发 CORS 预检而丢包）。
- `pageshow`（含 bfcache 恢复）：重新心跳，把刚才注销掉的自己补回来。
- SW 侧：push 事件里 `clients.matchAll({type:"window"})`，若一个窗口都没有，**仍然照常
  showNotification**，同时上报 `POST /api/web-push/presence {leave:true}` —— 这是进程崩溃 /
  强杀后服务端仍以为页面活着的自愈手段。第一次推送可能仍会弹（页面确实刚死，服务端还没收到
  beacon），从第二次起不再弹；这比「为了静默而抑制通知 → Chrome 弹兜底提示」可控。

### 3.4 TTL 与节流

- 心跳 30s；TTL 10 分钟。隐藏标签页被浏览器节流到约 1 次/分钟甚至被 Memory Saver 冻结，
  TTL 太短会把「页面还开着、只是后台」的合法通知一起掐掉。
- 正常关页由 `pagehide` beacon 立刻摘除，不需要等 TTL；TTL 只兜崩溃场景。

### 3.5 多节点一致性

`/api/web-push/*` 在 `controlProxyLocalPrefixes` 里（设备本地，不转发给主节点）；订阅表、VAPID
与 `controlPath` 都落在「页面所在的那台服务器」上，presence 表跟着订阅走即可。worker 来源的通知
经 relay 到主节点，主节点用自己的 presence 判定 —— 订阅在哪台，判定就在哪台，语义一致。

## 4. 验证计划

1. **RED**：`presence_test.go`（TTL / Leave / 多客户端 / 上限淘汰）；`send_presence_test.go` 用
   `httptest` 假 push 端点（真实 VAPID + ECDH P-256 订阅密钥、注入 HTTPClient）证明：
   - capable 订阅无心跳 → 0 次投递；
   - 心跳后 → 投递 1 次；
   - `Leave` 后 → 再次为 0；
   - 旧订阅（capable=false）无心跳照常投递；
   - 测试端点豁免。
2. **API RED**：`/api/web-push/presence` handler（心跳 / leave / 400）；订阅上报带 `presence: true`
   落库后 `PresenceCapable` 为真且粘性。
3. **前端契约**：`web/tests/web-push-presence.test.mjs` —— 心跳间隔、pagehide beacon 用 text/plain
   Blob + `sendBeacon`、pageshow 恢复、订阅上报带 presence/client_id、SW push handler 上报离线。
4. **变异**：去掉过滤、TTL 判错方向、leave 不摘除、beacon content-type 改 application/json、
   旧订阅不豁免 —— 各自打红后还原。
5. **复现路径**：用隔离实例起真服务 + 本地假 push 端点，按「订阅 → 无心跳 → 有心跳 → pagehide」
   四步观察投递次数，与修复前的「全量投递」对照。

## 5. 不做

- 不做「按会话/按项目」细粒度订阅。
- 不删存量订阅、不改 VAPID。
- 不在 SW 里抑制通知（Chrome 兜底提示会让体验更糟，见 1.4）。

## 6. 执行记录（2026-11-24）

### 6.1 实现

- **服务端在场表** `server/internal/webpush/presence.go`：`endpoint → client_id → 最后心跳`，
  `now` 可注入（TTL 测试）、TTL 10 分钟；每 endpoint 最多 16 个客户端、全表最多 512 个 endpoint，
  超限淘汰最旧（换个开法会不断产生新 client_id，账本不能无限涨）。`Leave` 带空 client_id
  表示整条订阅下已无页面（beacon 拿不到 id / SW 自愈），直接清整条。所有方法 nil-safe。
- **扇出闸门** `send.go::sendToItems(..., requirePresence bool)`：`PresenceCapable && !Live`
  跳过（不计失败、不删订阅）；全部被跳过时返回 nil（预期静默，不是错误）；
  `SendTestToEndpoint` 显式 `requirePresence=false`；`webpushgo.Options.HTTPClient` 供测试注入。
- **订阅能力粘性** `store.go`：`PresenceCapable`（`presence_capable,omitempty`），Upsert 已有行时
  `existing || in`。旧订阅保持 false（fail-open）。
- **HTTP 面**：`POST /api/web-push/subscriptions` 接受 `presence` / `client_id`，保存成功即记一次在场；
  新增 `POST /api/web-push/presence`（`{endpoint, client_id, leave}`，4 KiB 上限，返回
  `{ok, active}`；未启用推送的节点回 200 `ok:false` 而不是让 beacon 报错）。
  `DeleteSubscription` 顺带清在场。
- **客户端** `web/src/services/webPushPresence.ts`：30s 心跳（开着就发，含后台标签页 —— 浏览器把
  定时器节流到约 1 次/分钟，仍在 TTL 内）；`visibilitychange→visible` 与 `pageshow(persisted)`
  立即补心跳；`pagehide` 用 `sendBeacon` + `text/plain` Blob 上报 leave；client_id 存 sessionStorage
  （关一个标签页不影响另一个）。`webPush.ts` 订阅上报带 `presence:true` + client_id；手动订阅与
  每次启动的幂等回传之后都 `startWebPushPresence(endpoint)`（存量订阅靠这条升级）；退订先停心跳。
- **SW 自愈** `web/vite.config.ts`：push 时 `clients.matchAll({type:"window",includeUncontrolled:true})`，
  零窗口则向 `new URL("api/web-push/presence", self.registration.scope)` 补报 leave（跟部署前缀走），
  **照常 showNotification**（userVisibleOnly 订阅不显示通知会被 Chrome 换成兜底提示）。

### 6.2 测试与变异

RED→GREEN：`presence_test.go` 5 条（TTL / 只摘本客户端 / 无 id 清整条 / 客户端上限 / TTL 清理）、
`send_presence_test.go` 6 条（真实 VAPID + P-256 ECDH 密钥 + httptest 假端点：无心跳 0 投递、
心跳后投递、leave 后停、旧订阅照发、测试端点豁免、TTL 过期恢复静默）、
`http_webpush_presence_test.go` 4 条（心跳/leave 往返与 `active` 回包、缺 endpoint 400、
保存时能力粘性 + 当场在场、旧客户端 fail-open）、前端源码契约 4 条。

变异 10 处，各自先红后还原：去掉扇出过滤 / TTL 判反 / 无 client_id 不清整条 / 能力不粘 /
测试端点也受约束 / 旧客户端不豁免 / 订阅上报不记在场 / beacon 改 application/json /
SW 自愈删除 / 退订不停心跳。

### 6.3 复现路径（隔离实例，真进程 + 真 HTTP API + 假 push 端点）

隔离实例 `127.0.0.1:7481`（独立 `XDG_CONFIG_HOME`、`-web-push` 默认开），假 push 端点
`127.0.0.1:7482` 计真实 Web Push 请求数，假浏览器用 `crypto/ecdh` 生成真实 P-256 订阅密钥，
触发走真实后台事件路径 `POST /api/web-push/relay`（→ `NotifyPayload` → 扇出）：

| 步骤 | 场景 | 结果 |
|---|---|---|
| S0 | 旧订阅（无 presence 声明）+ 后台事件 | 投递 1 次 —— 这就是用户现场的路径（页面关没关都发） |
| S1 | presence 订阅 + 页面已关 | **0 次**；同轮旧订阅仍收到（只影响新客户端，fail-open 生效） |
| S2 | 页面心跳后 | 投递 1 次 |
| S3 | leave 后 | 不再新增（立即静默，不等 TTL） |
| S4 | 「发送测试」 | 不受在场约束，正常投递 |
| S5 | 两个标签页，关一个留一个 | 继续投递（不连坐） |

7/7 PASS。收尾：隔离进程与端口已停，生产 `127.0.0.1:7331/mindfs/health` 200，全程未碰生产。

### 6.4 未做 / 边界

- ~~未在真机浏览器上点验~~ —— 2026-11-27 补做，见 §6.5。结论：用户报「升级后仍弹」的路径
  真实存在，根因是旧订阅记录的 fail-open，不是客户端没跑新代码。
- 心跳只在页面内存活；浏览器彻底冻结标签页（Memory Saver 丢弃）超过 TTL 后视为已关闭 —— 与
  「页面没了」语义一致。
- presence 表是进程内状态：重启后归零，第一个心跳回来前新客户端订阅处于静默；旧订阅不受影响。

### 6.5 真机复现与验证（2026-11-27）

现场：`ssh pc` 的 Windows 11 设备（192.168.1.20），Edge 安装型 PWA（AUMID
`Microsoft.MicrosoftEdge.Stable_8wekyb3d8bbwe!https://home.xiaokubao.space/mindfs/`）；服务端是本机
`127.0.0.1:7331`（nginx 容器反代，mindfs 侧 `remote` 恒为 127.0.0.1，真实客户端只在 nginx 日志）。
取证全部只读：设备通知库与 Edge 会话文件的临时拷贝、eth0 passive 抓包、nginx 访问日志、`/proc`
与只读 presence 探针；未触发测试推送，未改任何生产状态。

1. 客户端新代码确实在跑：11-25 04:24Z 起持续 30s/60s 心跳；pagehide beacon 正常送达
   （27 字节响应 = `{"ok":true,"active":false}`）；service worker 零窗口自愈 leave 共 34 次。
2. 设备通知库（`%LOCALAPPDATA%\Microsoft\Windows\Notifications\wpndatabase.db`）中 103 条 MindFS
   通知，全部在最后一次心跳后 ≤ 1.17 分钟内到达 —— TTL 语义与设备行为一致。
3. **但 11-25 08:31–10:42Z 有 34 次「零窗口仍收到推送」**：每次 relay 后约 2 秒 SW 才补 leave；
   最后一次真实心跳 08:30:25Z、pagehide leave 08:45:37Z，10 分钟 TTL 解释不了 09:48–10:42 的投递。
   当时运行版本 `v0.5.5-1600`（本地 11-25 12:22 起）已含本修复
   （`git merge-base --is-ancestor 1d69f0c4 v0.5.5-1600` 成立）。
4. 与代码一致的唯一解释：接收推送的那条订阅当时 `presence_capable=false`（改动前保存的旧记录，
   fail-open）。该标志只在「页面用新版 bundle 重新保存订阅且带 `presence:true`」时翻转；设备上这条
   直到本地 02:39:48（11-26T18:39:48Z）页面重新加载保存才翻转。在此之前 SW 的自愈 leave 对
   fail-open 记录无效 —— tracker 记了 leave，扇出判断根本不看这类订阅。
5. 修复生效后的生产验证：02:39:51 本地 pagehide leave 返回 `active:false`；两个订阅现均为
   `presence_capable:true` 且 tracker 无在场（只读探针复核均 `active:false`）。11-26T21:23:35Z 一次
   真实 relay（来源 192.168.1.20）期间，eth0 抓包 0 次 `notify.windows.com` 解析、0 个 WNS SYN、
   mindfs 进程 0 个 443 出站；仪器有效性由 21:18:56Z / 21:19:09Z 两次手工 TLS 探测在同一条抓包中
   验证（均可见 DNS + SYN）。
6. 「关页后设备不再弹」单独不作为证据：Edge 于本地 02:48:58 退出（profile 最后写入时间），早于
   02:53 的第一次关页后 relay。决定性证据是第 5 条的服务端侧观测。

边界与后续（均未实施）：
- 升级不会补齐 `presence` 声明：旧订阅要等对应浏览器下一次加载新版页面并保存订阅才止血；长期
  不打开页面的设备会一直是 fail-open，继续「关页也弹」。
- 建议加固：收到任意该 endpoint 的 presence 上报（心跳或 SW 自愈 leave）即把订阅标记
  `presence_capable=true` —— 上报本身证明客户端已是新版本，不必等下一次保存；可选再对长期
  未更新且从未上报 presence 的记录禁用 / 清理。需另立 Scope 组号并单独授权部署。
- 语义边界不变：最小化 / 被遮挡的 PWA 窗口仍算「开着」（60s 心跳），通知照发；要求「不在前台
  就不发」属于产品语义变更。

#### 6.5.1 设备可见验收（2026-11-27 08:53 本地）

条件：用临时计划任务在会话 1 打开一个普通 Edge 窗口（18 个 msedge 进程存活），MindFS PWA 窗口
保持关闭 —— 服务端 00:45Z 起无任何 presence 上报，两条订阅的只读探针均为 `active:false`。从 wsl
用 worker 配置里的 relay URL（`https://home.xiaokubao.space/mindfs/api/web-push/relay`）发一次带
唯一 `event_id` 的真实 relay（00:53:13Z 发出，nginx 于 00:53:14.403Z 记录 200）。

结果（触发后 30 s 窗口，00:52:55–00:54:02Z 抓包）：
- 0 次 `notify.windows.com` 解析、0 个发往 `172.188.234.51:443` 的包；mindfs 进程远端连接仍只有
  触发前就在的 Cloudflare / 路由器两条长连接，无新增 443 出站；journal 无 `[webpush]` 错误。
- 设备通知库仍为 103 条 MindFS 通知，最后一条 `2026-11-26T18:31:48Z`，触发后新增 0 条。
- 仪器控制：同一条抓包链路在 00:56:30Z 的手工 WNS TLS 探测中捕获 DNS + SYN。
- 清理：临时计划任务已删除并复核不存在，临时脚本删除；`%TEMP%\wpn-accept` 与 `/tmp/wpn*`
  取证拷贝按要求保留。

说明：relay 是从 worker 主机按 worker 配置地址发出的同形请求，不是由真实 agent 回合触发的；
「发送测试」端点会绕过 presence 护栏，本验收刻意未用。

