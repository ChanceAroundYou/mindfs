package api

import (
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
)

func TestSessionResponseWindowFiltersAux(t *testing.T) {
	h := &HTTPHandler{}
	s := &session.Session{
		Key: "k1",
		Exchanges: []session.Exchange{
			{Seq: 2, LegacyContent: "b"},
			{Seq: 3, LegacyContent: "c"},
		},
	}
	aux := map[int][]session.ExchangeAux{
		1: {{Seq: 1, Thought: "t1"}},
		2: {{Seq: 2, Thought: "t2"}},
		3: {{Seq: 3, Thought: "t3"}},
		4: {{Seq: 4, Thought: "t4"}},
	}
	// window mode: only seq 2,3 should remain
	meta := &session.SessionWindowMeta{Total: 4, HasMore: true, MinSeq: 2, MaxSeq: 3}
	resp := h.sessionResponse(s, agenttypes.ContextWindow{}, aux, nil, meta)
	auxPayload, ok := resp["exchange_aux"].(map[string][]session.ExchangeAux)
	if !ok {
		t.Fatalf("exchange_aux type = %T", resp["exchange_aux"])
	}
	if _, ok := auxPayload["1"]; ok {
		t.Fatalf("seq 1 should be filtered, got %#v", auxPayload)
	}
	if _, ok := auxPayload["4"]; ok {
		t.Fatalf("seq 4 should be filtered")
	}
	if len(auxPayload["2"]) == 0 || len(auxPayload["3"]) == 0 {
		t.Fatalf("seq 2,3 should be present: %#v", auxPayload)
	}
	if _, ok := resp["window_meta"]; !ok {
		t.Fatalf("window_meta missing in window mode")
	}
	// non-window mode: all seqs pass through
	resp2 := h.sessionResponse(s, agenttypes.ContextWindow{}, aux, nil, nil)
	auxPayload2 := resp2["exchange_aux"].(map[string][]session.ExchangeAux)
	if len(auxPayload2) != 4 {
		t.Fatalf("non-window aux len = %d, want 4", len(auxPayload2))
	}
	if _, ok := resp2["window_meta"]; ok {
		t.Fatalf("window_meta should be absent in non-window mode")
	}
}
