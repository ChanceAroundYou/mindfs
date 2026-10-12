package session

import (
	"context"
	"testing"
	"time"

	rootfs "mindfs/server/internal/fs"
)

func TestLatestSessionUpdatedAtIncludesChildSessions(t *testing.T) {
	ctx := context.Background()
	manager := NewManager(rootfs.NewRootInfo("test", "test", t.TempDir()))
	parent, err := manager.Create(ctx, CreateInput{Type: TypeChat, Name: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := manager.Create(ctx, CreateInput{
		Type:             TypeChat,
		Name:             "child",
		ParentSessionKey: parent.Key,
	})
	if err != nil {
		t.Fatal(err)
	}

	base := time.Now().UTC().Add(time.Hour)
	parentAt := base
	childAt := base.Add(time.Minute)
	if err := manager.AddExchangeForAgentAt(ctx, parent, "agent", "parent", "claude", "", "", "", parentAt); err != nil {
		t.Fatalf("append parent exchange: %v", err)
	}
	if err := manager.AddExchangeForAgentAt(ctx, child, "agent", "child", "claude", "", "", "", childAt); err != nil {
		t.Fatalf("append child exchange: %v", err)
	}

	got, err := manager.LatestSessionUpdatedAt(ctx)
	if err != nil {
		t.Fatalf("LatestSessionUpdatedAt: %v", err)
	}
	if !got.Equal(childAt) {
		t.Fatalf("latest session time = %s, want child activity %s", got, childAt)
	}
}
