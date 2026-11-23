package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	rootfs "mindfs/server/internal/fs"
	"mindfs/server/internal/kanban"
)

// 已知 root 之外的 id 必须报「不存在」，不能像 singleRootProviderT 那样来者不拒。
type strictRootProviderT struct{ root rootfs.RootInfo }

func (p strictRootProviderT) GetRoot(id string) (rootfs.RootInfo, error) {
	if id != p.root.ID {
		return rootfs.RootInfo{}, errors.New("root not found")
	}
	return p.root, nil
}

func (p strictRootProviderT) ListRoots() []rootfs.RootInfo { return []rootfs.RootInfo{p.root} }

// 未知账户在 worker 上是空工作区（产品语义），root 查不到时只回「root not found」
// 会让调用方误判为路由或编码故障。错误里要说清是哪个账户、哪个 root，并指向核对方向。
func TestTaskCreateRootNotFoundNamesAccountAndNode(t *testing.T) {
	root := rootfs.NewRootInfo("root", "root", t.TempDir())
	svc := kanban.NewService(strictRootProviderT{root: root})
	h := (&HTTPHandler{AppContext: &AppContext{Kanban: svc}}).Routes()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/tasks?user=local",
		strings.NewReader(`{"root_id":"冰晶智能","input":"validation-only"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	for _, want := range []string{"root not found", "冰晶智能", "user=local", "target node"} {
		if !strings.Contains(body.Error, want) {
			t.Fatalf("error %q missing %q", body.Error, want)
		}
	}
}
