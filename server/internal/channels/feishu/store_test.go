package feishu

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStorePersistsCardsAtomicallyWithOwnerOnlyMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cards.json")
	st := NewStore(path)
	if err := st.Load(); err != nil {
		t.Fatal(err)
	}
	got, err := st.Put(Card{ID: "gate-nr-1", Kind: KindGate, State: StateOpen, TaskID: "task-1", Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "gate-nr-1" {
		t.Fatalf("id = %q", got.ID)
	}

	reloaded := NewStore(path)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Get("gate-nr-1"); !ok {
		t.Fatal("card did not survive reload")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestStoreUpdateIsAtomicAndReportsMissingCards(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "cards.json"))
	if err := st.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Put(Card{ID: "ask-1", Kind: KindAsk, State: StateOpen, Version: 1}); err != nil {
		t.Fatal(err)
	}
	got, changed, err := st.Update("ask-1", func(c *Card) bool { c.State = StateApplying; return true })
	if err != nil || !changed || got.State != StateApplying {
		t.Fatalf("got=%+v changed=%v err=%v", got, changed, err)
	}
	if _, changed, err := st.Update("missing", func(c *Card) bool { return true }); err != nil || changed {
		t.Fatalf("missing changed=%v err=%v", changed, err)
	}
}

func TestCardExpiredUsesExpiresAt(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	if !(Card{ExpiresAt: now.Add(-time.Second)}).Expired(now) {
		t.Fatal("past card not expired")
	}
	if (Card{ExpiresAt: now.Add(time.Second)}).Expired(now) {
		t.Fatal("future card not expired")
	}
}
