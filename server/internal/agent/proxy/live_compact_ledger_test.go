package proxy

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func ledgerTestEntry(saved int) LedgerEntry {
	return LedgerEntry{
		TS:          1700000000,
		Mode:        LiveCompactOn,
		ReqBytes:    1000,
		FoldedBytes: 1000 - saved,
		SavedBytes:  saved,
		Blocks:      2,
		TokensEst:   200,
		FoldMS:      3,
	}
}

func TestLedgerAppendAndRead(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ledger")
	store := newLedgerStore(dir)
	store.append("session-a", ledgerTestEntry(500))
	store.append("session-a", ledgerTestEntry(700))

	entries, err := store.read("session-a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("read %d entries, want 2", len(entries))
	}
	if entries[1].SavedBytes != 700 {
		t.Fatalf("entries are not in append order: %+v", entries)
	}
}

// Ledgers are per session: one session's folds must never show up as another's.
func TestLedgerIsPerSession(t *testing.T) {
	store := newLedgerStore(filepath.Join(t.TempDir(), "ledger"))
	store.append("session-a", ledgerTestEntry(500))
	store.append("session-b", ledgerTestEntry(900))

	a, err := store.read("session-a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 1 || a[0].SavedBytes != 500 {
		t.Fatalf("session-a ledger = %+v", a)
	}
}

// The file name is a hash, so a session key that contains a separator or dots
// cannot escape the directory.
func TestLedgerSessionNameIsHashed(t *testing.T) {
	name := ledgerSessionName("../../etc/passwd")
	if strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		t.Fatalf("session name %q is not path-safe", name)
	}
	if !strings.HasSuffix(name, ".jsonl") {
		t.Fatalf("session name %q has no suffix", name)
	}
	if ledgerSessionName("a") == ledgerSessionName("b") {
		t.Fatal("distinct sessions share a file")
	}
}

// A directory of ledgers is private like the archive next to it.
func TestLedgerFilesArePrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ledger")
	store := newLedgerStore(dir)
	store.append("s", ledgerTestEntry(1))

	info, err := os.Stat(store.path("s"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("ledger file mode = %o, want 600", mode)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Fatalf("ledger dir mode = %o, want 700", mode)
	}
}

// A crash can leave a half-written final line. The read must skip it and keep
// the good lines, because the chip failing to render is worse than losing one
// data point.
func TestLedgerReadSkipsTruncatedLine(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ledger")
	store := newLedgerStore(dir)
	store.append("s", ledgerTestEntry(10))
	file, err := os.OpenFile(store.path("s"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"ts":1,"saved_by`); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()

	entries, err := store.read("s", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].SavedBytes != 10 {
		t.Fatalf("good lines lost: %+v", entries)
	}
}

// The ledger cannot grow without bound: past the size cap it keeps a recent
// tail rather than the whole history.
func TestLedgerCompactsPastSizeCap(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ledger")
	store := newLedgerStore(dir)
	// Write enough entries to exceed the cap.
	entryBytes := 220
	want := ledgerMaxBytes/entryBytes + 50
	for i := 0; i < want; i++ {
		store.append("s", LedgerEntry{TS: int64(i), Mode: LiveCompactOn, SavedBytes: i})
	}
	info, err := os.Stat(store.path("s"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > ledgerMaxBytes {
		t.Fatalf("ledger is %d bytes, cap is %d", info.Size(), ledgerMaxBytes)
	}
	entries, err := store.read("s", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("compaction left no entries")
	}
	// The tail must be the newest entries, not the oldest.
	if entries[len(entries)-1].SavedBytes != want-1 {
		t.Fatalf("newest entry after compaction = %+v, want saved=%d", entries[len(entries)-1], want-1)
	}
}

func TestLedgerSummary(t *testing.T) {
	summary := summarizeLedger([]LedgerEntry{
		ledgerTestEntry(100),
		ledgerTestEntry(900),
		ledgerTestEntry(400),
	})
	if summary.Requests != 3 {
		t.Fatalf("requests = %d, want 3", summary.Requests)
	}
	if summary.MaxSavedBytes != 900 {
		t.Fatalf("max saved = %d, want 900", summary.MaxSavedBytes)
	}
	if summary.Latest.SavedBytes != 400 {
		t.Fatalf("latest = %+v", summary.Latest)
	}
	if got := summarizeLedger(nil); got.Requests != 0 || len(got.Recent) != 0 {
		t.Fatalf("empty summary = %+v", got)
	}
}

// Nil stores are usable: a node without an archive directory must not panic.
func TestLedgerNilStore(t *testing.T) {
	var store *ledgerStore
	store.append("s", ledgerTestEntry(1))
	entries, err := store.read("s", 0)
	if err != nil || entries != nil {
		t.Fatalf("nil store read = %v, %v", entries, err)
	}
}

// The request path writes one line per fold, attributed to the session key the
// proxy recorded — through the real handler, not by calling the store.
func TestProxyFoldWritesLedger(t *testing.T) {
	archiveDir := filepath.Join(t.TempDir(), "context")
	s, err := NewServerWithLiveCompact(LiveCompactConfig{
		Mode: LiveCompactOn, KeepRounds: 1, MinBlockTokens: 100,
		MinBodyBytes: 1, ArchiveDir: archiveDir, ArchiveEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	body := foldBody(t, 4, 8001)
	forwarded, _ := postMessages(t, s, "ledger-token", body)
	if bytes.Equal(forwarded, body) {
		t.Fatal("expected a fold")
	}

	summary := s.SessionContextSaving("session-ledger-token")
	if summary.Requests != 1 {
		t.Fatalf("ledger requests = %d, want 1", summary.Requests)
	}
	if summary.Latest.ReqBytes != len(body) || summary.Latest.FoldedBytes != len(forwarded) {
		t.Fatalf("ledger sizes = %+v, want %d -> %d", summary.Latest, len(body), len(forwarded))
	}
	if summary.Latest.SavedBytes <= 0 {
		t.Fatalf("saved bytes = %d", summary.Latest.SavedBytes)
	}
	if summary.Latest.Blocks == 0 {
		t.Fatal("ledger recorded no folded blocks")
	}
}

// A session that never folded has an empty summary rather than an error.
func TestSessionContextSavingUnknownSession(t *testing.T) {
	archiveDir := filepath.Join(t.TempDir(), "context")
	s, err := NewServerWithLiveCompact(LiveCompactConfig{
		Mode: LiveCompactOn, ArchiveDir: archiveDir, ArchiveEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := s.SessionContextSaving("never-seen"); got.Requests != 0 {
		t.Fatalf("unknown session summary = %+v", got)
	}
}

// Observe-only sessions never fold, so they must not appear in the ledger
// either: the saving the chip reports is the user's, not the router's.
func TestObserveOnlySessionDoesNotWriteLedger(t *testing.T) {
	archiveDir := filepath.Join(t.TempDir(), "context")
	s, err := NewServerWithLiveCompact(LiveCompactConfig{
		Mode: LiveCompactOn, KeepRounds: 1, MinBlockTokens: 100,
		MinBodyBytes: 1, ArchiveDir: archiveDir, ArchiveEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	upstream := httptestUpstream(t)
	s.RegisterSessionObserveOnly("isolated", upstream, "router-session")
	postToProxy(t, s, "isolated", foldBody(t, 4, 8001))
	if got := s.SessionContextSaving("router-session"); got.Requests != 0 {
		t.Fatalf("observe-only session wrote %d ledger lines", got.Requests)
	}
}

func TestLedgerEntryTimestampsAreRecent(t *testing.T) {
	store := newLedgerStore(filepath.Join(t.TempDir(), "ledger"))
	before := time.Now().Unix()
	store.append("s", LedgerEntry{TS: time.Now().Unix()})
	entries, _ := store.read("s", 0)
	if len(entries) != 1 || entries[0].TS < before {
		t.Fatalf("entry = %+v", entries)
	}
}
