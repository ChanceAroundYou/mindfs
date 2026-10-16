package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rootfs "mindfs/server/internal/fs"
)

func TestRenameManagedDirRenamesDirectoryAndRegistry(t *testing.T) {
	parent := t.TempDir()
	oldPath := filepath.Join(parent, "old-root")
	if err := os.Mkdir(oldPath, 0o755); err != nil {
		t.Fatal(err)
	}
	root := rootfs.NewRootInfo("old-root", "old-root", oldPath)
	registry := &renameManagedDirTestRegistry{root: root}
	service := Service{Registry: registry}

	out, err := service.RenameManagedDir(context.Background(), RenameManagedDirInput{
		RootID: "old-root",
		Name:   "new-root",
	})
	if err != nil {
		t.Fatalf("RenameManagedDir returned error: %v", err)
	}
	if out.OldRootID != "old-root" {
		t.Fatalf("OldRootID = %q, want old-root", out.OldRootID)
	}
	if out.Dir.ID != "new-root" {
		t.Fatalf("renamed ID = %q, want new-root", out.Dir.ID)
	}
	if out.Dir.RootPath != filepath.Join(parent, "new-root") {
		t.Fatalf("renamed path = %q", out.Dir.RootPath)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old path still exists or stat failed unexpectedly: %v", err)
	}
	if info, err := os.Stat(out.Dir.RootPath); err != nil || !info.IsDir() {
		t.Fatalf("new path was not created as directory: %v", err)
	}
	if !registry.releaseRootResourcesCalled {
		t.Fatal("expected root resources to be released before rename")
	}
}

func TestRenameManagedDirRollsBackDirectoryWhenRegistryFails(t *testing.T) {
	parent := t.TempDir()
	oldPath := filepath.Join(parent, "old-root")
	if err := os.Mkdir(oldPath, 0o755); err != nil {
		t.Fatal(err)
	}
	root := rootfs.NewRootInfo("old-root", "old-root", oldPath)
	registry := &renameManagedDirTestRegistry{
		root:      root,
		renameErr: errors.New("save failed"),
	}
	service := Service{Registry: registry}

	_, err := service.RenameManagedDir(context.Background(), RenameManagedDirInput{
		RootID: "old-root",
		Name:   "new-root",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if info, statErr := os.Stat(oldPath); statErr != nil || !info.IsDir() {
		t.Fatalf("old path was not restored: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(parent, "new-root")); !os.IsNotExist(statErr) {
		t.Fatalf("new path still exists or stat failed unexpectedly: %v", statErr)
	}
}

func TestListLocalDirsDefaultsEmptyPathToHome(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	mustWriteFile(t, filepath.Join(homeDir, "project-a", "README.md"), "a")
	if err := os.MkdirAll(filepath.Join(homeDir, "project-b"), 0o755); err != nil {
		t.Fatalf("mkdir project-b: %v", err)
	}

	service := Service{Registry: uploadTestRegistry{}}
	out, err := service.ListLocalDirs(context.Background(), ListLocalDirsInput{})
	if err != nil {
		t.Fatalf("ListLocalDirs returned error: %v", err)
	}
	if out.Path != homeDir {
		t.Fatalf("path = %q, want %q", out.Path, homeDir)
	}
	names := make([]string, 0, len(out.Items))
	for _, item := range out.Items {
		names = append(names, item.Name)
	}
	if strings.Join(names, ",") != "project-a,project-b" {
		t.Fatalf("items = %q, want project-a,project-b", strings.Join(names, ","))
	}
}

func TestListLocalDirsIncludesSymlinkedDirectory(t *testing.T) {
	baseDir := t.TempDir()
	targetDir := filepath.Join(baseDir, "outside", "target")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	mustWriteFile(t, filepath.Join(baseDir, "file.txt"), "x")
	if err := os.Symlink(targetDir, filepath.Join(baseDir, "linked-dir")); err != nil {
		t.Skipf("Symlink unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(baseDir, "file.txt"), filepath.Join(baseDir, "linked-file")); err != nil {
		t.Fatalf("symlink file: %v", err)
	}
	if err := os.Symlink(filepath.Join(baseDir, "missing"), filepath.Join(baseDir, "broken-link")); err != nil {
		t.Fatalf("symlink broken: %v", err)
	}

	service := Service{Registry: uploadTestRegistry{}}
	out, err := service.ListLocalDirs(context.Background(), ListLocalDirsInput{Path: baseDir})
	if err != nil {
		t.Fatalf("ListLocalDirs returned error: %v", err)
	}
	names := make([]string, 0, len(out.Items))
	for _, item := range out.Items {
		names = append(names, item.Name)
	}
	if strings.Join(names, ",") != "linked-dir,outside" {
		t.Fatalf("items = %q, want linked-dir,outside", strings.Join(names, ","))
	}
}
