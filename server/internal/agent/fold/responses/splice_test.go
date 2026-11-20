package responses

import (
	"bytes"
	"testing"

	"mindfs/server/internal/agent/fold"
)

// The replacement lands inside the output literal and nothing else moves: the
// reasoning item's encrypted blob, the message text, and every key order stay
// byte-identical. A splice that re-encodes the body would break the upstream
// cache for the whole conversation.
func TestSpliceOnlyReplacesOutputText(t *testing.T) {
	a := New()
	body := readFixture(t, "request_two_turns.json")
	conv, err := a.Scan(body)
	if err != nil {
		t.Fatal(err)
	}
	result := conv.Rounds[0].Results[0]
	text, ok := a.Text(body, result)
	if !ok || len(text) < 1000 {
		t.Fatalf("text extraction failed: ok=%v len=%d", ok, len(text))
	}
	out, err := a.Splice(body, []fold.Replacement{{Span: result.Parts[0], Literal: `"[folded]"`}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`"[folded]"`)) {
		t.Fatal("replacement missing")
	}
	// Byte-identical outside the replaced span, on both sides of it.
	if !bytes.Equal(out[:result.Parts[0].Start], body[:result.Parts[0].Start]) {
		t.Fatal("prefix changed")
	}
	suffix := len(body) - result.Parts[0].End
	if !bytes.Equal(out[len(out)-suffix:], body[result.Parts[0].End:]) {
		t.Fatal("suffix changed")
	}
	if !bytes.Contains(out, []byte(`"encrypted_content":""`)) {
		t.Fatal("reasoning rewritten")
	}
	if len(out) > len(body)-20000 {
		t.Fatalf("the folded output was not removed: %d -> %d bytes", len(body), len(out))
	}
}

func TestSpliceRejectsBadSpan(t *testing.T) {
	a := New()
	body := []byte(`{"input":[{"type":"function_call_output","call_id":"c","output":"x"}]}`)
	if _, err := a.Splice(body, []fold.Replacement{{Span: fold.Span{Start: 0, End: 0}, Literal: "y"}}); err == nil {
		t.Fatal("zero span must be rejected")
	}
	if _, err := a.Splice(body, []fold.Replacement{{Span: fold.Span{Start: 5, End: 3}, Literal: "y"}}); err == nil {
		t.Fatal("reversed span must be rejected")
	}
	if _, err := a.Splice(body, []fold.Replacement{{Span: fold.Span{Start: 0, End: len(body) + 1}, Literal: "y"}}); err == nil {
		t.Fatal("out-of-range span must be rejected")
	}
}

// An empty replacement list is a no-op, not an error.
func TestSpliceEmptyListReturnsBody(t *testing.T) {
	a := New()
	body := []byte(`{"input":[]}`)
	out, err := a.Splice(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, body) {
		t.Fatalf("body changed: %s", out)
	}
}

// A multi-part or non-text result has no single literal to replace, so Text
// refuses instead of folding the wrong block.
func TestTextRefusesMultiPartAndNonText(t *testing.T) {
	a := New()
	body := []byte(`{"input":[]}`)
	if _, ok := a.Text(body, fold.Result{Parts: []fold.Span{{Start: 0, End: 1}, {Start: 2, End: 3}}}); ok {
		t.Fatal("multi-part result must be refused")
	}
	if _, ok := a.Text(body, fold.Result{NonText: true, Parts: []fold.Span{{Start: 0, End: 1}}}); ok {
		t.Fatal("non-text result must be refused")
	}
}

// Encode must produce a literal that decodes back to the pointer text, and it
// must be what Splice writes.
func TestEncodeRoundTripsThroughSplice(t *testing.T) {
	a := New()
	body := []byte(`{"input":[
		{"type":"function_call","call_id":"c","name":"shell","arguments":"{\"command\":[\"ls\"]}"},
		{"type":"function_call_output","call_id":"c","output":"original"}]}`)
	conv, err := a.Scan(body)
	if err != nil {
		t.Fatal(err)
	}
	result := conv.Rounds[0].Results[0]
	literal := a.Encode("patched\n\"quoted\"")
	out, err := a.Splice(body, []fold.Replacement{{Span: result.Parts[0], Literal: literal}})
	if err != nil {
		t.Fatal(err)
	}
	after, err := a.Scan(out)
	if err != nil {
		t.Fatal(err)
	}
	text, ok := a.Text(out, after.Rounds[0].Results[0])
	if !ok || text != "patched\n\"quoted\"" {
		t.Fatalf("round trip = %q ok=%v", text, ok)
	}
}

func TestWindowReportsKnownModel(t *testing.T) {
	if got := New().Window("gpt-6-luna"); got != 272000 {
		t.Fatalf("window = %d", got)
	}
	if got := New().Window("unknown-model"); got != 0 {
		t.Fatalf("unknown window = %d, want 0", got)
	}
}
