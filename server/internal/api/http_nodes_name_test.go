package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mindfs/server/internal/nodes"
)

func nodesTestHandler(t *testing.T) (*HTTPHandler, *nodes.Store) {
	t.Helper()
	store, err := nodes.NewStoreAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &HTTPHandler{AppContext: &AppContext{Nodes: store}}, store
}

// 安装向导、设置页与 CLI 共用这一个查重入口：规则（trim + 忽略大小写）
// 只能有一份，否则三处结论不一致，用户装完才发现名字撞了。
func TestNodesValidateName(t *testing.T) {
	h, store := nodesTestHandler(t)
	if _, err := store.Upsert(nodes.NodeConnection{ID: "fn", Name: "客厅 NAS", URL: "https://fn.example"}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantBody   string
	}{
		{"重名（大小写与空白不敏感）", `{"name":"  客厅 nas  "}`, http.StatusOK, `"available":false`},
		{"改自己不算重名", `{"name":"客厅 NAS","id":"fn"}`, http.StatusOK, `"available":true`},
		{"可用", `{"name":"书房 PC"}`, http.StatusOK, `"available":true`},
		{"空名字是请求错误", `{"name":"   "}`, http.StatusBadRequest, `name required`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.handleNodesValidateName(rec, httptest.NewRequest(http.MethodPost, "/api/nodes/validate-name", strings.NewReader(tc.body)))
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Fatalf("body = %s, want %s", rec.Body.String(), tc.wantBody)
			}
		})
	}
}

// PUT 是前端保存节点表的唯一路径（整表替换），重名必须在这里拦住，
// 否则 UI 一步就能绕过 POST 的校验。
func TestNodesPutRejectsNewDuplicateNames(t *testing.T) {
	h, store := nodesTestHandler(t)
	if _, err := store.Upsert(nodes.NodeConnection{ID: "a", Name: "书房 PC", URL: "https://a.example"}); err != nil {
		t.Fatal(err)
	}

	payload := []nodes.NodeConnection{
		{ID: "a", Name: "书房 PC", URL: "https://a.example"},
		{ID: "b", Name: "书房 pc", URL: "https://b.example"},
	}
	body, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	h.handleNodesPut(rec, httptest.NewRequest(http.MethodPut, "/api/nodes", strings.NewReader(string(body))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	if got := store.List(); len(got) != 1 {
		t.Fatalf("store = %v, want unchanged single node", got)
	}
}

// 历史数据里已经并存的重名不能反过来卡住编辑：
// 用户只是想改一条 URL，不该被迫先改名。
func TestNodesPutToleratesHistoricalDuplicates(t *testing.T) {
	h, store := nodesTestHandler(t)
	legacy := []nodes.NodeConnection{
		{ID: "a", Name: "书房 PC", URL: "https://a.example"},
		{ID: "b", Name: "书房 pc", URL: "https://b.example"},
	}
	if err := store.Replace(legacy); err != nil {
		t.Fatal(err)
	}

	edited := []nodes.NodeConnection{
		{ID: "a", Name: "书房 PC", URL: "https://a2.example"},
		{ID: "b", Name: "书房 pc", URL: "https://b.example"},
	}
	body, _ := json.Marshal(edited)
	rec := httptest.NewRecorder()
	h.handleNodesPut(rec, httptest.NewRequest(http.MethodPut, "/api/nodes", strings.NewReader(string(body))))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got, _ := store.Get("a"); got.URL != "https://a2.example" {
		t.Fatalf("URL = %q, want edit applied", got.URL)
	}
}

func TestNodesPostRejectsNewDuplicateName(t *testing.T) {
	h, store := nodesTestHandler(t)
	if _, err := store.Upsert(nodes.NodeConnection{ID: "a", Name: "书房 PC", URL: "https://a.example"}); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"b","name":"书房 pc","url":"https://b.example"}`
	rec := httptest.NewRecorder()
	h.handleNodesPost(rec, httptest.NewRequest(http.MethodPost, "/api/nodes", strings.NewReader(body)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
}
