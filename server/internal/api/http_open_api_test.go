package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"mindfs/server/internal/fs"
)

// G-I：API 层不做鉴权（多账户 = 多配置档，不是安全边界）。
//
// 这是用户明确决策（CLAUDE.md 事实 12）。上游给 `/api/*` 套上 token 或 e2ee proof
// 校验之后，浏览器直连会全部 401/403 —— 这条测试钉住「不带任何凭证也能读数据面端点」。
// 它测的是**鉴权层本身**：不带 Authorization、不带 X-MindFS-Local-CLI-Token、
// 不带任何 e2ee_* 头，请求仍必须穿过中间件到达 handler。
func TestDataPlaneNeedsNoCredentials(t *testing.T) {
	registry := fs.NewRegistry(filepath.Join(t.TempDir(), "registry.json"))
	handler := (&HTTPHandler{AppContext: &AppContext{Dirs: registry}}).Routes()

	req := httptest.NewRequest(http.MethodGet, "/api/dirs", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Fatalf("无凭证 GET /api/dirs = %d，API 层不得鉴权（G-I）", rec.Code)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/dirs = %d, want 200（body: %s）", rec.Code, rec.Body.String())
	}
}

// G-I：CORS 默认全开放。
//
// 跨节点请求（浏览器在 home 页面上打 pc.xiaokubao.space）靠它放行；
// 上游把 CORS 白名单合回来之后，这些请求会被浏览器直接挡下，症状是
// 「节点列表在、但点进去什么都没有」。
func TestCORSIsOpenByDefault(t *testing.T) {
	registry := fs.NewRegistry(filepath.Join(t.TempDir(), "registry.json"))
	handler := (&HTTPHandler{AppContext: &AppContext{Dirs: registry}}).Routes()

	const origin = "https://pc.xiaokubao.space"
	req := httptest.NewRequest(http.MethodOptions, "/api/dirs", nil)
	req.Header.Set("Origin", origin)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS 预检 = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("Access-Control-Allow-Origin = %q, want 原样回显 %q", got, origin)
	}
	if got := rec.Header().Get("Access-Control-Allow-Private-Network"); got != "true" {
		t.Fatalf("Access-Control-Allow-Private-Network = %q, want true（局域网直连必需）", got)
	}
}
