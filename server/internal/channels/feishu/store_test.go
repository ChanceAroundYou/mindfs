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

// 进程死在「已回执、还没交给 MindFS」之间时，卡片会留在 applying。没人再能
// 把它推进：之后每一次点击都只会再补一次「正在处理…」。写回的路径本身是幂等
// 的（gate 重复决定是 no-op，提问重复回答是 ErrQuestionAlreadyAnswered），
// 所以启动时把它放回 open、让人能再点一次，比永久卡住安全。
func TestLoadRecoversApplyingCardsToOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cards.json")
	seed := `{"cards":[{"id":"gate-nr-1","kind":"gate","state":"applying","version":3,"expires_at":"2099-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	st := NewStore(path)
	if err := st.Load(); err != nil {
		t.Fatal(err)
	}
	c, ok := st.Get("gate-nr-1")
	if !ok {
		t.Fatal("card lost on load")
	}
	if c.State != StateOpen {
		t.Fatalf("state = %q, want open", c.State)
	}
	if c.Version <= 3 {
		t.Fatalf("version = %d, want bumped so the card is re-rendered", c.Version)
	}
	// 恢复必须落到盘上，否则下一次重启又回到 applying。
	reloaded := NewStore(path)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	if c, _ := reloaded.Get("gate-nr-1"); c.State != StateOpen {
		t.Fatalf("reloaded state = %q", c.State)
	}
}

// 卡片有 TTL，过期后点击就该被拒；但没人清的话 cards.json 会随任务数一直长，
// 而每次投递都要在锁里重写整个文件。
func TestPruneExpiredDropsOldCardsButKeepsLiveOnes(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "cards.json"))
	if err := st.Load(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	old := Card{ID: "old", Kind: KindNotice, State: StateDone, ExpiresAt: now.Add(-time.Hour)}
	fresh := Card{ID: "fresh", Kind: KindNotice, State: StateOpen, ExpiresAt: now.Add(time.Hour)}
	applying := Card{ID: "applying", Kind: KindGate, State: StateApplying, ExpiresAt: now.Add(-time.Hour)}
	for _, c := range []Card{old, fresh, applying} {
		if _, err := st.Put(c); err != nil {
			t.Fatal(err)
		}
	}
	if got := st.PruneExpired(now); got != 1 {
		t.Fatalf("pruned = %d, want 1", got)
	}
	if _, ok := st.Get("old"); ok {
		t.Fatal("expired card survived")
	}
	if _, ok := st.Get("fresh"); !ok {
		t.Fatal("live card pruned")
	}
	if _, ok := st.Get("applying"); !ok {
		t.Fatal("a click in flight must not be pruned")
	}
}

// 落盘失败时内存必须跟着回滚：只改了内存而不改盘，进程会以为卡片已经送达，
// 再也不会重试；重启后盘上的旧状态又会把它重发一次。
func TestPutRollsBackMemoryWhenSaveFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "cards.json")
	st := NewStore(path)
	if err := st.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Put(Card{ID: "gate-nr-1", Kind: KindGate, State: StateOpen, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	// 让写入失败：目录拿掉写权限。
	sub := filepath.Join(dir, "sub")
	if err := os.Chmod(sub, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })
	if _, _, err := st.MarkDelivered("gate-nr-1", "om_9", 2); err == nil {
		t.Fatal("MarkDelivered succeeded on a read-only directory")
	}
	if c, _ := st.Get("gate-nr-1"); c.MessageID != "" {
		t.Fatalf("memory kept a delivery the disk rejected: %+v", c)
	}
}

func TestFindOpenPickerByChatIgnoresDoneAndExpired(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	st := NewStore(filepath.Join(t.TempDir(), "cards.json"))
	st.now = func() time.Time { return now }
	cards := []Card{
		{ID: "settled", Kind: KindPicker, State: StateDone, ChatID: "oc_1",
			CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(23 * time.Hour)},
		{ID: "stale", Kind: KindPicker, State: StateOpen, ChatID: "oc_1",
			CreatedAt: now.Add(-25 * time.Hour), ExpiresAt: now.Add(-time.Hour)},
		{ID: "boundary", Kind: KindPicker, State: StateOpen, ChatID: "oc_1",
			CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now},
		{ID: "other-chat", Kind: KindPicker, State: StateOpen, ChatID: "oc_2",
			CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)},
		{ID: "gate", Kind: KindGate, State: StateOpen, ChatID: "oc_1",
			CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)},
		{ID: "live", Kind: KindPicker, State: StateOpen, ChatID: "oc_1",
			CreatedAt: now.Add(-30 * time.Minute), ExpiresAt: now.Add(PickerTTL - 30*time.Minute)},
	}
	for _, c := range cards {
		if _, err := st.Put(c); err != nil {
			t.Fatal(err)
		}
	}
	got, ok := st.FindOpenPickerByChat("oc_1")
	if !ok || got.ID != "live" {
		t.Fatalf("open picker = %q (found=%v), want live", got.ID, ok)
	}
	if _, ok := st.FindOpenPickerByChat("oc_missing"); ok {
		t.Fatal("unrelated chat must not find a picker")
	}

	onlyDone := NewStore(filepath.Join(t.TempDir(), "cards.json"))
	onlyDone.now = func() time.Time { return now }
	if _, err := onlyDone.Put(Card{ID: "only-done", Kind: KindPicker, State: StateDone,
		ChatID: "oc_1", CreatedAt: now.Add(-time.Minute), ExpiresAt: now.Add(23 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, ok := onlyDone.FindOpenPickerByChat("oc_1"); ok {
		t.Fatal("a settled picker must not be offered again")
	}

	onlyExpired := NewStore(filepath.Join(t.TempDir(), "cards.json"))
	onlyExpired.now = func() time.Time { return now }
	if _, err := onlyExpired.Put(Card{ID: "only-expired", Kind: KindPicker, State: StateOpen,
		ChatID: "oc_1", CreatedAt: now.Add(-25 * time.Hour), ExpiresAt: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, ok := onlyExpired.FindOpenPickerByChat("oc_1"); ok {
		t.Fatal("an expired picker must not be offered again")
	}
}
