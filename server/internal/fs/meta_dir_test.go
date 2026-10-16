package fs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMetaLocationForNewRootPrefersExistingProjectMetadata(t *testing.T) {
	rootDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootDir, ".mindfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := MetaLocationForNewRoot(rootDir, MetaLocationHome)
	if err != nil {
		t.Fatal(err)
	}
	if got != MetaLocationProject {
		t.Fatalf("location = %q, want project", got)
	}
}

func TestHomeMetadataReusesSameAbsolutePathAndRejectsDifferentPath(t *testing.T) {
	originalHome := userHomeDir
	home := t.TempDir()
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = originalHome })

	parentA := t.TempDir()
	rootPath := filepath.Join(parentA, "project")
	if err := os.Mkdir(rootPath, 0o755); err != nil {
		t.Fatal(err)
	}
	root := NewRootInfo("project", "project", rootPath)
	root.MetaLocation = MetaLocationHome
	if _, err := root.EnsureMetaDir(); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteMetaFile("state.json", []byte(`{"cursor":"ok"}`)); err != nil {
		t.Fatal(err)
	}

	readded := NewRootInfo("project", "project", rootPath)
	readded.MetaLocation = MetaLocationHome
	if _, err := readded.EnsureMetaDir(); err != nil {
		t.Fatalf("same-path reuse failed: %v", err)
	}
	read, err := readded.ReadFile(".mindfs/state.json", 0, 0, "full")
	if err != nil {
		t.Fatal(err)
	}
	if read.Path != ".mindfs/state.json" || !strings.Contains(read.Content, `"ok"`) {
		t.Fatalf("virtual metadata read = %#v", read)
	}
	location, err := MetaLocationForNewRoot(rootPath, MetaLocationProject)
	if err != nil {
		t.Fatal(err)
	}
	if location != MetaLocationHome {
		t.Fatalf("same-path re-add location = %q, want home", location)
	}

	parentB := t.TempDir()
	otherPath := filepath.Join(parentB, "project")
	if err := os.Mkdir(otherPath, 0o755); err != nil {
		t.Fatal(err)
	}
	other := NewRootInfo("project", "project", otherPath)
	other.MetaLocation = MetaLocationHome
	if _, err := other.EnsureMetaDir(); err == nil {
		t.Fatal("different project path unexpectedly reused home metadata")
	}
}

func TestEmptyMetaLocationDefaultsToProject(t *testing.T) {
	rootDir := t.TempDir()
	root := NewRootInfo("project", "project", rootDir)
	if root.EffectiveMetaLocation() != MetaLocationProject {
		t.Fatalf("effective location = %q", root.EffectiveMetaLocation())
	}
	if root.MetaDir() != filepath.Join(rootDir, ".mindfs") {
		t.Fatalf("MetaDir = %q", root.MetaDir())
	}
}

func TestEnsureMetaDirAddsGitExcludeEntry(t *testing.T) {
	rootDir := t.TempDir()
	gitInfoDir := filepath.Join(rootDir, ".git", "info")
	if err := os.MkdirAll(gitInfoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	excludePath := filepath.Join(gitInfoDir, "exclude")
	if err := os.WriteFile(excludePath, []byte("*.tmp"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := NewRootInfo("project", "project", rootDir)
	if _, err := root.EnsureMetaDir(); err != nil {
		t.Fatal(err)
	}
	if _, err := root.EnsureMetaDir(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "*.tmp\n/.mindfs/\n"; got != want {
		t.Fatalf("exclude content = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(rootDir, ".mindfs")); err != nil {
		t.Fatalf("metadata directory was not created: %v", err)
	}
}

func TestEnsureHomeMetaDirDoesNotChangeGitExclude(t *testing.T) {
	originalHome := userHomeDir
	home := t.TempDir()
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = originalHome })

	rootDir := t.TempDir()
	gitInfoDir := filepath.Join(rootDir, ".git", "info")
	if err := os.MkdirAll(gitInfoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	root := NewRootInfo("project", "project", rootDir)
	root.MetaLocation = MetaLocationHome
	if _, err := root.EnsureMetaDir(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(gitInfoDir, "exclude")); !os.IsNotExist(err) {
		t.Fatalf("project git exclude was unexpectedly created: %v", err)
	}
}
