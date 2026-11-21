package fold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func ledgerTestEntry(saved int) LedgerEntry {
	return LedgerEntry{
		TS:          1700000000,
		Mode:        ModeOn,
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
	store := NewLedgerStore(dir)
	if err := store.Write("session-a", ledgerTestEntry(500)); err != nil {
		t.Fatal(err)
	}
	if err := store.Write("session-a", ledgerTestEntry(700)); err != nil {
		t.Fatal(err)
	}

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

// 胶囊统一按 token 显示，账本行必须自带这次折叠的两端估算值：字节与 token 是
// 同一份 body 的两次测量，读口拿字节除 4 反推在 CJK 内容上会差一倍。
func TestLedgerKeepsTokenEstimates(t *testing.T) {
	store := NewLedgerStore(filepath.Join(t.TempDir(), "ledger"))
	entry := ledgerTestEntry(500)
	entry.ReqTokensEst = 11800
	entry.ForwardTokensEst = 11200
	if err := store.Write("session-a", entry); err != nil {
		t.Fatal(err)
	}
	entries, err := store.read("session-a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("read %d entries, want 1", len(entries))
	}
	if entries[0].ReqTokensEst != 11800 || entries[0].ForwardTokensEst != 11200 {
		t.Fatalf("token estimates = req %d / forward %d, want 11800 / 11200",
			entries[0].ReqTokensEst, entries[0].ForwardTokensEst)
	}
}

// 2026-11-21 之前写的账本行没有这两个键。缺字段只能读成 0（前端据此回退字节），
// 不能让整份历史解析失败。
func TestLedgerReadsLegacyRowsWithoutTokenEstimates(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ledger")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	store := NewLedgerStore(dir)
	line := `{"ts":1700000000,"mode":"on","req_bytes":1000,"folded_bytes":500,"saved_bytes":500,"blocks":2,"tokens_est":200,"fold_ms":3}` + "\n"
	if err := os.WriteFile(store.path("session-a"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := store.read("session-a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].SavedBytes != 500 {
		t.Fatalf("legacy row = %+v, want one row with saved_bytes 500", entries)
	}
	if entries[0].ReqTokensEst != 0 || entries[0].ForwardTokensEst != 0 {
		t.Fatalf("legacy row grew token estimates: %+v", entries[0])
	}
}

// Ledgers are per session: one session's folds must never show up as another's.
func TestLedgerIsPerSession(t *testing.T) {
	store := NewLedgerStore(filepath.Join(t.TempDir(), "ledger"))
	if err := store.Write("session-a", ledgerTestEntry(500)); err != nil {
		t.Fatal(err)
	}
	if err := store.Write("session-b", ledgerTestEntry(900)); err != nil {
		t.Fatal(err)
	}

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
	name := SessionFileName("../../etc/passwd")
	if strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		t.Fatalf("session name %q is not path-safe", name)
	}
	if !strings.HasSuffix(name, ".jsonl") {
		t.Fatalf("session name %q has no suffix", name)
	}
	if SessionFileName("a") == SessionFileName("b") {
		t.Fatal("distinct sessions share a file")
	}
}

// A directory of ledgers is private like the archive next to it.
func TestLedgerFilesArePrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ledger")
	store := NewLedgerStore(dir)
	if err := store.Write("s", ledgerTestEntry(1)); err != nil {
		t.Fatal(err)
	}

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
	store := NewLedgerStore(dir)
	if err := store.Write("s", ledgerTestEntry(10)); err != nil {
		t.Fatal(err)
	}
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
	store := NewLedgerStore(dir)
	// Write enough entries to exceed the cap.
	entryBytes := 220
	want := ledgerMaxBytes/entryBytes + 50
	for i := 0; i < want; i++ {
		if err := store.Write("s", LedgerEntry{TS: int64(i), Mode: ModeOn, SavedBytes: i}); err != nil {
			t.Fatal(err)
		}
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
	summary := SummarizeLedger([]LedgerEntry{
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
	if got := SummarizeLedger(nil); got.Requests != 0 || len(got.Recent) != 0 {
		t.Fatalf("empty summary = %+v", got)
	}
}

// Nil stores are usable: a node without an archive directory must not panic.
func TestLedgerNilStore(t *testing.T) {
	var store *LedgerStore
	if err := store.Write("s", ledgerTestEntry(1)); err != nil {
		t.Fatalf("nil store write = %v", err)
	}
	entries, err := store.read("s", 0)
	if err != nil || entries != nil {
		t.Fatalf("nil store read = %v, %v", entries, err)
	}
	if got := store.Read("s"); got != nil {
		t.Fatalf("nil store Read = %v", got)
	}
}

func TestLedgerEntryTimestampsAreRecent(t *testing.T) {
	store := NewLedgerStore(filepath.Join(t.TempDir(), "ledger"))
	before := time.Now().Unix()
	if err := store.Write("s", LedgerEntry{TS: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	entries, _ := store.read("s", 0)
	if len(entries) != 1 || entries[0].TS < before {
		t.Fatalf("entry = %+v", entries)
	}
}
