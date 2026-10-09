package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/internal/deploy"
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
	handler := StripDeployPrefix(deploy.NormalizedPrefix(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// 与 usecase 侧同名函数同源：API 层序列化 session 时也要剥掉迁移提示。
// 覆盖绝对路径形态 —— 相对路径时代的测试早就不存在，剥壳逻辑零覆盖。
func TestStripExternalSessionPrefixForAPIRemovesAbsolutePathHint(t *testing.T) {
	hint := "This session was migrated from elsewhere. Your context may lag behind this session;\n" +
		"Before replying, read the last 20 lines from /abs/root/.mindfs/sessions/1234-abcd.jsonl to recover context.\n" +
		"If you still need more context, decide and read older history yourself.\n" +
		"When continuing to read, keep each backward batch to about 20 lines.\n\n" +
		"Execution order: read history first, then compose the final answer.\n" +
		"Note: do not send any natural-language response before finishing the required history reads. Start reading immediately via tools/commands.\n" +
		"Only if reading fails, output a brief error and stop.\n\n"
	got := stripExternalSessionPrefixForAPI(hint + "真正的问题")
	if got != "真正的问题" {
		t.Fatalf("stripped = %q, want just the user message", got)
	}
}
