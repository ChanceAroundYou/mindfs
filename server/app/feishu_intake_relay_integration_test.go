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
	calls := make(chan feishuITTaskCall, 4)
	taskServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			t.Errorf("read task body: %v", err)
		}
		calls <- feishuITTaskCall{method: r.Method, path: r.URL.Path, body: body}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"task-1"}`)
	}))
	defer taskServer.Close()
	if _, err := nodesStore.Upsert(nodes.NodeConnection{ID: "fn", Name: "fn", URL: taskServer.URL}); err != nil {
		t.Fatal(err)
	}

	svc := feishuITService(t, dir, lark)
	opts := FeishuOptions{OwnerOpenID: feishuITOwner, PickerTTL: 24 * time.Hour, PickerMax: 5}
	if err := AttachFeishuIntake(svc, opts, FeishuIntakeDeps{
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
		t.Fatal("owner 私聊没有在 fn 节点建出任务")
	}

	// Picker TTL is a product promise (24h). Pin it here so a narrowed TTL
	// cannot silently make a card expire before the owner sees it.
	if feishu.PickerTTL != 24*time.Hour {
		t.Fatalf("feishu.PickerTTL = %s, want 24h", feishu.PickerTTL)
	}
}
