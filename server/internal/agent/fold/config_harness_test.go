package fold

import "testing"

// Per-harness overrides exist because two harnesses can need different policy
// on the same node: Codex folds a much larger context than Claude Code, so it
// can wait for a higher rail and still fold in time.
func TestHarnessOverrideAppliesCodexDefaults(t *testing.T) {
	c := Config{Mode: ModeOn, RailTokens: 100000, KeepTailTokens: 20000}.Normalized()
	c.Harnesses = map[string]HarnessOverride{
		"codex": {Enabled: boolPtr(true), RailTokens: 200000, KeepTailTokens: 60000, ColdRailTokens: 120000},
	}
	got := c.ForHarness("codex")
	if got.RailTokens != 200000 || got.KeepTailTokens != 60000 || got.ColdRailTokens != 120000 {
		t.Fatalf("codex override not applied: %+v", got)
	}
	// Fields the override leaves at zero keep the node-wide value.
	if got.MinFoldBytes != c.MinFoldBytes {
		t.Fatalf("unspecified fields must fall back: %+v", got)
	}
	if c.ForHarness("claude").RailTokens != 100000 {
		t.Fatal("claude must keep the global rail")
	}
	if c.ForHarness("never-configured").RailTokens != 100000 {
		t.Fatal("an unlisted harness must keep the global rail")
	}
}

// A harness can be switched off without touching the node-wide mode: it keeps
// observing (shadow) so the chip and the logs still work, but nothing is
// rewritten.
func TestHarnessDisabledObservesOnly(t *testing.T) {
	c := Config{Mode: ModeOn}.Normalized()
	c.Harnesses = map[string]HarnessOverride{"codex": {Enabled: boolPtr(false)}}
	got := c.ForHarness("codex")
	if got.Folds() {
		t.Fatal("disabled harness must not fold")
	}
	if !got.Observes() {
		t.Fatal("disabled harness must still observe")
	}
	if !c.ForHarness("claude").Folds() {
		t.Fatal("disabling one harness must not touch the others")
	}
}

// An override may turn cold folding off for one harness without turning it off
// everywhere.
func TestHarnessOverrideColdFold(t *testing.T) {
	c := Config{Mode: ModeOn}.Normalized()
	c.Harnesses = map[string]HarnessOverride{"codex": {ColdFold: boolPtr(false)}}
	if c.ForHarness("codex").ColdFoldOn() {
		t.Fatal("codex cold fold must be off")
	}
	if !c.ForHarness("claude").ColdFoldOn() {
		t.Fatal("claude cold fold must stay on")
	}
}

func boolPtr(v bool) *bool { return &v }
