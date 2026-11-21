package fold

import (
	"fmt"
	"strings"
	"testing"
)

// resultSpec describes one result in the neutral conversation the planner sees.
type resultSpec struct {
	bytes   int
	parts   int   // 0 means one part
	sizes   []int // per-part payload sizes; overrides parts/bytes when set
	nonText bool
	failed  bool
}

// buildConv assembles a conversation plus the byte storage its spans point
// into. The body is not valid JSON: PlanCandidates only slices it by span.
func buildConv(specs [][]resultSpec) (*Conversation, []byte) {
	var body []byte
	conv := &Conversation{}
	call := 0
	for _, roundSpec := range specs {
		round := Round{}
		for _, spec := range roundSpec {
			result := Result{
				CallID:       fmt.Sprintf("call-%d", call),
				ToolName:     "Read",
				InputSummary: fmt.Sprintf(`{"file_path":"/tmp/f%d"}`, call),
				Failed:       spec.failed,
				NonText:      spec.nonText,
			}
			call++
			// A non-text result may still carry text blocks (an image plus a
			// caption); spec.bytes > 0 models that, and the planner keeps all of
			// it because one pointer cannot replace the whole result.
			if !spec.nonText || spec.bytes > 0 {
				sizes := spec.sizes
				if len(sizes) == 0 {
					count := spec.parts
					if count == 0 {
						count = 1
					}
					sizes = make([]int, count)
					for part := range sizes {
						sizes[part] = spec.bytes
					}
				}
				for _, size := range sizes {
					if len(body) > 0 {
						body = append(body, ',')
					}
					start := len(body)
					body = append(body, EncodeJSONStringLiteral(strings.Repeat("x", size))...)
					result.Parts = append(result.Parts, Span{Start: start, End: len(body)})
					result.Bytes += len(body) - start
					result.Tokens += EstimateByteTokens(body[start:])
				}
			}
			round.Results = append(round.Results, result)
		}
		conv.Rounds = append(conv.Rounds, round)
	}
	return conv, body
}

func candidateIDs(candidates []Candidate) []string {
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate.Result.CallID)
	}
	return out
}

func planTestConfig() Config {
	return Config{
		KeepRounds:       0,
		KeepTailTokens:   1 << 30,
		MinBlockTokens:   10,
		LargeBlockTokens: 1 << 30,
	}
}

func TestPlanCandidatesProtectsNewestRounds(t *testing.T) {
	conv, body := buildConv([][]resultSpec{
		{{bytes: 4000}},
		{{bytes: 4000}},
		{{bytes: 4000}},
	})
	cfg := planTestConfig()
	cfg.KeepRounds = 1
	candidates := PlanCandidates(body, conv, cfg, nil)
	if got := strings.Join(candidateIDs(candidates), ","); got != "call-0,call-1" {
		t.Fatalf("candidates = %s, want call-0,call-1", got)
	}
}

func TestPlanCandidatesCarvesOutOversizedRound(t *testing.T) {
	conv, body := buildConv([][]resultSpec{
		{{bytes: 100}},
		{{bytes: 40000}},
	})
	cfg := planTestConfig()
	cfg.KeepRounds = 5 // every round inside the keep window
	cfg.LargeBlockTokens = 1000
	candidates := PlanCandidates(body, conv, cfg, nil)
	if got := strings.Join(candidateIDs(candidates), ","); got != "call-1" {
		t.Fatalf("candidates = %s, want the oversized round only", got)
	}
}

func TestPlanCandidatesHonoursKeepTailTokens(t *testing.T) {
	// Three rounds of ~1000 tokens each with a 1500-token tail budget: the
	// newest round always stays, the two before it become eligible.
	round := []resultSpec{{bytes: 4000}}
	conv, body := buildConv([][]resultSpec{round, round, round})
	cfg := planTestConfig()
	cfg.KeepRounds = 3
	cfg.KeepTailTokens = 1500
	candidates := PlanCandidates(body, conv, cfg, nil)
	if got := strings.Join(candidateIDs(candidates), ","); got != "call-0,call-1" {
		t.Fatalf("candidates = %s", got)
	}
}

// A result with several text blocks is foldable, but only through its largest
// block: a pointer replaces one literal. Codex sends every tool result as a
// header block plus the payload, so skipping multi-part results skipped the
// entire harness (measured on a real 347 KB request, 2026-11-20).
func TestPlanCandidatesFoldsLargestBlockOfMultiPartResult(t *testing.T) {
	conv, body := buildConv([][]resultSpec{{
		{bytes: 4000, nonText: true},
		{parts: 2, sizes: []int{20, 4000}},
		{bytes: 4000},
	}})
	candidates := PlanCandidates(body, conv, planTestConfig(), nil)
	if got := strings.Join(candidateIDs(candidates), ","); got != "call-1,call-2" {
		t.Fatalf("candidates = %s", got)
	}
	// call-1 must point at the payload literal (4000 bytes + two quotes), not at
	// the 20-byte header.
	parts := candidates[0].Result.Parts
	if len(parts) != 1 {
		t.Fatalf("parts = %d, want the largest block only", len(parts))
	}
	if got := parts[0].End - parts[0].Start; got != 4002 {
		t.Fatalf("span width = %d, want 4002", got)
	}
}

func TestPlanCandidatesSkipsSmallFailedResult(t *testing.T) {
	conv, body := buildConv([][]resultSpec{{
		{bytes: 8, failed: true},    // below min_block_tokens
		{bytes: 4000, failed: true}, // above it
	}})
	cfg := planTestConfig()
	candidates := PlanCandidates(body, conv, cfg, nil)
	if got := strings.Join(candidateIDs(candidates), ","); got != "call-1" {
		t.Fatalf("candidates = %s", got)
	}
}

func TestPlanCandidatesMarksCommitted(t *testing.T) {
	conv, body := buildConv([][]resultSpec{{{bytes: 4000}}, {{bytes: 4000}}})
	cfg := planTestConfig()
	first := PlanCandidates(body, conv, cfg, nil)
	if len(first) != 2 {
		t.Fatalf("first pass candidates = %d", len(first))
	}
	commits := NewCommits()
	commits.CommitAll([]PointerCacheKey{first[0].Key}, nil, nil)
	second := PlanCandidates(body, conv, cfg, commits)
	if len(second) != 2 {
		t.Fatalf("second pass candidates = %d", len(second))
	}
	if !second[0].Committed || second[1].Committed {
		t.Fatalf("commit marks = %v,%v", second[0].Committed, second[1].Committed)
	}
}
