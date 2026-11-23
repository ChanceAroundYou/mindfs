package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/channels/feishu"
	"mindfs/server/internal/nodes"
)

const (
	feishuITOwner     = "ou_owner"
	feishuITMessageEv = `{"header":{"event_id":"ev-intake","event_type":"im.message.receive_v1"},"event":{"sender":{"sender_id":{"open_id":"ou_owner"}},"message":{"message_id":"om-intake","chat_id":"oc_intake","chat_type":"p2p","message_type":"text","content":"{\"text\":\"在 fn 的 docs 建个任务\"}","create_time":"1700000000000"}}}`
	feishuITClickEv   = `{"type":"card.action.trigger","event_id":"ev-click","operator_id":"ou_owner","message_id":"om_sent","action_tag":"button","action_name":"card_button_click","action_value":"{\"card\":\"card-relay-1\",\"choice\":0}"}`
	feishuITPickerEv  = `{"header":{"event_id":"ev-picker","event_type":"im.message.receive_v1"},"event":{"sender":{"sender_id":{"open_id":"ou_owner"}},"message":{"message_id":"om-picker","chat_id":"oc_pick","chat_type":"p2p","message_type":"text","content":"{\"text\":\"docs wiki 建个任务\"}","create_time":"1700000000000"}}}`
)

type feishuITCatalog struct{ projects []feishu.ProjectRef }

func (c feishuITCatalog) List(context.Context) ([]feishu.ProjectRef, error) { return c.projects, nil }

// feishuITLark writes a lark-cli stand-in that emits one event per subscription.
// Feishu requires a separate long connection per event type, so the two ready
// files are what make the test deterministic rather than racing the supervisor.
func feishuITLark(t *testing.T, dir string, events map[string]string) (path, log string, ready map[string]string) {
	t.Helper()
	path = filepath.Join(dir, "fake-lark.sh")
	log = filepath.Join(dir, "lark.log")
	ready = map[string]string{}
	var branches strings.Builder
	branches.WriteString("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + log + "\"\n")
	branches.WriteString("if [ \"$1\" = \"event\" ]; then\n  typ=\"$3\"\n")
	first := true
	for eventType, event := range events {
		key := eventType
		if key == "" {
			key = "card.action.trigger"
		}
		readyFile := filepath.Join(dir, "ready-"+strings.ReplaceAll(key, ".", "_"))
		ready[key] = readyFile
		prefix := "  if"
		if !first {
			prefix = "  elif"
		}
		first = false
		branches.WriteString(prefix + " [ \"$typ\" = \"" + key + "\" ]; then\n")
		branches.WriteString("    i=0\n    while [ ! -f \"" + readyFile + "\" ]; do i=$((i+1)); [ $i -ge 400 ] && exit 0; sleep 0.05; done\n")
		branches.WriteString("    printf '%s\\n' '" + event + "'\n    exec sleep 30\n")
		branches.WriteString("  fi\n")
	}
	branches.WriteString("fi\nprintf '%s\\n' '{\"ok\":true,\"data\":{\"message_id\":\"om_sent\"}}'\n")
	if err := os.WriteFile(path, []byte(branches.String()), 0o700); err != nil {
		t.Fatal(err)
	}
	return path, log, ready
}

func feishuITWaitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待超时：%s", what)
}

func feishuITService(t *testing.T, dir, lark string) *feishu.Service {
	t.Helper()
	store := feishu.NewStore(filepath.Join(dir, "cards.json"))
	return feishu.New(feishu.Config{
		OwnerOpenID: feishuITOwner,
		NodeID:      "home",
		LarkCLIPath: lark,
		Logf:        func(string, ...any) {},
	}, store, &feishu.CLI{Path: lark, Timeout: 5 * time.Second})
}

type feishuITTaskCall struct {
	method string
	path   string
	body   []byte
	token  string
}

// TestFeishuIntakeEndToEndRoutesToRemoteTask drives the real lark event
// supervisor, intake policy, node table and feishuTaskCreator. No fake creator:
// the only way to create the task is an HTTP POST to the fn node's server.
func TestFeishuIntakeEndToEndRoutesToRemoteTask(t *testing.T) {
	dir := t.TempDir()
	lark, _, ready := feishuITLark(t, dir, map[string]string{
		"im.message.receive_v1": feishuITMessageEv,
	})
	nodesStore := feishuAnswerNodeStore(t,
		nodes.NodeConnection{ID: "home", Name: "home", URL: "http://127.0.0.1:1"},
		nodes.NodeConnection{ID: "fn", Name: "fn", URL: "http://127.0.0.1:2"},
	)
	var taskServer *httptest.Server
	calls := make(chan feishuITTaskCall, 8)
	taskServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			t.Errorf("read task body: %v", err)
		}
		calls <- feishuITTaskCall{method: r.Method, path: r.URL.Path, body: body}
		w.Header().Set("Content-Type", "application/json")
		// The confirmation card first asks for candidates, then a click creates
		// the task. Answer each shape so neither step sees a bad response.
		if r.Method == http.MethodGet && r.URL.Path == "/api/tasks" {
			_, _ = io.WriteString(w, `{"items":[]}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"task-1"}`)
	}))
	defer taskServer.Close()
	if _, err := nodesStore.Upsert(nodes.NodeConnection{ID: "fn", Name: "fn", URL: taskServer.URL}); err != nil {
		t.Fatal(err)
	}

	svc := feishuITService(t, dir, lark)
	svc.SetAnswerHandler(func(context.Context, feishu.Answer) error { return nil })
	opts := FeishuOptions{OwnerOpenID: feishuITOwner, PickerTTL: 24 * time.Hour, PickerMax: 5}
	if err := prepareFeishuChannel(svc, opts, FeishuIntakeDeps{
		ConfigDir: dir,
		NodeID:    "home",
		Nodes:     nodesStore,
		Catalog: feishuITCatalog{projects: []feishu.ProjectRef{
			{NodeID: "fn", RootID: "root-docs", Name: "docs · fn"},
		}},
		Creator: &feishuTaskCreator{nodes: nodesStore, localNode: "home"},
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); svc.Wait() }()
	svc.Start(ctx)
	if err := os.WriteFile(ready["im.message.receive_v1"], []byte("go"), 0o600); err != nil {
		t.Fatal(err)
	}

	select {
	case call := <-calls:
		if call.method != http.MethodGet || call.path != "/api/tasks" {
			t.Fatalf("确认卡生成后应先查候选任务，got %s %s", call.method, call.path)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("确认卡没有查询 fn 节点的任务候选")
	}

	// Explicit target confirmation: the message only opens a card; nothing is
	// written until the owner clicks 「确认」.
	cardStore := feishu.NewStore(filepath.Join(dir, "cards.json"))
	var card feishu.Card
	feishuITWaitFor(t, "目标确认卡", func() bool {
		if err := cardStore.Load(); err != nil {
			return false
		}
		found, ok := cardStore.FindOpenTargetByChat("oc_intake")
		if !ok {
			return false
		}
		card = found
		return true
	})
	if card.Target == nil || card.Target.RootID != "root-docs" || card.TargetMode != "create" {
		t.Fatalf("确认卡 = target=%+v mode=%q", card.Target, card.TargetMode)
	}
	if err := svc.HandleAction(ctx, feishu.Action{
		CardID: card.ID, Choice: 0, MessageID: card.MessageID, OperatorID: feishuITOwner,
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case call := <-calls:
		if call.method != http.MethodPost || call.path != "/api/tasks" {
			t.Fatalf("remote task request = %s %s", call.method, call.path)
		}
		var got struct {
			RootID           string `json:"root_id"`
			Input            string `json:"input"`
			Name             string `json:"name"`
			WorkflowChoice   string `json:"workflow_choice"`
			StartImmediately bool   `json:"start_immediately"`
		}
		if err := json.Unmarshal(call.body, &got); err != nil {
			t.Fatalf("task body is not JSON: %v", err)
		}
		if got.RootID != "root-docs" || got.Input != "在 fn 的 docs 建个任务" ||
			got.Name != feishuTaskName(got.Input) || got.WorkflowChoice != "empty" || !got.StartImmediately {
			t.Fatalf("task body = %+v", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("owner 点击确认后没有在 fn 节点建出任务")
	}

	// 一条消息只建一个任务：确认之后不应再有第二条建任务请求。
	select {
	case duplicate := <-calls:
		if duplicate.method == http.MethodPost && duplicate.path == "/api/tasks" {
			t.Fatalf("重复投递建出了第二个任务：%s %s", duplicate.method, duplicate.path)
		}
	case <-time.After(300 * time.Millisecond):
	}

	// Picker TTL is a product promise (24h). Pin it here so a narrowed TTL
	// cannot silently make a card expire before the owner sees it.
	if feishu.PickerTTL != 24*time.Hour {
		t.Fatalf("feishu.PickerTTL = %s, want 24h", feishu.PickerTTL)
	}
}

// TestServerRunPreparesFeishuIntakeBeforeStart pins the production assembly:
// a test that only calls AttachFeishuIntake directly cannot catch a regression
// where server.Run forgets to install the inbound side at all.
func TestServerRunPreparesFeishuIntakeBeforeStart(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	const call = "prepareFeishuChannel(feishuChannel, opts.Feishu, FeishuIntakeDeps{"
	装配 := strings.Index(text, call)
	if 装配 < 0 {
		t.Fatal("server.Run 没有调用 prepareFeishuChannel")
	}
	start := strings.Index(text, "feishuChannel.Start(ctx)")
	if start < 0 || 装配 > start {
		t.Fatal("入站装配必须发生在 feishuChannel.Start(ctx) 之前")
	}
	block := strings.Join(strings.Fields(text[装配:start]), " ")
	for _, field := range []string{
		"ConfigDir: configDir", "NodeID: opts.FeishuNodeID",
		"Nodes: sharedNodes", "Workspace: workspaces",
	} {
		if !strings.Contains(block, field) {
			t.Fatalf("生产装配缺少 %s", field)
		}
	}
	if !strings.Contains(text, "[feishu] 入站装配失败，M2 不启用: %v") {
		t.Fatal("装配失败必须有可见日志，不能静默降级")
	}
}

func TestPrepareFeishuChannelAllowsEmptyNodeID(t *testing.T) {
	dir := t.TempDir()
	svc := feishuITService(t, dir, filepath.Join(dir, "no-lark"))
	opts := FeishuOptions{OwnerOpenID: feishuITOwner}
	if err := prepareFeishuChannel(svc, opts, FeishuIntakeDeps{
		ConfigDir: dir,
		Catalog:   feishuITCatalog{},
		Creator:   &feishuTaskCreator{nodes: feishuAnswerNodeStore(t), localNode: "home"},
	}); err != nil {
		t.Fatalf("控制节点没有 feishu_node_id 也应能装配入站：%v", err)
	}
}

// TestPrepareFeishuChannelInstallsPickerTaskDeps drives a real ambiguous DM and
// a real card click. Without SetPickerTaskDeps the click only reports
// "选择卡缺少建任务依赖" and no task POST ever reaches the chosen node.
func TestPrepareFeishuChannelInstallsPickerTaskDeps(t *testing.T) {
	dir := t.TempDir()
	lark, _, ready := feishuITLark(t, dir, map[string]string{
		"im.message.receive_v1": feishuITPickerEv,
	})
	nodesStore := feishuAnswerNodeStore(t,
		nodes.NodeConnection{ID: "home", Name: "home", URL: "http://127.0.0.1:1"},
		nodes.NodeConnection{ID: "fn", Name: "fn", URL: "http://127.0.0.1:2"},
	)
	var taskServer *httptest.Server
	calls := make(chan feishuITTaskCall, 4)
	taskServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			t.Errorf("read task body: %v", err)
		}
		calls <- feishuITTaskCall{method: r.Method, path: r.URL.Path, body: body}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"task-picker"}`)
	}))
	defer taskServer.Close()
	if _, err := nodesStore.Upsert(nodes.NodeConnection{ID: "fn", Name: "fn", URL: taskServer.URL}); err != nil {
		t.Fatal(err)
	}

	svc := feishuITService(t, dir, lark)
	opts := FeishuOptions{OwnerOpenID: feishuITOwner, PickerTTL: 24 * time.Hour, PickerMax: 5}
	if err := prepareFeishuChannel(svc, opts, FeishuIntakeDeps{
		ConfigDir: dir,
		NodeID:    "home",
		Nodes:     nodesStore,
		Catalog: feishuITCatalog{projects: []feishu.ProjectRef{
			{NodeID: "fn", RootID: "root-a", Name: "docs"},
			{NodeID: "fn", RootID: "root-b", Name: "wiki"},
		}},
		Creator: &feishuTaskCreator{nodes: nodesStore, localNode: "home"},
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); svc.Wait() }()
	svc.Start(ctx)
	if err := os.WriteFile(ready["im.message.receive_v1"], []byte("go"), 0o600); err != nil {
		t.Fatal(err)
	}

	cardStore := feishu.NewStore(filepath.Join(dir, "cards.json"))
	var card feishu.Card
	feishuITWaitFor(t, "项目选择卡", func() bool {
		if err := cardStore.Load(); err != nil {
			return false
		}
		found, ok := cardStore.FindOpenPickerByChat("oc_pick")
		if !ok {
			return false
		}
		card = found
		return true
	})
	if len(card.Projects) != 2 || card.SourceMessageID != "om-picker" {
		t.Fatalf("picker = %+v", card)
	}
	if err := svc.HandleAction(ctx, feishu.Action{
		CardID: card.ID, Choice: 0, MessageID: card.MessageID, OperatorID: feishuITOwner,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case call := <-calls:
		if call.method != http.MethodPost || call.path != "/api/tasks" {
			t.Fatalf("picker task request = %s %s", call.method, call.path)
		}
		var got struct {
			RootID string `json:"root_id"`
			Input  string `json:"input"`
		}
		if err := json.Unmarshal(call.body, &got); err != nil {
			t.Fatal(err)
		}
		if got.RootID != card.Projects[0].RootID || got.Input != "docs wiki 建个任务" {
			t.Fatalf("picker task body = %+v, card = %+v", got, card.Projects)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("选择卡点击后没有通过真实 creator 建任务")
	}
}
