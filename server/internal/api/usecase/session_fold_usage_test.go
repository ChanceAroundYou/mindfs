package usecase

import (
	"context"
	"testing"
)

// 完成记录的用量差分（Plan 2026-11-20 Task 13）：账本按池级 key 记，调用方给的是
// MindFS 会话 key，所以这一层必须先把 agent 前缀补上 —— 漏了只会读到空账本，
// 界面上表现为「没有用量」，看不出是接线错了。
func TestGetSessionFoldUsageResolvesPoolSessionKey(t *testing.T) {
	registry, service, current, rootID := newContextSavingFixture(t)
	recordFoldInLedger(t, registry.archiveDir, "claude-"+current.Key)

	rows, err := service.GetSessionFoldUsage(context.Background(), rootID, current.Key)
	if err != nil {
		t.Fatalf("usecase error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 (usecase did not resolve the pool key)", len(rows))
	}
	if rows[0].At.IsZero() {
		t.Fatal("row lost its timestamp")
	}
	// 这份夹具的上游没有 usage 块：读数必须是「没有」，而不是三个 0 ——
	// 0 会被完成记录当成「这一轮没花钱」。
	if !rows[0].Missing {
		t.Fatalf("row = %+v, want Missing=true for an upstream without usage", rows[0])
	}

	// 未知会话是真错误（与 GetSessionContextSaving 一致）：账本再空也不该
	// 让「会话根本不存在」看起来像「这个会话没折过」。
	if _, err := service.GetSessionFoldUsage(context.Background(), rootID, "never-seen"); err == nil {
		t.Fatal("unknown session key must error")
	}
}

func TestGetSessionFoldUsageRequiresAKey(t *testing.T) {
	_, service, _, rootID := newContextSavingFixture(t)
	if _, err := service.GetSessionFoldUsage(context.Background(), rootID, "  "); err == nil {
		t.Fatal("empty session key must be rejected")
	}
}
