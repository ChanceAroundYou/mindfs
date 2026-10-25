package usecase

import (
	"context"
	"testing"
	"time"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

func TestDropEmptyBashSubSessions(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := session.NewManager(root)
	parent, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "claude", Name: "parent"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	newChild := func(name string) *session.Session {
		child, err := manager.Create(ctx, session.CreateInput{
			Type:             session.TypeChat,
			ParentSessionKey: parent.Key,
			Agent:            "claude",
			Name:             name,
		})
		if err != nil {
			t.Fatalf("create child %q: %v", name, err)
		}
		return child
	}
	empty := newChild("Bash")
	if err := manager.AddExchangeForAgentAt(ctx, empty, "agent", "", "claude", "", "", "", time.Time{}); err != nil {
		t.Fatalf("add empty exchange: %v", err)
	}
	realContent := newChild("Bash")
	if err := manager.AddExchangeForAgentAt(ctx, realContent, "agent", "real output", "claude", "", "", "", time.Time{}); err != nil {
		t.Fatalf("add real exchange: %v", err)
	}
	namedOther := newChild("Explore thing")

	out := dropEmptyBashSubSessions(ctx, manager, []*session.Session{empty, realContent, namedOther})
	keys := map[string]bool{}
	for _, item := range out {
		keys[item.Key] = true
	}
	if keys[empty.Key] {
		t.Fatal("empty bash sub-session should be dropped")
	}
	if !keys[realContent.Key] {
		t.Fatal("bash sub-session with content must stay")
	}
	if !keys[namedOther.Key] {
		t.Fatal("non-bash sub-session must stay")
	}
}
