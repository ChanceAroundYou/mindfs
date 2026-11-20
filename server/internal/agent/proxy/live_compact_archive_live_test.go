package proxy

import (
	"mindfs/server/internal/agent/fold"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
		if strings.HasSuffix(entry.Name(), fold.ArchiveSuffix) {
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
