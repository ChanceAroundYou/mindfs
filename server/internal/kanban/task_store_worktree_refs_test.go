package kanban

import (
	"context"
	"testing"
)

// 引用计数是「共享 worktree 收尾」的唯一判据：数错了不是多拆一棵树、就是永远
// 拆不掉。三个口径分别钉住 —— 只数别的任务、只数同一个路径、空路径一律 0。
func TestWorktreeReferenceCount(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	mk := func(name string) Task {
		detail, err := svc.CreateTask(ctx, CreateTaskInput{
			RootID: root.ID,
			Stages: []StageTemplate{userStage("Describe")},
			Input:  name,
		})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		return detail.Task
	}
	a, b, c := mk("a"), mk("b"), mk("c")
	pinWorktree(t, store, a, root.ID, "/trees/shared")
	pinWorktree(t, store, b, root.ID, "/trees/shared")
	pinWorktree(t, store, c, root.ID, "/trees/other")

	cases := []struct {
		name  string
		path  string
		excl  string
		want  int
		notes string
	}{
		{"same path, other task", "/trees/shared", a.ID, 1, "b 还在用，a 自己不算"},
		{"other path", "/trees/other", c.ID, 0, "同名的树才算共享"},
		{"empty path", "", a.ID, 0, "还没建树不是「大家都在用主 checkout」"},
		{"last holder", "/trees/other", "", 1, "不排除自己时把自己也算上"},
	}
	for _, tc := range cases {
		got, err := store.WorktreeReferenceCount(ctx, tc.path, tc.excl)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s: got %d, want %d (%s)", tc.name, got, tc.want, tc.notes)
		}
	}
}
