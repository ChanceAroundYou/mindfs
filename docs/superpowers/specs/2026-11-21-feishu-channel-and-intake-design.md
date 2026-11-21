# 飞书通道与外部入口设计

**日期**：2026-11-21
**状态**：设计待确认（未写产品代码）
**目标**：让 MindFS 的任务能「从飞书进来、在飞书里拍板、结果回到飞书」，且不需要公网地址。

## 1. 目标与非目标

### 目标

1. **出口**：HumanGate、Router `ask_user`、任务终态、定时任务结果，能以飞书卡片/消息推给 owner；卡片上的按钮或输入就是回答。
2. **入口**：owner 在飞书私聊机器人发一条消息，可以创建一条 MindFS 任务（进入现有 Router → 工作流/直连 → 证据门闭环）。
3. **可核对**：卡片从创建、送达、回答到结算的每一步都落任务事件；重复点击、重复投递、进程重启都不产生第二条任务或第二次动作。
4. **无公网**：飞书回调走长连接（WebSocket），本机不需要暴露入站端口。

### 非目标（本期不做）

- 群聊、多人协作、非 owner 回答。
- 图片、语音、文件消息入站；只做文本。
- 多节点：先只接本机（control 节点），WSL/fn/pc-win 后续按同一接口扩。
- 完整规则引擎（source 匹配、cron 到任务的统一调度）另立计划。
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
| 定时任务绕过 Router | `server/internal/scheduled/tasks_runner.go` 直接给会话发 prompt |

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

**选择 B**。把 `lark-cli` 藏在 `Channel` 接口后面；将来若要换 C，只换实现。

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

## 5. 数据与状态

### 5.1 卡片存储

`<config-dir>/channels/feishu/cards.json`，原子写（temp + rename），按账户一份（与 webpush 订阅表同一模式）。

```jsonc
{
  "version": 1,
  "cards": [{
    "id": "card_...",            // 本地唯一
    "root_id": "...", "task_id": "...",
    "kind": "gate|ask|notice",
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
- 回答只接受 `state=open`；`answered/expired/superseded` 的点击回「已处理」。
- 卡片过期后不自动替用户回答，任务保持 `waiting_user` 并在任务事件里记一条「飞书卡片已过期」。

### 5.3 去重

- 入站消息：`(source='feishu', external_id=message_id)` 唯一。落各 root 的任务库新表 `task_intake_refs`，与建任务同事务。
- 卡片回答：`(card_id, operator_open_id, action_name)` 幂等；重复点击不改状态、不重复写留言。
- 发送：`--idempotency-key mindfs-card-<card_id>`；重发已结算卡用 `-v<version_no>`。

## 6. 入口消息 → 任务

- 只接受 owner（`owner_open_id`）或 `answerers` 白名单；其他人记日志并忽略（不回复，避免被探测）。
- 只接受私聊（`chat_id` 与配置一致）；群聊后续再做。
- 文本上限 32 KiB、标题 200 runes、`external_id` 128；未知字段拒绝（复用 `intake_policy.go`）。
- 默认项目：`channels.feishu.default_root_id`；配置为空则回复「未配置默认项目」。
- 消息 → `CreateTask`（首条用户留言 = 消息正文）→ 回复一张「已创建任务 #N」卡，带 MindFS 任务链接。
- 建任务失败：回复文本说明错误，不重试（避免重复建卡）。
- 不在本期：把消息并进已有任务（除卡片回答外）。

## 7. 参数

| 参数 | 值 | 依据 | 需测 |
|---|---|---|---|
| `enabled` | `false` | 缺省关，开启才 exec 子进程 | — |
| `owner_open_id` | 必填 | 只服务 owner | 真机 |
| `default_root_id` | 空 | 空则拒绝入站建任务 | 单测 |
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
- 参数边界：0/负数/空值归一化。

### 集成（不连飞书）

用一个 `fake-lark-cli` 脚本（记录 argv、按脚本输出事件行）替换真实二进制：

- 建卡 → 发送 argv 正确、幂等键正确、message_id 回填。
- 发送失败 → 重试序列正确；最终 failed。
- 收到点击事件 → gate 被批准 / ask 生成用户留言。
- 重复点击 → 只一次。
- 进程重启 → 未送达卡重发、已送达卡不重发。
- 发送成功但回填前 kill → 重启后同一幂等键重发。
- 入站消息 → 建任务 + 回执；重复事件 → 一条任务。
- 监听冲突错误 → 60s 退避并再试。

### 真机（飞书）

- 发卡、点击、卡片更新（已答/已取代/已过期）。
- 双击：第二次回「已处理」。
- 非 owner 点击：无效果（需要另一个飞书账号）。
- 私聊机器人发一条消息：建任务并收到回执卡。
- 两个监听同时跑：冲突可恢复。
- 断网/重启：长连接自动恢复，期间卡片不丢。

### 安全

- 卡片 markdown 注入：标题含 `[链接](...)`、尖括号、换行，按纯文本显示。
- 入站正文含指令性内容：作为用户留言处理，但受字段上限与审核分类约束，不进入系统提示词模板。
- 日志不含 app secret、token、完整卡片正文。

## 11. 分期

| 期 | 内容 | 验收 |
|---|---|---|
| M1 出口 | 卡片 store + sender + supervisor + HumanGate/ask/notice 三个创建点 | 假 CLI 集成全绿；真机发卡、点按钮、任务状态改变 |
| M2 入口 | `im.message.receive_v1` + 去重表 + 建任务 + 回执 | 真机私聊建任务；重复投递只一条 |
| M3 韧性 | 重启/冲突/token 过期/过期卡/限流 | 故障注入用例全绿 |
| M4 其他来源 | 通用 webhook 入口、定时任务并入任务模型、其他 IM | 另立计划 |

## 12. 配置

只走 `config.json`，缺省关闭：

```json
{
  "channels": {
    "feishu": {
      "enabled": false,
      "owner_open_id": "",
      "default_root_id": "",
      "card_ttl_hours": 168,
      "inbound_per_minute": 30
    }
  }
}
```

不新增环境变量；lark-cli 路径取 `PATH`，可用 `lark_cli_path` 覆盖（测试用 fake）。

## 13. 与 agent-autoloop 的对照

| 采用 | 不采用 |
|---|---|
| 长连接回调，无需公网 | 规则引擎与 `rules.json`（另立计划） |
| 每来源凭据、稳定外部 id 去重 | worker 池与 claim 协议（MindFS 节点是完整实例，不是 job runner） |
| 卡片生命周期、owner 白名单、首个回答生效 | 通用插件市场/热加载 |
| 发送幂等键、失败重试、监听单实例 | 全量事件溯源重写（现有 NodeRun + 投影 + router_events 足够） |
| `later`/复查思路（本期不做，记在后续） | hop 熔断（仅在允许 Agent 自发事件后才需要） |
