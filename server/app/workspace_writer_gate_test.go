package app

import (
	"testing"

	"mindfs/server/internal/kanban"
)

// D15：每账户的 Kanban Service 是独立实例，但必须注入同一个 app 级物理
// 工作树 writer gate；各账户各拿一把锁等于没有跨账户互斥。
func TestEmptyWorkspaceUsesSharedWriterGate(t *testing.T) {
	gate := kanban.NewWriterGate()
	m := &workspaceManager{shared: sharedServices{writerGate: gate}}
	ctx, err := m.emptyWorkspace()
	if err != nil {
		t.Fatalf("emptyWorkspace: %v", err)
	}
	if ctx.Kanban == nil {
		t.Fatal("empty workspace has no kanban service")
	}
	if ctx.Kanban.WriterGate != gate {
		t.Fatal("empty workspace kanban did not receive the app-shared writer gate")
	}
}

// 两账户各自调用 emptyWorkspace 会构造不同的 Kanban 实例；它们必须拿到同一个
// gate 指针，否则第二个账户的写节点不受第一个账户持有的锁约束。
func TestTwoWorkspacesShareOneWriterGate(t *testing.T) {
	gate := kanban.NewWriterGate()
	m := &workspaceManager{shared: sharedServices{writerGate: gate}}
	a, err := m.emptyWorkspace()
	if err != nil {
		t.Fatalf("first emptyWorkspace: %v", err)
	}
	b, err := m.emptyWorkspace()
	if err != nil {
		t.Fatalf("second emptyWorkspace: %v", err)
	}
	if a.Kanban == b.Kanban {
		t.Fatal("emptyWorkspace should build a separate Kanban instance per call")
	}
	if a.Kanban.WriterGate != b.Kanban.WriterGate {
		t.Fatal("two account services do not share the writer gate")
	}
}
