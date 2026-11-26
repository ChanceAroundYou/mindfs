package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mindfs/server/internal/nodes"
)

// 主控置顶是服务端保证：GET 里本机（主控）行永远第一，PUT 里也不许把它挪走，
// 顺序按请求数组下标落成 Order —— 边缘的 /api/nodes 被代理到这里，拿到的就是同一份。
func TestNodesListPinsPrimaryFirst(t *testing.T) {
	h, store := nodesTestHandler(t)
	h.NodeID = "home"
	if err := store.Replace([]nodes.NodeConnection{
		{ID: "pc", Name: "pc", URL: "https://pc.example/mindfs-win", Order: 1},
		{ID: "home", Name: "home", URL: "https://home.example/mindfs", Order: 2},
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.handleNodesList(rec, httptest.NewRequest(http.MethodGet, "/api/nodes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var got []nodes.NodeConnection
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "home" || got[1].ID != "pc" {
		t.Fatalf("list order = %+v, want home first", got)
	}
}

func TestNodesPutAssignsOrderAndPinsPrimaryFirst(t *testing.T) {
	h, store := nodesTestHandler(t)
	h.NodeID = "home"

	body := `[
		{"id":"a","name":"A","url":"https://a.example"},
		{"id":"home","name":"home","url":"https://home.example/mindfs"},
		{"id":"b","name":"B","url":"https://b.example"}
	]`
	rec := httptest.NewRecorder()
	h.handleNodesPut(rec, httptest.NewRequest(http.MethodPut, "/api/nodes", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}

	got := store.List()
	want := []string{"home", "a", "b"}
	if len(got) != len(want) {
		t.Fatalf("store rows = %+v", got)
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("List()[%d] = %q, want %q (all=%+v)", i, got[i].ID, id, got)
		}
		if got[i].Order != i+1 {
			t.Fatalf("%s Order = %d, want %d", got[i].ID, got[i].Order, i+1)
		}
	}
}

// 边缘离线时本机行不在共享表里（边缘的 node id 是 instance_id，表里那条是随机 uuid）：
// 没有可固定的行就按请求顺序原样落 Order，不能凭空插一条。
func TestNodesPutWithoutPrimaryRowKeepsRequestOrder(t *testing.T) {
	h, store := nodesTestHandler(t)
	h.NodeID = "pc"

	body := `[
		{"id":"home","name":"home","url":"https://home.example/mindfs"},
		{"id":"685a","name":"pc","url":"https://pc.example/mindfs-win"}
	]`
	rec := httptest.NewRecorder()
	h.handleNodesPut(rec, httptest.NewRequest(http.MethodPut, "/api/nodes", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	got := store.List()
	if len(got) != 2 || got[0].ID != "home" || got[0].Order != 1 || got[1].Order != 2 {
		t.Fatalf("store = %+v, want request order preserved", got)
	}
}