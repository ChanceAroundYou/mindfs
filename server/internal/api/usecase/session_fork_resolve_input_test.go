package usecase

import (
	"testing"
	"time"

	"mindfs/server/internal/session"
)

// fork 解析必须带上目标轮的落盘时间：只传 AgentTurnIndex 时，MindFS 行序与转录
// 回合切分的漂移会让长会话直接越界（实测 claude 168 vs 145、codex 12 vs 9）。
// 这里钉住「时间戳确实被传下去」这条接线，避免有人在调用点把它丢掉。
func TestForkResolveInputCarriesTargetTimestamp(t *testing.T) {
	ts := time.Date(2026, 11, 19, 17, 19, 5, 783970941, time.UTC)
	in := forkResolveInput("/repo", "agent-session", 168, session.Exchange{Seq: 362, Timestamp: ts})
	if !in.TargetTimestamp.Equal(ts) {
		t.Fatalf("TargetTimestamp = %v, want %v", in.TargetTimestamp, ts)
	}
	if in.AgentTurnIndex != 168 || in.AgentSessionID != "agent-session" || in.RootPath != "/repo" {
		t.Fatalf("unexpected input: %#v", in)
	}
}
