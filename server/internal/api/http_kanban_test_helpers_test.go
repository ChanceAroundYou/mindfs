package api

import (
	"context"
	"net/http"
	"testing"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/kanban"
)

type singleRootProviderT struct{ root fs.RootInfo }

func (p singleRootProviderT) GetRoot(id string) (fs.RootInfo, error) { return p.root, nil }
func (p singleRootProviderT) ListRoots() []fs.RootInfo               { return []fs.RootInfo{p.root} }

func kanbanHTTPFixture(t *testing.T) (http.Handler, *kanban.Service, fs.RootInfo, string) {
	t.Helper()
	root := fs.NewRootInfo("root", "root", t.TempDir())
	svc := kanban.NewService(singleRootProviderT{root: root})
	svc.SetWriterGate(kanban.NewWriterGate())
	detail, err := svc.CreateTask(context.Background(), kanban.CreateTaskInput{
		RootID: root.ID, Input: "", // empty to avoid triggering background activity goroutine
	})
	if err != nil {
		t.Fatal(err)
	}
	return (&HTTPHandler{AppContext: &AppContext{Kanban: svc}}).Routes(), svc, root, detail.Task.ID
}
