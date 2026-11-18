package proxy

import (
	"encoding/json"
	"strings"
	"testing"
)

func wireBody(t *testing.T, messages []any) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{"model": "test", "messages": messages})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return body
}

func userText(text string) map[string]any {
	return map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "text", "text": text},
	}}
}

func assistantToolUse(id, name string) map[string]any {
	return map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{"file_path": "/tmp/x"}},
	}}
}

func userToolResult(id, text string, isError bool) map[string]any {
	return map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "tool_result", "tool_use_id": id, "content": text, "is_error": isError},
	}}
}

func userToolResultBlocks(id string, blocks []any, isError bool) map[string]any {
	return map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "tool_result", "tool_use_id": id, "content": blocks, "is_error": isError},
	}}
}

func mustParse(t *testing.T, body []byte) *wireRequest {
	t.Helper()
	req, err := parseWireRequest(body)
	if err != nil {
		t.Fatalf("parseWireRequest: %v", err)
	}
	return req
}

func TestParseWireRequestFlattensStringToolResult(t *testing.T) {
	req := mustParse(t, wireBody(t, []any{
		userText("hi"),
		assistantToolUse("toolu_1", "Read"),
		userToolResult("toolu_1", "file body", false),
	}))
	if len(req.messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(req.messages))
	}
	result := req.messages[2].blocks[0]
	if result.typ != "tool_result" || result.toolUseID != "toolu_1" {
		t.Fatalf("unexpected result block: %+v", result)
	}
	if result.text != "file body" || result.isError || result.nonText {
		t.Fatalf("flatten failed: %+v", result)
	}
}

func TestParseWireRequestMarksImageToolResultNonText(t *testing.T) {
	req := mustParse(t, wireBody(t, []any{
		assistantToolUse("toolu_1", "Read"),
		userToolResultBlocks("toolu_1", []any{
			map[string]any{"type": "text", "text": "shot"},
			map[string]any{"type": "image", "source": map[string]any{"type": "base64", "data": "AAAA"}},
		}, false),
	}))
	result := req.messages[1].blocks[0]
	if !result.nonText {
		t.Fatalf("image tool_result should be non-text: %+v", result)
	}
}

func TestPairToolRoundsClosesOnlyWithAllResults(t *testing.T) {
	body := wireBody(t, []any{
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "a", "name": "Read", "input": map[string]any{}},
			map[string]any{"type": "tool_use", "id": "b", "name": "Bash", "input": map[string]any{}},
		}},
		userToolResult("a", "one", false),
		userToolResult("b", "two", false),
	})
	rounds := pairToolRounds(mustParse(t, body).messages)
	if len(rounds) != 1 || !rounds[0].closed {
		t.Fatalf("rounds = %+v, want one closed round", rounds)
	}
	if rounds[0].pending != 0 {
		t.Fatalf("pending = %d, want 0", rounds[0].pending)
	}

	open := wireBody(t, []any{
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "a", "name": "Read", "input": map[string]any{}},
			map[string]any{"type": "tool_use", "id": "b", "name": "Bash", "input": map[string]any{}},
		}},
		userToolResult("a", "one", false),
	})
	rounds = pairToolRounds(mustParse(t, open).messages)
	if len(rounds) != 1 || rounds[0].closed {
		t.Fatalf("partial results must stay open: %+v", rounds)
	}
}

func foldTestMessages(rounds, size int) []any {
	messages := []any{userText("start")}
	for index := 0; index < rounds; index++ {
		id := "toolu_" + string(rune('a'+index))
		messages = append(messages, assistantToolUse(id, "Read"))
		messages = append(messages, userToolResult(id, strings.Repeat("a", size), false))
	}
	return messages
}

func TestAnalyzeWireKeepsNewestRounds(t *testing.T) {
	size := 8001 // ~2001 tokens
	req := mustParse(t, wireBody(t, foldTestMessages(5, size)))
	analysis := analyzeWire(req, 3, 2000)
	if analysis.Rounds != 5 || analysis.ClosedRounds != 5 {
		t.Fatalf("rounds = %d closed = %d, want 5/5", analysis.Rounds, analysis.ClosedRounds)
	}
	if len(analysis.Candidates) != 2 {
		t.Fatalf("candidates = %d, want 2 (the two oldest rounds)", len(analysis.Candidates))
	}
	if analysis.Candidates[0].ToolUseID != "toolu_a" || analysis.Candidates[1].ToolUseID != "toolu_b" {
		t.Fatalf("wrong candidates: %+v", analysis.Candidates)
	}
	if analysis.CandidateTokens != 2*estimateTokens(strings.Repeat("a", size)) {
		t.Fatalf("candidate tokens = %d", analysis.CandidateTokens)
	}
}

func TestAnalyzeWireSkipsErrorImageAndSmallResults(t *testing.T) {
	large := strings.Repeat("a", 8001)
	small := "tiny"
	messages := []any{
		userText("start"),
		assistantToolUse("err", "Bash"),
		userToolResult("err", large, true),
		assistantToolUse("img", "Read"),
		userToolResultBlocks("img", []any{
			map[string]any{"type": "text", "text": large},
			map[string]any{"type": "image", "source": map[string]any{"type": "base64", "data": "AAAA"}},
		}, false),
		assistantToolUse("small", "Read"),
		userToolResult("small", small, false),
		assistantToolUse("keep1", "Read"),
		userToolResult("keep1", large, false),
		assistantToolUse("keep2", "Read"),
		userToolResult("keep2", large, false),
	}
	analysis := analyzeWire(mustParse(t, wireBody(t, messages)), 2, 2000)
	if len(analysis.Candidates) != 0 {
		t.Fatalf("candidates = %+v, want none", analysis.Candidates)
	}
}

func TestAnalyzeWireNeverFoldsOpenRound(t *testing.T) {
	large := strings.Repeat("a", 8001)
	messages := []any{
		userText("start"),
		assistantToolUse("old", "Read"),
		userToolResult("old", large, false),
		assistantToolUse("open", "Read"),
		// no result for "open"
	}
	analysis := analyzeWire(mustParse(t, wireBody(t, messages)), 0, 2000)
	if len(analysis.Candidates) != 1 || analysis.Candidates[0].ToolUseID != "old" {
		t.Fatalf("candidates = %+v, want only the closed round", analysis.Candidates)
	}
}

func TestAnalyzeWireFlagsErrorLookingSuccessfulResults(t *testing.T) {
	large := "Traceback (most recent call last):\n" + strings.Repeat("a", 8001)
	messages := []any{
		userText("start"),
		assistantToolUse("bad", "Bash"),
		// is_error=false but the tool reported a failure as normal output.
		userToolResult("bad", large, false),
		assistantToolUse("good", "Read"),
		userToolResult("good", strings.Repeat("a", 8001), false),
	}
	analysis := analyzeWire(mustParse(t, wireBody(t, messages)), 0, 2000)
	if len(analysis.Candidates) != 2 {
		t.Fatalf("candidates = %d, want 2", len(analysis.Candidates))
	}
	if analysis.ErrorCandidates != 1 {
		t.Fatalf("error candidates = %d, want 1", analysis.ErrorCandidates)
	}
	if !analysis.Candidates[0].ErrorText || analysis.Candidates[1].ErrorText {
		t.Fatalf("error classification wrong: %+v", analysis.Candidates)
	}
}

func TestParseWireRequestRejectsBodyWithoutMessages(t *testing.T) {
	if _, err := parseWireRequest([]byte(`{}`)); err == nil {
		t.Fatal("body without messages must be rejected")
	}
	if _, err := parseWireRequest([]byte(`{"messages": not-json`)); err == nil {
		t.Fatal("malformed JSON must be rejected")
	}
}

func TestMessageHashesIgnoreCacheControlRelocation(t *testing.T) {
	plain := mustParse(t, wireBody(t, []any{
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "hello"}}},
	}))
	marked := mustParse(t, wireBody(t, []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "hello", "cache_control": map[string]any{"type": "ephemeral"}},
		}},
	}))
	if messageHashes(plain, 0)[0] != messageHashes(marked, 0)[0] {
		t.Fatal("cache_control relocation must not count as history drift")
	}

	other := mustParse(t, wireBody(t, []any{
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "HELLO"}}},
	}))
	if messageHashes(plain, 0)[0] == messageHashes(other, 0)[0] {
		t.Fatal("real content change must still change the hash")
	}
}

func TestStripCacheControlKeepsOtherMembers(t *testing.T) {
	got := string(stripCacheControl([]byte(`{"a":1,"cache_control":{"type":"ephemeral"},"b":2}`)))
	if strings.Contains(got, "cache_control") {
		t.Fatalf("cache_control survived: %s", got)
	}
	if !strings.Contains(got, `"a":1`) || !strings.Contains(got, `"b":2`) {
		t.Fatalf("other members lost: %s", got)
	}
}

func TestMessageHashesIgnoreTextShapeFlip(t *testing.T) {
	// Real traffic: the system message arrives as a block array on turn 1 and
	// as a plain string afterwards. Same text, different JSON shape.
	blocksReq := mustParse(t, wireBody(t, []any{
		map[string]any{"role": "system", "content": []any{map[string]any{"type": "text", "text": "hello"}}},
	}))
	msg, err := parseWireMessage([]byte(`{"role":"system","content":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	stringReq := &wireRequest{messages: []wireMessage{msg}}
	if messageHashes(blocksReq, 0)[0] != messageHashes(stringReq, 0)[0] {
		t.Fatal("text-shape flip must not count as history drift")
	}

	changed, err := parseWireMessage([]byte(`{"role":"system","content":"HELLO"}`))
	if err != nil {
		t.Fatal(err)
	}
	changedReq := &wireRequest{messages: []wireMessage{changed}}
	if messageHashes(blocksReq, 0)[0] == messageHashes(changedReq, 0)[0] {
		t.Fatal("real text change must still change the hash")
	}
}

func TestMessageHashesIgnoreToolResultShapeFlip(t *testing.T) {
	arrayShape := mustParse(t, wireBody(t, []any{
		userToolResultBlocks("toolu_1", []any{map[string]any{"type": "text", "text": "body"}}, false),
	}))
	stringShape := mustParse(t, wireBody(t, []any{
		userToolResult("toolu_1", "body", false),
	}))
	if messageHashes(arrayShape, 0)[0] != messageHashes(stringShape, 0)[0] {
		t.Fatal("tool_result shape flip must not count as history drift")
	}

	errorShape := mustParse(t, wireBody(t, []any{
		userToolResult("toolu_1", "body", true),
	}))
	if messageHashes(stringShape, 0)[0] == messageHashes(errorShape, 0)[0] {
		t.Fatal("is_error flip must still change the hash")
	}
}

func TestLooksLikeErrorTextRequiresLineStart(t *testing.T) {
	source := "package main\n\nfunc f() error {\n\treturn fmt.Errorf(\"failed: %w\", err)\n}\n"
	if looksLikeErrorText(source) {
		t.Fatal("source code mentioning errors must not be classified as error output")
	}
	if !looksLikeErrorText("some output\nerror: permission denied\n") {
		t.Fatal("line-start error marker must be detected")
	}
	tail := strings.Repeat("line\n", 60) + "exit status 1\n"
	if !looksLikeErrorText(tail) {
		t.Fatal("error marker at the tail must be detected")
	}
}

func TestDriftDistanceFromTail(t *testing.T) {
	cases := []struct {
		previousLen, drift, want int
	}{
		{21, 20, 0},  // previous last message changed
		{21, 18, 2},  // two messages before the tail
		{21, -1, -1}, // no drift
		{0, 3, -1},
	}
	for _, tc := range cases {
		if got := driftDistanceFromTail(tc.previousLen, tc.drift); got != tc.want {
			t.Fatalf("driftDistanceFromTail(%d,%d) = %d, want %d", tc.previousLen, tc.drift, got, tc.want)
		}
	}
}

func TestFirstDrift(t *testing.T) {
	if got := firstDrift([]uint64{1, 2, 3}, []uint64{1, 2, 3, 4}); got != -1 {
		t.Fatalf("append drift = %d, want -1", got)
	}
	if got := firstDrift([]uint64{1, 2, 3}, []uint64{1, 9, 3}); got != 1 {
		t.Fatalf("middle drift = %d, want 1", got)
	}
	if got := firstDrift([]uint64{1, 2, 3}, []uint64{1, 2}); got != 2 {
		t.Fatalf("shrunk history drift = %d, want 2", got)
	}
}
