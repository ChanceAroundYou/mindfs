package codexsync

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testLLMuxProfile = `model = "deepseek-v4"
model_provider = "llmux"

[model_providers.llmux]
name = "LLMUX"
base_url = "https://llmux.example/v1"
wire_api = "responses"
env_key = "LLMUX_API_KEY"
`

func TestEnsureLLMuxProfileUsesRelativeSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows symlink behavior is covered by the copy fallback test")
	}
	paths := testPaths(t)
	if err := os.MkdirAll(filepath.Dir(paths.SharedLLMux), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedLLMux, []byte(testLLMuxProfile), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureLLMuxProfile(paths); err != nil {
		t.Fatalf("ensureLLMuxProfile: %v", err)
	}
	link, err := os.Readlink(paths.RuntimeLLMux)
	if err != nil {
		t.Fatalf("llmux profile is not a symlink: %v", err)
	}
	if filepath.IsAbs(link) {
		t.Fatalf("symlink target is absolute: %q", link)
	}
	resolved := filepath.Clean(filepath.Join(paths.CodexHome, link))
	if resolved != filepath.Clean(paths.SharedLLMux) {
		t.Fatalf("symlink resolves to %q, want %q", resolved, paths.SharedLLMux)
	}
}

func TestEnsureLLMuxProfileFallsBackWhenRelativeLinkCannotBeBuilt(t *testing.T) {
	paths := testPaths(t)
	if err := os.MkdirAll(filepath.Dir(paths.SharedLLMux), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedLLMux, []byte(testLLMuxProfile), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkCalled := false
	relativeFailure := func(string, string) (string, error) { return "", errors.New("different volumes") }
	symlink := func(string, string) error { symlinkCalled = true; return nil }
	if err := ensureLLMuxProfileWithFuncs(paths, relativeFailure, symlink); err != nil {
		t.Fatalf("ensureLLMuxProfileWithFuncs: %v", err)
	}
	if symlinkCalled {
		t.Fatal("symlink attempted without a relative target")
	}
	local, err := os.ReadFile(paths.RuntimeLLMux)
	if err != nil || string(local) != testLLMuxProfile {
		t.Fatalf("copy fallback = %q, %v", local, err)
	}
}

func TestLocalProfileEditImportsWhenSharedSourceIsUnchanged(t *testing.T) {
	paths := testPaths(t)
	if err := os.MkdirAll(filepath.Dir(paths.SharedLLMux), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedLLMux, []byte(testLLMuxProfile), 0o644); err != nil {
		t.Fatal(err)
	}
	noSymlink := func(string, string) error { return errors.New("symlinks unavailable") }
	if err := ensureLLMuxProfileWith(paths, noSymlink); err != nil {
		t.Fatalf("materialize copy fallback: %v", err)
	}
	localEdit := strings.Replace(testLLMuxProfile, "deepseek-v4", "deepseek-v5", 1)
	if err := os.WriteFile(paths.RuntimeLLMux, []byte(localEdit), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareLocalProfileEdit(paths); err != nil {
		t.Fatalf("prepareLocalProfileEdit: %v", err)
	}
	shared, err := os.ReadFile(paths.SharedLLMux)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(shared), "deepseek-v5") {
		t.Fatalf("local edit was not imported: %s", shared)
	}
}

func TestLocalAndSharedProfileEditsRemainAsConflict(t *testing.T) {
	paths := testPaths(t)
	if err := os.MkdirAll(filepath.Dir(paths.SharedLLMux), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedLLMux, []byte(testLLMuxProfile), 0o644); err != nil {
		t.Fatal(err)
	}
	noSymlink := func(string, string) error { return errors.New("symlinks unavailable") }
	if err := ensureLLMuxProfileWith(paths, noSymlink); err != nil {
		t.Fatalf("materialize copy fallback: %v", err)
	}
	localEdit := strings.Replace(testLLMuxProfile, "deepseek-v4", "deepseek-v5", 1)
	sharedEdit := strings.Replace(testLLMuxProfile, "deepseek-v4", "qwen-plus", 1)
	if err := os.WriteFile(paths.RuntimeLLMux, []byte(localEdit), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedLLMux, []byte(sharedEdit), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := prepareLocalProfileEdit(paths); err == nil {
		t.Fatal("concurrent local/shared edits did not report conflict")
	}
	local, err := os.ReadFile(paths.RuntimeLLMux)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := os.ReadFile(paths.SharedLLMux)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(local), "deepseek-v5") || !strings.Contains(string(shared), "qwen-plus") {
		t.Fatalf("conflict overwrote a side: local=%s shared=%s", local, shared)
	}
}

func TestEnsureLLMuxProfilePreservesConcurrentCopyAndSharedEdits(t *testing.T) {
	paths := testPaths(t)
	if err := os.MkdirAll(filepath.Dir(paths.SharedLLMux), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedLLMux, []byte(testLLMuxProfile), 0o644); err != nil {
		t.Fatal(err)
	}
	noSymlink := func(string, string) error { return errors.New("symlinks unavailable") }
	if err := ensureLLMuxProfileWith(paths, noSymlink); err != nil {
		t.Fatalf("initial local copy: %v", err)
	}
	localEdit := strings.Replace(testLLMuxProfile, "deepseek-v4", "deepseek-v5", 1)
	sharedEdit := strings.Replace(testLLMuxProfile, "deepseek-v4", "qwen-plus", 1)
	if err := os.WriteFile(paths.RuntimeLLMux, []byte(localEdit), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedLLMux, []byte(sharedEdit), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureLLMuxProfileWith(paths, noSymlink); err == nil {
		t.Fatal("concurrent local/shared edits did not report conflict")
	}
	local, err := os.ReadFile(paths.RuntimeLLMux)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := os.ReadFile(paths.SharedLLMux)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(local), "deepseek-v5") || !strings.Contains(string(shared), "qwen-plus") {
		t.Fatalf("ensure overwrote one side of the conflict: local=%s shared=%s", local, shared)
	}
}

func TestSyncLockExcludesConcurrentInvocation(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	release, err := acquireLock(stateDir)
	if err != nil {
		t.Fatalf("first acquireLock: %v", err)
	}
	defer release()
	if second, err := acquireLock(stateDir); err == nil {
		second()
		t.Fatal("second concurrent lock was acquired")
	}
}

func TestSharedProfileWritersHonorSyncLock(t *testing.T) {
	paths := testPaths(t)
	release, err := acquireLock(paths.StateDir)
	if err != nil {
		t.Fatalf("acquire sync lock: %v", err)
	}
	defer release()

	if err := ExportSharedProfileAt(paths, "default", []byte(`model = "gpt-5-codex"`)); err == nil {
		t.Fatal("profile export succeeded while the sync lock was held")
	}
	if _, err := os.Stat(paths.SharedDefault); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("profile export changed shared config while locked: stat err=%v", err)
	}

	if err := ApplySharedCodexProfileAt(paths, "default"); err == nil {
		t.Fatal("profile apply succeeded while the sync lock was held")
	}
	if _, err := os.Stat(paths.RuntimeConfig); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("profile apply changed runtime config while locked: stat err=%v", err)
	}
}
