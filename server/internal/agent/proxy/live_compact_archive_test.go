package proxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestArchiveWriteIsContentAddressedAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	text := bigText("archive")

	first, err := store.write(text)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	second, err := store.write(text)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if first != second {
		t.Fatalf("same text produced two paths: %q %q", first, second)
	}
	// Name is derived from the content, not from a counter or a timestamp.
	if !strings.HasSuffix(first, archiveSuffix) {
		t.Fatalf("archive path %q does not end in %q", first, archiveSuffix)
	}
	body, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(body) != text {
		t.Fatal("archived content differs from the folded text")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("dedup failed: %d files for one text", len(entries))
	}
}

func TestArchiveFilesArePrivate(t *testing.T) {
	// A subdirectory, so this exercises the creation path that production uses
	// (<config-dir>/context) rather than t.TempDir's own 0755 directory.
	dir := filepath.Join(t.TempDir(), "context")
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	path, err := store.write("secret output")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("archive file mode = %o, want 600", mode)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Fatalf("archive dir mode = %o, want 700", mode)
	}
}

// A text that failed to write must not be retried on every turn: a disk-full
// node would otherwise attempt a multi-megabyte write per request for the rest
// of the session.
func TestArchiveRemembersFailedWrites(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newArchiveStore(blocker, time.Hour, archiveMaxBytes)
	text := "payload that cannot be written"

	if _, err := store.write(text); err == nil {
		t.Fatal("expected the first write to fail")
	}
	if _, err := store.write(text); err == nil {
		t.Fatal("expected the memoized failure to fail too")
	} else if !strings.Contains(err.Error(), "previously failed") {
		t.Fatalf("second failure = %v, want the memoized one", err)
	}
}

// Retention and the byte cap are both enforced. The order matters: expired
// files go first regardless of size, then the oldest until the total fits.
func TestArchiveSweepEnforcesRetentionAndCap(t *testing.T) {
	dir := t.TempDir()
	store := newArchiveStore(dir, 24*time.Hour, 4096)
	now := time.Now()

	write := func(name, body string, age time.Duration) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		mod := now.Add(-age)
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
		return path
	}
	expired := write("expired.txt", strings.Repeat("a", 100), 48*time.Hour)
	oldButFresh := write("old.txt", strings.Repeat("b", 3000), 2*time.Hour)
	recent := write("recent.txt", strings.Repeat("c", 3000), time.Minute)
	// A non-archive file must never be touched.
	other := write("notes.json", "keep me", 48*time.Hour)

	store.sweep(now)

	if _, err := os.Stat(expired); !os.IsNotExist(err) {
		t.Error("expired archive was not removed")
	}
	if _, err := os.Stat(recent); err != nil {
		t.Error("newest archive should survive the cap sweep")
	}
	if _, err := os.Stat(oldButFresh); !os.IsNotExist(err) {
		t.Error("cap sweep should remove the oldest remaining archive first")
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("non-archive files must not be swept")
	}
}

// The sweep is rate-limited: calling it on every fold must not walk the
// directory on every fold.
func TestArchiveSweepIsRateLimited(t *testing.T) {
	dir := t.TempDir()
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	store.sweepEvery = time.Hour
	path := filepath.Join(dir, "old.txt")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	store.sweep(time.Now())
	if _, err := os.Stat(path); err != nil {
		t.Fatal("fixture missing")
	}
	// A second sweep inside the interval must be a no-op. If it walked the
	// directory it would delete nothing here; assert instead that lastSweep
	// did not advance, which is the observable part of the rate limit.
	first := store.lastSweep
	store.sweep(first.Add(time.Minute))
	if !store.lastSweep.Equal(first) {
		t.Fatal("sweep ran twice inside the interval")
	}
}
