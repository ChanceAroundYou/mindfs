package responses_test

// End-to-end shape test over the real Codex wire format: the engine (not just
// the adapter) must fold a tool result that arrives as two `input_text` blocks.

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/agent/fold"
	"mindfs/server/internal/agent/fold/responses"
)

// codexToolResultBody mirrors a captured Codex request: the output of every
// custom tool call is an array of two `input_text` blocks — a short header the
// CLI writes ("Script completed …") and the payload.
func codexToolResultBody(header string, payload string) []byte {
	body := `{"model":"gpt-6-luna","store":false,"input":[` +
		`{"type":"custom_tool_call","call_id":"c1","name":"exec","input":"ls"},` +
		`{"type":"custom_tool_call_output","call_id":"c1","output":[` +
		`{"type":"input_text","text":"` + header + `"},` +
		`{"type":"input_text","text":"` + payload + `"}]}]}`
	return []byte(body)
}

func newCodexEngine(t *testing.T) *fold.Engine {
	t.Helper()
	// Deliberately not Normalized: the policy thresholds are out of the way so
	// this test is about the wire shape, not about the rail (see the kernel's
	// own gate and plan tests).
	cfg := fold.Config{
		Mode:           fold.ModeOn,
		RailTokens:     -1,
		KeepTailTokens: 1 << 30,
		MinBlockTokens: 10,
		ArchiveDir:     t.TempDir(),
	}
	return fold.NewEngine(cfg, responses.New(), fold.NewArchiveStore(cfg.ArchiveDir, 0, 0), fold.NewPointerCache())
}

func TestEngineFoldsCodexPayloadBlockAndKeepsHeader(t *testing.T) {
	header := "Script completed\\nWall time 2.0 seconds\\nOutput:\\n"
	payload := strings.Repeat("x", 40000)
	body := codexToolResultBody(header, payload)

	out, err := newCodexEngine(t).Fold(fold.Request{
		Body:        body,
		Session:     &fold.Session{Commits: fold.NewCommits()},
		Now:         time.Now(),
		FoldAllowed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Folded || out.NewBlocks != 1 {
		t.Fatalf("outcome = %+v", out)
	}
	if len(out.Body) >= len(body)/2 {
		t.Fatalf("fold saved too little: %d -> %d", len(body), len(out.Body))
	}
	// The header block is not the fold target and must survive byte for byte.
	if !bytes.Contains(out.Body, []byte(header)) {
		t.Fatalf("header block was rewritten: %s", out.Body[:400])
	}
	if !bytes.Contains(out.Body, []byte(fold.PointerMarker)) {
		t.Fatal("payload block was not replaced by a pointer")
	}
	if bytes.Contains(out.Body, []byte(payload)) {
		t.Fatal("payload survived the fold")
	}
}

// The next turn resends the original body (the CLI keeps its own transcript),
// so the folded block is matched by its commit, not re-planned. The folded
// output of the previous turn, however, may also come back — and its largest
// block is now our own card, which must never be folded again.
func TestEngineDoesNotFoldItsOwnPointerOnTheNextTurn(t *testing.T) {
	header := "Script completed\\nWall time 2.0 seconds\\nOutput:\\n"
	body := codexToolResultBody(header, strings.Repeat("x", 40000))
	e := newCodexEngine(t)
	session := &fold.Session{Commits: fold.NewCommits()}

	first, err := e.Fold(fold.Request{Body: body, Session: session, Now: time.Now(), FoldAllowed: true})
	if err != nil || !first.Folded {
		t.Fatalf("first fold: %+v err=%v", first, err)
	}
	// Replay the folded body: the header stays, the payload block is a card.
	second, err := e.Fold(fold.Request{Body: first.Body, Session: session, Now: time.Now(), FoldAllowed: true})
	if err != nil {
		t.Fatal(err)
	}
	if second.NewBlocks != 0 || !bytes.Equal(second.Body, first.Body) {
		t.Fatalf("folded body was rewritten again: %+v", second)
	}
}
