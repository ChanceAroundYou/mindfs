package usecase

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	rootfs "mindfs/server/internal/fs"
)

func TestFileOperationSymlinkAndExternalMove(t *testing.T) {
	root := rootfs.NewRootInfo("test", "test", t.TempDir())
	service := Service{Registry: uploadTestRegistry{root: root}}
	external := t.TempDir()
	target := filepath.Join(external, "keep")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root.RootPath, "link")); err != nil {
		t.Skip(err)
	}
	if err := service.OperateFile(FileOperationInput{Root: root.ID, Path: "link", Action: "delete"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("deleted link target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root.RootPath, "move"), []byte("move"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := service.OperateFile(FileOperationInput{Root: root.ID, Path: "move", Action: "move", Destination: external}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(external, "move")); err != nil {
		t.Fatal(err)
	}
}

func TestFileOperationUsesLiteralNames(t *testing.T) {
	root := rootfs.NewRootInfo("test", "test", t.TempDir())
	service := Service{Registry: uploadTestRegistry{root: root}}
	for _, name := range []string{"report", "report#1"} {
		if err := os.WriteFile(filepath.Join(root.RootPath, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.OperateFile(FileOperationInput{Root: root.ID, Path: "report#1", Action: "delete"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root.RootPath, "report#1")); !os.IsNotExist(err) {
		t.Fatalf("literal file not deleted: %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(root.RootPath, "report")); err != nil || string(content) != "report" {
		t.Fatalf("unrelated file modified: %q, %v", content, err)
	}
}

func TestFileOperations(t *testing.T) {
	for _, directory := range []bool{false, true} {
		name := "file"
		if directory {
			name = "directory"
		}
		t.Run(name, func(t *testing.T) {
			root := rootfs.NewRootInfo("test", "test", t.TempDir())
			service := Service{Registry: uploadTestRegistry{root: root}}
			source := filepath.Join(root.RootPath, "source")
			contentPath := source
			if directory {
				if err := os.Mkdir(source, 0700); err != nil {
					t.Fatal(err)
				}
				contentPath = filepath.Join(source, "child")
			}
			if err := os.WriteFile(contentPath, []byte("keep content"), 0600); err != nil {
				t.Fatal(err)
			}
			run := func(path, action, name, destination string) error {
				return service.OperateFile(FileOperationInput{Root: root.ID, Path: path, Action: action, Name: name, Destination: destination})
			}
			if err := run(".", "delete", "", ""); err == nil {
				t.Fatal("root deletion allowed")
			}
			if err := run("../outside", "delete", "", ""); err == nil {
				t.Fatal("traversal allowed")
			}
			if err := run("source", "rename", "../outside", ""); err == nil {
				t.Fatal("invalid name allowed")
			}
			if directory {
				if err := run("source", "move", "", source); err == nil {
					t.Fatal("move into self allowed")
				}
			}
			if err := os.WriteFile(filepath.Join(root.RootPath, "existing"), []byte("existing"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := run("source", "rename", "existing", ""); !errors.Is(err, os.ErrExist) {
				t.Fatalf("conflict: %v", err)
			}
			if err := run("source", "rename", "renamed", ""); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root.RootPath, "target")
			if err := os.Mkdir(target, 0700); err != nil {
				t.Fatal(err)
			}
			if err := run("renamed", "move", "", target); err != nil {
				t.Fatal(err)
			}
			moved := filepath.Join(target, "renamed")
			if directory {
				moved = filepath.Join(moved, "child")
			}
			if content, err := os.ReadFile(moved); err != nil || string(content) != "keep content" {
				t.Fatalf("content lost: %q %v", content, err)
			}
			if err := run("target/renamed", "delete", "", ""); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(target, "renamed")); !os.IsNotExist(err) {
				t.Fatalf("not deleted: %v", err)
			}
		})
	}
}
