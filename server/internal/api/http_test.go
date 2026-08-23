package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/internal/deploy"
	"mindfs/server/internal/e2ee"
	"mindfs/server/internal/relay"
)

func TestPathForStaticAssetCleansURLPaths(t *testing.T) {
	tests := []struct {
		name        string
		requestPath string
		want        string
	}{
		{
			name:        "absolute asset path",
			requestPath: "/assets/app.js",
			want:        "assets/app.js",
		},
		{
			name:        "duplicate slash path",
			requestPath: "//assets/app.js",
			want:        "assets/app.js",
		},
		{
			name:        "root path",
			requestPath: "/",
			want:        "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pathForStaticAsset(tt.requestPath)
			if got != tt.want {
				t.Fatalf("pathForStaticAsset(%q) = %q, want %q", tt.requestPath, got, tt.want)
			}
		})
	}
}

func TestServeFrontendIndexRewritesRelayedAssetRefs(t *testing.T) {
	staticDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(staticDir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(staticDir, "index.html")
	content := `<!doctype html><script type="module" src="./assets/index-test.js"></script><link rel="stylesheet" href="./assets/index-test.css">`
	if err := os.WriteFile(indexPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "assets", "index-test.js"), []byte("console.log('ok')"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "assets", "index-test.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	prev := deploy.Prefix
	deploy.Prefix = "/mindfs"
	defer func() { deploy.Prefix = prev }()

	handler := &HTTPHandler{StaticDir: staticDir, Version: "v0.3.5"}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-MindFS-Relayed", "1")
	resp := httptest.NewRecorder()

	handler.serveFrontendIndex(resp, req, staticDir, indexPath)

	body := resp.Body.String()
	if strings.Contains(body, "./assets/") {
		t.Fatalf("body still contains local assets path: %s", body)
	}
	if !strings.Contains(body, "/mindfs-assets/index-test.js") || !strings.Contains(body, "/mindfs-assets/index-test.css") {
		t.Fatalf("body missing relayed asset paths: %s", body)
	}
}

func TestServeFrontendIndexKeepsLocalAssetRefsWhenNotRelayed(t *testing.T) {
	staticDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(staticDir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(staticDir, "index.html")
	content := `<!doctype html><script type="module" src="./assets/index-test.js"></script>`
	if err := os.WriteFile(indexPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "assets", "index-test.js"), []byte("console.log('ok')"), 0o644); err != nil {
		t.Fatal(err)
	}

	prev := deploy.Prefix
	deploy.Prefix = "/mindfs"
	defer func() { deploy.Prefix = prev }()

	handler := &HTTPHandler{StaticDir: staticDir, Version: "v0.3.5-9-g92b8c85-dirty"}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// 直连（无 X-MindFS-Relayed）时不做绝对化改写，沿用本地相对引用。
	resp := httptest.NewRecorder()

	handler.serveFrontendIndex(resp, req, staticDir, indexPath)

	body := resp.Body.String()
	if !strings.Contains(body, "./assets/index-test.js") {
		t.Fatalf("body should keep local asset path when not relayed: %s", body)
	}
	if strings.Contains(body, "/mindfs-assets/") {
		t.Fatalf("body should not contain relayed asset path when not relayed: %s", body)
	}
}

func TestIsStandardReleaseVersion(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{version: "v0.3.5", want: true},
		{version: "0.3.5", want: true},
		{version: "v0.3.5-9-g92b8c85-dirty", want: false},
		{version: "dev", want: false},
		{version: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			if got := isStandardReleaseVersion(tt.version); got != tt.want {
				t.Fatalf("isStandardReleaseVersion(%q) = %v, want %v", tt.version, got, tt.want)
			}
		})
	}
}

func TestIsLocalCLIRequestRequiresTokenLoopbackAndWhitelistedRoute(t *testing.T) {
	handler := &HTTPHandler{LocalCLIToken: "secret-token"}
	req := httptest.NewRequest(http.MethodPost, "/api/dirs", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set(localCLIHeaderName, "secret-token")

	if !handler.isLocalCLIRequest(req) {
		t.Fatal("expected local CLI request to be accepted")
	}
}

func TestIsLocalCLIRequestAllowsRelayBindStart(t *testing.T) {
	handler := &HTTPHandler{LocalCLIToken: "secret-token"}
	req := httptest.NewRequest(http.MethodPost, "/api/relay/bind/start", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set(localCLIHeaderName, "secret-token")

	if !handler.isLocalCLIRequest(req) {
		t.Fatal("expected local CLI relay bind request to be accepted")
	}
}

func TestIsLocalCLIRequestRejectsNonWhitelistedRoute(t *testing.T) {
	handler := &HTTPHandler{LocalCLIToken: "secret-token"}
	req := httptest.NewRequest(http.MethodGet, "/api/tree", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set(localCLIHeaderName, "secret-token")

	if handler.isLocalCLIRequest(req) {
		t.Fatal("expected non-whitelisted route to be rejected")
	}
}

func TestIsLocalCLIRequestRejectsRemoteAddress(t *testing.T) {
	handler := &HTTPHandler{LocalCLIToken: "secret-token"}
	req := httptest.NewRequest(http.MethodPost, "/api/dirs", nil)
	req.RemoteAddr = "192.0.2.1:54321"
	req.Header.Set(localCLIHeaderName, "secret-token")

	if handler.isLocalCLIRequest(req) {
		t.Fatal("expected remote address to be rejected")
	}
}

func TestIsLocalCLIRequestRejectsInvalidToken(t *testing.T) {
	handler := &HTTPHandler{LocalCLIToken: "secret-token"}
	req := httptest.NewRequest(http.MethodPost, "/api/dirs", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set(localCLIHeaderName, "wrong-token")

	if handler.isLocalCLIRequest(req) {
		t.Fatal("expected invalid token to be rejected")
	}
}

func TestRelayStatusWithE2EEDoesNotSetNodeIDWhenE2EEDisabled(t *testing.T) {
	handler := &HTTPHandler{AppContext: &AppContext{
		E2EE: e2ee.NewManager(e2ee.Config{Enabled: false, NodeID: "node-id", PairingSecret: "secret"}),
	}}
	status := handler.relayStatusWithE2EE(relay.Status{NodeID: "relay-node"})

	if status.E2EERequired {
		t.Fatal("expected E2EERequired to be false")
	}
	if status.E2EENodeID != "" {
		t.Fatalf("E2EENodeID = %q, want empty", status.E2EENodeID)
	}
	if status.NodeID != "relay-node" {
		t.Fatalf("NodeID = %q, want relay-node", status.NodeID)
	}
}

func TestRelayStatusWithE2EEDoesNotFallbackNodeIDWhenEnabled(t *testing.T) {
	handler := &HTTPHandler{AppContext: &AppContext{
		E2EE: e2ee.NewManager(e2ee.Config{Enabled: true, NodeID: "e2ee-node", PairingSecret: "secret"}),
	}}
	status := handler.relayStatusWithE2EE(relay.Status{})

	if !status.E2EERequired {
		t.Fatal("expected E2EERequired to be true")
	}
	if status.E2EENodeID != "e2ee-node" {
		t.Fatalf("E2EENodeID = %q, want e2ee-node", status.E2EENodeID)
	}
	if status.NodeID != "" {
		t.Fatalf("NodeID = %q, want empty", status.NodeID)
	}
}

func TestRelayStatusSessionAllowsPublicStatusWithoutE2EEHeader(t *testing.T) {
	handler := &HTTPHandler{AppContext: &AppContext{
		E2EE: e2ee.NewManager(e2ee.Config{Enabled: true, NodeID: "e2ee-node", PairingSecret: "secret"}),
	}}
	req := httptest.NewRequest(http.MethodGet, "/api/relay/status", nil)

	sess, err := handler.relayStatusSession(req)
	if err != nil {
		t.Fatalf("relayStatusSession() error = %v", err)
	}
	if sess != nil {
		t.Fatalf("relayStatusSession() = %+v, want nil public session", sess)
	}
}

func TestPublicRelayStatusRedactsSensitiveRelayFields(t *testing.T) {
	status := publicRelayStatus(relay.Status{
		Bound:        true,
		NoRelayer:    false,
		PendingCode:  "pc_secret",
		NodeName:     "node-name",
		NodeID:       "node-id",
		E2EENodeID:   "e2ee-node",
		RelayBaseURL: "https://relay.example.com",
		NodeURL:      "https://relay.example.com/n/node-id/",
		LastError:    "err",
		E2EERequired: true,
	})

	if !status.E2EERequired || status.E2EENodeID != "e2ee-node" {
		t.Fatalf("public E2EE fields = required:%v node:%q", status.E2EERequired, status.E2EENodeID)
	}
	if status.PendingCode != "" || status.NodeID != "" || status.NodeURL != "" || status.RelayBaseURL != "" || status.NodeName != "" || status.LastError != "" {
		t.Fatalf("public status leaked sensitive fields: %+v", status)
	}
}

func TestStripDeployPrefixStripsAndRejects(t *testing.T) {
	tests := []struct {
		name         string
		prefix       string
		path         string
		wantStatus   int
		wantStripped string
	}{
		{name: "exact prefix", prefix: "/mindfs", path: "/mindfs", wantStatus: http.StatusOK, wantStripped: "/"},
		{name: "under prefix", prefix: "/mindfs", path: "/mindfs/api/tree", wantStatus: http.StatusOK, wantStripped: "/api/tree"},
		{name: "prefix slash", prefix: "/mindfs", path: "/mindfs/", wantStatus: http.StatusOK, wantStripped: "/"},
		{name: "bare rejected", prefix: "/mindfs", path: "/api/tree", wantStatus: http.StatusNotFound, wantStripped: ""},
		{name: "double prefix rejected", prefix: "/mindfs", path: "/mindfs/mindfs/api", wantStatus: http.StatusNotFound, wantStripped: ""},
		{name: "other path rejected", prefix: "/mindfs", path: "/health", wantStatus: http.StatusNotFound, wantStripped: ""},
		{name: "root deploy passes through", prefix: "", path: "/api/tree", wantStatus: http.StatusOK, wantStripped: "/api/tree"},
		{name: "root deploy root passes", prefix: "", path: "/", wantStatus: http.StatusOK, wantStripped: "/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.URL.Path
				w.WriteHeader(http.StatusOK)
			})
			handler := StripDeployPrefix(tt.prefix, inner)
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusOK && got != tt.wantStripped {
				t.Fatalf("stripped path = %q, want %q", got, tt.wantStripped)
			}
		})
	}
}

func TestRelayAssetsAliasRoutesThroughStrictPrefix(t *testing.T) {
	prev := deploy.Prefix
	deploy.Prefix = "/mindfs"
	defer func() { deploy.Prefix = prev }()

	var got string
	handler := StripDeployPrefix(NormalizedDeployPrefix(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))

	aliasReq := httptest.NewRequest(http.MethodGet, "/mindfs-assets/index.js", nil)
	aliasRec := httptest.NewRecorder()
	handler.ServeHTTP(aliasRec, aliasReq)
	if aliasRec.Code != http.StatusOK || got != "/assets/index.js" {
		t.Fatalf("relay alias status/path = %d/%q, want 200/%q", aliasRec.Code, got, "/assets/index.js")
	}

	bareReq := httptest.NewRequest(http.MethodGet, "/assets/index.js", nil)
	bareRec := httptest.NewRecorder()
	handler.ServeHTTP(bareRec, bareReq)
	if bareRec.Code != http.StatusNotFound {
		t.Fatalf("bare asset status = %d, want 404", bareRec.Code)
	}
}

func TestRelayAssetsAlias(t *testing.T) {
	tests := []struct {
		prefix string
		want   string
	}{
		{prefix: "/mindfs", want: "/mindfs-assets/"},
		{prefix: "/app", want: "/app-assets/"},
		{prefix: "", want: "/assets/"},
	}
	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			prev := deploy.Prefix
			deploy.Prefix = tt.prefix
			defer func() { deploy.Prefix = prev }()
			if got := relayAssetsAlias(); got != tt.want {
				t.Fatalf("relayAssetsAlias() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizedDeployPrefix(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "/mindfs", want: "/mindfs"},
		{in: "/mindfs/", want: "/mindfs"},
		{in: "mindfs", want: "/mindfs"},
		{in: "/", want: ""},
		{in: "", want: ""},
		{in: "  /x/  ", want: "/x"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			prev := deploy.Prefix
			deploy.Prefix = tt.in
			defer func() { deploy.Prefix = prev }()
			if got := NormalizedDeployPrefix(); got != tt.want {
				t.Fatalf("NormalizedDeployPrefix() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCleanFrontendResourcePathUsesDeployPrefix(t *testing.T) {
	prev := deploy.Prefix
	deploy.Prefix = "/mindfs"
	defer func() { deploy.Prefix = prev }()

	tests := []struct {
		in   string
		want string
	}{
		{in: "/mindfs/assets/app.js", want: "assets/app.js"},
		{in: "/assets/app.js", want: "assets/app.js"},
		{in: "/mindfs", want: "mindfs"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := cleanFrontendResourcePath(tt.in); got != tt.want {
				t.Fatalf("cleanFrontendResourcePath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
