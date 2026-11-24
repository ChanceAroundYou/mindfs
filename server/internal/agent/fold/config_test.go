package fold

import "testing"

// The defaults are the single record of every threshold decision, so they are
// pinned here rather than left to reappear as magic numbers in a harness.
func TestConfigNormalizedDefaults(t *testing.T) {
	got := Config{Mode: "ON"}.Normalized()
	if got.Mode != ModeOn {
		t.Fatalf("mode = %q, want %q", got.Mode, ModeOn)
	}
	if got.KeepRounds != 3 || got.KeepTailTokens != 20000 || got.MinBlockTokens != 2000 {
		t.Fatalf("keep defaults: %+v", got)
	}
	if got.RailTokens != 100000 {
		t.Fatalf("rail defaults: %+v", got)
	}
	if got.MinFoldBytes != 4096 || got.MaxFoldBytes != 262144 || got.LargeBlockTokens != 32000 {
		t.Fatalf("fold defaults: %+v", got)
	}
	if got.ColdRailTokens != 60000 || got.CacheTTLSeconds != 300 {
		t.Fatalf("cold defaults: %+v", got)
	}
	if got.RetentionDays != 7 || got.MaxArchiveBytes != DefaultMaxArchiveBytes {
		t.Fatalf("archive defaults: %+v", got)
	}
}

func TestConfigZeroValueIsOff(t *testing.T) {
	var c Config
	if c.Normalized().Mode != ModeOff {
		t.Fatal("zero config must normalize to off")
	}
	if c.Observes() {
		t.Fatal("off must not observe")
	}
}

func TestConfigTriStateDefaultsOn(t *testing.T) {
	var c Config
	if !c.PrefixGuardOn() || !c.ColdFoldOn() {
		t.Fatal("nil tri-state must mean on")
	}
	off := false
	c.PrefixGuard, c.ColdFold = &off, &off
	if c.PrefixGuardOn() || c.ColdFoldOn() {
		t.Fatal("explicit false must mean off")
	}
}

// An archive-less node must never fold: a pointer to a file that does not
// exist is worse than the full result.
func TestConfigWithoutArchiveDirDisablesArchiving(t *testing.T) {
	got := Config{Mode: ModeOn, ArchiveEnabled: true}.Normalized()
	if got.ArchiveEnabled {
		t.Fatal("archiving must be off without an archive dir")
	}
}
