package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateNodeName(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{"普通名字", "  hasee  ", "hasee", ""},
		{"中文名", "书房 PC", "书房 PC", ""},
		{"空名字", "   ", "", "不能为空"},
		{"太长", strings.Repeat("a", nodeNameMaxRunes+1), "", "最多"},
		{"路径分隔符", "a/b", "", "不能包含字符"},
		{"控制字符", "a\tb", "", "控制字符"},
		{"保留名 local", "local", "", "保留名称"},
		{"保留名大小写", "HOME", "", "保留名称"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateNodeName(tc.input)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tc.want {
					t.Fatalf("got %q want %q", got, tc.want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// 主控可能挂在 https://host/mindfs 下：拼 URL 时丢掉前缀会 404，
// 而界面上只会显示「主控不可达」—— 责任看起来在网络，其实在拼接。
func TestJoinPrimaryPathKeepsDeploymentPrefix(t *testing.T) {
	got, err := joinPrimaryPath("https://home.example.com/mindfs/", "/api/nodes/validate-name")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://home.example.com/mindfs/api/nodes/validate-name" {
		t.Fatalf("got %q", got)
	}
	if _, err := joinPrimaryPath("home.example.com", "/api/x"); err == nil {
		t.Fatal("缺少 scheme 必须报错，而不是拼出相对路径")
	}
}

func TestCheckNodeNameDefersWithoutPrimary(t *testing.T) {
	result, code := checkNodeName(t.Context(), "hasee", "", "", nodeNameClient{})
	if code != nodeNameExitOK {
		t.Fatalf("code = %d", code)
	}
	if !result.Valid || !result.Available || !result.Deferred {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCheckNodeNameQueriesPrimary(t *testing.T) {
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mindfs/api/nodes/validate-name" {
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"available":false,"reason":"已存在同名节点"}`))
	}))
	defer srv.Close()

	client := nodeNameClient{http: srv.Client()}
	result, code := checkNodeName(t.Context(), "hasee", srv.URL+"/mindfs", "", client)
	if code != nodeNameExitTaken {
		t.Fatalf("code = %d, want %d (%+v)", code, nodeNameExitTaken, result)
	}
	if gotBody["name"] != "hasee" {
		t.Fatalf("body = %#v", gotBody)
	}
	if result.Available || !strings.Contains(result.Reason, "同名") {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCheckNodeNameUnreachableIsNotAvailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	result, code := checkNodeName(t.Context(), "hasee", srv.URL, "", nodeNameClient{http: srv.Client()})
	if code != nodeNameExitUnreachable {
		t.Fatalf("code = %d, want %d (%+v)", code, nodeNameExitUnreachable, result)
	}
	// 关键：网络失败绝不能被当成「可用」。
	if result.Available {
		t.Fatal("主控不可达时 available 必须是 false")
	}
}

func TestCheckNodeNameInvalidSkipsNetwork(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	result, code := checkNodeName(t.Context(), "a/b", srv.URL, "", nodeNameClient{http: srv.Client()})
	if code != nodeNameExitInvalid || result.Valid {
		t.Fatalf("code = %d result = %+v", code, result)
	}
	if called {
		t.Fatal("格式不合法时不该发请求")
	}
}

func TestRunCheckNodeNameWritesOutFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "nested", "result.json")
	var stdout bytes.Buffer
	err := runCheckNodeName([]string{"--name", "hasee", "--out", out}, &stdout, nodeNameClient{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("out file missing: %v", err)
	}
	var result nodeNameResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("out file is not valid JSON: %v", err)
	}
	if result.Name != "hasee" || !result.Deferred {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestRunSetNodeNameWritesConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"control_sync":{"role":"edge"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runSetNodeName([]string{"--name", "hasee", "--config", path}, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	node, _ := doc["node"].(map[string]any)
	if node["name"] != "hasee" {
		t.Fatalf("node.name = %#v", node["name"])
	}
	cs, _ := doc["control_sync"].(map[string]any)
	if cs["instance_id"] != "hasee" {
		t.Fatalf("control_sync.instance_id = %#v", cs["instance_id"])
	}
	if cs["role"] != "edge" {
		t.Fatal("既有字段必须保留")
	}
}

// 已有的 instance_id 是这台机器的同步身份，改名只能改展示名，不能换身份。
func TestRunSetNodeNameKeepsExistingInstanceID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"control_sync":{"instance_id":"old-id"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runSetNodeName([]string{"--name", "new-name", "--config", path}, nil); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var doc map[string]any
	_ = json.Unmarshal(data, &doc)
	cs, _ := doc["control_sync"].(map[string]any)
	if cs["instance_id"] != "old-id" {
		t.Fatalf("instance_id changed: %#v", cs["instance_id"])
	}
}

func TestRunNodeNameCommandDispatch(t *testing.T) {
	handled, err := runNodeNameCommandWith([]string{"--desktop"}, nil, nodeNameClient{})
	if handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
}
