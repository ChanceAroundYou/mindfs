package agent

import (
	"path/filepath"
	"testing"

	"mindfs/server/internal/agent/proxy"
)

// 完成记录的用量差分（Task 13）直接走池级账本：与胶囊用同一个 key，
// 否则同一个会话的摘要和用量会落在两本账上，数字对不上还没人发现。
func TestPoolSessionFoldUsageForwardsPoolKey(t *testing.T) {
	archiveDir := filepath.Join(t.TempDir(), "context")
	ps, err := proxy.NewServerWithLiveCompact(proxy.LiveCompactConfig{
		Mode: proxy.LiveCompactOn, RailTokens: -1, ArchiveDir: archiveDir, ArchiveEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()
	p := &Pool{compactProxy: ps}
	postPoolFold(t, ps, "pool-usage-token", "claude-session-9")

	rows := p.SessionFoldUsage("claude-session-9")
	if len(rows) != 1 {
		t.Fatalf("pool key rows = %d, want 1", len(rows))
	}
	if rows[0].At.IsZero() {
		t.Fatal("row lost its timestamp")
	}
	if got := p.SessionFoldUsage("session-9"); len(got) != 0 {
		t.Fatalf("bare session key aliased onto the agent-prefixed ledger: %d rows", len(got))
	}
}

func TestPoolSessionFoldUsageWithoutProxy(t *testing.T) {
	p := &Pool{}
	if got := p.SessionFoldUsage("anything"); len(got) != 0 {
		t.Fatalf("rows without a proxy = %d", len(got))
	}
	var nilPool *Pool
	if got := nilPool.SessionFoldUsage("anything"); len(got) != 0 {
		t.Fatalf("nil pool rows = %d", len(got))
	}
}
