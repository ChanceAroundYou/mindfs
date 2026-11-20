package fold

import (
	"fmt"
	"strings"
	"testing"
)

// resultSpec describes one result in the neutral conversation the planner sees.
type resultSpec struct {
	bytes   int
	parts   int // 0 means one part
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
			if !spec.nonText {
				parts := spec.parts
				if parts == 0 {
					parts = 1
				}
				for part := 0; part < parts; part++ {
					if len(body) > 0 {
						body = append(body, ',')
					}
					start := len(body)
					body = append(body, EncodeJSONStringLiteral(strings.Repeat("x", spec.bytes))...)
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
	candidates, _ := PlanCandidates(body, conv, cfg, nil)
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
	candidates, _ := PlanCandidates(body, conv, cfg, nil)
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
	candidates, _ := PlanCandidates(body, conv, cfg, nil)
	if got := strings.Join(candidateIDs(candidates), ","); got != "call-0,call-1" {
		t.Fatalf("candidates = %s", got)
	}
}

func TestPlanCandidatesSkipsNonTextAndMultiPart(t *testing.T) {
	conv, body := buildConv([][]resultSpec{{
		{bytes: 4000, nonText: true},
		{bytes: 4000, parts: 2},
		{bytes: 4000},
	}})
	candidates, multiPart := PlanCandidates(body, conv, planTestConfig(), nil)
	if got := strings.Join(candidateIDs(candidates), ","); got != "call-2" {
		t.Fatalf("candidates = %s", got)
	}
	if multiPart != 1 {
		t.Fatalf("multi-part count = %d, want 1", multiPart)
	}
}

func TestPlanCandidatesSkipsSmallFailedResult(t *testing.T) {
	conv, body := buildConv([][]resultSpec{{
		{bytes: 8, failed: true},    // below min_block_tokens
		{bytes: 4000, failed: true}, // above it
	}})
	cfg := planTestConfig()
	candidates, _ := PlanCandidates(body, conv, cfg, nil)
	if got := strings.Join(candidateIDs(candidates), ","); got != "call-1" {
		t.Fatalf("candidates = %s", got)
	}
}

func TestPlanCandidatesMarksCommitted(t *testing.T) {
	conv, body := buildConv([][]resultSpec{{{bytes: 4000}}, {{bytes: 4000}}})
	cfg := planTestConfig()
	first, _ := PlanCandidates(body, conv, cfg, nil)
	if len(first) != 2 {
		t.Fatalf("first pass candidates = %d", len(first))
	}
	commits := NewCommits()
	commits.CommitAll([]PointerCacheKey{first[0].Key}, nil, nil)
	second, _ := PlanCandidates(body, conv, cfg, commits)
	if len(second) != 2 {
		t.Fatalf("second pass candidates = %d", len(second))
	}
	if !second[0].Committed || second[1].Committed {
		t.Fatalf("commit marks = %v,%v", second[0].Committed, second[1].Committed)
	}
}
