# 飞书通道与外部入口设计

**日期**：2026-11-21
**状态**：设计待确认（进程内、本机首发已确认；跨节点与大中控为 2026-11-21 新增方向）
**目标**：让 MindFS 的任务能「从飞书进来、在飞书里拍板、结果回到飞书」，且不需要公网地址。

## 1. 目标与非目标

### 目标

1. **出口**：HumanGate、Router `ask_user`、任务终态、定时任务结果，能以飞书卡片/消息推给 owner；卡片上的按钮或输入就是回答。
2. **入口**：owner 在飞书私聊机器人发一条消息，可以创建一条 MindFS 任务（进入现有 Router → 工作流/直连 → 证据门闭环）；**不要求用户写项目名**，由控制面解析或让用户点选。
3. **可核对**：卡片从创建、送达、回答到结算的每一步都落任务事件；重复点击、重复投递、进程重启都不产生第二条任务或第二次动作。
4. **无公网**：飞书回调走长连接（WebSocket），本机不需要暴露入站端口。
5. **控制端唯一网关**：只有控制节点持有 lark-cli 与凭据；worker 通过 HTTP 中继收发卡片，回答按 `node_id` 回到原任务。
6. **大中控**：入口先按显式绑定/前缀/唯一匹配解析项目；解析不出就发选择卡，不猜；不需要项目的消息直接回答。

### 非目标（本期不做）

- 群聊、多人协作、非 owner 回答。
- 图片、语音、文件消息入站；只做文本。
- 多节点**首发**只接本机（control 节点）；跨节点的中继接口在本设计里定好（§4.2），M5 接通。
- 完整规则引擎（source 匹配、cron 到任务的统一调度）另立计划；本期只做「入口绑定 + 项目选择」这一层。
- Agent 自发事件/建任务；因此本期不需要 hop 熔断。

## 2. 现状与证据

| 事实 | 证据 |
|---|---|
| 本机已有飞书应用 `cli_aa86ddd4f3755cc9`，bot 身份可发消息 | `lark-cli auth status`；bot 文本消息返回 `ok`，message_id `om_x100b7f3bc713b0a4b322f3bc7203c75` |
| bot 可发交互卡片 | 卡片发送返回 `ok`，message_id `om_x100b7f3bdef414a0cf53cda279a0232` |
| 卡片回调需要后台订阅 `card.action.trigger` | 未订阅时 `lark-cli event consume` 返回 `failed_precondition` + 一键订阅链接 |
| 订阅后长连接可建立 | `[source] feishu-websocket: connected`，`event status` 显示 bus running |
| MindFS 已有入站形状但端口未接线 | `server/internal/kanban/intake_policy.go` 文件头：TaskIntake 保持未实现、未注册 |
| 现有通知只有 Web Push / 安卓 | `server/internal/notify` + `http_webpush.go`；无 IM 通道 |
| worker 已经会「把通知交给控制节点代发」 | `webpush.Service.SetRelayURL` + `POST /api/web-push/relay`（`server/internal/webpush/relay.go`），控制节点持有订阅与密钥 |
| 任务模型按项目分区 | `CreateTask` 需要 `root_id`；`root_id` 决定任务库与项目目录，是数据面事实，不是用户输入 |
| 定时任务绕过 Router | `server/internal/scheduled/tasks_runner.go` 直接给会话发 prompt |
| 卡片点击已经真机打通 | 2026-11-21 长连接收到 3 次 `card.action.trigger`，`operator_id`/`action_value` 正确；当时只是没有回执，补 `im messages patch` 后卡片变为「已收到你的点击」 |

## 3. 方案选择

### A. 独立 sidecar 进程 + 本机 HTTP

sidecar 持有 lark-cli 与长连接，MindFS 与 sidecar 通过 `127.0.0.1` HTTP 交换卡片与回答。

- 优点：边界清楚，lark-cli 崩溃不拖垮 MindFS；可单独重启。
- 缺点：本机只有 system 单元、Agent 不能 `sudo`，新增单元必须由用户安装；多一套本地 HTTP 鉴权；部署链多一环。

### B. MindFS 进程内监管 lark-cli 子进程（推荐）

MindFS 在启用通道时 `exec` 两个 `lark-cli event consume` 子进程（卡片点击、入站消息），逐行读 JSON；发送用 `exec` `lark-cli im +messages-send` / `im messages patch`。卡片状态与回答都在 MindFS 内处理。

- 优点：随 `ship` 一起部署，不新增 systemd 单元、不需要 `sudo`；没有本地 HTTP 入站面；卡片与任务在同一事务域里，幂等和审计好写。
- 缺点：运行时依赖 lark-cli（已装）；子进程生命周期要自己管（重启、冲突、SIGTERM）；lark-cli 输出格式变化要靠容错解析。
- 本机现状适配：mindfs system 单元以 `xiaokubao` 运行，`HOME` 可用，`~/.lark-cli` 凭据直接可读；`PATH` 已含 nvm 的 bin。

### C. Go 原生飞书客户端 + WebSocket

自己实现 tenant_access_token、消息 API、长连接协议。

- 优点：无 Node 运行时依赖。
- 缺点：长连接协议与卡片回调要自己维护，工作量大、容易跟不上飞书版本；本期收益低。

**选择 B**（用户 2026-11-21 已确认「进程内 ok」「先本机 ok」）。把 `lark-cli` 藏在 `Channel` 接口后面；将来若要换 C，只换实现。

## 4. 架构

```
kanban.Service ──(Cards.Create/Answer)──> channels/feishu.Service
      ▲                                        │
      │                                 ┌──────┴──────┐
      │                                 │ card store  │  <config-dir>/channels/feishu/cards.json
      │                                 └──────┬──────┘
      │                                        │ exec argv（无 shell）
      │                                 lark-cli im +messages-send / messages patch
      │                                        │
      └──(Answer: gate / user message)── lark-cli event consume card.action.trigger
                                         lark-cli event consume im.message.receive_v1
```

- `server/internal/channels/feishu`：supervisor（子进程）、sender、card store、event parser、answer router。
- `kanban` 只依赖一个窄接口：
  ```go
  type CardNotifier interface {
      NotifyCard(ctx context.Context, card ChannelCard) error
  }
  ```
  以及一个回答入口 `ApplyChannelAnswer(ctx, ChannelAnswer) error`，由 AppContext 注入。通道失败只记日志，绝不阻断任务执行。
- 卡片创建点（三个）：
  1. HumanGate 进入 `waiting_user`；
  2. `applyRouteAskUser` 落 waiting_user 回复之后；
  3. 任务进入终态 / 定时任务结束（无按钮的通知卡）。
- 回答落点：
  1. HumanGate：调用现有 approve/reject 服务方法，附言作为 note；
  2. `ask_user`：把选项或自由文本作为一条用户留言写入该任务（`reply_to_id` 指向提问活动），走现有 Router 决策；
  3. 通知卡没有回答，点击只跳转。

### 4.1 控制端唯一网关

飞书应用只能有一个长连接监听（autoloop 实测：第二个监听报 `another event bus is already connected`），凭据也只在本机。因此：

- **只有控制节点**跑 `lark-cli` 与两个 `event consume` 子进程；worker 不装 Node、不复制 `~/.lark-cli`。
- 每张卡片记录 `node_id`：卡从哪台机器的任务来，回答就回哪台机器。
- 控制节点负责发卡、更新、收点击；它不替 worker 执行任务，只做通道与路由。
- 本机自己的任务不经过中继，直接在进程内调用。

### 4.2 跨节点中继（复用 webpush 先例）

worker 已经有「把通知交给控制节点代发」的成熟路径：`webpush.Service.SetRelayURL` + `POST /api/web-push/relay`。飞书卡片照同一形状做，不发明新协议：

```
worker kanban ──CardNotifier──> POST <control>/api/channels/feishu/relay   {node_id, card}
                                      │  (X-MindFS-Relay-Token)
                                      ▼
                              控制节点 card store ── lark-cli ──> 飞书
                                      ▲
                        用户点击 ──────┘
                                      │
                                      ▼
                     POST http://<worker>/api/channels/feishu/answer
                          {card_id, operator, choice, text}  (X-MindFS-Relay-Token)
                                      │
                                      ▼
                               worker 本地 ApplyChannelAnswer
```

- worker 侧配置 `feishu_relay_url`，与现有 `web_push_relay_url` 同一条启动路径、同一个 `config.json` 解析。
- 控制节点从 `nodes.json` 的节点 `URL` 找 worker；超时 5s，按 10s/30s/2m 重试三次。worker 不可达时卡片保持 `open`、任务保持 `waiting_user`，恢复后重试，不丢回答。
- **鉴权**：`channels.feishu.relay_token`（≥32 字符共享密钥）必须同时配在控制节点与 worker；空值 = 跨节点关闭（fail closed）。常量时间比较，不进日志。API 层对浏览器仍不鉴权（用户既有决策），这个 token 只守通道中继这条新面。
- **为什么设备间用 HTTP 而不是 ssh/CLI**：worker 没有源码库，但有完整二进制与 HTTP 数据面；`nodes.json`、`appPath`、webpush relay 已经证明这条路可用。2026-11-21 实测：控制节点访问四端 `health` 全部 200（`home`/`pc`/`fn` 的 `/mindfs/health`、`pc-win` 的 `/mindfs-win/health`），WSL 与 fn 反向访问 `https://home.xiaokubao.space/mindfs/health` 也都是 200。WSL 只监听 `127.0.0.1:7331` 不是问题：PC 宿主的 nginx 把 `pc.xiaokubao.space/mindfs` 反代到它，控制节点走注册 URL 即可。ssh 要密钥、要 shell 拼接、拿不到结构化状态码，且与「不在 worker 上编译/执行运维命令」的边界冲突。CLI 只作为**人的入口**（`mindfs task new --node fn --project docs ...`）调用控制面 API，不是新的传输层。
- 单机（本机任务）走进程内直调，不经过 relay，也不要求 token。

## 5. 数据与状态

### 5.1 卡片存储

`<config-dir>/channels/feishu/cards.json`，原子写（temp + rename），按账户一份（与 webpush 订阅表同一模式）。

```jsonc
{
  "version": 1,
  "cards": [{
    "id": "card_...",            // 本地唯一
    "node_id": "home",           // 任务在哪个节点；回答回哪台机器
    "root_id": "...", "task_id": "...",
    "kind": "gate|ask|notice|pick_project",
    "chat_id": "oc_...",         // pick_project 用：同一会话只允许一张未答的选择卡
    "gate_node_run_id": "...",   // gate 用
    "ask_activity_id": "...",    // ask 用
    "title": "...", "body": "...",
    "options": ["...", "..."],   // ask 用，最多 5 项
    "state": "draft|open|answered|expired|superseded|failed",
    "message_id": "om_...",      // 送达后回填
    "version_no": 1,             // 卡片内容版本
    "created_at": "...", "updated_at": "...", "expires_at": "...",
    "answered_by": "ou_...", "answer_choice": 0, "answer_text": "..."
  }]
}
```

不存任务正文全文以外的敏感信息；卡片值只带 `{card, choice}`。

### 5.2 状态机

```
draft ──send ok──> open ──answer──> answered
  │                  │
  │                  ├──ttl──> expired
  │                  └──new card for same task──> superseded
  └──send failed after retries──> failed
```

规则：
- 同一任务同时最多一张 `open` 卡；新卡创建时旧卡置 `superseded` 并尝试更新为「已由新问题取代」。
- 同一会话同时最多一张 `open` 的 `pick_project` 卡；用户再发一条无法解析的消息时，旧选择卡置 `superseded`，避免多张卡竞争。
- 回答只接受 `state=open`；`answered/expired/superseded` 的点击回「已处理」。
- 卡片过期后不自动替用户回答，任务保持 `waiting_user` 并在任务事件里记一条「飞书卡片已过期」；选择卡过期则不建任务，回一句「没选项目，这条没建任务」。

### 5.3 去重

- 入站消息：`(source='feishu', external_id=message_id)` 唯一。落各 root 的任务库新表 `task_intake_refs`，与建任务同事务。
- 项目选择卡：点选以 `(chat_id, message_id)` 幂等；同一会话新的选择卡让旧卡 `superseded`，旧卡点击回「已由新选择取代」。
- 卡片回答：`(card_id, operator_open_id, action_name)` 幂等；重复点击不改状态、不重复写留言。
- 跨节点中继：卡片按 `(node_id, card_id)` 幂等，回答按 `(card_id, operator_open_id, action_name)` 在 worker 本地幂等。
- 发送：`--idempotency-key mindfs-card-<card_id>`；重发已结算卡用 `-v<version_no>`。

## 6. 入口消息 → 任务（不要求用户写项目名）

### 6.1 为什么原来要项目名，现在不要

`root_id` 是任务库与项目目录的分区键，`CreateTask` 必须有它；但它是**数据面事实**，不该变成用户每次发消息都要填的表单。用户在飞书里只说「把 docs 的部署脚本修一下」，系统应该自己找到 `docs` 属于哪台机器、哪个 root。

所以入口分两层：

- **用户输入**：只有一条自然语言消息，没有必填字段。
- **控制面派发**：由一个确定性的「大中控」把消息解析成 `(node_id, root_id)`，解析不出来就问一次，不猜。

### 6.2 大中控的解析顺序

按顺序尝试，命中即停；每一步都可审计（任务事件里记 `dispatch_reason`）：

1. **显式绑定**：`channels.feishu.bindings` 里 `chat_id`（后续可加 topic）→ `{node_id, root_id}`。这是 autoloop `rules.json` 的对应物，最稳定，适合「这个群/这个私聊永远做某个项目」。
2. **消息前缀**：`#项目名` 或 `@项目名` 开头，按项目名/别名解析；同名项目跨节点时要求带节点名（`#fn/docs`）。
3. **唯一匹配**：消息里出现项目名/别名，且全节点只命中一个 `(node, root)` 时采用；命中 0 个或多个一律不进第 3 步的「猜」。项目清单来自控制节点定期拉取各节点的 `/api/tree`（60s 缓存），只读缓存、不实时扫盘。
4. **选择卡**：回复一张 `pick_project` 卡，按钮是候选项目（最多 5 个，按最近使用排序，显示「项目名 · 节点名」）；点选后创建任务。同一会话同时只有一张未答的选择卡，TTL 24h。
5. **直接回答**：消息明显不需要动项目（「现在几点」「昨天任务跑完没」），由控制面直接回答或查询，不建任务。

`default_root_id` 只作为**可选兜底**：只有用户显式配置时才用；未配置不再拒绝入站，而是走第 4 步。

### 6.3 建任务与回执

- 解析出 `(node_id, root_id)` 后：控制节点本地任务直接 `CreateTask`；worker 任务调用该 worker 的建任务 API（带 relay token）。
- 首条用户留言 = 消息正文；标题取正文首行截断到 200 runes；正文上限 32 KiB。
- 建任务成功回一张「已创建任务 #N」卡，带项目名、节点名、任务链接；失败回文本说明，不重试建卡。
- 只接受 owner 的私聊；非 owner 静默丢弃，避免被探测。
- 去重仍按 `(source='feishu', external_id=message_id)`；选择卡点选也以 `(chat_id, message_id)` 幂等，重复点击不会建第二条任务。

### 6.4 CLI 的位置

`mindfs task new --node fn --project docs --title "..."` 这类 CLI 是**人的另一个入口**，内部调用控制面同一套 API；它不参与设备间传输。设备间传输统一走 §4.2 的 HTTP 中继。

### 6.5 不在本期

- 多轮对话式补充项目信息（这一期只做一次选择卡）。
- 让 LLM 自由决定项目（规则优先；LLM 只在规则无命中时生成候选排序，且必须由用户点选确认）。
- 把消息并进已有任务（除卡片回答外）。

## 7. 参数

| 参数 | 值 | 依据 | 需测 |
|---|---|---|---|
| `enabled` | `false` | 缺省关，开启才 exec 子进程 | — |
| `owner_open_id` | 必填 | 只服务 owner | 真机 |
| `bindings` | `[]` | `chat_id → {node_id, root_id}` 显式绑定；0 条也能用（走选择卡） | 单测 + 真机 |
| `default_root_id` | 空 | **可选兜底**，不再必填；空则走项目选择卡 | 单测 |
| `project_picker_ttl` | 24h | 选择卡过夜仍可点；再长会积累未决卡 | 假时钟 |
| `project_picker_max` | 5 | 与 ask 按钮同一手机可用上限 | 真机 |
| `relay_token` | 空 | 跨节点共享密钥；空 = 跨节点关闭（fail closed） | 单测 + 真机 |
| `relay_timeout` | 5s | 与 webpush 中继同一量级；worker 不可达不阻塞任务 | 集成 |
| `relay_url` | 空（worker 配） | `feishu_relay_url`，指向控制节点 `/api/channels/feishu/relay` | 集成 |
| `title_max_runes` | 200 | 已有 `intakeTitleMaxRunes` | 单测 |
| `body_max_bytes` | 32768 | 已有 `intakeBodyMaxBytes` | 单测 |
| `external_id_max_runes` | 128 | 飞书 message_id 远小于此 | 单测 |
| `card_title_max_runes` | 40 | 卡片标题一行可读；只影响展示 | 真机看排版 |
| `card_body_max_runes` | 600 | 沿用 `notify.BodyMaxRunes` | 单测 |
| `ask_options_max` | 5 | 按钮过多在手机上不可用 | 真机 |
| `card_ttl` | 168h | 7 天未答视为过期；任务不自动推进 | 假时钟 |
| `send_retry` | 10s/30s/2m/5m 封顶 | 与现有通知重试节奏一致 | 集成 |
| `listener_reconnect` | 10s；冲突 60s | autoloop 实测值；一个应用只能一个监听 | 集成 + 真机冲突 |
| `inbound_rate` | 30/分，突发 10 | 先给保守值，压测后调 | 压测 |
| `answer_idempotency` | 首个生效 | 与 HumanGate 现有幂等语义一致 | 单测 + 真机双击 |
| `open_cards_per_task` | 1 | 避免同任务多张卡互相覆盖 | 单测 |
| `poll/none` | 无 | 进程内直接调用，无轮询 | — |

## 8. 安全

- 飞书 app secret 只在 `~/.lark-cli/config.json`（0600）；MindFS 不读、不复制、不落日志。
- `exec` 一律固定 argv，不经 shell；事件文本只作为 JSON 解析，不拼进命令行。
- 卡片里的任务标题/摘要用 `plain_text` 或转义后的 markdown；不把外部正文原样当 markdown 渲染。
- 只接受 owner 的点击与私聊；其他人静默丢弃。
- 卡片值只含 `card` 与 `choice`，不含任务正文、路径、token。
- 跨节点中继必须带 `relay_token`（常量时间比较）；控制节点只接受 `node_id` 在 `nodes.json` 里的卡片，回答只发回卡片记录里的 `node_id`，**绝不按请求体里的 URL 转发**（防 SSRF）。worker 只接受来自控制节点地址的回答，token 不匹配一律 401。
- 卡片点击先回执再执行：点击到达后立即把卡片改成「已收到」，再走 approve/留言；回执失败不阻断回答（回答仍按幂等键执行）。
- 通道端点不新增公网入站；长连接由本机主动连出。
- 审核分类（删除数据、停止服务、花钱、对外发消息、改产品方向、建/换密钥、改权限）继续由 Router 强制先问人；飞书卡片只是「问人」的通道，不改变分类表。

## 9. 失败与恢复

| 场景 | 行为 |
|---|---|
| lark-cli 不存在/版本过旧 | 启动时记一条错误日志，通道禁用；MindFS 正常工作 |
| 长连接断开 | 10s 后重连；连续失败指数退避，封顶 5m |
| 另一台机器占着同一应用 | 错误里明确提示；60s 重试；不反复刷屏 |
| MindFS 重启 | 向子进程发 SIGTERM；启动后 `open` 且无 `message_id` 的卡重发（同一幂等键），有 message_id 的不重发 |
| 发送成功但回填前崩溃 | 重启后用同一幂等键重发：飞书返回同一条消息，不会出现第二张 |
| 卡片更新 token 过期（30 分钟/2 次） | 直接发一张新版本的结算卡，幂等键带 `-v<version_no>` |
| 回答到达时任务已终态/已换轮 | 拒绝并回「该问题已结束」，卡片置 `superseded` |
| worker 不可达（发卡/回答案） | 控制节点按 10s/30s/2m 重试三次；失败时卡片保持 `open`、回答不丢，恢复后重试；任务保持 `waiting_user` |
| relay token 不匹配/未配置 | 拒绝中继（401），日志记一次；跨节点整体关闭，本机功能不受影响 |
| 同一张卡被 worker 重复中继 | 按 `card_id` 幂等，只发一条消息 |
| 选择卡过期 | 不建任务，回一句「没选项目，这条没建任务」；同会话可再发消息重新触发 |
| 项目解析命中多个 | 不发任务，发选择卡；不按「第一个」猜 |
| 点击回执补丁失败 | 回答照常执行（幂等）；日志记一次，卡片保持原样，下次状态变化再补 |
| 通道存储写失败 | 记错误，任务继续；不因卡片发不出去卡住执行 |

## 10. 测试计划

### 单元

- 卡片状态机：draft→open→answered/expired/superseded；只允许合法迁移。
- 回答幂等：同一 card + operator 重复点击只生效一次。
- 非 owner 点击：不生效、不写留言、记一次拒绝日志。
- 同任务新卡：旧卡 superseded。
- 过期：假时钟推进 168h，状态变 expired，任务仍 waiting_user。
- 入站字段：超长标题/正文、未知字段、非白名单来源一律拒绝。
- 去重：同 `message_id` 两次只建一条任务。
- 项目解析：显式绑定 > 前缀 > 唯一匹配 > 选择卡；0/多命中不得猜；同名跨节点必须带节点名。
- 参数边界：0/负数/空值归一化。

### 集成（不连飞书）

用一个 `fake-lark-cli` 脚本（记录 argv、按脚本输出事件行）替换真实二进制：

- 建卡 → 发送 argv 正确、幂等键正确、message_id 回填。
- 发送失败 → 重试序列正确；最终 failed。
- 收到点击事件 → 先回执 patch，再 gate 批准 / ask 生成用户留言。
- 重复点击 → 只一次。
- 进程重启 → 未送达卡重发、已送达卡不重发。
- 发送成功但回填前 kill → 重启后同一幂等键重发。
- 入站消息 → 解析项目 → 建任务 + 回执；重复事件 → 一条任务。
- 选择卡：点选建任务；同一会话新卡取代旧卡；过期不建任务。
- 跨节点：worker 提交卡片 → 控制节点只发一次；回答转发回原 worker；token 错/缺 → 401；worker 不可达 → 重试后保持 open。
- 监听冲突错误 → 60s 退避并再试。

### 真机（飞书）

- 发卡、点击、卡片更新（已答/已取代/已过期）；点击后 1 秒内卡片出现「已收到」。
- 双击：第二次回「已处理」。
- 非 owner 点击：无效果（需要另一个飞书账号）。
- 私聊机器人发一条不带项目名的消息：收到选择卡，点选后建任务并收到回执卡。
- 跨节点：本机发卡、fn 上执行、本机点批准、fn 任务继续。
- 两个监听同时跑：冲突可恢复。
- 断网/重启：长连接自动恢复，期间卡片不丢。

### 安全

- 卡片 markdown 注入：标题含 `[链接](...)`、尖括号、换行，按纯文本显示。
- 入站正文含指令性内容：作为用户留言处理，但受字段上限与审核分类约束，不进入系统提示词模板。
- 中继：无 token、错 token、未知 `node_id`、回答请求体里塞外部 URL 一律拒绝。
- 日志不含 app secret、token、完整卡片正文。

## 11. 分期

| 期 | 内容 | 验收 |
|---|---|---|
| M1 出口 | 卡片 store + sender + supervisor + HumanGate/ask/notice 三个创建点；点击先回执 | 假 CLI 集成全绿；真机发卡、点按钮、卡片出现「已收到」、任务状态改变 |
| M2 入口（本机） | `im.message.receive_v1` + 去重表 + 项目解析（绑定/前缀/唯一匹配/选择卡）+ 建任务 + 回执 | 真机私聊不带项目名也能建任务；重复投递只一条 |
| M3 韧性 | 重启/冲突/token 过期/过期卡/限流 | 故障注入用例全绿 |
| M4 大中控跨节点 | 控制端唯一网关 + worker 卡片中继 + 控制端派发到 fn/wsl/pc-win | 本机发卡、fn 执行、本机点批准、fn 任务继续；worker 掉线回答不丢 |
| M5 其他来源 | 通用 webhook 入口、定时任务并入任务模型、其他 IM | 另立计划 |

## 12. 配置

只走 `config.json`，缺省关闭：

```json
{
  "channels": {
    "feishu": {
      "enabled": false,
      "owner_open_id": "",
      "bindings": [
        {"chat_id": "oc_...", "node_id": "home", "root_id": "..."}
      ],
      "default_root_id": "",
      "project_picker_ttl_hours": 24,
      "project_picker_max": 5,
      "card_ttl_hours": 168,
      "inbound_per_minute": 30,
      "relay_token": ""
    }
  },
  "feishu_relay_url": ""
}
```

- 控制节点：`enabled=true`、`owner_open_id` 必填、`bindings` 可选；`relay_token` 与 worker 同值。
- worker：`enabled=false`（不跑 lark-cli）、`feishu_relay_url` 指向控制节点、`relay_token` 同值；空值等于关闭跨节点。
- `lark_cli_path` 可覆盖 `PATH` 里的 `lark-cli`（测试用 fake）；不新增环境变量。

## 13. 与 agent-autoloop 的对照

| 采用 | 不采用 |
|---|---|
| 长连接回调，无需公网 | 全量规则引擎与 `rules.json` 热加载（本期只做 `bindings` 这一层） |
| 每来源凭据、稳定外部 id 去重 | worker 池与 claim 协议（MindFS 节点是完整实例，不是 job runner） |
| 卡片生命周期、owner 白名单、首个回答生效 | 通用插件市场/热加载 |
| 发送幂等键、失败重试、监听单实例 | 全量事件溯源重写（现有 NodeRun + 投影 + `router_events` 足够） |
| 单点 hub 收事件、单点发卡（对应「控制端唯一网关」） | hop 熔断（仅在允许 Agent 自发事件后才需要） |
| 规则把来源映射到执行位置（对应 `bindings` + 项目选择卡） | 把 LLM 当无约束路由器（规则优先，LLM 只排序候选） |
| `later`/复查思路（本期不做，记在后续） | 直接在 worker 上跑 lark-cli / 复制凭据 |
