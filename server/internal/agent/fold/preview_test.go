package fold

import (
	"os"
	"testing"
	"time"
)

// The rehearsal path (shadow mode). A preview must describe exactly the fold
// the real path would perform, and must leave nothing behind that a later
// mode=on turn could mistake for work already done.

func previewEngine(t *testing.T, cfg Config) (*Engine, string) {
	t.Helper()
	dir := t.TempDir()
	return NewEngine(cfg, &fakeAdapter{}, NewArchiveStore(dir, time.Hour, DefaultMaxArchiveBytes), NewPointerCache()), dir
}

func previewBody(rounds int) []byte {
	body := []byte(`{"items":[`)
	for index := 0; index < rounds; index++ {
		if index > 0 {
			body = append(body, ',')
		}
		body = append(body, '"')
		body = append(body, bigItem()...)
		body = append(body, '"')
	}
	return append(body, ']', '}')
}

func TestPreviewReportsTheSameDecisionAsTheRealFold(t *testing.T) {
	cfg := gateConfig()
	engine, _ := previewEngine(t, cfg)
	body := previewBody(3)

	preview, err := engine.Fold(Request{
		Body: body, Session: newTestSession(), Preview: true, Now: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	real, err := engine.Fold(Request{
		Body: body, Session: newTestSession(), FoldAllowed: true, Now: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Verdict.Reason != real.Verdict.Reason {
		t.Fatalf("decision = %q, real fold = %q", preview.Verdict.Reason, real.Verdict.Reason)
	}
	if preview.NewBlocks != real.NewBlocks || preview.FoldedBlocks != real.FoldedBlocks {
		t.Fatalf("preview blocks = new %d / total %d, real = new %d / total %d",
			preview.NewBlocks, preview.FoldedBlocks, real.NewBlocks, real.FoldedBlocks)
	}
	// The rehearsal measures the scanned span (quotes included), the real fold
	// measures the decoded text, so a block can differ by the JSON framing —
	// under a token for these bodies. Anything larger means the two paths
	// disagree on what they are counting.
	if delta := preview.FoldedTokens - real.FoldedTokens; delta < 0 || delta > real.FoldedBlocks {
		t.Fatalf("preview tokens = %d, real = %d", preview.FoldedTokens, real.FoldedTokens)
	}
	if preview.Folded || string(preview.Body) != string(body) {
		t.Fatal("preview changed the body")
	}
}

func TestPreviewWritesNoArchiveAndNoRenderCache(t *testing.T) {
	engine, dir := previewEngine(t, gateConfig())
	body := previewBody(2)
	out, err := engine.Fold(Request{Body: body, Session: newTestSession(), Preview: true, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if out.Folded {
		t.Fatal("preview reported a fold")
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("preview wrote %d archive entries", len(entries))
	}
}

func TestPreviewCommitsWouldFoldKeys(t *testing.T) {
	engine, _ := previewEngine(t, gateConfig())
	body := previewBody(3)
	session := newTestSession()

	first, err := engine.Fold(Request{Body: body, Session: session, Preview: true, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if first.NewBlocks == 0 {
		t.Fatal("first preview folded nothing")
	}
	second, err := engine.Fold(Request{Body: body, Session: session, Preview: true, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if second.StickyBlocks != first.NewBlocks || second.NewBlocks != 0 {
		t.Fatalf("second preview = sticky %d / new %d, want sticky %d / new 0",
			second.StickyBlocks, second.NewBlocks, first.NewBlocks)
	}
	if session.Commits.Len() != first.NewBlocks {
		t.Fatalf("committed keys = %d, want %d", session.Commits.Len(), first.NewBlocks)
	}
}

func TestPreviewPlansWithoutFoldPermissionOrArchive(t *testing.T) {
	// Shadow calls the engine with FoldAllowed=false and, on a node with no
	// archive configured, a nil archive. A rehearsal still has to plan.
	engine := NewEngine(gateConfig(), &fakeAdapter{}, nil, nil)
	out, err := engine.Fold(Request{Body: previewBody(2), Session: newTestSession(), Preview: true, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if out.NewBlocks == 0 || out.Verdict.Reason != GateFold {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestPreviewBelowRailCommitsNothing(t *testing.T) {
	cfg := gateConfig()
	cfg.RailTokens = 1 << 30
	off := false
	cfg.ColdFold = &off
	engine, _ := previewEngine(t, cfg)
	session := newTestSession()
	out, err := engine.Fold(Request{Body: previewBody(3), Session: session, Preview: true, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if out.NewBlocks != 0 || out.FoldedBlocks != 0 {
		t.Fatalf("below-rail preview folded %+v", out)
	}
	if out.Verdict.Reason != GateBelowRail {
		t.Fatalf("decision = %q", out.Verdict.Reason)
	}
	if session.Commits.Len() != 0 {
		t.Fatal("below-rail preview committed keys")
	}
}

func TestPreviewUnmeasuredCommitsEverything(t *testing.T) {
	cfg := engineTestConfig() // RailTokens -1: the unmeasured path
	engine, _ := previewEngine(t, cfg)
	out, err := engine.Fold(Request{Body: previewBody(3), Session: newTestSession(), Preview: true, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if out.NewBlocks != 3 {
		t.Fatalf("new blocks = %d, want every eligible round", out.NewBlocks)
	}
	if out.Verdict.Reason != GateUnmeasured {
		t.Fatalf("decision = %q", out.Verdict.Reason)
	}
}

// gateConfig is the rail gate opened wide enough that any eligible block is
// worth folding, which is what the preview tests need to see a fold at all.
func gateConfig() Config {
	return Config{
		Mode:             ModeOn,
		RailTokens:       1,
		KeepRounds:       0,
		KeepTailTokens:   1 << 30,
		MinBlockTokens:   10,
		LargeBlockTokens: 1 << 30,
		MinFoldBytes:     1,
		MaxFoldBytes:     0,
		CacheTTLSeconds:  300,
		ColdRailTokens:   60000,
	}
}
