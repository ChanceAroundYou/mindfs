package fold

import "testing"

// The rail fires on total request size, but the planner can only act on tool
// output outside the protected tail. Without the split, "a 130 KB request
// folded 15 KB" cannot be told apart from "the request had nothing foldable",
// and the only lever left is a config guess.
//
// Every literal in the fixture carries two quote bytes, so a 5000-byte payload
// is a 5002-byte span on the wire — the numbers below are the wire widths the
// planner actually acts on.
func TestPlanStatsSplitsResultBytesByRule(t *testing.T) {
	conv, body := buildConv([][]resultSpec{
		{{bytes: 5000}},               // eligible, one text block: 5002
		{{sizes: []int{1000, 2000}}},  // eligible, multi-part: 3004, largest is 2002
		{{bytes: 900, failed: true}},  // failed below min_block_tokens: 902
		{{bytes: 400, nonText: true}}, // carries an image and text: 402
		{{bytes: 7000}},               // newest round: protected tail, 7002
	})
	cfg := planTestConfig()
	cfg.KeepRounds = 1
	cfg.KeepTailTokens = 0
	cfg.MinBlockTokens = 2000

	candidates, stats := Plan(body, conv, cfg, nil)
	if len(candidates) != 2 {
		t.Fatalf("candidates = %d, want 2", len(candidates))
	}
	if stats.BodyBytes != len(body) {
		t.Fatalf("body bytes = %d, want %d", stats.BodyBytes, len(body))
	}
	if stats.ResultBytes != 16312 {
		t.Fatalf("result bytes = %d, want 16312", stats.ResultBytes)
	}
	if stats.ProtectedBytes != 7002 {
		t.Fatalf("protected bytes = %d, want 7002", stats.ProtectedBytes)
	}
	if stats.NonTextBytes != 402 {
		t.Fatalf("non-text bytes = %d, want 402", stats.NonTextBytes)
	}
	if stats.MultiPartKeptBytes != 1002 {
		t.Fatalf("multi-part kept bytes = %d, want 1002", stats.MultiPartKeptBytes)
	}
	if stats.FailedSmallBytes != 902 {
		t.Fatalf("failed-small bytes = %d, want 902", stats.FailedSmallBytes)
	}
	if stats.CandidateBytes != 7004 {
		t.Fatalf("candidate bytes = %d, want 7004", stats.CandidateBytes)
	}
	// The buckets partition ResultBytes exactly; a byte counted twice would make
	// the diagnostic lie in the direction of "there was more to fold".
	if sum := stats.ProtectedBytes + stats.NonTextBytes + stats.MultiPartKeptBytes + stats.FailedSmallBytes + stats.CandidateBytes; sum != stats.ResultBytes {
		t.Fatalf("buckets sum to %d, want %d", sum, stats.ResultBytes)
	}
}

// A committed block is still foldable — it is folded every turn to keep the
// boundary monotone — so it belongs in the candidate bucket, not in "already
// folded, ignore".
func TestPlanStatsCountsCommittedBlocksAsCandidates(t *testing.T) {
	conv, body := buildConv([][]resultSpec{{{bytes: 4000}}, {{bytes: 100}}})
	cfg := planTestConfig()
	cfg.KeepRounds = 0
	first, _ := Plan(body, conv, cfg, nil)
	commits := NewCommits()
	commits.CommitAll([]PointerCacheKey{first[0].Key}, nil, nil)

	_, stats := Plan(body, conv, cfg, commits)
	if stats.CandidateBytes != 4104 {
		t.Fatalf("candidate bytes = %d, want 4104", stats.CandidateBytes)
	}
}

// The body size is reported even when there is no conversation to plan: a parse
// that produced nothing must still be distinguishable from a body that never
// arrived.
func TestPlanStatsReportsBodyBytesWithoutConversation(t *testing.T) {
	candidates, stats := Plan([]byte("{}"), nil, planTestConfig(), nil)
	if len(candidates) != 0 {
		t.Fatalf("candidates = %d, want 0", len(candidates))
	}
	if stats.BodyBytes != 2 {
		t.Fatalf("body bytes = %d, want 2", stats.BodyBytes)
	}
	if stats.ResultBytes != 0 || stats.ProtectedBytes != 0 || stats.CandidateBytes != 0 {
		t.Fatalf("stats = %+v, want only BodyBytes", stats)
	}
}
