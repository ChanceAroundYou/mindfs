package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/fs"

	"github.com/go-chi/chi/v5"
)

// newWorktreeOwnershipHandler 装配到「空 registry」为止：两个入口都不该在
// 参数校验阶段就 404 —— 那条路径缺失时脚本拿到的错误与「会话不存在」无从区分。
func newWorktreeOwnershipHandler(t *testing.T) *HTTPHandler {
	t.Helper()
	registry := fs.NewRegistry(filepath.Join(t.TempDir(), "registry.json"))
	return &HTTPHandler{AppContext: &AppContext{Dirs: registry}}
}

func worktreeOwnershipRouter(h *HTTPHandler) *chi.Mux {
	router := chi.NewRouter()
	router.Post("/api/sessions/{key}/worktree-ownership", h.handleSessionWorktreeOwnershipClear)
	router.Post("/api/sessions/worktree-ownership", h.handleSessionWorktreeOwnershipClear)
	return router
}

// 两条入口都要钉住：带 key 的那条前端用，不带 key 的那条给只有
// CLAUDE_CODE_SESSION_ID 的脚本用（与 repoint 同构）。任何一条丢线，
// 修复路径就又回到「没有入口」。
func TestSessionWorktreeOwnershipRouteRegistered(t *testing.T) {
	for _, path := range []string{
		"/api/sessions/some-key/worktree-ownership",
		"/api/sessions/worktree-ownership",
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"root_id":"root"}`))
		worktreeOwnershipRouter(newWorktreeOwnershipHandler(t)).ServeHTTP(response, request)
		// 空 registry 里的 root 查不到 → 400。404 只该由路由缺失产生。
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d body=%s, want 400", path, response.Code, response.Body.String())
		}
	}
}

// 两个标识都没给时必须显式报错，不能带着空串往下走（那会反查成空 key）。
func TestSessionWorktreeOwnershipRequiresIdentifier(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/sessions/worktree-ownership",
		strings.NewReader(`{"root_id":"root"}`))
	worktreeOwnershipRouter(newWorktreeOwnershipHandler(t)).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "agent_session_id") {
		t.Fatalf("status = %d body=%s, want 400 naming the identifier", response.Code, response.Body.String())
	}
}

// root_id 是必须的：缺了它 service 层会因为「root not found」而不是「参数没给」,
// 前端看到的提示会指向错误的方向。
func TestSessionWorktreeOwnershipRequiresRootID(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/sessions/some-key/worktree-ownership",
		strings.NewReader(`{}`))
	worktreeOwnershipRouter(newWorktreeOwnershipHandler(t)).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "root_id") {
		t.Fatalf("status = %d body=%s, want 400 naming root_id", response.Code, response.Body.String())
	}
}
