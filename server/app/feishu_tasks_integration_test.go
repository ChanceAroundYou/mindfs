package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/channels/feishu"
	"mindfs/server/internal/nodes"
)

// TestFeishuTaskClientAppendPostsToRemoteActivities proves the explicit target
// append reaches a remote worker's /api/tasks/{id}/activities with the same
// body shape the HTTP handler expects.
func TestFeishuTaskClientAppendPostsToRemoteActivities(t *testing.T) {
	calls := make(chan feishuITTaskCall, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		calls <- feishuITTaskCall{method: r.Method, path: r.URL.Path, body: body}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"user_activity":{"id":"a1"},"agent_activity":{"id":"a2"}}`)
	}))
	defer srv.Close()
	nodesStore := feishuAnswerNodeStore(t, nodes.NodeConnection{ID: "fn", Name: "fn", URL: srv.URL})
	client := newFeishuTaskClient(nodesStore, nil, "home")

	if err := client.Append(context.Background(), feishu.TargetRef{
		NodeID: "fn", RootID: "root-docs", TaskID: "task_42",
	}, "补一句"); err != nil {
		t.Fatal(err)
	}

	select {
	case call := <-calls:
		if call.method != http.MethodPost || call.path != "/api/tasks/task_42/activities" {
			t.Fatalf("append request = %s %s", call.method, call.path)
		}
		var got struct {
			RootID         string `json:"root_id"`
			Body           string `json:"body"`
			WorkflowChoice string `json:"workflow_choice"`
		}
		if err := json.Unmarshal(call.body, &got); err != nil {
			t.Fatalf("append body is not JSON: %v", err)
		}
		if got.RootID != "root-docs" || got.Body != "补一句" || got.WorkflowChoice != "empty" {
			t.Fatalf("append body = %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("远程节点没有收到追加请求")
	}
}

// TestFeishuTaskClientListReadsRemoteTasks proves the task catalog finder reads
// /api/tasks?root=… and uses task_number for an exact `#N` lookup, while a
// non-numeric keyword filters by name.
func TestFeishuTaskClientListReadsRemoteTasks(t *testing.T) {
	queries := make(chan string, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries <- r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[
			{"task":{"id":"task_12","task_number":12,"name":"登录页","status":"waiting_user","root_id":"root-docs"}},
			{"task":{"id":"task_3","task_number":3,"name":"wiki 索引","status":"running","root_id":"root-docs"}}
		]}`)
	}))
	defer srv.Close()
	nodesStore := feishuAnswerNodeStore(t, nodes.NodeConnection{ID: "fn", Name: "fn", URL: srv.URL})
	client := newFeishuTaskClient(nodesStore, nil, "home")

	refs, err := client.List(context.Background(), "fn", "root-docs", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs[0].TaskID != "task_12" || refs[0].NodeID != "fn" || refs[0].RootID != "root-docs" {
		t.Fatalf("list refs = %+v", refs)
	}
	if q := <-queries; !strings.Contains(q, "root=root-docs") || !strings.Contains(q, "limit=50") {
		t.Fatalf("list query = %q", q)
	}

	if _, err := client.List(context.Background(), "fn", "root-docs", "#12"); err != nil {
		t.Fatal(err)
	}
	if q := <-queries; !strings.Contains(q, "task_number=12") {
		t.Fatalf("按编号查询 = %q", q)
	}

	refs, err = client.List(context.Background(), "fn", "root-docs", "登录")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].TaskID != "task_12" {
		t.Fatalf("按名字过滤 = %+v", refs)
	}
	<-queries // 名字过滤会先拉一份列表
}

// TestFeishuTaskClientAppendStaleClassifiesMissingTask proves a 404 or a
// "no rows" body wraps feishu.ErrTargetStale, while any other failure stays
// transient.
func TestFeishuTaskClientAppendStaleClassifiesMissingTask(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		isStale bool
	}{
		{name: "not found", status: http.StatusNotFound, body: `{"error":"task gone"}`, isStale: true},
		{name: "no rows body", status: http.StatusInternalServerError, body: `{"error":"sql: no rows in result set"}`, isStale: true},
		{name: "transient", status: http.StatusInternalServerError, body: `{"error":"boom"}`, isStale: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			nodesStore := feishuAnswerNodeStore(t, nodes.NodeConnection{ID: "fn", Name: "fn", URL: srv.URL})
			client := newFeishuTaskClient(nodesStore, nil, "home")

			err := client.Append(context.Background(), feishu.TargetRef{
				NodeID: "fn", RootID: "root-docs", TaskID: "task_42",
			}, "补一句")
			if err == nil {
				t.Fatal("期望 append 失败")
			}
			if got := errors.Is(err, feishu.ErrTargetStale); got != tc.isStale {
				t.Fatalf("ErrTargetStale = %v, want %v (err=%v)", got, tc.isStale, err)
			}
		})
	}
}

// TestFeishuTaskClientRejectsUnknownNode proves a message can never redirect an
// append to a host that is not in the node table.
func TestFeishuTaskClientRejectsUnknownNode(t *testing.T) {
	nodesStore := feishuAnswerNodeStore(t, nodes.NodeConnection{ID: "fn", Name: "fn", URL: "http://127.0.0.1:1"})
	client := newFeishuTaskClient(nodesStore, nil, "home")

	err := client.Append(context.Background(), feishu.TargetRef{
		NodeID: "evil", RootID: "root-docs", TaskID: "task_42",
	}, "补一句")
	if err == nil || !strings.Contains(err.Error(), "unknown node") {
		t.Fatalf("未知节点必须拒绝：%v", err)
	}
}
