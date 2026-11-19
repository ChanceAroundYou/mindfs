package proxy

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

func writeArchive(t *testing.T, store *archiveStore, text string) string {
	t.Helper()
	path, err := store.write(text)
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
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	kept := writeArchive(t, store, strings.Repeat("kept", 100))
	dropped := writeArchive(t, store, strings.Repeat("dropped", 100))
	ageFile(t, kept, 2*time.Hour)
	ageFile(t, dropped, 2*time.Hour)

	store.sweepProtected(time.Now(), map[string]struct{}{filepath.Base(kept): {}})

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
	store := newArchiveStore(dir, time.Hour, 1)
	kept := writeArchive(t, store, strings.Repeat("kept", 100))
	dropped := writeArchive(t, store, strings.Repeat("dropped", 100))

	store.sweepProtected(time.Now(), map[string]struct{}{filepath.Base(kept): {}})

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
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	path := writeArchive(t, store, strings.Repeat("persisted", 100))
	ageFile(t, path, 2*time.Hour)

	log := newCommitLog(dir, "session-persisted")
	log.append([]foldCommitRecord{{TS: time.Now().Unix(), Name: filepath.Base(path), Bytes: 900}})

	store.sweepProtected(time.Now(), nil)

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("archive named by a live commit log was swept: %v", err)
	}
}

// Once a commit is outside the retention window the protection it grants is
// gone as well — otherwise the commit logs would pin archives forever.
func TestSweepDropsArchiveWhoseCommitExpired(t *testing.T) {
	dir := t.TempDir()
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	path := writeArchive(t, store, strings.Repeat("expired", 100))
	ageFile(t, path, 3*time.Hour)

	log := newCommitLog(dir, "session-expired")
	log.append([]foldCommitRecord{{TS: time.Now().Add(-3 * time.Hour).Unix(), Name: filepath.Base(path), Bytes: 900}})
	ageFile(t, filepath.Join(dir, commitLogDirName, filepath.Base(log.path)), 3*time.Hour)

	store.sweepProtected(time.Now(), nil)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("archive of an expired commit survived: %v", err)
	}
	if _, err := os.Stat(log.path); !os.IsNotExist(err) {
		t.Fatalf("expired commit log was not pruned: %v", err)
	}
}

// The proxy-level protection set is built from the live commitment sets.
func TestReferencedArchivesCoversLiveCommits(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "context")
	s, forwarded := restartProxy(t, dir, "ref1", -1, restartFoldBody(t))
	if len(forwarded) == 0 {
		t.Fatal("no request reached the upstream")
	}
	referenced := s.referencedArchives()
	if len(referenced) != 2 {
		t.Fatalf("referenced archives = %d, want 2 (the two committed blocks)", len(referenced))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	archives := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), archiveSuffix) {
			archives++
			if _, ok := referenced[entry.Name()]; !ok {
				t.Fatalf("archive %s is not protected", entry.Name())
			}
		}
	}
	if archives != 2 {
		t.Fatalf("archive files = %d, want 2", archives)
	}
}
