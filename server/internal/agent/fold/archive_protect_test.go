package fold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The sweep deletes archives by age and by disk pressure. A pointer already
// inside the model's context cannot survive its archive disappearing: the next
// request would either point at a missing file or — because renderPointer
// refuses a missing archive — silently unfold the block and resend the whole
// prefix. So a referenced archive is never deleted, in either phase.

func writeArchive(t *testing.T, store *ArchiveStore, text string) string {
	t.Helper()
	path, err := store.Write(text)
	if err != nil {
		t.Fatalf("archive write: %v", err)
	}
	return path
}

func ageFile(t *testing.T, path string, age time.Duration) {
	t.Helper()
	stamp := time.Now().Add(-age)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

func TestSweepKeepsReferencedArchive(t *testing.T) {
	dir := t.TempDir()
	store := NewArchiveStore(dir, time.Hour, DefaultMaxArchiveBytes)
	kept := writeArchive(t, store, strings.Repeat("kept", 100))
	dropped := writeArchive(t, store, strings.Repeat("dropped", 100))
	ageFile(t, kept, 2*time.Hour)
	ageFile(t, dropped, 2*time.Hour)

	store.SweepProtected(time.Now(), map[string]struct{}{filepath.Base(kept): {}})

	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("referenced archive was swept: %v", err)
	}
	if _, err := os.Stat(dropped); !os.IsNotExist(err) {
		t.Fatalf("unreferenced archive survived the retention sweep: %v", err)
	}
}

// Protection wins over the byte cap too: going over the cap only degrades new
// folds (they stay whole), while deleting a referenced file corrupts the
// conversation that is already running.
func TestSweepProtectionWinsOverByteCap(t *testing.T) {
	dir := t.TempDir()
	store := NewArchiveStore(dir, time.Hour, 1)
	kept := writeArchive(t, store, strings.Repeat("kept", 100))
	dropped := writeArchive(t, store, strings.Repeat("dropped", 100))

	store.SweepProtected(time.Now(), map[string]struct{}{filepath.Base(kept): {}})

	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("referenced archive was swept under cap pressure: %v", err)
	}
	if _, err := os.Stat(dropped); !os.IsNotExist(err) {
		t.Fatalf("unreferenced archive survived cap pressure: %v", err)
	}
}

// A session that has not folded since the last restart has no in-memory set;
// its commit log is the only record that the archive is still referenced.
func TestSweepProtectsArchiveNamedByCommitLog(t *testing.T) {
	dir := t.TempDir()
	store := NewArchiveStore(dir, time.Hour, DefaultMaxArchiveBytes)
	path := writeArchive(t, store, strings.Repeat("persisted", 100))
	ageFile(t, path, 2*time.Hour)

	log := NewCommitLog(dir, "session-persisted")
	log.Append([]CommitRecord{{TS: time.Now().Unix(), Name: filepath.Base(path), Bytes: 900}})

	store.SweepProtected(time.Now(), nil)

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("archive named by a live commit log was swept: %v", err)
	}
}

// Once a commit is outside the retention window the protection it grants is
// gone as well — otherwise the commit logs would pin archives forever.
func TestSweepDropsArchiveWhoseCommitExpired(t *testing.T) {
	dir := t.TempDir()
	store := NewArchiveStore(dir, time.Hour, DefaultMaxArchiveBytes)
	path := writeArchive(t, store, strings.Repeat("expired", 100))
	ageFile(t, path, 3*time.Hour)

	log := NewCommitLog(dir, "session-expired")
	log.Append([]CommitRecord{{TS: time.Now().Add(-3 * time.Hour).Unix(), Name: filepath.Base(path), Bytes: 900}})
	ageFile(t, filepath.Join(dir, CommitLogDirName, filepath.Base(log.path)), 3*time.Hour)

	store.SweepProtected(time.Now(), nil)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("archive of an expired commit survived: %v", err)
	}
	if _, err := os.Stat(log.path); !os.IsNotExist(err) {
		t.Fatalf("expired commit log was not pruned: %v", err)
	}
}
