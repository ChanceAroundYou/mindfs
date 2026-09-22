package session

import (
	"context"
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
)

// 游标是「已提交到哪」的唯一凭据，必须完整落盘。
// 曾经只持久化 path/offset/mtime 三列，新增的 CommittedOffset 被静默丢弃 ——
// 读回来恒为 0，导入器于是退回「提交到文件末尾」，把刻意扣下的未收尾轮永久跳过。
func TestExternalSessionCursorRoundTrip(t *testing.T) {
	ctx := context.Background()
	m := NewManager(rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir()))
	s, err := m.Create(ctx, CreateInput{Type: TypeChat, Name: "cursor"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := m.UpsertAgentBinding(ctx, AgentBinding{
		SessionKey:     s.Key,
		Agent:          "claude",
		AgentSessionID: "claude-session-1",
	}); err != nil {
		t.Fatalf("upsert binding: %v", err)
	}

	want := agenttypes.ExternalSessionCursor{
		SourcePath:      "/tmp/transcript.jsonl",
		Offset:          4096,
		ModTimeUnixNano: 1700000000000000000,
		CommittedOffset: 2048,
	}
	if err := m.UpdateExternalSessionCursor(ctx, s.Key, "claude", want); err != nil {
		t.Fatalf("update cursor: %v", err)
	}

	got, err := m.GetAgentBinding(ctx, s.Key, "claude")
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	if cursor := got.ExternalCursor(); cursor != want {
		t.Fatalf("游标未完整往返：\n got=%+v\nwant=%+v", cursor, want)
	}
}
