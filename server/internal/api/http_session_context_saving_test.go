package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"mindfs/server/internal/fs"

	"github.com/go-chi/chi/v5"
)

// newContextSavingHandler 装配到「空 registry」为止：会话查不到时 service 层直接
// 返回错误，用例因此不需要 SQLite。
func newContextSavingHandler(t *testing.T) *HTTPHandler {
	t.Helper()
	registry := fs.NewRegistry(filepath.Join(t.TempDir(), "registry.json"))
	return &HTTPHandler{AppContext: &AppContext{Dirs: registry}}
}

func contextSavingRouter(h *HTTPHandler) *chi.Mux {
	router := chi.NewRouter()
	router.Get("/api/sessions/{key}/context-saving", h.handleSessionContextSaving)
	return router
}

// TestSessionContextSavingRouteRegistered 钉住路由本身：路径拼错时前端 chip 静默
// 不出现，只有 404 能说明是接线问题而不是「这个会话没有节省」。
func TestSessionContextSavingRouteRegistered(t *testing.T) {
	response := httptest.NewRecorder()
	contextSavingRouter(newContextSavingHandler(t)).ServeHTTP(response,
		httptest.NewRequest(http.MethodGet, "/api/sessions/some-key/context-saving", nil))
	// 会话在空 registry 里不存在 → 400，而不是 404：404 只该由路由缺失产生。
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400 for an unknown session", response.Code, response.Body.String())
	}
}

// 空 key 必须显式报错，不能静默返回空摘要（那会显示成「没有节省」而不是「请求写错了」）。
func TestSessionContextSavingRequiresKey(t *testing.T) {
	response := httptest.NewRecorder()
	contextSavingRouter(newContextSavingHandler(t)).ServeHTTP(response,
		httptest.NewRequest(http.MethodGet, "/api/sessions/%20/context-saving", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if message, _ := payload["error"].(string); message == "" {
		t.Fatalf("error body missing message: %s", response.Body.String())
	}
}
