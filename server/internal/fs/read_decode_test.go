package fs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestRootInfoReadFileDecodesGB18030CodeFile(t *testing.T) {
	rootDir := t.TempDir()
	source := "package main\n\n// 中文注释\nfunc main() {}\n"
	encoded, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(source))
	if err != nil {
		t.Fatalf("GB18030 encode returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootDir, "main.go"), encoded, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	root := NewRootInfo("mindfs", "mindfs", rootDir)
	got, err := root.ReadFile("main.go", 0, 0, "full")
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if got.Encoding != "gb18030" {
		t.Fatalf("ReadFile encoding = %q, want gb18030", got.Encoding)
	}
	if got.Content != source {
		t.Fatalf("ReadFile content = %q, want %q", got.Content, source)
	}
}

func TestRootInfoReadFileFullRejectsFilesLargerThanLimit(t *testing.T) {
	rootDir := t.TempDir()
	path := filepath.Join(rootDir, "large.txt")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if err := file.Truncate(maxFullReadBytes + 1); err != nil {
		file.Close()
		t.Fatalf("Truncate returned error: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	root := NewRootInfo("mindfs", "mindfs", rootDir)
	_, err = root.ReadFile("large.txt", 0, 0, "full")
	if err == nil {
		t.Fatal("expected full read of large file to fail")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("ReadFile error = %v, want too large", err)
	}
}
