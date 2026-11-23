package controlsync

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const pairTestToken = "0123456789abcdef0123456789abcdef"

// fakePrimary 起一个只实现配对所需两条端点的假主控端。
//
// 必须是 TLS：配对只接受 https，而客户端不信系统根之外的东西 —— 测试服自己的证书
// 要通过 ca_file 喂回去。这顺带把「自签/内网 CA 走 ca_file」这条路径也真跑了一遍。
//
// requireToken 为空表示不校验 bearer（用来构造 401）。
func newPairPrimary(t *testing.T, role string, requireToken string, schema int) (*httptest.Server, string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "role": role})
	})
	mux.HandleFunc("/api/control-sync/head", func(w http.ResponseWriter, r *http.Request) {
		if requireToken != "" && r.Header.Get("Authorization") != "Bearer "+requireToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// 配对走的是签名请求，缺任何一个头真实主控端都会 401 —— 这里一并钉住。
		for _, name := range []string{SyncTimestampHeader, SyncNonceHeader, SyncSignatureHeader, SyncInstanceHeader} {
			if strings.TrimSpace(r.Header.Get(name)) == "" {
				http.Error(w, "missing "+name, http.StatusUnauthorized)
				return
			}
		}
		_ = json.NewEncoder(w).Encode(HeadInfo{
			HeadRev:       7,
			InstanceID:    "home",
			BuildVersion:  "v0.5.5-test",
			SchemaVersion: schema,
		})
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv, writeServerCert(t, srv)
}

func writeServerCert(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	block := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, block, 0o600); err != nil {
		t.Fatalf("write ca: %v", err)
	}
	return path
}

func pairFixture(t *testing.T) (configPath, tokenFile string) {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "config.json"), filepath.Join(dir, "control_sync.token")
}

func TestPairWritesTokenAndConfig(t *testing.T) {
	srv, caFile := newPairPrimary(t, "control", pairTestToken, SyncSchemaVersion)
	configPath, tokenFile := pairFixture(t)
	if err := os.WriteFile(configPath, []byte(`{"role":"control","router":{"mode":"coordinate"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Pair(context.Background(), PairRequest{
		PrimaryURL: srv.URL,
		Token:      pairTestToken,
		InstanceID: "pc-win",
		CAFile:     caFile,
	}, tokenFile, configPath)
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	if res.InstanceID != "pc-win" || res.HeadRev != 7 || res.PrimaryBuild != "v0.5.5-test" {
		t.Fatalf("result = %+v", res)
	}

	raw, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatalf("token file: %v", err)
	}
	if strings.TrimSpace(string(raw)) != pairTestToken {
		t.Fatalf("token file = %q", string(raw))
	}
	if info, err := os.Stat(tokenFile); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token perm: %v %v", info, err)
	}

	doc := map[string]any{}
	body, _ := os.ReadFile(configPath)
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if _, ok := doc["router"]; !ok {
		t.Fatalf("router section lost: %#v", doc)
	}
	cs, ok := doc["control_sync"].(map[string]any)
	if !ok {
		t.Fatalf("control_sync missing: %#v", doc)
	}
	if cs["role"] != "edge" || cs["enabled"] != true || cs["instance_id"] != "pc-win" {
		t.Fatalf("control_sync = %#v", cs)
	}
	if cs["primary_url"] != srv.URL || cs["token_file"] != tokenFile || cs["ca_file"] != caFile {
		t.Fatalf("control_sync = %#v", cs)
	}
}

// 组成「配对成功」的几个前提各失败一次，磁盘上都不能留痕迹 —— 半个配对会把
// 「没配上」伪装成「配上了，只是同步不上」。
func TestPairLeavesNothingOnFailure(t *testing.T) {
	cases := []struct {
		name    string
		role    string
		token   string // 假主控端接受的 token；空 = 不校验
		schema  int
		mutate  func(*PairRequest)
		wantSub string
	}{
		{
			name:    "token rejected",
			role:    "control",
			token:   "another-token-value-32-bytes-long",
			schema:  SyncSchemaVersion,
			wantSub: "401",
		},
		{
			name:    "primary is a worker",
			role:    "worker",
			token:   pairTestToken,
			schema:  SyncSchemaVersion,
			wantSub: "requires a control node",
		},
		{
			name:    "schema mismatch",
			role:    "control",
			token:   pairTestToken,
			schema:  SyncSchemaVersion + 1,
			wantSub: "schema",
		},
		{
			name:    "plain http",
			role:    "control",
			token:   pairTestToken,
			schema:  SyncSchemaVersion,
			mutate:  func(r *PairRequest) { r.PrimaryURL = strings.Replace(r.PrimaryURL, "https://", "http://", 1) },
			wantSub: "https",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, caFile := newPairPrimary(t, tc.role, tc.token, tc.schema)
			configPath, tokenFile := pairFixture(t)
			const original = `{"role":"control"}`
			if err := os.WriteFile(configPath, []byte(original), 0o644); err != nil {
				t.Fatal(err)
			}
			req := PairRequest{PrimaryURL: srv.URL, Token: pairTestToken, InstanceID: "pc-win", CAFile: caFile}
			if tc.mutate != nil {
				tc.mutate(&req)
			}
			_, err := Pair(context.Background(), req, tokenFile, configPath)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.wantSub)
			}
			if _, statErr := os.Stat(tokenFile); !os.IsNotExist(statErr) {
				t.Fatalf("token file left behind: %v", statErr)
			}
			body, _ := os.ReadFile(configPath)
			if string(body) != original {
				t.Fatalf("config rewritten: %q", string(body))
			}
		})
	}
}

// 显式把本机写成 worker 的配置，配对后必须纠正成 control：worker 不注入静态目录，
// 「配对成功」会表现成一个 403 空页面。
func TestPairPromotesWorkerRoleToControl(t *testing.T) {
	srv, caFile := newPairPrimary(t, "control", pairTestToken, SyncSchemaVersion)
	configPath, tokenFile := pairFixture(t)
	if err := os.WriteFile(configPath, []byte(`{"role":"worker"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Pair(context.Background(), PairRequest{
		PrimaryURL: srv.URL,
		Token:      pairTestToken,
		CAFile:     caFile,
	}, tokenFile, configPath); err != nil {
		t.Fatalf("pair: %v", err)
	}
	body, _ := os.ReadFile(configPath)
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["role"] != "control" {
		t.Fatalf("role = %#v", doc["role"])
	}
}

// 短 token 在发出任何网络请求之前就该被拒：它挡不住任何人，而且错在主控端拷贝那一步，
// 跟「连不上」是两回事。
func TestPairRejectsShortTokenBeforeNetwork(t *testing.T) {
	srv, caFile := newPairPrimary(t, "control", pairTestToken, SyncSchemaVersion)
	configPath, tokenFile := pairFixture(t)
	_, err := Pair(context.Background(), PairRequest{
		PrimaryURL: srv.URL, Token: "short", CAFile: caFile,
	}, tokenFile, configPath)
	if err == nil || !strings.Contains(err.Error(), "16") {
		t.Fatalf("err = %v", err)
	}
}

// 部署前缀必须出现在探活路径上：主节点常常挂在 /mindfs 下，漏了前缀会 404，
// 而 404 会被读成「主控端没开同步」。
func TestPairKeepsDeployPrefix(t *testing.T) {
	var seen []string
	mux := http.NewServeMux()
	mux.HandleFunc("/mindfs/health", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "role": "control"})
	})
	mux.HandleFunc("/mindfs/api/control-sync/head", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		_ = json.NewEncoder(w).Encode(HeadInfo{HeadRev: 1, SchemaVersion: SyncSchemaVersion})
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	caFile := writeServerCert(t, srv)

	configPath, tokenFile := pairFixture(t)
	if _, err := Pair(context.Background(), PairRequest{
		PrimaryURL: srv.URL + "/mindfs",
		Token:      pairTestToken,
		InstanceID: "edge1",
		CAFile:     caFile,
	}, tokenFile, configPath); err != nil {
		t.Fatalf("pair: %v", err)
	}
	if len(seen) != 2 || seen[0] != "/mindfs/health" || seen[1] != "/mindfs/api/control-sync/head" {
		t.Fatalf("paths seen = %v", seen)
	}
}
