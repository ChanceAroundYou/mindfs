# 飞书通道 M1（本机出口与点击）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 在本机把 MindFS 的 HumanGate、Router `ask_user` 与任务终态变成飞书卡片；点击卡片能在本机完成批准/驳回/回答，不需要公网地址。

**Architecture:** 新增 `server/internal/channels/feishu`：JSON 卡片仓库、schema 2.0 渲染、`lark-cli` 子进程适配器、长连接 supervisor、卡片状态机与投递循环。`kanban` 只暴露一个窄接口 `CardNotifier`，在三个创建点调用；点击通过 `AppContext.ApplyFeishuAnswer` 回到现有 `ResolveWorkflowHumanGate` / 用户留言链路。M1 不做入站建任务（M2）、不做跨节点中继（M4）。

**Tech Stack:** Go 1.26、`os/exec`、`encoding/json`、现有 SQLite/kanban 服务；测试用假 `lark-cli` 脚本，不连真实飞书。

**Spec:** `docs/superpowers/specs/2026-11-21-feishu-channel-and-intake-design.md`（§4.1、§5、§9、§11 M1）

## Global Constraints

- 本机 `sudo systemctl restart mindfs` **仅用户执行**；Agent 只打印命令。
- 本机首发；不编译/复制任何东西到 WSL、fn、pc-win；不做跨节点 relay。
- 飞书 app secret 只留在 `~/.lark-cli`，不进入 MindFS 配置、日志、卡片或测试夹具。
- 新增配置只走 `config.json`，无对应 flag；缺省 `enabled=false`，通道不启动也不影响 MindFS。
- 单文件硬线 500 行；新增代码按 `docs/agent/rules/backend/README.md`，测试镜像源码结构。
- 所有新功能先写失败测试，看到红再实现；提交信息以 `Scope: G-NW` 结尾，登记 `docs/upstream-customizations.yaml`。
- 只用 `git commit -- <pathspec>` 提交本组文件；共享工作区里其他会话的未提交改动一律不碰。
- 不记录或输出凭据；日志只打 `owner_open_id` 的短后缀、卡片 ID 与 `lark-cli` 的错误摘要。
- M1 的卡片只能由 `owner_open_id` 点击生效；非 owner 静默忽略，不回卡片、不写库。

## Review Focus

- 卡片点击发生在任务已经由网页处理之后：不得再写一条用户留言或重复批准，卡片应显示「已在网页处理」。
- 同一张卡被连点或飞书重投：只能有一个回答生效，后续点击只刷新卡片，不重复执行。
- 卡片过期（默认 168h）后点击：不得改动任务，只把卡片置 `expired` 并回一句「已过期」。
- `lark-cli` 不存在、退出、输出非 JSON 或长连接冲突：MindFS 主流程不得阻塞或崩溃，卡片保留待重发。
- 任务终态由多处写入（`setTaskStatus`、gate 驳回、workflow 取消）：任一路径进入终态都要有且只有一张 notice 卡。

---

### Task 1: Card model and JSON store

**Files:**
- Create: `server/internal/channels/feishu/types.go`
- Create: `server/internal/channels/feishu/store.go`
- Test: `server/internal/channels/feishu/store_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Kind string`; `KindGate/KindAsk/KindNotice`
  - `type State string`; `StateOpen/StateApplying/StateDone/StateFailed/StateExpired/StateSuperseded`
  - `type Choice struct { Label string; Value string }`
  - `type Card struct { ID string; Kind Kind; State State; AccountID, RootID, TaskID, TaskTitle, WorkflowNodeID, NodeName, QuestionID, Prompt, Status, Summary, Text, Note, MessageID, ChatID, LastError string; TaskNumber int; Choices []Choice; Selected int; Version int; RenderedVersion int; CreatedAt, UpdatedAt, ExpiresAt time.Time }`
  - `func NewStore(path string) *Store`
  - `func (s *Store) Load() error`
  - `func (s *Store) Put(c Card) (Card, error)`
  - `func (s *Store) Get(id string) (Card, bool)`
  - `func (s *Store) List() []Card`
  - `func (s *Store) Update(id string, fn func(*Card) bool) (Card, bool, error)`
  - `func (c Card) Expired(now time.Time) bool`

- [x] **Step 1: Write the failing store tests**

```go
func TestStorePersistsCardsAtomicallyWithOwnerOnlyMode(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "cards.json")
    st := NewStore(path)
    if err := st.Load(); err != nil { t.Fatal(err) }
    got, err := st.Put(Card{ID: "gate-nr-1", Kind: KindGate, State: StateOpen, TaskID: "task-1", Version: 1})
    if err != nil { t.Fatal(err) }
    if got.ID != "gate-nr-1" { t.Fatalf("id = %q", got.ID) }

    reloaded := NewStore(path)
    if err := reloaded.Load(); err != nil { t.Fatal(err) }
    if _, ok := reloaded.Get("gate-nr-1"); !ok { t.Fatal("card did not survive reload") }
    info, err := os.Stat(path)
    if err != nil { t.Fatal(err) }
    if info.Mode().Perm() != 0o600 { t.Fatalf("mode = %v, want 0600", info.Mode().Perm()) }
}

func TestStoreUpdateIsAtomicAndReportsMissingCards(t *testing.T) {
    st := NewStore(filepath.Join(t.TempDir(), "cards.json"))
    _ = st.Load()
    _, _ = st.Put(Card{ID: "ask-1", Kind: KindAsk, State: StateOpen, Version: 1})
    got, changed, err := st.Update("ask-1", func(c *Card) bool { c.State = StateApplying; return true })
    if err != nil || !changed || got.State != StateApplying { t.Fatalf("got=%+v changed=%v err=%v", got, changed, err) }
    _, changed, err = st.Update("missing", func(c *Card) bool { return true })
    if err != nil || changed { t.Fatalf("missing changed=%v err=%v", changed, err) }
}

func TestCardExpiredUsesExpiresAt(t *testing.T) {
    now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
    if !(Card{ExpiresAt: now.Add(-time.Second)}).Expired(now) { t.Fatal("past card not expired") }
    if (Card{ExpiresAt: now.Add(time.Second)}).Expired(now) { t.Fatal("future card expired") }
}
```

- [x] **Step 2: Run and verify failure**

Run: `go test ./server/internal/channels/feishu/ -run 'TestStore|TestCardExpired' -v`
Expected: FAIL because the package/types do not exist.

- [x] **Step 3: Implement `types.go` and `store.go`**

`store.go`: mutex-protected `map[string]Card`, JSON file shape `{"version":1,"cards":[...]}`. `Save` writes `0700` dir, temp file in same dir, `Chmod(0600)`, encode, `Sync`, `Close`, `Rename`; remove temp on error. `Put` preserves `CreatedAt` for existing IDs and bumps `Version`; `Update` bumps `Version` and `UpdatedAt` only when `fn` returns true. `Load` treats missing file as an empty store and rejects corrupt JSON with the path in the error.

- [x] **Step 4: Run tests**

Run: `go test ./server/internal/channels/feishu/ -run 'TestStore|TestCardExpired' -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add server/internal/channels/feishu/types.go server/internal/channels/feishu/store.go server/internal/channels/feishu/store_test.go
git commit -m "feat(feishu): 卡片模型与原子 JSON 仓库（Scope: G-NW）"
```

---

### Task 2: Card rendering (schema 2.0)

**Files:**
- Create: `server/internal/channels/feishu/render.go`
- Test: `server/internal/channels/feishu/render_test.go`

**Interfaces:**
- Consumes: `Card`, `Choice`, `Kind`, `State` from Task 1.
- Produces: `func Render(c Card) map[string]any` and `func RenderJSON(c Card) (string, error)`.

- [x] **Step 1: Write failing render tests**

```go
func TestRenderGateOpenHasTwoCallbackChoices(t *testing.T) {
    raw := RenderJSONForTest(t, Card{ID: "gate-nr-1", Kind: KindGate, State: StateOpen, Prompt: "确认继续",
        Choices: []Choice{{Label: "批准", Value: "approve"}, {Label: "驳回", Value: "reject"}}})
    if raw["schema"] != "2.0" { t.Fatalf("schema = %v", raw["schema"]) }
    body := mustJSON(t, raw["body"])
    text := string(body)
    if !strings.Contains(text, `"card":"gate-nr-1"`) || !strings.Contains(text, `"choice":1`) { t.Fatalf("body = %s", text) }
    if strings.Contains(text, "input") { t.Fatalf("gate card must not render a text input: %s", text) }
}

func TestRenderAskAndNoticeHaveTextForms(t *testing.T) {
    ask := string(mustJSON(t, Render(Card{ID: "ask-1", Kind: KindAsk, State: StateOpen, Prompt: "选哪个？", Choices: []Choice{{Label: "A", Value: "a"}}}))
    if !strings.Contains(ask, `"form"`) || !strings.Contains(ask, "提交文字") { t.Fatalf("ask = %s", ask) }
    notice := string(mustJSON(t, Render(Card{ID: "notice-1", Kind: KindNotice, State: StateOpen, Prompt: "任务完成"})))
    if !strings.Contains(notice, "记录并结案") { t.Fatalf("notice = %s", notice) }
}

func TestRenderSettledCardHidesButtonsAndShowsOutcome(t *testing.T) {
    raw := string(mustJSON(t, Render(Card{ID: "gate-1", Kind: KindGate, State: StateDone, Selected: 0,
        Choices: []Choice{{Label: "批准", Value: "approve"}}, Summary: "已批准"})))
    if strings.Contains(raw, `"callback"`) { t.Fatalf("settled card still has buttons: %s", raw) }
    if !strings.Contains(raw, "已批准") { t.Fatalf("settled card lost outcome: %s", raw) }
}
```

- [x] **Step 2: Run and verify failure**

Run: `go test ./server/internal/channels/feishu/ -run TestRender -v`
Expected: FAIL with `undefined: Render`.

- [x] **Step 3: Implement `Render` / `RenderJSON`**

Use schema 2.0 with `config.update_multi=true`, header title `c.TaskTitle` or `c.Prompt`, `template` blue for open and grey for settled. Open gate/ask cards render each choice as `interactive_container` with `behaviors:[{type:"callback", value:{card:c.ID, choice:i}}]`. Ask adds a form named `ask_text_<id>` with input `note` and submit button `提交文字`; notice adds `handling_note` with `记录并结案`. Settled cards render state-specific text from `Summary`/`Text` and no behaviors. `RenderJSON` returns `string(json.Marshal(Render(c)))`.

- [x] **Step 4: Run tests**

Run: `go test ./server/internal/channels/feishu/ -run TestRender -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add server/internal/channels/feishu/render.go server/internal/channels/feishu/render_test.go
git commit -m "feat(feishu): 卡片 schema 2.0 渲染（Scope: G-NW）"
```

---

### Task 3: `lark-cli` adapter

**Files:**
- Create: `server/internal/channels/feishu/cli.go`
- Test: `server/internal/channels/feishu/cli_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type CLI struct { Path string; Timeout time.Duration }`
  - `func (c *CLI) SendInteractive(ctx context.Context, userID, content, idempotencyKey string) (string, error)`
  - `func (c *CLI) PatchInteractive(ctx context.Context, messageID, content string) error`

- [x] **Step 1: Write failing CLI tests with a fake executable**

```go
func TestCLISendUsesFixedArgumentsAndReturnsMessageID(t *testing.T) {
    bin, argsPath := fakeLark(t, `{"ok":true,"identity":"bot","data":{"message_id":"om_123"}}`)
    cli := &CLI{Path: bin, Timeout: time.Second}
    id, err := cli.SendInteractive(context.Background(), "ou_owner", `{"schema":"2.0"}`, "mindfs-card-gate-1")
    if err != nil || id != "om_123" { t.Fatalf("id=%q err=%v", id, err) }
    args := strings.Join(readLines(t, argsPath), "\n")
    for _, want := range []string{"im\n+messages-send", "--user-id\nou_owner", "--as\nbot", "--msg-type\ninteractive",
        "--idempotency-key\nmindfs-card-gate-1", "--content\n{\"schema\":\"2.0\"}"} {
        if !strings.Contains(args, want) { t.Fatalf("args missing %q:\n%s", want, args) }
    }
}

func TestCLIPatchFeedsContentOnStdinAndSurfacesErrors(t *testing.T) {
    bin, _ := fakeLark(t, `{"ok":false,"error":{"type":"authorization","message":"no scope"}}`)
    err := (&CLI{Path: bin, Timeout: time.Second}).PatchInteractive(context.Background(), "om_1", `{"schema":"2.0"}`)
    if err == nil || !strings.Contains(err.Error(), "authorization") { t.Fatalf("err = %v", err) }
}
```

`fakeLark` writes a `#!/bin/sh` script that appends `"$@"` and its stdin to files, then prints the given JSON.

- [x] **Step 2: Run and verify failure**

Run: `go test ./server/internal/channels/feishu/ -run TestCLI -v`
Expected: FAIL with `undefined: CLI`.

- [x] **Step 3: Implement `cli.go`**

`SendInteractive` runs `Path im +messages-send --user-id <userID> --as bot --msg-type interactive --idempotency-key <key> --content <content> --json`; `PatchInteractive` runs `Path im messages patch --message-id <messageID> --as bot --data - --json` with stdin `{"content":"<card json>"}`. Both use `exec.CommandContext` with `c.Timeout` (default 60s), capture stdout/stderr, parse the last JSON object; `ok:false` or `error` is an error containing `error.type` and a one-line `error.message`; non-JSON output is an error with a 300-rune excerpt. Never log the full content.

- [x] **Step 4: Run tests**

Run: `go test ./server/internal/channels/feishu/ -run TestCLI -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add server/internal/channels/feishu/cli.go server/internal/channels/feishu/cli_test.go
git commit -m "feat(feishu): lark-cli 发送与卡片补丁适配（Scope: G-NW）"
```

---

### Task 4: Click event parsing

**Files:**
- Create: `server/internal/channels/feishu/event.go`
- Test: `server/internal/channels/feishu/event_test.go`

**Interfaces:**
- Consumes: `Card` kinds from Task 1.
- Produces:
  - `type Event struct { Type, EventID, OperatorID, MessageID, ChatID, ActionTag, ActionName string; ActionValue, FormValue json.RawMessage }`
  - `type Action struct { CardID string; Choice int; Text string; MessageID, OperatorID, ChatID string }`
  - `func ParseEvent(line []byte) (Event, bool)`
  - `func (e Event) ParseAction() (Action, bool)`

- [x] **Step 1: Write failing parser tests**

```go
func TestParseEventReadsButtonCallbackWithStringActionValue(t *testing.T) {
    line := []byte(`{"type":"card.action.trigger","event_id":"ev_1","operator_id":"ou_owner","message_id":"om_1","chat_id":"oc_1","action_tag":"button","action_value":"{\"card\":\"gate-nr-1\",\"choice\":1}"}`)
    ev, ok := ParseEvent(line)
    if !ok { t.Fatal("event not parsed") }
    got, ok := ev.ParseAction()
    if !ok || got.CardID != "gate-nr-1" || got.Choice != 1 || got.MessageID != "om_1" { t.Fatalf("action = %+v ok=%v", got, ok) }
}

func TestParseActionReadsAskTextForm(t *testing.T) {
    ev, _ := ParseEvent([]byte(`{"type":"card.action.trigger","operator_id":"ou_owner","message_id":"om_2","action_name":"ask_text_submit_ask-1","form_value":"{\"note\":\"先做 A\"}"}`))
    got, ok := ev.ParseAction()
    if !ok || got.CardID != "ask-1" || got.Text != "先做 A" { t.Fatalf("action = %+v ok=%v", got, ok) }
}

func TestParseEventIgnoresOtherTypesAndMalformedJSON(t *testing.T) {
    if _, ok := ParseEvent([]byte(`{"type":"im.message.receive_v1"}`)); ok { t.Fatal("wrong type accepted") }
    if _, ok := ParseEvent([]byte(`{`)); ok { t.Fatal("malformed accepted") }
}
```

- [x] **Step 2: Run and verify failure**

Run: `go test ./server/internal/channels/feishu/ -run TestParse -v`
Expected: FAIL with `undefined: ParseEvent`.

- [x] **Step 3: Implement `event.go`**

Accept only `type == "card.action.trigger"`. `ParseAction` decodes `action_value` whether it is a JSON string or object, reads `card` as string (or number formatted with `strconv.Itoa`), `choice` as int. If `action_name` matches `ask_text_submit_<id>` or `handling_note_submit_<id>`, derive card ID from the name and decode `form_value.note`; ask form requires non-empty text, notice form defaults `Choice=0`. Empty action returns false.

- [x] **Step 4: Run tests**

Run: `go test ./server/internal/channels/feishu/ -run TestParse -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add server/internal/channels/feishu/event.go server/internal/channels/feishu/event_test.go
git commit -m "feat(feishu): 卡片点击事件解析（Scope: G-NW）"
```

---

### Task 5: Long-connection supervisor

**Files:**
- Create: `server/internal/channels/feishu/supervisor.go`
- Test: `server/internal/channels/feishu/supervisor_test.go`

**Interfaces:**
- Consumes: `CLI.Path`, `ParseEvent`/`ParseAction`.
- Produces:
  - `type Supervisor struct { Path string; Handle func(context.Context, Action) error; Logf func(string, ...any); RestartDelay, ConflictDelay time.Duration }`
  - `func (s *Supervisor) Run(ctx context.Context)`

- [x] **Step 1: Write failing supervisor tests**

```go
func TestSupervisorDispatchesClickAndRestartsAfterExit(t *testing.T) {
    bin, state := fakeEventCLI(t, `{"type":"card.action.trigger","operator_id":"ou_owner","message_id":"om_1","action_value":"{\"card\":\"gate-1\",\"choice\":0}"}`)
    seen := make(chan Action, 1)
    sup := &Supervisor{Path: bin, RestartDelay: 10 * time.Millisecond, ConflictDelay: 10 * time.Millisecond,
        Handle: func(_ context.Context, a Action) error { seen <- a; return nil }}
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go sup.Run(ctx)
    select {
    case a := <-seen:
        if a.CardID != "gate-1" || a.Choice != 0 { t.Fatalf("action = %+v", a) }
    case <-time.After(2 * time.Second): t.Fatal("click not dispatched")
    }
    waitFor(t, 2*time.Second, func() bool { return readCount(state) >= 2 })
}

func TestSupervisorUsesConflictDelayAndStopsOnContext(t *testing.T) {
    bin, _ := fakeEventCLI(t, `another event bus is already connected`)
    sup := &Supervisor{Path: bin, RestartDelay: 10 * time.Millisecond, ConflictDelay: 50 * time.Millisecond,
        Handle: func(context.Context, Action) error { return nil }}
    ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
    defer cancel()
    started := time.Now()
    sup.Run(ctx)
    if time.Since(started) < 50*time.Millisecond { t.Fatal("conflict delay not applied") }
}
```

- [x] **Step 2: Run and verify failure**

Run: `go test ./server/internal/channels/feishu/ -run TestSupervisor -v`
Expected: FAIL with `undefined: Supervisor`.

- [x] **Step 3: Implement `supervisor.go`**

Loop until ctx done: `exec.CommandContext(ctx, s.Path, "event", "consume", "card.action.trigger", "--as", "bot")`, stdin pipe kept open. Scan stdout with `bufio.Scanner` (1 MiB buffer) and dispatch each action in a goroutine via `Handle`; scan stderr for `another event bus is already connected`. On process exit, sleep `ConflictDelay` (default 60s) if conflict else `RestartDelay` (default 10s), unless ctx done. Log once per conflict episode, not per retry. `Logf` nil means `log.Printf`.

- [x] **Step 4: Run tests**

Run: `go test ./server/internal/channels/feishu/ -run TestSupervisor -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add server/internal/channels/feishu/supervisor.go server/internal/channels/feishu/supervisor_test.go
git commit -m "feat(feishu): 卡片点击长连接 supervisor（Scope: G-NW）"
```

---

### Task 6: Service delivery and click state machine

**Files:**
- Create: `server/internal/channels/feishu/service.go`
- Create: `server/internal/channels/feishu/service_click.go`
- Test: `server/internal/channels/feishu/service_test.go`

**Interfaces:**
- Consumes: Tasks 1–5.
- Produces:
  - `type Config struct { OwnerOpenID, LarkCLIPath, NodeID string; CardTTL time.Duration; Now func() time.Time; Logf func(string, ...any) }`
  - `type Notice struct { CardID string; Kind Kind; AccountID, RootID, TaskID, TaskTitle, WorkflowNodeID, NodeName, QuestionID, Prompt, Status, Summary string; TaskNumber int; Options []string }`
  - `type Answer struct { CardID string; Kind Kind; AccountID, RootID, TaskID, WorkflowNodeID, QuestionID, Decision, Text, Note string }`
  - `func New(cfg Config, store *Store, cli *CLI) *Service`
  - `func (s *Service) SetAnswerHandler(fn func(context.Context, Answer) error)`
  - `func (s *Service) Notify(ctx context.Context, n Notice) error`
  - `func (s *Service) Start(ctx context.Context)`
  - `func (s *Service) DeliverOnce(ctx context.Context)`

- [x] **Step 1: Write failing service tests**

```go
func TestServiceSendsUnsentCardWithStableIdempotencyKey(t *testing.T) {
    bin, calls := fakeLarkOK(t)
    st := NewStore(filepath.Join(t.TempDir(), "cards.json")); _ = st.Load()
    svc := New(Config{OwnerOpenID: "ou_owner", LarkCLIPath: bin, CardTTL: time.Hour,
        Now: fixedNow, Logf: func(string, ...any) {}}, st, &CLI{Path: bin, Timeout: time.Second})
    if err := svc.Notify(context.Background(), Notice{CardID: "gate-nr-1", Kind: KindGate, RootID: "root", TaskID: "task-1",
        TaskTitle: "修复登录", Prompt: "确认继续", Options: []string{"批准", "驳回"}}); err != nil { t.Fatal(err) }
    if err := svc.DeliverOnce(context.Background()); err != nil { t.Fatal(err) }
    if !strings.Contains(readFile(t, calls), "mindfs-card-gate-nr-1") { t.Fatal("idempotency key missing") }
    c, _ := st.Get("gate-nr-1")
    if c.MessageID != "om_1" || c.RenderedVersion != c.Version { t.Fatalf("card = %+v", c) }
}

func TestServiceClickAcksBeforeApplyingAndAppliesOnce(t *testing.T) {
    order := []string{}
    bin := fakeLarkRecording(t, &order)
    st := NewStore(filepath.Join(t.TempDir(), "cards.json")); _ = st.Load()
    svc := New(Config{OwnerOpenID: "ou_owner", LarkCLIPath: bin, CardTTL: time.Hour, Now: fixedNow,
        Logf: func(string, ...any) {}}, st, &CLI{Path: bin, Timeout: time.Second})
    _ = svc.Notify(context.Background(), Notice{CardID: "gate-nr-1", Kind: KindGate, RootID: "root", TaskID: "task-1",
        Prompt: "确认继续", Options: []string{"批准", "驳回"}})
    _ = svc.DeliverOnce(context.Background())
    order = nil
    applied := 0
    svc.SetAnswerHandler(func(context.Context, Answer) error { order = append(order, "apply"); applied++; return nil })
    _ = svc.HandleAction(context.Background(), Action{CardID: "gate-nr-1", Choice: 0, OperatorID: "ou_owner", MessageID: "om_1"})
    _ = svc.HandleAction(context.Background(), Action{CardID: "gate-nr-1", Choice: 1, OperatorID: "ou_owner", MessageID: "om_1"})
    if applied != 1 { t.Fatalf("applied = %d", applied) }
    if len(order) < 2 || order[0] != "patch" || order[1] != "apply" { t.Fatalf("order = %v", order) }
}

func TestServiceIgnoresNonOwnerAndExpiredCardDoesNotApply(t *testing.T) {
    ...
}
```

- [x] **Step 2: Run and verify failure**

Run: `go test ./server/internal/channels/feishu/ -run TestService -v`
Expected: FAIL with `undefined: New`.

- [x] **Step 3: Implement `service.go` / `service_click.go`**

`Notify` creates a card (`StateOpen`, `Version=1`, `Selected=-1`, `ExpiresAt=now+TTL`, choices from `Options` with values `approve/reject` for gate and `option-<i>` for ask, `notice` has `ack`), `Put` it, and returns; duplicate `CardID` leaves the existing card untouched. `Start` starts `DeliverOnce` every 2s and the supervisor with `Handle: s.HandleAction`. `DeliverOnce`: for each card with empty `MessageID`, send with key `mindfs-card-<id>`, store message ID and `RenderedVersion`; for cards with message ID and `RenderedVersion < Version`, patch; send failures record `LastError` and retry on the next tick. `HandleAction`:

1. ignore if `OperatorID != OwnerOpenID`;
2. ignore if `MessageID != "" && card.MessageID != "" && MessageID != card.MessageID`;
3. if expired, set `StateExpired`, patch, return;
4. if state != open, patch the current card (idempotent ack) and return;
5. validate choice/text; atomically set `StateApplying`, selected/text, bump version;
6. patch "已收到你的点击，正在处理…" **before** calling `AnswerHandler`;
7. call handler with the card fields; on success set `StateDone`, `Summary`, patch outcome; on error set `StateFailed`, `LastError`, patch failure text (do not re-open).

For gate `Answer.Decision` is `approve`/`reject`; for ask `Answer.Text` is the selected option label or free text; notice has no handler (directly done with note).

- [x] **Step 4: Run tests**

Run: `go test ./server/internal/channels/feishu/ -run TestService -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add server/internal/channels/feishu/service.go server/internal/channels/feishu/service_click.go server/internal/channels/feishu/service_test.go
git commit -m "feat(feishu): 卡片投递与点击状态机（Scope: G-NW）"
```

---

### Task 7: Kanban notification port and three creation points

**Files:**
- Create: `server/internal/kanban/card_notifier.go`
- Modify: `server/internal/kanban/service.go` (add `cardNotifier` field)
- Modify: `server/internal/kanban/workflow_driver.go` (`advanceWorkflowHumanGate`)
- Modify: `server/internal/kanban/activity_route_apply.go` (`applyRouteAskUser`)
- Modify: `server/internal/kanban/service_state_moves.go` (`setTaskStatus` terminal branch)
- Test: `server/internal/kanban/card_notifier_test.go`

**Interfaces:**
- Consumes: existing `Service`, `ActivityService`, `TaskActivity`.
- Produces:
  - `type CardNotice struct { ID string; Kind string; AccountID, RootID, TaskID, TaskTitle, WorkflowNodeID, NodeName, QuestionID, Prompt, Status, Summary string; TaskNumber int; Options []string }`
  - `const CardKindGate/CardKindAsk/CardKindNotice`
  - `type CardNotifier interface { NotifyCard(context.Context, CardNotice) error }`
  - `func (s *Service) SetCardNotifier(n CardNotifier)`

- [x] **Step 1: Write failing kanban tests**

```go
func TestHumanGateWaitingNotifiesCardOnce(t *testing.T) {
    h := newCardNotifierHarness(t)
    h.runToHumanGate(t)
    if got := h.notifier.byKind(CardKindGate); len(got) != 1 { t.Fatalf("gate notices = %+v", got) }
    if got := h.notifier.byKind(CardKindGate)[0]; got.ID == "" || got.RootID != h.root.ID || got.TaskID != h.task.ID {
        t.Fatalf("notice = %+v", got)
    }
    h.advanceAgain()
    if got := h.notifier.byKind(CardKindGate); len(got) != 1 { t.Fatalf("duplicate gate notice: %+v", got) }
}

func TestRouterAskUserNotifiesCardWithQuestionActivity(t *testing.T) { ... }

func TestTerminalTaskNotifiesNoticeButWaitingUserDoesNot(t *testing.T) {
    h := newCardNotifierHarness(t)
    h.setStatus(StatusWaitingUser)
    if got := h.notifier.byKind(CardKindNotice); len(got) != 0 { t.Fatalf("waiting_user notified: %+v", got) }
    h.setStatus(StatusSuccess)
    if got := h.notifier.byKind(CardKindNotice); len(got) != 1 { t.Fatalf("terminal notices = %+v", got) }
}
```

- [x] **Step 2: Run and verify failure**

Run: `go test ./server/internal/kanban/ -run 'TestHumanGateWaitingNotifies|TestRouterAskUserNotifies|TestTerminalTaskNotifies' -v`
Expected: FAIL because `CardNotifier`/`CardNotice` do not exist.

- [x] **Step 3: Implement the port and call sites**

`card_notifier.go` holds the interface, constants, and `notifyCard` which logs and swallows errors (a channel failure never changes task state). Add `cardNotifier CardNotifier` to `Service` and the setter. In `advanceWorkflowHumanGate`, after the waiting NodeRun/activity is committed, call `notifyCard` with ID `"gate-"+nodeRun.ID`, kind gate, task fields, workflow node ID/name, prompt, options `批准/驳回`. In `applyRouteAskUser`, after `markRouteReply`, call `notifyCard` with ID `"ask-"+act.ID`, kind ask, `QuestionID: act.ID`, the reply body as prompt, and action options. In `setTaskStatus`, after a successful `terminal` update and broadcast, call `notifyCard` with ID `"notice-"+taskID+"-"+status+"-"+strconv.FormatInt(now.UnixNano(),10)`, kind notice, task title/number, status, reason. Do not notify when the task was already terminal.

- [x] **Step 4: Run tests**

Run: `go test ./server/internal/kanban/ -run 'TestHumanGateWaitingNotifies|TestRouterAskUserNotifies|TestTerminalTaskNotifies' -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add server/internal/kanban/card_notifier.go server/internal/kanban/service.go server/internal/kanban/workflow_driver.go server/internal/kanban/activity_route_apply.go server/internal/kanban/service_state_moves.go server/internal/kanban/card_notifier_test.go
git commit -m "feat(kanban): 卡片通知端口与三个创建点（Scope: G-NW）"
```

---

### Task 8: Ask answer write-back

**Files:**
- Modify: `server/internal/kanban/activity_service.go` (new `CreateUserReply`)
- Modify: `server/internal/kanban/activity_store.go` (question reply lookup)
- Test: `server/internal/kanban/activity_service_reply_test.go`

**Interfaces:**
- Consumes: `ActivityStore.Get`, `insertUserActivity`, `dispatchUserActivity`.
- Produces:
  - `var ErrQuestionAlreadyAnswered = errors.New("question already answered")`
  - `func (s *ActivityService) CreateUserReply(ctx context.Context, taskID, body, replyToID string) (*TaskActivity, *TaskActivity, error)`

- [x] **Step 1: Write failing tests**

```go
func TestCreateUserReplyBindsQuestionAndDispatches(t *testing.T) {
    h := newAskHarness(t) // creates task + waiting_user question activity
    userAct, agentAct, err := h.svc.CreateUserReply(context.Background(), h.task.ID, "先做 A", h.question.ID)
    if err != nil { t.Fatal(err) }
    if userAct.ReplyToID != h.question.ID || userAct.Body != "先做 A" { t.Fatalf("user = %+v", userAct) }
    if agentAct == nil { t.Fatal("dispatch did not create a reply") }
}

func TestCreateUserReplyIsIdempotentAndRejectsAnsweredQuestion(t *testing.T) {
    h := newAskHarness(t)
    first, _, err := h.svc.CreateUserReply(context.Background(), h.task.ID, "A", h.question.ID); if err != nil { t.Fatal(err) }
    second, _, err := h.svc.CreateUserReply(context.Background(), h.task.ID, "A", h.question.ID)
    if err != nil || second.ID != first.ID { t.Fatalf("second=%+v err=%v", second, err) }
    h.answerQuestion(t, h.question.ID)
    if _, _, err := h.svc.CreateUserReply(context.Background(), h.task.ID, "B", h.question.ID); !errors.Is(err, ErrQuestionAlreadyAnswered) {
        t.Fatalf("err = %v", err)
    }
}
```

- [x] **Step 2: Run and verify failure**

Run: `go test ./server/internal/kanban/ -run TestCreateUserReply -v`
Expected: FAIL with `CreateUserReply undefined`.

- [x] **Step 3: Implement `CreateUserReply`**

Load the question with `activities.Get`; require `TaskID == taskID` and `Status == ActivityStatusWaitingUser` (or the existing waiting status constant), else `ErrQuestionAlreadyAnswered`. Look up `UserReplyForQuestion(ctx, replyToID)`; if present return it with the existing agent reply (idempotent). Insert a queued user activity with `insertUserActivity`, set `ReplyToID`, persist via a new store method `SetReplyTo`, then call the same dispatch path as `CreateUserActivity`. Do not change `CreateUserActivity`'s signature.

- [x] **Step 4: Run tests**

Run: `go test ./server/internal/kanban/ -run TestCreateUserReply -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add server/internal/kanban/activity_service.go server/internal/kanban/activity_store.go server/internal/kanban/activity_service_reply_test.go
git commit -m "feat(kanban): 卡片回答写回等待中的提问（Scope: G-NW）"
```

---

### Task 9: App wiring, config, and scheduled notices

**Files:**
- Create: `server/internal/api/appcontext_feishu.go`
- Modify: `server/internal/api/appcontext.go` (add `Feishu *feishu.Service`)
- Modify: `server/internal/api/appcontext_broadcast.go` (scheduled done/failed)
- Modify: `server/app/server.go` (`StartOptions.Feishu`, create/start service, answer handler)
- Modify: `server/app/workspace_build.go` (set `Feishu`, `Kanban.SetCardNotifier`)
- Modify: `cli/cmd/startup.go` (config struct, defaults, apply)
- Modify: `cli/cmd/mindfs.go` (pass `FeishuOptions` into `StartOptions`)
- Test: `server/internal/api/appcontext_feishu_test.go`, `cli/cmd/startup_feishu_test.go`

**Interfaces:**
- Consumes: Tasks 1–8.
- Produces:
  - `type FeishuOptions struct { Enabled bool; OwnerOpenID, LarkCLIPath string; CardTTL time.Duration }` in `server/app`
  - `func (s *AppContext) ApplyFeishuAnswer(ctx context.Context, a feishu.Answer) error`
  - `func (n feishuCardNotifier) NotifyCard(ctx context.Context, notice kanban.CardNotice) error`

- [x] **Step 1: Write failing config tests**

```go
func TestStartupConfigReadsFeishuChannel(t *testing.T) {
    cfg := startupConfig{Channels: &startupChannelsConfig{Feishu: &startupFeishuConfig{
        Enabled: boolPtr(true), OwnerOpenID: strPtr("ou_owner"), LarkCLI: strPtr("lark-cli"), CardTTLHours: intPtr(24)}}}
    enabled, owner, path, ttl := false, "", "", 0
    if err := applyStartupConfig(cfg, map[string]bool{}, startupFlags{
        FeishuEnabled: &enabled, FeishuOwnerOpenID: &owner, FeishuLarkCLI: &path, FeishuCardTTLHours: &ttl}); err != nil { t.Fatal(err) }
    if !enabled || owner != "ou_owner" || path != "lark-cli" || ttl != 24 { t.Fatalf("got %v %q %q %d", enabled, owner, path, ttl) }
}

func TestFeishuDefaultsAreSafe(t *testing.T) {
    if enabled, _, path, ttl := feishuDefaults(); enabled || path != "lark-cli" || ttl != 168 { ... }
}
```

- [x] **Step 2: Run and verify failure**

Run: `go test ./cli/cmd/ -run TestStartupConfigReadsFeishu -v`
Expected: FAIL with `undefined: startupChannelsConfig`.

- [x] **Step 3: Implement config and wiring**

Add `Channels *startupChannelsConfig` to `startupConfig` with nested `Feishu`; add pointer fields to `startupFlags`; apply only when the pointer is non-nil, normalizing `CardTTLHours <= 0` to 168 and empty `LarkCLI` to `lark-cli`. `StartOptions.Feishu` carries the values. In `Start`, create `feishu.New` with path `<config-dir>/channels/feishu/cards.json`, `NodeID` from `opts.Feishu` (empty allowed), and a CLI path; check `exec.LookPath` and log + skip when missing. Set `shared.feishu` before building workspaces. After `workspaces` exists, call `SetAnswerHandler` that resolves `workspaces.Workspace(answer.AccountID)` and calls `ApplyFeishuAnswer`. In `build`, set `AppContext.Feishu` and `Kanban.SetCardNotifier(feishuCardNotifier{svc: m.shared.feishu, accountID: userID})`. `feishuCardNotifier` maps `kanban.CardNotice` to `feishu.Notice` (gate options `批准/驳回`; ask options as labels). `ApplyFeishuAnswer` gates on `a.Kind`: gate calls `Kanban.ResolveWorkflowHumanGate(ctx, RootID, TaskID, WorkflowNodeID, Decision, "", Note)`; ask calls `ActivityServiceForRoot(RootID).CreateUserReply(ctx, TaskID, Text, QuestionID)`. Map `kanban.ErrQuestionAlreadyAnswered` to a friendly `已在网页处理` error. In `BroadcastScheduledTaskDone/Failed`, after webpush, call `s.Feishu.Notify` with kind notice, ID `scheduled-<taskID>-<status>`, task name and summary; errors only log.

- [x] **Step 4: Run tests**

Run: `go test ./cli/cmd/ -run TestStartupConfigReadsFeishu -v && go test ./server/internal/api/ -run TestFeishu -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add server/internal/api/appcontext_feishu.go server/internal/api/appcontext.go server/internal/api/appcontext_broadcast.go server/internal/api/appcontext_feishu_test.go server/app/server.go server/app/workspace_build.go cli/cmd/startup.go cli/cmd/mindfs.go cli/cmd/startup_feishu_test.go
git commit -m "feat(feishu): 配置、应用装配与定时任务通知（Scope: G-NW）"
```

---

### Task 10: Terminal paths, integration test, registry, gates

**Files:**
- Modify: `server/internal/kanban/workflow_human_gate.go` (reject path notice)
- Modify: `server/internal/kanban/workflow_runtime.go` (cancel path notice)
- Create: `server/internal/channels/feishu/integration_test.go`
- Modify: `docs/upstream-customizations.yaml` (new group G-NW)
- Test: `server/internal/channels/feishu/integration_test.go`, `server/internal/kanban/card_notifier_test.go`

**Interfaces:**
- Consumes: all prior tasks.
- Produces: no new API; closes the "every terminal path notifies once" rule.

- [x] **Step 1: Write failing integration and terminal-path tests**

```go
func TestFakeLarkEndToEndGateApprove(t *testing.T) {
    bin, state := fakeLarkWithEvents(t, `{"type":"card.action.trigger","operator_id":"ou_owner","message_id":"om_1","action_value":"{\"card\":\"gate-nr-1\",\"choice\":0}"}`)
    st := NewStore(filepath.Join(t.TempDir(), "cards.json")); _ = st.Load()
    svc := New(Config{OwnerOpenID: "ou_owner", LarkCLIPath: bin, CardTTL: time.Hour, Now: fixedNow,
        Logf: func(string, ...any) {}}, st, &CLI{Path: bin, Timeout: time.Second})
    var got Answer
    svc.SetAnswerHandler(func(_ context.Context, a Answer) error { got = a; return nil })
    _ = svc.Notify(context.Background(), Notice{CardID: "gate-nr-1", Kind: KindGate, RootID: "root", TaskID: "task-1", Prompt: "确认继续"})
    ctx, cancel := context.WithCancel(context.Background()); defer cancel()
    svc.Start(ctx)
    waitFor(t, 3*time.Second, func() bool { return got.Decision == "approve" })
    if !strings.Contains(readFile(t, state), "已收到") { t.Fatal("ack patch missing") }
    c, _ := st.Get("gate-nr-1"); if c.State != StateDone { t.Fatalf("state = %s", c.State) }
}

func TestGateRejectAndWorkflowCancelNotifyNoticeOnce(t *testing.T) { ... }
```

- [x] **Step 2: Run and verify failure**

Run: `go test ./server/internal/channels/feishu/ -run TestFakeLarkEndToEnd -v && go test ./server/internal/kanban/ -run 'TestGateReject|TestWorkflowCancel' -v`
Expected: FAIL (integration wiring or missing notice call).

- [x] **Step 3: Implement direct terminal notices and registry**

In `workflow_human_gate.go`, after a successful reject writes `StatusCancelled`, call `notifyCard` with kind notice, `ID: "notice-"+taskID+"-cancelled-gate"`, title/reason. In `workflow_runtime.go`, after a successful cancel writes a terminal status, do the same with `"cancelled-run"`. Guard against duplicate notices by checking the before-status is non-terminal. Add group `G-NW` to `docs/upstream-customizations.yaml` with: new directory `server/internal/channels/feishu/`, modified kanban/api/app/cli files, and anchors `func (s *Service) SetCardNotifier`, `type CardNotifier interface`, `func (s *AppContext) ApplyFeishuAnswer`, `func (s *ActivityService) CreateUserReply`, `json:"channels"`. The group's "why" says M1 is MindFS-local outbound/click only; no relay and no credentials stored.

- [x] **Step 4: Run full gates**

Run:

```bash
gofmt -w server/internal/channels/feishu server/internal/kanban/card_notifier.go server/internal/kanban/activity_service.go server/internal/api/appcontext_feishu.go server/app server/cli 2>/dev/null || true
go test ./server/internal/channels/feishu/ ./server/internal/kanban/ ./server/internal/api/ ./server/app/ ./cli/cmd/ -count=1
go test ./... -count=1
go build ./...
```

Expected: all PASS. If `server/internal/kanban` is slow, run it alone and record the time; do not skip.

- [x] **Step 5: Commit and check upstream**

```bash
git add server/internal/channels/feishu/integration_test.go server/internal/kanban/workflow_human_gate.go server/internal/kanban/workflow_runtime.go docs/upstream-customizations.yaml server/internal/kanban/card_notifier_test.go
git commit -m "test(feishu): 假 CLI 端到端与终态路径收口（Scope: G-NW）"
git worktree add --detach /tmp/mindfs-check-gnw HEAD
cd /tmp/mindfs-check-gnw && make check-upstream
```

Expected: `✓ 全部通过`.

---

## Self-Review

- Spec §4.1/§5/§9/§11 M1 covered: card store, renderer, sender, supervisor, three creation points, click ack + apply, owner-only, expiry, idempotency, resend unsent, scheduled notices.
- Not in M1: `im.message.receive_v1`, project picker, relay token, worker relay, cross-node dispatch (M2/M4).
- Review Focus items are each pinned by a task test: stale click (Task 6/8), duplicate click (Task 6), expiry (Task 1/6), lark failure (Task 3/5), terminal paths (Task 7/10).
- Type names are consistent across tasks: `Card`, `Notice`, `Answer`, `CardNotice`, `CardNotifier`, `CreateUserReply`.
- Config keys match the spec sample; only the M1 subset is read, unknown keys are ignored so M2/M4 can add them without migration.

## 执行记录（2026-11-21）

**M1 全部 10 个任务已实现并提交**（每个任务 RED→GREEN→提交，提交信息均带 `Scope: G-NW`）：

| 任务 | 提交 | 交付 |
|---|---|---|
| T1 卡片模型与原子 JSON 仓库 | `f8704727` | `types.go` / `store.go`：`Card` 状态机、原子写（temp+rename）、TTL 过期、重启加载 |
| T2 schema 2.0 渲染 | `2830c94b` | `render.go`：gate/ask/notice 三种卡片、选项按钮、已处理态 |
| T3 lark-cli 发送与补丁适配 | `098565dc` | `cli.go`：`im message send` / `messages patch`，超时与错误透传 |
| T4 点击事件解析 | `e1908257` | `event.go`：`card.action.trigger` → `Answer`，owner 校验、陈旧/重复点击判据 |
| T5 点击长连接 supervisor | `091f0939` | `supervisor.go`：`event consume` 常驻、退避重启、context 收口 |
| T6 投递与点击状态机 | `64bb05f6` | `service.go` / `service_click.go`：先回执再交给应用层、幂等、未发送重投 |
| T7 回答写回等待中的提问 | `079cc346` | `ActivityService.CreateUserReply` + `AppContext.ApplyFeishuAnswer`：gate 走 `ResolveWorkflowHumanGate`，ask 走网页同一条留言链路 |
| T8 卡片通知端口与三个创建点 | `7647c661` | `kanban.CardNotifier`；HumanGate 等待、Router ask_user、任务终态三处通知 |
| T9 通道接线、config 与定时任务卡片 | `d7476a81` | `server/app/feishu.go`、`appcontext_notify.go`、`cli/cmd/startup_feishu.go`：`channels.feishu.*` 只走 `config.json`，缺省关闭 |
| T10 终态路径、端到端、登记、门禁 | `90f528e3` | 驳回/取消两条绕开 `setTaskStatus` 的路径补 notice；`integration_test.go` 假 lark-cli 真跑「收事件 → 认 owner → 先回执 → 再应用」；G-NW 登记 |

**门禁（2026-11-21）**：

- `go test -count=1 -timeout 40m ./...` 全绿（0 FAIL）。
- `npx tsc -b --noEmit` clean；`npx vitest run` 254 文件 / 814 通过 / 6 跳过。
- 干净 worktree（`90f528e3`）`make check-upstream` 首跑 3 红，均在本轮修掉：G-NW 的 `kind: feat` → `feature`；`CreateUserReply` 锚点是多行签名，改取首行 `func (s *ActivityService) CreateUserReply(`；计划文档当时未提交、不在 delta（本提交一并纳管）。

**边界（与 spec §2 一致，M1 不做）**：群聊与非 owner 点击、图片/语音/文件入站、跨节点中继（M4）、项目选择器与入口绑定（M2）、完整规则引擎。凭据不落 MindFS 库：`lark-cli` 自己持有登录态，MindFS 只记 owner_open_id 与 CLI 路径。

**未部署**（截至复审轮）：本机二进制未重编安装，四节点均未部署、未重启。上线需在 `config.json` 填 `channels.feishu.enabled=true` + `owner_open_id`，并确认 `lark-cli` 已登录。部署与随后查出的监听缺陷见下一节。

### 整分支复审（2026-11-21，`7837b1fd`）

整条 M1 分支（`f8704727..90f528e3`）过一遍全新上下文的复审，8 条 Critical/Important 一次性修完，每条先写出会红的测试：

| 发现 | 后果 | 修法 | 钉住它的用例 |
|---|---|---|---|
| `newFeishuChannel` 读不出卡片库时当成空库继续启动 | 重启后已答过的 gate 卡被当成新点击，再答一次 | 读失败即不建通道（文件原样留着，修好再启动） | `TestNewFeishuChannelRefusesToStartOnCorruptState` |
| 进程死在「已回执、未应用」之间，卡片永久停在 `applying` | 之后的每次点击只补一次「正在处理…」，人再也答不了 | `Store.Load` 把 `applying` 放回 `open`（版本 +1）并落盘；两条写回路径本身幂等 | `TestLoadRecoversApplyingCardsToOpen` |
| 事件流中途断掉时监听 goroutine 静默退出 | 飞书点击从此再也收不到，进程看着还活着 | 扫描结束且 ctx 未取消时收掉子进程，交给 supervisor 重连 | `TestSupervisorReconnectsWhenAStdoutLineIsTooLong` |
| 单次点击处理没有超时 | 一次卡住的写回拖死整个监听循环 | 每次点击一个带超时的 ctx（默认 2 分钟） | `TestSupervisorHandlerTimeoutDoesNotBlockReconnect` |
| `Put`/`Update`/`MarkDelivered`/`RecordDeliveryError` 落盘失败后内存已改 | 内存与磁盘长期分叉，重启后状态回退 | 失败时把内存回滚到改前 | `TestPutRollsBackMemoryWhenSaveFails` |
| `lark-cli` 在 JSON 前打警告行时整段解析失败 | 一次成功发送被记成失败并被反复重发 | 先整段解析，失败再取最后一个完整 JSON 对象 | `TestCLIIgnoresLinesBeforeTheJSONReply` |
| `cards.json` 只增不减 | 文件随任务数无限增长，每拍在锁里重写整份 | 按 TTL 清理（`Load` 与每拍投递各一次，`applying` 保留） | `TestPruneExpiredDropsOldCardsButKeepsLiveOnes`、`TestDeliverOnceDropsExpiredCards` |
| 取消 ctx 不等于循环真的停了 | 测试 TempDir 清理与在飞的写盘互抢（`-race` 下 3/40 复现） | `Service.Wait()` 等两个循环退出，integration 测试 defer 里用 | 复跑 40/40 绿（变异：`Wait` 空实现 → 7/40 红） |

变异验证：事件流中断与点击超时两处各自打红后还原；`Wait` 空实现 7/40 复现清理失败。门禁：干净 worktree（`86aeee6b`）`go build ./...` 成功、`go test -count=1 ./...` 34 个包全绿（0 FAIL）、`make check-upstream` 2279 delta / 350 组全绿；`go test -race ./server/internal/channels/feishu/` 绿。全量套件前三轮各红一条互不相同、且都不在本组改动范围内的用例（`kanban` 的 `TestRouterRouteTimeoutWithoutActionsNotifiesAndFallsBack` 撞 sqlite readonly、`agent/proxy` 的 `TestCodexHarnessOverrideSelectsItsOwnRail` 撞 TempDir 清理、`api` 的 `TestWorkflowTaskPauseResumeCancelMapToRunLifecycle` 撞异步状态未落定），每条单跑与 `-count=3..5` 重复均绿，属既有资源竞争型 flake。

**复审后仍未处理（Minor，记录在案）**：

- `activity_route_apply.go` 的 ask 通知内联了一段与 `Service.notifyCard` 行为等价的调用（`ActivityService` 与 `Service` 是两个接收者，共用需要抽包级函数；日志格式与失败隔离语义已一致），纯重复无行为差异。
- `feishu.StateSuperseded` 有渲染分支但从无赋值点；`Card.NodeID` 会随点击回传到 `Answer`，但 M1 没有创建点填它（`CardNotice` 没有该字段）。两者都是预留给后续里程碑的字段，删除会丢语义，保留。
- `sendCard` 成功后 `message_id` 为空串的匹配、`ExpiresAt` 为零值的卡片（新卡不从这条路径产生）、notice 卡挂在 `GetDetail` 之后 —— 均无可达路径。

### 部署核对与监听 stdin 缺陷（2026-11-21）

四端已 ship 并重启：本机 / wsl / fn / pc-win 均 `v0.5.5-1200-185-gd91f03e3`（逐字一致，无 `-dirty`，HEAD == origin/main，M1 提交在 HEAD 内）。本机 PID 740558 于 13:16:35 启动，`/proc/<pid>/exe` 与 `~/.local/bin/mindfs` 同一 inode；wsl / fn `is-active` + health 200 `worker`；pc-win `:7332` LISTENING + health 200 `worker`。服务单元 `User=xiaokubao`、`HOME` 与 `PATH`（含 nvm 的 `lark-cli`）均正确。

核对飞书运行时状态时查出一个会让 M1 完全失效的缺陷：`Supervisor.consume` 用 `exec.CommandContext` 起 `lark-cli event consume` 却没设 `Stdin`，子进程拿到 `/dev/null`；而 lark-cli 1.0.100 把 stdin EOF 当停机信号，实测 1.5 秒即自行退出（`stdin closed — shutting down` → `exited — received 0 event(s)`）。后果是开 `channels.feishu.enabled=true` 后 supervisor 只会「起 1.5 秒 → 退 → 10 秒后重连」空转，点击一条都收不到。`integration_test.go` 的假 CLI 不读 stdin，所以原套件看不见。同轮修掉同一函数里的收尾方式：`CommandContext` 默认 SIGKILL，而 lark-cli 警告 SIGKILL 会泄漏服务端订阅、重启后数小时报 `another event bus is already connected`（正是 supervisor 60 秒退避在等的冲突），改为 `cmd.Cancel` 发 SIGTERM + `WaitDelay` 5s。

修法：`os.Pipe()` 给子进程一根活到退出才关的 stdin（父进程 `Start` 后关读端、`Wait` 后关写端）。用例先红后绿：`TestSupervisorKeepsListenerStdinOpen`（修前 300ms 内重启 13 次）、`TestSupervisorStopsListenerWithSIGTERM`（修前等不到 TERM）。真 `lark-cli` 复核：同样的 exec 形状下 `feishu-websocket: connected`、8 秒仍在跑、SIGTERM 正常收尾。`go test -race ./server/internal/channels/feishu/` 绿、`go build ./...` 成功。

**尚未验证**：真卡片端到端（`config.json` 目前没有 `channels` 段，通道处于关闭状态）。下一步是填 `channels.feishu`（`enabled` / `owner_open_id` / `lark_cli`）→ 用户重启本机 → 发一张 HumanGate 卡走通「先回执后执行」。

### 上线与真卡片端到端（2026-11-21，本机）

重部署一轮后四端为 `v0.5.5-1201-39-g6159f483`（构建自 origin/main `6159f483`，含 `93125ed6` 的 stdin/SIGTERM 修复；本机、wsl、fn `is-active`/health 200，pc-win PID 50752 + `:7332` LISTENING + health 200）。`config.json` 新增 `channels.feishu = {enabled: true, owner_open_id: ou_…674f, card_ttl_hours: 168}`（只写本机：飞书一个应用只允许一条事件连接，跨节点要等 M4 中继），备份 `config.json.bak-20261121-feishu`。自检卡经真服务发出成功（`message_id om_x100b7f2e33d0a0a0ce81abfb8ce680f`）。

用户重启本机后的运行时核对：

- system 单元 22:38:24 重启，PID 1223682；`/proc/1223682/exe` 与 `~/.local/bin/mindfs` 同一 inode（262346），`mindfs --version` = `v0.5.5-1201-39-g6159f483`，`/mindfs/health` → `{"ok":true,"role":"control"}`。
- 日志 `[feishu] 通道已启用 owner=…96774f node=ubuntu state=…/feishu/cards.json` → `lark-cli: [event] ready event_key=card.action.trigger` → `[source] feishu-websocket: connected`；监听进程连续存活 ≥9 分钟，`飞书卡片监听退出了` 与 `another event bus is already connected` 各 0 行（修复前是 1.5 秒自退、每 10 秒一行重连）——stdin/SIGTERM 修复在真实通道上生效。
- `~/.config/mindfs/feishu/cards.json` 以 0600 落盘、目录 0700。

真卡片端到端（走服务自己的创建点，不是直接调 `lark-cli`）：

1. 临时发布 workflow `feishu-gate-e2e:v1`：`gate(human_gate) → done(notify)`，无 agent 节点、不调模型、不碰 worktree。
2. 在 `proj` 根建任务 `task_667fa660ccceed70`（`workflow_choice=specified`），任务停在 `waiting_user`，卡片 `gate-run-deb14034340d96d8:gate::0:1` 发出到 owner：`message_id om_x100b7f2ea1ffcca0cd1c726a3801856`，选项「批准 / 驳回」。
3. 用户在飞书点「批准」（延迟约 19 分钟，仍远在 TTL 内）。核对结果：卡片 `state=done`、`selected=0`、`rendered_version` 1→3（回执与终态各一次补丁）；`human_waiting` 活动 `status=completed`、`decision=approve`；Run 投影 `gate=success`、`done=success`；任务摘要写明审批结果与 `feishu e2e done`。任务停在 `waiting_user` 是设计语义（Run 成功只回到待审核，成功/失败由显式动作写），不是缺陷。
4. 清理：删除临时任务与 workflow（`GET /api/workflows/published` 已不含该条目，`proj` 板面为空）。

**未覆盖**：群聊与非 owner 点击、多账户并发、跨节点中继（M4）、入口与项目选择卡（M2）。「先回执后执行」的顺序由单测钉住，运行时证据是 `rendered_version` 的两次递增与 `done` 落定。

**范围裁定（2026-11-21，用户）**：「现在这个出入口就够了」——M1 的出卡 + 点击闭环即最终范围；M2（飞书消息建任务 / 项目选择卡）、M4（跨节点中继）不做，群聊与非 owner 点击、多账户并发也不补验收。飞书配置因此只保留在本机（单事件连接约束不再是待解问题）。
