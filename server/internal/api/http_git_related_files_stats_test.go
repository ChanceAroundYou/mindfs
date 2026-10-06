package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 批量关联文件统计的入口守卫。
//
// 这里钉的是**入口的三道闸**：路由注册、限体后的 JSON 校验、目标数量上限。
// 上限的意义是别让单个请求把后端的 git 子进程数放大到不可控 —— 一个任务的关联文件
// 实测在 100 以内。
//
// 统计本身的正确性（批量与逐文件逐字段一致、git 调用次数真的更少）在
// gitview_test.go 里对拍，不在这里重复。
func TestGitRelatedFileStatsRejectsBadRequest(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "invalid json", body: "{"},
		{name: "missing root", body: `{"targets":[{"id":"a","path":"a.txt"}]}`},
		{name: "too many targets", body: tooManyStatTargetsBody(t)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 直接用零值 handler 调方法：三条校验都在 h.service() 之前，
			// 所以不需要真的把 registry 搭起来。
			h := &HTTPHandler{}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/git/related-files/stats", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			h.handleGitRelatedFileStats(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func tooManyStatTargetsBody(t *testing.T) string {
	t.Helper()
	targets := make([]map[string]string, 0, maxGitRelatedFileStatTargets+1)
	for i := 0; i <= maxGitRelatedFileStatTargets; i++ {
		targets = append(targets, map[string]string{"id": "id", "path": "a.txt"})
	}
	body, err := json.Marshal(map[string]any{"root": "root", "targets": targets})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return string(body)
}

// 路由必须真的挂上：撤掉它前端会静默退回 404、徽标全空，而 Go 侧的单测照样全绿。
//
// 不能用 GET 探 —— 这个路由表把 MethodNotAllowed 也指向 handleNotFound，
// 「方法不对」和「路径没注册」同样是 404，区分不出来。所以发一个合法的 POST：
// 只要不是 404，就说明路径确实进了路由表。
func TestGitRelatedFileStatsRouteIsRegistered(t *testing.T) {
	handler := (&HTTPHandler{AppContext: &AppContext{}}).Routes()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/git/related-files/stats",
		strings.NewReader(`{"root":"root","targets":[]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusNotFound {
		t.Fatalf("批量统计的路由没注册 —— 前端会拿到 404，徽标全空；body=%s", rec.Body.String())
	}
}
