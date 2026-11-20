package anthropic

import (
	"testing"

	"mindfs/server/internal/agent/fold"
)

func guardConfig() fold.Config { return fold.Config{} }

func TestGuardBlocksPreservedThinkingFamilies(t *testing.T) {
	a := New()
	for _, model := range []string{"claude-sonnet-5-5-20260101", "opus-5-5", "CLAUDE-FABLE-5-1[1m]"} {
		if verdict := a.Guard(model, guardConfig()); !verdict.Block {
			t.Fatalf("%s was not blocked", model)
		}
	}
	// Plain 5.0 and earlier do not run the prefix check.
	for _, model := range []string{"claude-opus-5", "claude-sonnet-4-5", "haiku-4-5"} {
		if verdict := a.Guard(model, guardConfig()); verdict.Block {
			t.Fatalf("%s was blocked", model)
		}
	}
}

func TestGuardBlocksConfiguredAliases(t *testing.T) {
	cfg := fold.Config{PrefixUnsafeModels: []string{"ok"}}
	verdict := New().Guard("ok", cfg)
	if !verdict.Block {
		t.Fatalf("alias was not blocked: %+v", verdict)
	}
}

func TestGuardReportsUnknownAliasWithoutBlocking(t *testing.T) {
	verdict := New().Guard("ok", guardConfig())
	if verdict.Block || !verdict.UnknownModel {
		t.Fatalf("verdict = %+v", verdict)
	}
	if verdict := New().Guard("sonnet-5-5", guardConfig()); verdict.UnknownModel {
		t.Fatal("a known family was reported unknown")
	}
	if verdict := New().Guard("", guardConfig()); verdict.Block || verdict.UnknownModel {
		t.Fatalf("empty model = %+v", verdict)
	}
}

func TestGuardCanBeTurnedOff(t *testing.T) {
	off := false
	cfg := fold.Config{PrefixGuard: &off}
	if verdict := New().Guard("claude-sonnet-5-5", cfg); verdict.Block {
		t.Fatal("guard stayed on")
	}
}
