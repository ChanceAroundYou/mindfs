package session

import (
	"context"
	"testing"

	rootfs "mindfs/server/internal/fs"
)

func boolPtr(value bool) *bool { return &value }

// TestManagerUpdateSessionAuxFlagsRoundTrip 钉住会话行 aux 的 set→get→list→clear
// 全链路（G-AD 定制防覆盖测试：合上游时 sessions 表新列被冲掉，这里先红）。
func TestManagerUpdateSessionAuxFlagsRoundTrip(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	ctx := context.Background()

	created, err := manager.Create(ctx, CreateInput{Type: TypeChat, Name: "Chat"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	msg := "boom"
	if err := manager.UpdateSessionAuxFlags(ctx, created.Key, SessionAuxFlagsPatch{
		AskUserWaiting: boolPtr(true),
		HasPlan:        boolPtr(true),
		HasTodos:       boolPtr(true),
		HasTask:        boolPtr(true),
		SessionError:   &msg,
	}); err != nil {
		t.Fatalf("update aux flags: %v", err)
	}

	got, err := manager.GetMeta(ctx, created.Key)
	if err != nil {
		t.Fatalf("get meta: %v", err)
	}
	if !got.Aux.AskUserWaiting || !got.Aux.HasPlan || !got.Aux.HasTodos || !got.Aux.HasTask {
		t.Fatalf("aux bools = %+v, want all true", got.Aux)
	}
	if got.Aux.SessionError != msg {
		t.Fatalf("session error = %q, want %q", got.Aux.SessionError, msg)
	}

	// 空 patch 是 no-op，不报错也不改。
	if err := manager.UpdateSessionAuxFlags(ctx, created.Key, SessionAuxFlagsPatch{}); err != nil {
		t.Fatalf("empty patch: %v", err)
	}

	// 清零：全 false + 空串，与 ClearTaskAuxFlagsForSession 的 patch 同义。
	empty := ""
	no := false
	if err := manager.UpdateSessionAuxFlags(ctx, created.Key, SessionAuxFlagsPatch{
		AskUserWaiting: &no,
		HasPlan:        &no,
		HasTodos:       &no,
		HasTask:        &no,
		SessionError:   &empty,
	}); err != nil {
		t.Fatalf("clear aux flags: %v", err)
	}
	cleared, err := manager.GetMeta(ctx, created.Key)
	if err != nil {
		t.Fatalf("get meta after clear: %v", err)
	}
	if cleared.Aux != (SessionAuxFlags{}) {
		t.Fatalf("aux after clear = %+v, want zero", cleared.Aux)
	}
}
