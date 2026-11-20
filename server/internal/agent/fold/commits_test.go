package fold

import (
	"path/filepath"
	"testing"
)

// A nil commitment set must read as "nothing committed". The engine passes nil
// below the rail and in shadow preview; a nil that answered true would silently
// stop every fold.
func TestCommitsNilSetHasNothing(t *testing.T) {
	var commits *Commits
	key := NewPointerCacheKey("Read", "/x", []byte("text"))
	if commits.Has(key) {
		t.Fatal("a nil commitment set reported a commitment")
	}
	if commits.Len() != 0 || commits.RestoredCount() != 0 || commits.ProtectedNames() != nil {
		t.Fatal("a nil commitment set reported state")
	}
}

func TestCommitsAddAndCommitAll(t *testing.T) {
	commits := NewCommits()
	added := NewPointerCacheKey("Read", "/a", []byte("a"))
	commits.Add(added)
	if !commits.Has(added) {
		t.Fatal("Add did not commit the key")
	}
	if got := commits.ProtectedNames(); len(got) != 0 {
		t.Fatalf("Add recorded archive names: %v", got)
	}

	first := NewPointerCacheKey("Read", "/b", []byte("b"))
	second := NewPointerCacheKey("Read", "/c", []byte("c"))
	commits.CommitAll([]PointerCacheKey{first, second}, []string{"a.txt", "b.txt"}, []int{10, 20})
	if !commits.Has(first) || !commits.Has(second) {
		t.Fatal("CommitAll did not commit every key")
	}
	if got := len(commits.ProtectedNames()); got != 2 {
		t.Fatalf("protected names = %d, want 2", got)
	}
}

// Restore is name-matched on purpose: a fork or truncation leaves the session's
// transcript without the folded block, and adopting it anyway would protect an
// archive nothing points at. Only a name present in the log may be adopted, and
// only once.
func TestCommitsAdoptOnlyRestoredNames(t *testing.T) {
	dir := t.TempDir()
	log := NewCommitLog(dir, "session-adopt")
	log.Append([]CommitRecord{{TS: 1, Name: "kept.txt", Bytes: 1}})

	commits := NewCommitsFor(log)
	if commits.RestoredCount() != 1 {
		t.Fatalf("restored = %d, want 1", commits.RestoredCount())
	}
	key := NewPointerCacheKey("Read", "/kept", []byte("kept"))
	if commits.Adopt(key, "other.txt") {
		t.Fatal("adopted a name that was never restored")
	}
	if !commits.Adopt(key, "kept.txt") {
		t.Fatal("did not adopt the restored name")
	}
	if !commits.Has(key) {
		t.Fatal("adopted key is not committed")
	}
	if commits.Adopt(NewPointerCacheKey("Read", "/again", []byte("again")), "kept.txt") {
		t.Fatal("adopted the same restored name twice")
	}
	if _, err := filepath.Glob(filepath.Join(dir, CommitLogDirName, "*.jsonl")); err != nil {
		t.Fatal(err)
	}
	commits.DropUnrestored()
	if commits.RestoredCount() != 0 {
		t.Fatalf("restored after drop = %d, want 0", commits.RestoredCount())
	}
	if got := len(commits.ProtectedNames()); got != 1 {
		t.Fatalf("protected names after drop = %d, want the live one", got)
	}
}
