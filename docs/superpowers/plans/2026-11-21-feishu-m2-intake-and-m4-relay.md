# 飞书通道 M2 + M4（消息入口与主控中转）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在已上线的飞书出卡/点击闭环之上，加入 owner 私聊消息建任务与项目选择卡，并让所有跨节点卡片与回答只经主节点中转。

**Architecture:** 主节点仍是唯一 `lark-cli` 凭据与事件连接持有者；新增第二条 `im.message.receive_v1` 监听，入站经确定性解析后在本机或目标 worker 的 `POST /api/tasks` 建任务。worker 不跑 `lark-cli`：卡片由 `RelayNotifier` 经带 token 的 `POST /api/channels/feishu/relay` 送到主节点代发；点击按卡片持久化的 `node_id` 由主节点转发到对应 worker 的 `POST /api/channels/feishu/answer`。所有目标 URL 只来自 `nodes.json`，绝不取请求体字段。

**Tech Stack:** Go 1.26.5，chi、`httptest`、`os/exec`、`crypto/subtle`，JSON 原子仓库；无新增第三方依赖，无前端改动。

**Spec:** [docs/superpowers/specs/2026-11-21-feishu-channel-and-intake-design.md](../specs/2026-11-21-feishu-channel-and-intake-design.md)

**Scope:** `G-PH`（尚需在最终任务登记进 `docs/upstream-customizations.yaml`）。

## Global Constraints

- 用户 2026-11-21 裁定：**不做群聊、暂不做多账户；发消息选项目卡可做；全走主控节点中转**。入站只接 `chat_type=p2p` 且 `sender.open_id == owner_open_id` 的文本消息。
- 不得在本任务中改 `config.json`、部署、重启任何节点；本机 `sudo systemctl restart mindfs` 只能由用户执行。最终真机验收需另行授权。
- 飞书凭据与长连接永远只在主节点；worker 只持有共享 relay token、根级 `feishu_relay_url` 与根级 `feishu_node_id`。
- 新配置键只走 `config.json`，无 flag；`enabled` 缺省 `false`，`relay_token` 空 = 跨节点关闭（fail closed）。token 长度必须 ≥32 字符；比较用 `crypto/subtle.ConstantTimeCompare`；日志不得打 token 或消息全文。
- `POST /api/channels/feishu/relay` 只能存在于 control；精确加入 `nodeinfo.controlPlanePrefixes`，worker 返回 403。`POST /api/channels/feishu/answer` 留在数据面，只接受已配置 token 且 `answer.NodeID` 等于本节点 id 的请求。
- relay 请求体 ≤64 KiB；建任务输入 ≤32 KiB、任务名 ≤200 runes（复用 `kanban.intakeBodyMaxBytes` / `intakeTitleMaxRunes` 的现有口径）；消息 id 去重保留 7 天。
- 项目选择卡最多 5 个候选、TTL 24h；同一 `chat_id` 只能有一张未答选择卡；无法唯一确定项目时不猜，候选多于上限或无候选时直接文字回答。
- 入站限流 30 条/分钟、burst 10；超时、失败、未知项目都必须有用户可见回执，不允许静默丢消息。
- 所有新代码 TDD（RED→GREEN→变异），提交标 `Scope: G-PH`，只按 pathspec 提交；push / ship 不在本计划内。
- 门禁：`go test -count=1 ./...`、`tsc -b --noEmit`、`npx vitest run`、干净 worktree `make check-upstream` 全绿。
- 保持 M1 已验证的 `os.Pipe()` stdin 与 SIGTERM + `WaitDelay` 语义不变。

## Review Focus

- **不可信飞书行**：畸形 JSON、群聊、非 owner、图片/语音、重复 `message_id` → 忽略或去重，不建任务、不泄露 owner 之外信息。测试放在 T1、T4。
- **项目歧义**：`#fn/docs`、`@项目名`、重名项目、0/多命中、过期选择卡 → 按 spec §6.2 的确定性顺序解析，选卡过期后不误建任务。测试放在 T3、T5。
- **relay 伪造与 SSRF**：空/短/错误 token、伪造 `node_id`、请求体带任意 URL、worker 打 control 之外地址 → 一律拒绝；转发 URL 只查 `nodes.json`。测试放在 T6、T7、T8。
- **跨节点失败**：control 不可达、5xx、5s 超时、三次重试耗尽 → 卡片保持 `open` 且留可读错误，不伪装成功。测试放在 T7、T9。
- **点击落错节点**：worker 产生的 gate/ask 卡被点击 → control 按卡片 `node_id` 发给该 worker 的 answer 端点，绝不落在本机。测试放在 T8、T9。

---

### Task 1: 入站事件解析与双事件监听

**Files:**
- Create: `server/internal/channels/feishu/event_message.go`
- Create: `server/internal/channels/feishu/event_message_test.go`
- Create: `server/internal/channels/feishu/testdata/message_p2p.json`
- Modify: `server/internal/channels/feishu/supervisor.go`
- Modify: `server/internal/channels/feishu/supervisor_test.go`
- Modify: `server/internal/channels/feishu/service.go`

**Interfaces:**
- Produces: `type InboundMessage struct { EventID, MessageID, ChatID, ChatType, SenderOpenID, Text string; CreateTime int64 }`
- Produces: `func ParseMessage(line []byte) (InboundMessage, bool)` — 接受 schema 2.0 的 `im.message.receive_v1`，只认 `message_type=text`；`Text` 从 `event.message.content` 的 JSON 字符串里取 `text` 并 `strings.TrimSpace`。
- Produces: `Supervisor.EventType string`（空 = `card.action.trigger`）与 `Supervisor.HandleRaw func(context.Context, []byte) error`；`HandleRaw != nil` 时直接把每行原文交给它，不再走 `ParseEvent`/`Handle`。
- Consumes: 现有 `ParseEvent(line) (Event,bool)`、`Event.ParseAction()` 与 M1 的 stdin/SIGTERM 修复。

- [ ] **Step 1: 写入站解析 RED 测试**

`event_message_test.go` 中写三个测试：`TestParseMessageAcceptsP2PText` 读 `testdata/message_p2p.json`，断言 `MessageID=="om_im_1"`、`ChatID=="oc_p2p_1"`、`ChatType=="p2p"`、`SenderOpenID=="ou_owner"`、`Text=="修一下 #proj/main.go"`；`TestParseMessageRejectsNonTextAndGroup` 用 `message_type=image` 与 `chat_type=group` 的行断言 `ok==false`；`TestParseEventStillIgnoresInboundMessage` 保持 M1 的 `{"type":"im.message.receive_v1"}` 仍被 `ParseEvent` 忽略。fixture 用真实 schema 2.0 形状：`header.event_id/status/event_type` + `event.sender.sender_id.open_id` + `event.message.{message_id,chat_id,chat_type,message_type,content,create_time}`，`content` 是 JSON 字符串 `"{\"text\":\"  修一下 #proj/main.go  \"}"`。

- [ ] **Step 2: 验证 RED**

Run: `go test ./server/internal/channels/feishu -run 'TestParseMessage|TestParseEventStillIgnores' -count=1`
Expected: FAIL，报未定义的 `ParseMessage` / `InboundMessage`。

- [ ] **Step 3: 实现解析**

在 `event_message.go` 实现 `ParseMessage`：先 `json.Unmarshal` 到带 `header` 与 `event` 的最小结构；`header.event_type != "im.message.receive_v1"`、`message_type != "text"`、`message_id/chat_id/sender.open_id` 任一为空即返回 false；`content` 二次解码取 `text`，空白文本返回 false。不要在这里过滤 owner / chat_type（那是服务层策略）。

- [ ] **Step 4: 验证解析 GREEN**

Run: `go test ./server/internal/channels/feishu -run 'TestParseMessage|TestParseEventStillIgnores' -count=1`
Expected: PASS。

- [ ] **Step 5: 写双监听 RED 测试**

在 `supervisor_test.go` 加 `TestSupervisorUsesConfiguredEventTypeAndRawHandler`：假 CLI 脚本记录 argv 与 stdin 存活，启一个 `Supervisor{EventType:"im.message.receive_v1", HandleRaw:...}`，写一行 fixture 后断言 argv 含 `event consume im.message.receive_v1 --as bot`、回调收到原文、进程退出后不发 `Handle`（旧路径不触发）。保留 M1 两个 stdin/SIGTERM 测试原样。

- [ ] **Step 6: 验证双监听 RED**

Run: `go test ./server/internal/channels/feishu -run 'TestSupervisorUsesConfiguredEventTypeAndRawHandler|TestSupervisorKeepsListenerStdinOpen|TestSupervisorStopsListenerWithSIGTERM' -count=1`
Expected: FAIL，argv 仍是 `card.action.trigger` 或没有 `HandleRaw`。

- [ ] **Step 7: 泛化 Supervisor 并接第二条监听**

在 `supervisor.go` 增加两个字段；`consume()` 用 `eventType := s.EventType; if eventType=="" { eventType="card.action.trigger" }` 拼 argv；逐行分派若 `HandleRaw != nil` 则调用它，否则保留原 `ParseEvent` + `Handle`。在 `Service.Start` 保留原 click supervisor，新增 message supervisor；两者共用 `HandlerTimeout` 与日志前缀，但事件类型不同。`Service.Wait` 等待两条循环。

- [ ] **Step 8: 验证监听 GREEN 与包级回归**

Run: `go test ./server/internal/channels/feishu -count=1`
Expected: PASS，M1 全部用例不变。

- [ ] **Step 9: 变异验证**

依次临时改坏三处并各自重跑对应测试：事件类型回退成硬编码 `card.action.trigger`、`HandleRaw` 分支删除、解析时对 `message_type` 不校验。Expected: 各自有测试打红后还原复绿。

- [ ] **Step 10: 提交**

```bash
git commit -- \
  server/internal/channels/feishu/event_message.go \
  server/internal/channels/feishu/event_message_test.go \
  server/internal/channels/feishu/testdata/message_p2p.json \
  server/internal/channels/feishu/supervisor.go \
  server/internal/channels/feishu/supervisor_test.go \
  server/internal/channels/feishu/service.go \
  -m "feat(feishu): 接入 owner 私聊文本事件监听（Scope: G-PH）"
```

### Task 2: 配置解析与项目目录缓存

**Files:**
- Modify: `cli/cmd/startup.go`
- Modify: `cli/cmd/startup_feishu.go`
- Modify: `cli/cmd/mindfs.go`
- Modify: `cli/cmd/startup_config_test.go`
- Modify: `server/app/server.go`
- Modify: `server/app/feishu.go`
- Create: `server/internal/channels/feishu/catalog.go`
- Create: `server/internal/channels/feishu/catalog_test.go`

**Interfaces:**
- Consumes: `nodes.Store.List() []NodeConnection`（字段 `ID/Name/URL`）、`GET /api/dirs` 响应 `{"id","display_name","root_path"}`。
- Produces: `app.FeishuOptions` 新增 `Bindings []FeishuBinding`、`DefaultRootID string`、`PickerTTL time.Duration`、`PickerMax int`、`InboundRatePerMinute int`、`InboundBurst int`、`RelayToken string`；`app.Options` 新增根级 `FeishuRelayURL string` 与 `FeishuNodeID string`（对应 spec 的 `feishu_relay_url` / worker 节点 id）；`app.FeishuBinding{ChatID,NodeID,RootID string}` 与等价的 `feishu.Binding{ChatID,NodeID,RootID string}`，`newFeishuChannel` 做一次显式映射（feishu 包不依赖 app）。
- Produces: `type ProjectRef struct{ NodeID, RootID, Name string }`
- Produces: `type ProjectCatalog interface{ List(ctx context.Context) ([]ProjectRef, error) }`
- Produces: `func NewProjectCatalog(nodeLister interface{ List() []nodes.NodeConnection }, local func() []ProjectRef, ttl time.Duration, now func() time.Time, fetch func(context.Context,string) ([]byte,error)) ProjectCatalog`

- [ ] **Step 1: 写配置 RED 测试**

在 `startup_config_test.go` 写 `TestFeishuM2M4ConfigComesFromFileOnly`：配置 JSON 在 `channels.feishu` 下含 `bindings`（一条 `chat_id=oc_1,node_id=fn,root_id=docs`）、`default_root_id=proj`、`project_picker_ttl_hours=48`、`project_picker_max=4`、`inbound_per_minute=20`、`inbound_burst=5`、`relay_token=32+字符`；根级含 `feishu_relay_url=http://127.0.0.1:7331/mindfs/api/channels/feishu/relay` 与 `feishu_node_id=fn`。断言全部落到启动参数，且对应 flag 不存在。缺省断言为 `24h / 5 / 30 / 10`、relay 字段空。`inbound_burst` 与 `feishu_node_id` 是 spec 给出值但样例未列键的实现补齐，不改已有 `web_push_relay_url` 语义。

- [ ] **Step 2: 验证配置 RED**

Run: `go test ./cli/cmd -run TestFeishuM2M4ConfigComesFromFileOnly -count=1`
Expected: FAIL，字段未定义或值为零。

- [ ] **Step 3: 实现配置透传**

按现有 `WebPushRelayURL` 的“只在 config.json、无 flag”路径，把新字段加进 `startup.go` 的文件配置结构与 flags 结构（flags 字段不暴露命令行）、`startup_feishu.go` 的 opts 构造与缺省值、`mindfs.go` 传给 `app.Options` 的位置、`server/app/server.go` 的 `FeishuOptions`。token 只做长度校验，不打印。

- [ ] **Step 4: 验证配置 GREEN**

Run: `go test ./cli/cmd -run TestFeishuM2M4ConfigComesFromFileOnly -count=1`
Expected: PASS。

- [ ] **Step 5: 写目录缓存 RED 测试**

`catalog_test.go` 写 `TestProjectCatalogMergesNodesAndCaches`：本地给 `proj`，两个远程节点分别给 `docs`、`mindfs`；`fetch` 计数断言第一次 2 次 HTTP、60s 内第二次 0 次、61s 后再次 2 次；`TestProjectCatalogIgnoresFailedNode` 让一个节点返回 500，仍返回其余项目且记录一次错误；`TestProjectCatalogFormatsLabel` 断言显示名是 `docs · fn`。

- [ ] **Step 6: 验证目录 RED**

Run: `go test ./server/internal/channels/feishu -run TestProjectCatalog -count=1`
Expected: FAIL，`NewProjectCatalog` 未定义。

- [ ] **Step 7: 实现目录缓存**

`catalog.go`：`List` 在锁内检查 `now()-fetchedAt < 60s`；本地用 `local()`；远程对每个节点 `GET strings.TrimRight(node.URL,"/") + "/api/dirs"`，5s 超时，`decode` 只取 `id/display_name`，`id` 或名字为空跳过；失败节点跳过但计错，全部失败才返回 error。缓存值按 `NodeID + "\x00" + RootID` 去重。

- [ ] **Step 8: 验证目录 GREEN 与变异**

Run: `go test ./server/internal/channels/feishu ./cli/cmd -count=1`
Expected: PASS。临时把 TTL 判断改成永不过期与失败节点整体返回 error，各自打红对应测试后还原。

- [ ] **Step 9: 提交**

```bash
git commit -- \
  cli/cmd/startup.go cli/cmd/startup_feishu.go cli/cmd/mindfs.go \
  cli/cmd/startup_config_test.go server/app/server.go server/app/feishu.go \
  server/internal/channels/feishu/catalog.go \
  server/internal/channels/feishu/catalog_test.go \
  -m "feat(feishu): 解析入口配置并缓存跨节点项目目录（Scope: G-PH）"
```

### Task 3: 确定性项目解析与选择卡渲染

**Files:**
- Create: `server/internal/channels/feishu/resolve.go`
- Create: `server/internal/channels/feishu/resolve_test.go`
- Modify: `server/internal/channels/feishu/types.go`
- Modify: `server/internal/channels/feishu/render.go`
- Modify: `server/internal/channels/feishu/render_test.go`
- Modify: `server/internal/channels/feishu/store.go`

**Interfaces:**
- Produces: `type Resolution struct{ Kind string; Project ProjectRef; Candidates []ProjectRef }`，`Kind` 为 `"project"` / `"picker"` / `"answer"`。
- Produces: `func ResolveProject(text, chatID string, bindings []feishu.Binding, catalog ProjectCatalog, defaultRoot string, max int) (Resolution, error)`
- Produces: `KindPicker Kind = "picker"`；`Card`/`Notice` 增 `SourceMessageID string`、`IntakeText string`、`Projects []PickerProject`；`PickerProject{NodeID,RootID,Label string}`。
- Produces: `Store.FindOpenPickerByChat(chatID string) (Card, bool)`
- Consumes: T2 的 `ProjectRef` / `ProjectCatalog` / `feishu.Binding`。

- [ ] **Step 1: 写解析 RED 测试**

`resolve_test.go` 写四张表：`ResolvePrefersExplicitBinding`（`chat_id=oc_1` 命中 binding，文本里的 `#proj` 被忽略）；`ResolveParsesNodeAndNamePrefix`（`#fn/docs`、`@proj` 分别命中）；`ResolveUniqueNameMatches`（文本含唯一项目名）；`ResolveReturnsPickerCandidates`（文字含 `docs` 且目录里 `fn/docs`、`pc/docs` 两个命中 → `Kind=="picker"`、`len(Candidates)==2`）。再写 `ResolveNoGuess`：`max=5` 但候选 7 个 → 只回 5 个；0 命中 → `Kind=="answer"` 且 `Candidates` 为空。`default_root_id` 只在 binding 与前缀都未命中且该 root 存在于目录时使用；不存在时必须忽略。

- [ ] **Step 2: 验证解析 RED**

Run: `go test ./server/internal/channels/feishu -run TestResolve -count=1`
Expected: FAIL，`ResolveProject` 未定义。

- [ ] **Step 3: 实现解析**

按 spec §6.2 顺序实现：binding → `#node/root` / `@name` / `#name` 前缀 → 唯一匹配 → 选择卡 → 文字回答。前缀中的 node 用大小写不敏感的节点名或 id 比较；root 用项目 `RootID` 或 `Name` 精确比较。候选排序按 `NodeID + "\x00" + RootID`，截断到 `max`；绝不因为只有一个目录项就猜与文本无关的项目。

- [ ] **Step 4: 验证解析 GREEN**

Run: `go test ./server/internal/channels/feishu -run TestResolve -count=1`
Expected: PASS。

- [ ] **Step 5: 写选择卡 RED 测试**

`render_test.go` 加 `TestRenderPickerCardHasProjectChoicesAndNoOtherKinds`：用 `KindPicker`、`Projects` 三项、`SourceMessageID="om_im_1"`、`IntakeText="修一下"` 渲染，断言 schema 2.0 的 `card.elements` 里恰好三项且标签为 `docs · fn` 等，正文含原问题；`store_test.go` 加 `TestFindOpenPickerByChatIgnoresDoneAndExpired`：两张卡一张 `done`、一张 25h 前创建，查找均不命中。

- [ ] **Step 6: 验证选择卡 RED**

Run: `go test ./server/internal/channels/feishu -run 'TestRenderPickerCard|TestFindOpenPickerByChat' -count=1`
Expected: FAIL，字段或方法未定义。

- [ ] **Step 7: 实现选择卡**

在 `types.go` 增字段与常量；`render.go` 复用 ask 卡的元素构造但只用按钮，不生成输入框；`store.go` 增加按 `ChatID + KindPicker + StateOpen + ExpiresAt>now` 的扫描。选择卡的 `ExpiresAt` 由调用方按 `PickerTTL` 设置。

- [ ] **Step 8: 验证与变异**

Run: `go test ./server/internal/channels/feishu -count=1`
Expected: PASS。临时把解析顺序改成“先唯一匹配再 binding”、把候选上限改 10、把已 done 的选择卡也算命中，各自打红后还原。

- [ ] **Step 9: 提交**

```bash
git commit -- \
  server/internal/channels/feishu/resolve.go \
  server/internal/channels/feishu/resolve_test.go \
  server/internal/channels/feishu/types.go \
  server/internal/channels/feishu/render.go \
  server/internal/channels/feishu/render_test.go \
  server/internal/channels/feishu/store.go \
  server/internal/channels/feishu/store_test.go \
  -m "feat(feishu): 增加确定性项目解析与项目选择卡（Scope: G-PH）"
```

### Task 4: 入站去重与建任务服务

**Files:**
- Create: `server/internal/channels/feishu/intake_store.go`
- Create: `server/internal/channels/feishu/intake_store_test.go`
- Create: `server/internal/channels/feishu/intake.go`
- Create: `server/internal/channels/feishu/intake_test.go`
- Modify: `server/internal/channels/feishu/service.go`
- Create: `server/app/feishu_intake.go`

**Interfaces:**
- Produces: `type IntakeStore struct{...}` with `func NewIntakeStore(path string, now func() time.Time) (*IntakeStore, error)`、`func (s *IntakeStore) Seen(messageID string) bool`、`func (s *IntakeStore) Record(messageID, taskID, status string) error`、`func (s *IntakeStore) Prune() error`。
- Produces: `type TaskCreator interface{ Create(ctx context.Context, nodeID, rootID, input string) (taskID string, err error) }`
- Produces: `type IntakeService struct{...}` with `func NewIntakeService(cfg Config, catalog ProjectCatalog, store *IntakeStore, creator TaskCreator, notify func(context.Context, Notice) error, clock func() time.Time, limiter func(string) bool) *IntakeService` and `func (s *IntakeService) Handle(ctx context.Context, msg InboundMessage) error`.
- `Config` 增 `OwnerOpenID string`、`NodeID string`、`RelayToken string`、`Bindings []Binding`、`DefaultRootID string`、`PickerTTL time.Duration`、`PickerMax int`；`NodeID` 由根级 `feishu_node_id` 映射。
- Consumes: T1 `InboundMessage`、T2 `ProjectCatalog`、T3 `Resolution`。

- [ ] **Step 1: 写入站仓库 RED 测试**

`intake_store_test.go` 写 `TestIntakeStorePersistsAndPrunesAtomically`：写入 `om_1`、`om_2`，重新打开后都 `Seen`；把 `om_1` 时间设成 8 天前，`Prune` 后 `Seen("om_1")==false`、`Seen("om_2")==true`；断言文件权限 0600、目录 0700、写入过程 temp+rename（通过注入失败路径验证内存回滚）。

- [ ] **Step 2: 验证仓库 RED**

Run: `go test ./server/internal/channels/feishu -run TestIntakeStore -count=1`
Expected: FAIL，类型未定义。

- [ ] **Step 3: 实现入站仓库**

照 `store.go` 的原子 JSON 模式实现，路径 `<config-dir>/feishu/intake.json`，记录 `{message_id,task_id,status,created_at}`，只按 id 索引；`Record` 已存在时返回 nil 且不覆盖。`Prune` 只删 7 天前记录。

- [ ] **Step 4: 验证仓库 GREEN**

Run: `go test ./server/internal/channels/feishu -run TestIntakeStore -count=1`
Expected: PASS。

- [ ] **Step 5: 写入站服务 RED 测试**

`intake_test.go` 写四条：`TestIntakeIgnoresGroupNonOwnerNonTextAndDuplicate`（四类输入都不调用 creator/notify）；`TestIntakeCreatesTaskForResolvedProject`（`#fn/docs` → creator 收到 `nodeID=fn,rootID=docs,input=原文本`，写 intake 记录，发 notice）；`TestIntakeCreatesPickerWhenAmbiguous`（两个候选 → notify 收到 `KindPicker`，`Projects` 两项，`ExpiresAt` 24h，不调 creator）；`TestIntakeRateLimitRejectsBurst`（limiter 返回 false → 回一条“稍后再试”的 notice，不建任务）。文本超过 32 KiB 时截断到 32 KiB 并记录截断说明。

- [ ] **Step 6: 验证服务 RED**

Run: `go test ./server/internal/channels/feishu -run TestIntake -count=1`
Expected: FAIL，`IntakeService` 未定义。

- [ ] **Step 7: 实现入站服务**

`Handle` 顺序：owner/p2p/text 校验 → `store.Seen(messageID)` → 限流 → 长度截断 → `ResolveProject` → `project` 时调 creator 并 `Record(...,"created")` 后发成功 notice → `picker` 时若已有未答选择卡则只回一条提示，否则发选择卡并 `Record(...,"picker")` → `answer` 时发文字回答。所有错误都经 notice 告知 owner，且返回 error 让 supervisor 记日志，不重试同一条消息。

- [ ] **Step 8: 实现真实 TaskCreator**

`server/app/feishu_intake.go` 定义 `feishuTaskCreator`：本地节点用 `workspaceManager` 的 kanban `CreateTask`（`workflow_choice=empty`、`start_immediately=true`）；远程节点从 `nodes.Store` 查 URL，`POST <url>/api/tasks`，body `{root_id,input,name,workflow_choice:"empty",start_immediately:true}`，10s 超时，只接受 2xx 并解析 `id`。URL 只来自 node store；找不到节点时返回明确错误。把 `IntakeService` 接到 T1 的 message supervisor 回调。

- [ ] **Step 9: 验证 GREEN 与变异**

Run: `go test ./server/internal/channels/feishu ./server/app -count=1`
Expected: PASS。临时去掉 owner 判断、去掉 message_id 去重、把 picker TTL 改成 1h、把 creator 的 nodeID 忽略，各自打红后还原。

- [ ] **Step 10: 提交**

```bash
git commit -- \
  server/internal/channels/feishu/intake_store.go \
  server/internal/channels/feishu/intake_store_test.go \
  server/internal/channels/feishu/intake.go \
  server/internal/channels/feishu/intake_test.go \
  server/internal/channels/feishu/service.go \
  server/app/feishu_intake.go \
  -m "feat(feishu): owner 私聊消息建任务与选择卡入站链路（Scope: G-PH）"
```

### Task 5: 项目选择卡点击落库

**Files:**
- Modify: `server/internal/channels/feishu/service_click.go`
- Modify: `server/internal/channels/feishu/service_click_test.go`

**Interfaces:**
- Consumes: `Card.Projects []PickerProject`、`Card.SourceMessageID`、`TaskCreator.Create`、`IntakeStore.Record`、`Answer.Kind == KindPicker`。
- Produces: 点击选择卡后写回同一 `SourceMessageID` 的 intake 记录，status `"created"`；卡终态为 `done`。

- [ ] **Step 1: 写点击 RED 测试**

`service_click_test.go` 加 `TestPickerClickCreatesTaskOnChosenNode`：构造 `KindPicker` 卡，`Projects=[{NodeID:"fn",RootID:"docs"},{NodeID:"pc",RootID:"proj"}]`，`selected=0`，点击后断言 creator 收到 `fn/docs`、intake 记录状态 `created`、卡片 `state=done`、先发 patch 回执再执行 creator（记录调用顺序）。再加 `TestPickerClickIgnoresNonOwnerAndExpired` 与 `TestPickerClickRejectsOutOfRangeSelection`。

- [ ] **Step 2: 验证 RED**

Run: `go test ./server/internal/channels/feishu -run TestPickerClick -count=1`
Expected: FAIL，KindPicker 分支不存在。

- [ ] **Step 3: 实现点击分支**

在 `HandleAction` 的 switch 里加 `KindPicker`：owner 校验与卡状态 CAS 复用现有 gate/ask 路径；按 `selected` 取 `Card.Projects[i]`，越界直接 settle 为 failed 并回执“选择已失效”；按 `SourceMessageID` 写 intake 记录；调用 `TaskCreator.Create`；保留 M1 的“先 patch 回执，后应用动作”顺序。

- [ ] **Step 4: 验证与变异**

Run: `go test ./server/internal/channels/feishu -count=1`
Expected: PASS。临时把选择卡点击改成按卡片 `RootID` 空值直接建任务、把回执挪到 creator 之后，各自打红后还原。

- [ ] **Step 5: 提交**

```bash
git commit -- \
  server/internal/channels/feishu/service_click.go \
  server/internal/channels/feishu/service_click_test.go \
  -m "feat(feishu): 选择卡点击在目标节点建任务（Scope: G-PH）"
```

### Task 6: 控制节点 relay 端点与 token 校验

**Files:**
- Create: `server/internal/channels/feishu/relay.go`
- Create: `server/internal/channels/feishu/relay_test.go`
- Modify: `server/internal/api/http_routes.go`
- Modify: `server/internal/api/http.go`
- Create: `server/internal/api/http_feishu_relay.go`
- Create: `server/internal/api/http_feishu_relay_test.go`
- Modify: `server/internal/nodeinfo/role.go`
- Modify: `server/internal/nodeinfo/role_test.go`

**Interfaces:**
- Produces: `const RelayTokenHeader = "X-MindFS-Relay-Token"`
- Produces: `func ValidateRelayToken(token string) error` — 空/裁剪后长度 <32 返回 error。
- Produces: `type RelayRequest struct{ NodeID string; Notice Notice }`
- Produces: `func ValidateRelayRequest(req RelayRequest, knownNode func(string) bool) error` — `NodeID` 必须非空、非 `local` 且 `knownNode(NodeID)==true`；`Notice.NodeID` 与 `NodeID` 不一致拒绝；`Notice.CardID/Kind` 非空。
- Consumes: T2 `Config.RelayToken` / `Config.NodeID`（运行时由 `feishu_node_id` 归一化）、`app.Options.FeishuNodeID`、`nodes.Store.List()`。

- [ ] **Step 1: 写 token/载荷 RED 测试**

`relay_test.go` 写 `TestValidateRelayTokenRejectsEmptyShortAndWhitespace`、`TestValidateRelayRequestRejectsForgedNodeAndMismatch`、`TestValidateRelayRequestAcceptsKnownWorker`。fake `knownNode` 只认 `fn`。

- [ ] **Step 2: 验证 RED**

Run: `go test ./server/internal/channels/feishu -run TestValidateRelay -count=1`
Expected: FAIL，函数未定义。

- [ ] **Step 3: 实现校验**

`relay.go` 用 `strings.TrimSpace` 后 `len(token) >= 32`；比较放在 handler：`subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1`。校验只认 node id，不读请求体 URL。

- [ ] **Step 4: 写 HTTP RED 测试**

`http_feishu_relay_test.go` 写 `TestFeishuRelayHandlerRequiresTokenAndControlNode`：无 token → 401；短 token → 401；错误 token → 401；正确 token + 合法载荷 → 200 且 fake `Service.Notify` 收到 Notice；`NodeID` 不在 node store → 400；body >64 KiB → 400；`nodeinfo.IsControlPlane("/api/channels/feishu/relay")==true` 且 `/api/channels/feishu/answer` 为 false。

- [ ] **Step 5: 验证 HTTP RED**

Run: `go test ./server/internal/api ./server/internal/nodeinfo -run 'TestFeishuRelay|TestControlPlane' -count=1`
Expected: FAIL，路由/前缀/处理函数未定义。

- [ ] **Step 6: 实现 handler 与路由**

在 `http_routes.go` 注册 `POST /api/channels/feishu/relay`；`HTTPHandler` 增 `FeishuRelayToken` 字段（T7 复用同一字段），由 `app.Options.FeishuOptions.RelayToken` 注入；在 `nodeinfo.controlPlanePrefixes` 加**精确路径** `/api/channels/feishu/relay`（不要加整个 `/api/channels` 前缀，否则 worker 的 answer 端点会被 403）；handler 先 token 常量时间比较，再 `MaxBytesReader(64<<10)` 解码，再 `ValidateRelayRequest`，最后调用主节点 `Service.Notify`。`Service` 缺席时 503。日志只打 node id 与 kind。

- [ ] **Step 7: 验证 GREEN 与变异**

Run: `go test ./server/internal/channels/feishu ./server/internal/api ./server/internal/nodeinfo -count=1`
Expected: PASS。临时去掉常量时间比较改为字符串相等、把前缀改成整个 `/api/channels`、把 node 校验去掉，各自打红后还原。

- [ ] **Step 8: 提交**

```bash
git commit -- \
  server/internal/channels/feishu/relay.go \
  server/internal/channels/feishu/relay_test.go \
  server/internal/api/http_routes.go \
  server/internal/api/http.go \
  server/internal/api/http_feishu_relay.go \
  server/internal/api/http_feishu_relay_test.go \
  server/internal/nodeinfo/role.go \
  server/internal/nodeinfo/role_test.go \
  -m "feat(feishu): 主控节点卡片中继端点与 token 校验（Scope: G-PH）"
```

### Task 7: worker relay notifier 与 answer 端点

**Files:**
- Create: `server/internal/channels/feishu/relay_notifier.go`
- Create: `server/internal/channels/feishu/relay_notifier_test.go`
- Create: `server/internal/api/http_feishu_answer.go`
- Create: `server/internal/api/http_feishu_answer_test.go`
- Modify: `server/internal/api/http_routes.go`
- Modify: `server/internal/api/http.go`
- Modify: `server/app/workspace_build.go`
- Modify: `server/app/feishu.go`

**Interfaces:**
- Produces: `type RelayNotifier struct{ URL, Token, NodeID string; Client *http.Client; Sleep func(time.Duration) }` with `func (r *RelayNotifier) NotifyCard(ctx context.Context, notice kanban.CardNotice) error`
- Produces: worker answer handler 消费 `Answer` JSON 与 `RelayTokenHeader`，调用 `AppContext.ApplyFeishuAnswer(ctx, answer)`。
- Consumes: T6 的 `RelayRequest` / `ValidateRelayToken`；T2 的 `app.Options.FeishuRelayURL` / `FeishuNodeID` 与 `Config.RelayToken`。

- [ ] **Step 1: 写 notifier RED 测试**

`relay_notifier_test.go` 写 `TestRelayNotifierRetriesThenSucceeds`（control 依次 500/500/200，注入 `Sleep` 记录 `10s/30s/2m` 三次中的前两次与最后一次不睡）、`TestRelayNotifierGivesUpWithReadableError`（三次全 500 → error 含状态码、卡片未从调用方移除）、`TestRelayNotifierSendsOnlyConfiguredFields`（请求体无任意 URL 字段，header token 正确，body ≤64 KiB）。

- [ ] **Step 2: 验证 RED**

Run: `go test ./server/internal/channels/feishu -run TestRelayNotifier -count=1`
Expected: FAIL，`RelayNotifier` 未定义。

- [ ] **Step 3: 实现 notifier**

用 `http.NewRequestWithContext` + `io.LimitReader`/`bytes.NewReader`，5s 客户端超时；把 `kanban.CardNotice` 转成 `Notice` 时固定 `NodeID=r.NodeID`；重试只针对 5xx/网络错，4xx 立即失败；每次失败写一条不含 token 的日志。`Sleep` 缺省 `time.Sleep`，测试注入。

- [ ] **Step 4: 写 answer 端点 RED 测试**

`http_feishu_answer_test.go` 写 `TestFeishuAnswerRejectsBadTokenOrForeignNode`（无/错 token 401；`answer.NodeID != 本节点 id` 403）与 `TestFeishuAnswerAppliesLocally`（正确 token + 本节点 id → fake `ApplyFeishuAnswer` 收到同一 Answer）。确认路由不是 control-plane。

- [ ] **Step 5: 验证 answer RED**

Run: `go test ./server/internal/api -run TestFeishuAnswer -count=1`
Expected: FAIL，路由/处理函数未定义。

- [ ] **Step 6: 实现 answer 端点并选择 notifier**

注册 `POST /api/channels/feishu/answer`；`HTTPHandler` 增 `FeishuRelayToken` / `FeishuNodeID` 字段，由 `app.Options` 注入（不进 `AppContext`，避免每个账户一份）。handler 常量时间比较 token，`Answer.NodeID` 与 `FeishuNodeID` 不一致直接 403，然后调用本地 apply。`server/app/workspace_build.go` 的 `feishuCardNotifier` 装配点改为：`app.Options.FeishuRelayURL` 非空 → `RelayNotifier`，否则保留 M1 的本地 `feishuCardNotifier`；worker 的 `enabled=false` 不启动 `lark-cli`，但 relay notifier 仍装配。

- [ ] **Step 7: 验证 GREEN 与变异**

Run: `go test ./server/internal/channels/feishu ./server/internal/api ./server/app -count=1`
Expected: PASS。临时把 4xx 也重试、把 `answer.NodeID` 校验删掉、把 notifier 选择条件改成 `enabled`，各自打红后还原。

- [ ] **Step 8: 提交**

```bash
git commit -- \
  server/internal/channels/feishu/relay_notifier.go \
  server/internal/channels/feishu/relay_notifier_test.go \
  server/internal/api/http_feishu_answer.go \
  server/internal/api/http_feishu_answer_test.go \
  server/internal/api/http_routes.go \
  server/internal/api/http.go \
  server/app/workspace_build.go \
  server/app/feishu.go \
  -m "feat(feishu): worker 卡片中继与本地回答端点（Scope: G-PH）"
```

### Task 8: 主控点击按卡片节点回传

**Files:**
- Modify: `server/app/server.go`
- Create: `server/app/feishu_answer_relay.go`
- Create: `server/app/feishu_answer_relay_test.go`

**Interfaces:**
- Produces: `type answerRelay func(ctx context.Context, answer feishu.Answer) error`
- Consumes: T6/T7 的 token 与 answer 端点；`nodes.Store.List()` / `Answer.NodeID` / T2 `Config.NodeID`。

- [ ] **Step 1: 写路由 RED 测试**

`feishu_answer_relay_test.go` 写 `TestFeishuAnswerRelayRoutesByCardNode`：卡片 `NodeID=fn` → 请求发到 node store 中 fn 的 URL；`NodeID` 空或等于 `Config.NodeID` → 走本地 apply；未知节点 → 返回明确错误且不发 HTTP；请求体里没有任何 URL 字段。

- [ ] **Step 2: 验证 RED**

Run: `go test ./server/app -run TestFeishuAnswerRelay -count=1`
Expected: FAIL，路由函数未定义。

- [ ] **Step 3: 实现 answer 路由**

`server/app/feishu_answer_relay.go` 用 `answerRelay` 包装 `workspaceManager.ApplyFeishuAnswer`：本地节点直接 apply；远程只从 node store 取 URL，加 token header，POST `/api/channels/feishu/answer`，5s 超时，5xx/网络错按 10s/30s/2m 三次重试（注入 sleeper 便于测试），4xx 立即失败。未知节点错误必须能在卡片上看到。把它作为 `Service.SetAnswerHandler` 的实现替换 T1 前的直接 apply。

- [ ] **Step 4: 验证与变异**

Run: `go test ./server/app ./server/internal/channels/feishu -count=1`
Expected: PASS。临时把远程答案发到请求体 URL、把未知节点回落本地 apply、去掉重试，各自打红后还原。

- [ ] **Step 5: 提交**

```bash
git commit -- \
  server/app/server.go \
  server/app/feishu_answer_relay.go \
  server/app/feishu_answer_relay_test.go \
  -m "feat(feishu): 点击按卡片节点回传 worker（Scope: G-PH）"
```

### Task 9: 跨端集成、变异账本与全量门禁

**Files:**
- Create: `server/internal/channels/feishu/integration_intake_relay_test.go`
- Modify: `docs/upstream-customizations.yaml`
- Modify: 本计划文件（执行结果）

**Interfaces:**
- Consumes: T1–T8 的全部接口；无新增生产接口。

- [ ] **Step 1: 写端到端集成测试**

用两个 `httptest.Server` 分别表示 control 与 worker，假 `lark-cli` 记录发送/补丁命令，真实 `feishu.Service` 与真实 store；场景：`owner` 私聊文本 `在 fn 的 docs 建个任务` → 解析命中 → worker `/api/tasks` 收到创建请求 → worker 产生 gate 卡 → `RelayNotifier` 送 control `/api/channels/feishu/relay` → control 发出飞书卡 → 点击 → control 按 `node_id=fn` POST worker `/api/channels/feishu/answer` → worker apply 且卡片 `state=done`。断言全程没有向请求体 URL 发请求、token 错误时没有卡片产生。

- [ ] **Step 2: 验证 RED / GREEN**

先临时删除 answer 路由跑测试，Expected: FAIL（点击后 worker 未收到答案）；还原后 `go test ./server/internal/channels/feishu -run TestIntakeRelayEndToEnd -count=1` Expected: PASS。

- [ ] **Step 3: 变异账本**

依次临时改坏并在账本记录被哪个测试打红：去重删除、owner 校验删除、picker TTL 1h、relay token 比较改明文、control 前缀改整段 `/api/channels`、node 校验删除、answer 按请求体 URL 转发、notifier 4xx 也重试。每次打红后还原复绿。

- [ ] **Step 4: 全量门禁**

Run:
```bash
go test -count=1 ./...
tsc -b --noEmit
npx vitest run
git worktree add --detach /tmp/mindfs-gph-check HEAD
# 在干净 worktree 中直接检查已提交 HEAD；不要从共享工作区带未提交文件
(cd /tmp/mindfs-gph-check && make check-upstream)
```
Expected: 四项全绿；`make check-upstream` 的 G-PH 锚点逐个命中，零告警。共享工作区里其他会话在飞改动造成的红项，必须在干净 worktree 复跑确认。

- [ ] **Step 5: 登记定制组**

在 `docs/upstream-customizations.yaml` 新增 `G-PH`，逐条写清「文件 / 改了什么 / 为什么必须保留 / 可见症状 / 针对性测试 → 钉住什么」，覆盖 T1–T8 的全部新增与修改文件；计划文件写明执行结果与变异账本。

- [ ] **Step 6: 提交**

```bash
git commit -- \
  server/internal/channels/feishu/integration_intake_relay_test.go \
  docs/upstream-customizations.yaml \
  docs/superpowers/plans/2026-11-21-feishu-m2-intake-and-m4-relay.md \
  -m "docs(feishu): 登记 M2+M4 定制与端到端验收（Scope: G-PH）"
```

## 上线验收（另行授权，不属于本计划的代码任务）

实现完成且门禁全绿后，先不部署、不重启。需要用户明确授权后再按 `ship` 流程分发；届时必须逐项核对：

- 主节点 `channels.feishu.enabled=true`、`owner_open_id` 不变、新增 `channels.feishu.relay_token` 与 `bindings`；worker 只新增 `channels.feishu.relay_token`、根级 `feishu_relay_url` 与 `feishu_node_id`，`enabled` 保持 false。
- 主节点 `lark-cli` 两条监听同时存活：`card.action.trigger` 与 `im.message.receive_v1`；重复连接与 stdin EOF 自退日志各为 0。
- owner 私聊一条带项目前缀的消息 → 正确节点建任务；一条含重名项目的消息 → 项目选择卡；24h 过期卡点击不建任务。
- worker 节点任务产生 gate → 主节点飞书收到卡；用户点击 → 对应 worker 的 NodeRun 推进，主节点任务库不误写。
- 错误 token / 伪造 node_id 的 relay 与 answer 请求分别被 401/400/403 拒绝，日志无 token、无消息全文。
- 本机 `sudo systemctl restart mindfs` 只能由用户执行；WSL / fn / pc-win 的重启按节点表执行，版本逐字一致。

## Task 9 执行结果（2026-11-22）

### 端到端接线

- 新增 `server/app/feishu_intake_relay_integration_test.go`（171 行）与 `server/app/feishu_click_relay_integration_test.go`（240 行），均位于 `package app`：能同时触达 `feishuAnswerRelay`、`feishuTaskCreator` 与 `api.HTTPHandler.Routes()`，不需要导出任何包私有符号。
- `TestFeishuIntakeEndToEndRoutesToRemoteTask`：用真实 `im.message.receive_v1` 事件驱动真实 Service/supervisor、真实 intake 策略、真实项目解析和真实 `feishuTaskCreator`，断言 owner 私聊「在 fn 的 docs 建个任务」只向 fn 的 `/api/tasks` 发一次 POST，载荷 `root_id=root-docs`、`input` 原文、`name=feishuTaskName(input)`、`workflow_choice=empty`、`start_immediately=true`；同时钉住 `PickerTTL=24h`。
- `TestFeishuRelayEndToEndCardAndAnswer`：worker 卡片经真实 `RelayNotifier` → 主控 `/api/channels/feishu/relay` → 真实 card store + lark-cli 假脚本；断言卡片落在主控、`NodeID=fn`，且点击后 `state=done`。再把 `Answer` 经生产 `feishuAnswerRelay` 发到 worker 的 `/api/channels/feishu/answer`，断言口令头、路径和整个 `Answer` 原样到达，`AppContext{}` 下 `KindNotice` 直接返回 200；载荷里塞了 decoy URL，decoy 服务器零命中。
- `TestFeishuRelayEndToEndRejectsWrongToken`：两端 401，错误口令不建卡、不到 apply。
- RED/GREEN：临时注释掉 `http_routes.go` 中 answer 路由，定向测试报 `answer relay: feishu answer: node fn rejected answer with HTTP 404` 与 `answer wrong token = 404`；恢复后 3 条全部 PASS。

### 变异账本

| # | 变异 | 定向命令 | RED 证据 | 恢复后 |
|---|---|---|---|---|
| M1 | 去掉 `s.store.Seen(id)` 去重 | `go test ./server/internal/channels/feishu -run TestIntakeDedupAndRateLimit -count=1` | `同一 message_id 只能处理一次：calls=2 notices=2` | PASS |
| M2 | 去掉 owner 检查 | `go test ./server/internal/channels/feishu -run TestIntakeIgnoresNonOwnerGroupAndNonText -count=1` | `非 owner / 群聊 / 非文本必须忽略：calls=0 notices=1` | PASS |
| M3 | `PickerTTL` 改为 1h | `go test ./server/app -run TestFeishuIntakeEndToEndRoutesToRemoteTask -count=1` | `feishu.PickerTTL = 1h0m0s, want 24h` | PASS |
| M4 | relay token 改成明文比较 | `go test ./server/internal/api -run TestFeishuRelayUsesConstantTimeTokenCompare -count=1` | `relay token must not be compared with plain string equality` | PASS |
| M5 | 控制面前缀放宽到 `/api/channels` | `go test ./server/internal/nodeinfo -run TestFeishuRelayIsExactControlPlanePath -count=1` | `/api/channels must stay data-plane` 等 4 行 | PASS |
| M6 | 去掉节点表校验 | `go test ./server/internal/channels/feishu -run TestValidateRelayRequestRejectsUnconfiguredNode -count=1` | 第一次因 `known` 未使用编译失败，不构成行为证据；改为 `_ = known` 后真实 RED：`ValidateRelayRequest(...NodeID:attacker...) accepted an unconfigured node` | PASS |
| M7 | answer 按载荷 URL 路由 | `go test ./server/app -run TestFeishuRelayEndToEndCardAndAnswer -count=1` | `answer 没有被回传到 fn 节点`（decoy 收到请求） | PASS |
| M8 | notifier 重试 4xx | `go test ./server/internal/channels/feishu -run TestRelayNotifierFailsImmediatelyOnClientError -count=1` | `4xx must fail immediately` | PASS |

### 门禁

- `go test -count=1 ./...`：全绿（28 包，`server/internal/kanban` 271.6s）。
- `(cd web && npx tsc -b --noEmit)`：clean（只有 npm 的 `allow-scripts` 警告）。
- `(cd web && npx vitest run)`：260 passed / 5 skipped 文件，863 passed / 6 skipped 用例，77.93s。
- `git worktree add --detach /tmp/mindfs-gph-check HEAD && (cd /tmp/mindfs-gph-check && make check-upstream)`：结果见提交后复跑记录；本轮没有基线 flake 出现。

### 已知边界

- notice 卡点击在 `service_click.go` 里走 `settleNotice` 本地结案，不经过 answer handler；因此「卡片点击 → 主控 answer handler → worker」这一段由直接调用生产 `feishuAnswerRelay` 覆盖，卡片状态机另由真实 click 事件覆盖。gate/ask 的 NodeRun 级推进由 T7/T8 单测覆盖，未在本轮隔离端到端里跑真实 kanban。
- 未部署、未重启任何节点；本机 system 单元仍只能由用户重启。

