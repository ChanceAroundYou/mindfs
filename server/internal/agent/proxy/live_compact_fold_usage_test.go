package proxy

import (
	"path/filepath"
	"testing"
)

// 完成记录要按 Run 的时间窗求和（Task 13），所以读口必须交出账本整行 ——
// 胶囊只需要汇总，汇总里没有时间戳，用它裁窗口只能裁出「全部」或「全无」。
func TestSessionFoldUsageReturnsLedgerRows(t *testing.T) {
	archiveDir := filepath.Join(t.TempDir(), "context")
	s, err := NewServerWithLiveCompact(LiveCompactConfig{
		Mode: LiveCompactOn, KeepRounds: 1, MinBlockTokens: 100, RailTokens: -1,
		MinBodyBytes: 1, ArchiveDir: archiveDir, ArchiveEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	upstream := upstreamWithUsage(t, `{"usage":{"input_tokens":1234,"cache_creation_input_tokens":5000,"cache_read_input_tokens":99,"output_tokens":7}}`)
	s.RegisterSessionWithKey("u4", upstream.URL, "session-u4")

	postToProxy(t, s, "u4", foldBody(t, 4, 8001))
	waitForLedgerRequests(t, s, "session-u4", 1)

	rows := s.SessionFoldUsage("session-u4")
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.UsageInput != 1234 || row.UsageOutput != 7 || row.UsageCacheRead != 99 || row.UsageCacheCreation != 5000 {
		t.Fatalf("row counters = %+v, want input 1234 / output 7 / read 99 / write 5000", row)
	}
	if row.TS == 0 {
		t.Fatal("row lost its timestamp — the run window cannot be cut without it")
	}
	// 胶囊的每一行都按 token 显示，所以这一行必须记下两个端点：折叠前的原请求
	// 与真正发出去的 body。只记字节会让前端拿字节除 4 反推，CJK 内容会失真。
	if row.ReqTokensEst <= 0 || row.ForwardTokensEst <= 0 {
		t.Fatalf("token estimates = req %d / forward %d, want both > 0", row.ReqTokensEst, row.ForwardTokensEst)
	}
	if row.ReqTokensEst <= row.ForwardTokensEst {
		t.Fatalf("token estimates = req %d / forward %d, want req > forward", row.ReqTokensEst, row.ForwardTokensEst)
	}
	// 「折叠结果 N 个」把 sticky 与本轮新折混在一起；新列让 UI 说清这一轮到底
	// 折了没有，而 sticky 只会让总数变大、不能证明本轮干过活。
	if row.NewBlocks <= 0 || row.NewBlocks > row.Blocks {
		t.Fatalf("new blocks = %d / total %d, want 0 < new <= total", row.NewBlocks, row.Blocks)
	}
	if got := s.SessionFoldUsage("never-seen"); len(got) != 0 {
		t.Fatalf("unknown session rows = %d, want 0", len(got))
	}
}
