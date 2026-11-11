package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 任务模板（预设）已随工作流迁移彻底下线（Phase 3，Scope: G-LJ）：
// 前端不再有入口，后端也不再保留任何模板库。旧客户端/脚本再打这些端点
// 必须落到「路由不存在」，而不是拿到一个空列表然后以为自己还能读写模板。
func TestTaskTemplateRoutesAreGone(t *testing.T) {
	handler := (&HTTPHandler{AppContext: &AppContext{}}).Routes()

	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/task-templates"},
		{http.MethodPost, "/api/task-templates"},
		{http.MethodPut, "/api/task-templates/t1"},
		{http.MethodDelete, "/api/task-templates/t1"},
		{http.MethodGet, "/api/task-stage-templates"},
		{http.MethodPost, "/api/task-stage-templates"},
		{http.MethodDelete, "/api/task-stage-templates/t1"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s %s = %d, want 404 — task templates are gone", tc.method, tc.path, rec.Code)
			}
		})
	}
}
