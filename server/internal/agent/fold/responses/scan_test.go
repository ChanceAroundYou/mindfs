package responses

import (
	"os"
	"path/filepath"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return body
}

// The two call shapes pair the same way and keep their own output literal: a
// custom tool answers with content blocks, a function call with a bare string.
func TestScanPairsCustomAndFunctionOutputs(t *testing.T) {
	conv, err := New().Scan(readFixture(t, "request_two_turns.json"))
	if err != nil {
		t.Fatal(err)
	}
	if conv.Refuse != "" {
		t.Fatalf("refuse = %q", conv.Refuse)
	}
	if len(conv.Rounds) != 2 {
		t.Fatalf("rounds = %d", len(conv.Rounds))
	}
	custom := conv.Rounds[0].Results
	if len(custom) != 1 || custom[0].CallID == "" || custom[0].ToolName != "exec" {
		t.Fatalf("custom round: %+v", conv.Rounds[0])
	}
	if len(custom[0].Parts) != 1 || custom[0].NonText {
		t.Fatalf("custom result must have one text part: %+v", custom[0])
	}
	function := conv.Rounds[1].Results
	if len(function) != 1 || function[0].ToolName != "shell" || function[0].CallID != "call_fn_1" {
		t.Fatalf("function round: %+v", conv.Rounds[1])
	}
	if len(function[0].Parts) != 1 || function[0].Bytes < 30000 {
		t.Fatalf("function result must be the bare string output: %+v", function[0])
	}
}

// A call with no answer yet is an open round: the model is still waiting, so
// nothing in it may be folded.
func TestScanMarksOpenRound(t *testing.T) {
	body := []byte(`{"model":"gpt-6-luna","input":[{"type":"custom_tool_call","call_id":"c1","name":"exec","input":"x"}]}`)
	conv, err := New().Scan(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.Rounds) != 1 {
		t.Fatalf("rounds = %d", len(conv.Rounds))
	}
	if len(conv.Rounds[0].Results) != 0 {
		t.Fatalf("open round must not be foldable: %+v", conv.Rounds[0])
	}
}

// Both shapes of server-side state mean the history lives upstream, not in this
// body.
func TestScanRefusesStatefulRequests(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`{"model":"gpt-6-luna","store":true,"input":[]}`),
		[]byte(`{"model":"gpt-6-luna","store":false,"previous_response_id":"resp_1","input":[]}`),
	} {
		conv, err := New().Scan(body)
		if err != nil {
			t.Fatalf("body %s: %v", body, err)
		}
		if conv.Refuse != RefuseUnsupported {
			t.Fatalf("body %s: refuse = %q", body, conv.Refuse)
		}
	}
}

func TestScanAllowsStatelessRequest(t *testing.T) {
	conv, err := New().Scan([]byte(`{"model":"gpt-6-luna","store":false,"input":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if conv.Refuse != "" {
		t.Fatalf("refuse = %q", conv.Refuse)
	}
}

// An output with more than one text block cannot be replaced by a single
// pointer without changing the block structure, so it must never be a
// candidate — the engine folds only single-part results.
func TestScanKeepsEveryTextBlockOfMultiPartOutput(t *testing.T) {
	body := []byte(`{"model":"m","input":[
		{"type":"custom_tool_call","call_id":"c1","name":"exec","input":"x"},
		{"type":"custom_tool_call_output","call_id":"c1","output":[
			{"type":"input_text","text":"first"},
			{"type":"input_text","text":"second"}]}]}`)
	conv, err := New().Scan(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.Rounds) != 1 || len(conv.Rounds[0].Results) != 1 {
		t.Fatalf("rounds = %+v", conv.Rounds)
	}
	if got := len(conv.Rounds[0].Results[0].Parts); got != 2 {
		t.Fatalf("parts = %d, want 2", got)
	}
}

// An output carrying an image or an unknown block keeps its bytes verbatim.
func TestScanMarksNonTextOutput(t *testing.T) {
	body := []byte(`{"model":"m","input":[
		{"type":"custom_tool_call","call_id":"c1","name":"exec","input":"x"},
		{"type":"custom_tool_call_output","call_id":"c1","output":[
			{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}]}`)
	conv, err := New().Scan(body)
	if err != nil {
		t.Fatal(err)
	}
	result := conv.Rounds[0].Results[0]
	if !result.NonText || len(result.Parts) != 0 {
		t.Fatalf("result = %+v, want non-text with no parts", result)
	}
}

// The message items, reasoning items, and tool definitions carry no result and
// must not produce a round of their own.
func TestScanIgnoresNonToolItems(t *testing.T) {
	body := []byte(`{"model":"m","input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
		{"type":"reasoning","summary":[],"encrypted_content":""},
		{"type":"additional_tools","tools":[]}]}`)
	conv, err := New().Scan(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.Rounds) != 0 {
		t.Fatalf("rounds = %+v, want none", conv.Rounds)
	}
}
