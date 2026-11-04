package usecase

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	rootfs "mindfs/server/internal/fs"
)

func TestCreateEntryDirectory(t *testing.T) {
	root := rootfs.NewRootInfo("test", "test", t.TempDir())
	service := Service{Registry: uploadTestRegistry{root: root}}

	rel, err := service.CreateEntry(root.ID, ".", "newdir", true)
	if err != nil {
		t.Fatal(err)
	}
	if rel != "newdir" {
		t.Fatalf("rel = %q, want newdir", rel)
	}
	info, err := os.Stat(filepath.Join(root.RootPath, "newdir"))
	if err != nil || !info.IsDir() {
		t.Fatalf("dir not created: %v", err)
	}
	if info.Mode().Perm() != 0755 {
		t.Fatalf("mode = %o, want 0755", info.Mode().Perm())
	}

	// conflict
	if _, err := service.CreateEntry(root.ID, ".", "newdir", true); !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected os.ErrExist, got %v", err)
	}
}

func TestCreateEntryFileStillWorks(t *testing.T) {
	root := rootfs.NewRootInfo("test", "test", t.TempDir())
	service := Service{Registry: uploadTestRegistry{root: root}}

	rel, err := service.CreateEntry(root.ID, ".", "a.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if rel != "a.txt" {
		t.Fatalf("rel = %q", rel)
	}
	info, err := os.Stat(filepath.Join(root.RootPath, "a.txt"))
	if err != nil || info.IsDir() {
		t.Fatalf("file not created: %v", err)
	}
	if info.Mode().Perm() != 0644 {
		t.Fatalf("mode = %o, want 0644", info.Mode().Perm())
	}
}

func TestCreateEntryValidation(t *testing.T) {
	root := rootfs.NewRootInfo("test", "test", t.TempDir())
	service := Service{Registry: uploadTestRegistry{root: root}}

	for _, name := range []string{"", ".", "..", "a/b", "a\\b", "a\x00b"} {
		if _, err := service.CreateEntry(root.ID, ".", name, true); err == nil {
			t.Fatalf("expected error for name %q", name)
		}
	}
	if _, err := service.CreateEntry(root.ID, "../escape", "x", true); err == nil {
		t.Fatal("expected path-outside-root error")
	}
	if _, err := service.CreateEntry(root.ID, ".", ".mindfs", true); err == nil {
		t.Fatal("expected .mindfs guard")
	}
	if _, err := service.CreateEntry(root.ID, ".mindfs", "x", true); err == nil {
		t.Fatal("expected .mindfs prefix guard")
	}
}
