package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateLocalDirCreatesFolderUnderParent(t *testing.T) {
	parent := t.TempDir()
	service := Service{}

	out, err := service.CreateLocalDir(context.Background(), CreateLocalDirInput{Path: parent, Name: "new-project"})
	if err != nil {
		t.Fatalf("CreateLocalDir returned error: %v", err)
	}
	if out.Path != filepath.Join(parent, "new-project") {
		t.Fatalf("created path = %q, want %q", out.Path, filepath.Join(parent, "new-project"))
	}
	if out.Parent != parent {
		t.Fatalf("parent = %q, want %q", out.Parent, parent)
	}
	info, err := os.Stat(out.Path)
	if err != nil || !info.IsDir() {
		t.Fatalf("created entry is not a directory: %v", err)
	}
}

func TestCreateLocalDirRejectsInvalidNames(t *testing.T) {
	parent := t.TempDir()
	service := Service{}
	for _, name := range []string{"", "   ", ".", "..", "a/b", `a\b`, "a\x00b", ".mindfs"} {
		if _, err := service.CreateLocalDir(context.Background(), CreateLocalDirInput{Path: parent, Name: name}); err == nil {
			t.Fatalf("name %q was accepted, want rejection", name)
		}
	}
}

func TestCreateLocalDirReportsExistingFolder(t *testing.T) {
	parent := t.TempDir()
	if err := os.Mkdir(filepath.Join(parent, "taken"), 0o755); err != nil {
		t.Fatal(err)
	}
	service := Service{}

	_, err := service.CreateLocalDir(context.Background(), CreateLocalDirInput{Path: parent, Name: "taken"})
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("err = %v, want os.ErrExist so the handler can answer 409", err)
	}
}

func TestCreateLocalDirReportsMissingParent(t *testing.T) {
	service := Service{}

	_, err := service.CreateLocalDir(context.Background(), CreateLocalDirInput{
		Path: filepath.Join(t.TempDir(), "does-not-exist"),
		Name: "child",
	})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want os.ErrNotExist so the handler can answer 404", err)
	}
}

func TestCreateLocalDirRejectsFileAsParent(t *testing.T) {
	parent := t.TempDir()
	file := filepath.Join(parent, "plain.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	service := Service{}

	if _, err := service.CreateLocalDir(context.Background(), CreateLocalDirInput{Path: file, Name: "child"}); err == nil {
		t.Fatal("file parent was accepted, want rejection")
	}
}
