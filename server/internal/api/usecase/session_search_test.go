package usecase

import (
	"context"
	"testing"
	"time"

	rootfs "mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

func TestSearchSessionsMultiRootIncludesRootIDs(t *testing.T) {
	ctx := context.Background()
	rootA := rootfs.NewRootInfo("root-a", "Root A", t.TempDir())
	rootB := rootfs.NewRootInfo("root-b", "Root B", t.TempDir())
	managerA := session.NewManager(rootA)
	managerB := session.NewManager(rootB)
	registry := &multiRootSearchTestRegistry{
		roots:    []rootfs.RootInfo{rootA, rootB},
		managers: map[string]*session.Manager{rootA.ID: managerA, rootB.ID: managerB},
	}
	service := Service{Registry: registry}

	if _, err := managerA.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "needle alpha"}); err != nil {
		t.Fatalf("create root A session: %v", err)
	}
	rootBSession, err := managerB.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "beta"})
	if err != nil {
		t.Fatalf("create root B session: %v", err)
	}
	if err := managerB.AddExchangeForAgent(ctx, rootBSession, "user", "content has needle inside", "codex", "", "", ""); err != nil {
		t.Fatalf("add root B exchange: %v", err)
	}

	out, err := service.SearchSessions(ctx, SearchSessionsInput{
		Query:     "needle",
		Limit:     20,
		MultiRoot: true,
	})
	if err != nil {
		t.Fatalf("SearchSessions returned error: %v", err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("items len = %d, want 2: %#v", len(out.Items), out.Items)
	}
	seen := map[string]bool{}
	for _, item := range out.Items {
		seen[item.RootID] = true
	}
	if !seen[rootA.ID] || !seen[rootB.ID] {
		t.Fatalf("root ids = %#v, want %q and %q", seen, rootA.ID, rootB.ID)
	}
}

func TestSearchSessionsMultiRootAppliesGlobalLimit(t *testing.T) {
	ctx := context.Background()
	rootA := rootfs.NewRootInfo("root-a", "Root A", t.TempDir())
	rootB := rootfs.NewRootInfo("root-b", "Root B", t.TempDir())
	managerA := session.NewManager(rootA)
	managerB := session.NewManager(rootB)
	registry := &multiRootSearchTestRegistry{
		roots:    []rootfs.RootInfo{rootA, rootB},
		managers: map[string]*session.Manager{rootA.ID: managerA, rootB.ID: managerB},
	}
	service := Service{Registry: registry}

	if _, err := managerA.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "needle alpha"}); err != nil {
		t.Fatalf("create root A session: %v", err)
	}
	if _, err := managerB.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "needle beta"}); err != nil {
		t.Fatalf("create root B session: %v", err)
	}

	out, err := service.SearchSessions(ctx, SearchSessionsInput{
		Query:     "needle",
		Limit:     1,
		MultiRoot: true,
	})
	if err != nil {
		t.Fatalf("SearchSessions returned error: %v", err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("items len = %d, want 1: %#v", len(out.Items), out.Items)
	}
	if out.Items[0].RootID == "" {
		t.Fatal("first item root id is empty")
	}
}

// after_time 只过滤普通列表；置顶**不受时间窗约束**（要过滤的话用户就没法把
// 一个老会话钉在顶上）—— 这条规则从前挂在 ListSessionsOutput.PinnedSessions 上，
// 置顶迁到主节点表后改由 api 层从置顶表单独取（http_pins.go），所以这里
// 只守剩下的那半：普通列表确实被 after_time 正确过滤了。
func TestListSessionsAppliesAfterTimeToOrdinaryList(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	manager := session.NewManager(root, session.WithClock(func() time.Time { return now }))
	registry := &commandTestRegistry{root: root, manager: manager}
	service := Service{Registry: registry}

	old, err := manager.Create(context.Background(), session.CreateInput{Type: session.TypeChat, Name: "Old"})
	if err != nil {
		t.Fatalf("create old session: %v", err)
	}
	afterTime := old.UpdatedAt.Add(30 * time.Second)

	now = afterTime.Add(1 * time.Minute)
	fresh, err := manager.Create(context.Background(), session.CreateInput{Type: session.TypeChat, Name: "Fresh"})
	if err != nil {
		t.Fatalf("create fresh session: %v", err)
	}

	out, err := service.ListSessions(context.Background(), ListSessionsInput{
		RootID:    root.ID,
		AfterTime: afterTime,
		Limit:     50,
	})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(out.Sessions) != 1 || out.Sessions[0].Key != fresh.Key {
		t.Fatalf("Sessions = %v, want only fresh %s", usecaseSessionKeys(out.Sessions), fresh.Key)
	}
	if out.TotalCount != 1 {
		t.Fatalf("TotalCount = %d, want 1", out.TotalCount)
	}
}
