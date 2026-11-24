package sessionmaintenance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanImagesPreservesRecordsAndUUIDChain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	raw := `{"type":"user","uuid":"u1","message":{"role":"user","content":"hello"}}
{"type":"assistant","uuid":"a1","parentUuid":"u1","message":{"role":"assistant","content":[{"type":"text","text":"ok"},{"type":"image","source":{"type":"base64","data":"AAAAAAAA"}}]}}
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := CleanImages(path)
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedImages != 1 || result.ReclaimedBytes <= 0 {
		t.Fatalf("unexpected clean result: %+v", result)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(after)), "\n")
	if len(lines) != 2 {
		t.Fatalf("record count changed: %d", len(lines))
	}
	if !strings.Contains(lines[0], `"uuid":"u1"`) || !strings.Contains(lines[1], `"parentUuid":"u1"`) {
		t.Fatalf("uuid chain changed: %s", string(after))
	}
	if strings.Contains(string(after), `"data":"AAAA`) || !strings.Contains(string(after), "[image removed:") {
		t.Fatalf("image block not replaced safely: %s", string(after))
	}
	if _, err := os.Stat(result.BackupPath); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
}

func TestCleanLocksRemovesOnlyDeadPIDs(t *testing.T) {
	dir := t.TempDir()
	dead := filepath.Join(dir, "99999999.json")
	alive := filepath.Join(dir, "1.json")
	if err := os.WriteFile(dead, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(alive, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := CleanLocks(dir)
	if len(result.Removed) != 1 || result.Removed[0] != dead {
		t.Fatalf("unexpected removed locks: %#v", result.Removed)
	}
	if _, err := os.Stat(alive); err != nil {
		t.Fatalf("live PID lock was removed: %v", err)
	}
}

func TestPurgeEmptyRefusesConversationWithText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	raw := `{"type":"user","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := PurgeEmpty(path)
	if err != nil {
		t.Fatal(err)
	}
	if result.Purged {
		t.Fatal("conversation with text must not be purged")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("transcript disappeared: %v", err)
	}
}

func TestCleanImagesHonorsKeepBackupsFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	raw := `{"type":"user","uuid":"u1","message":{"role":"user","content":"hello"}}
{"type":"assistant","uuid":"a1","parentUuid":"u1","message":{"role":"assistant","content":[{"type":"image","source":{"type":"base64","data":"AAAAAAAA"}}]}}
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	no := false
	result, err := CleanImagesWithOptions(path, Options{KeepBackups: &no})
	if err != nil {
		t.Fatal(err)
	}
	if result.BackupPath != "" {
		t.Fatalf("keep_backups=false must not leave a backup, got %q", result.BackupPath)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("unexpected backup file: %v", err)
	}
}

func TestPurgeEmptyKeepsToolOnlyAndCodexContent(t *testing.T) {
	dir := t.TempDir()
	toolOnly := filepath.Join(dir, "tool-only.jsonl")
	raw := `{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"x","content":"ok"}]}}` + "\n"
	if err := os.WriteFile(toolOnly, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := PurgeEmpty(toolOnly)
	if err != nil {
		t.Fatal(err)
	}
	if result.Purged {
		t.Fatal("a tool-only turn is not an empty transcript")
	}

	codex := filepath.Join(dir, "codex.jsonl")
	rollout := `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"real answer"}]}}` + "\n"
	if err := os.WriteFile(codex, []byte(rollout), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err = PurgeEmpty(codex)
	if err != nil {
		t.Fatal(err)
	}
	if result.Purged {
		t.Fatal("Codex rollout content must never be read as empty")
	}
}

func TestCleanImagesNeverOverwritesAnEarlierBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	first := `{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"image","source":{"type":"base64","data":"FIRST"}}]}}` + "\n"
	second := `{"type":"assistant","uuid":"a2","message":{"role":"assistant","content":[{"type":"image","source":{"type":"base64","data":"SECOND"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(first), 0o600); err != nil {
		t.Fatal(err)
	}
	firstResult, err := CleanImages(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(second), 0o600); err != nil {
		t.Fatal(err)
	}
	secondResult, err := CleanImages(path)
	if err != nil {
		t.Fatal(err)
	}
	if secondResult.BackupPath == firstResult.BackupPath || secondResult.BackupPath == "" {
		t.Fatalf("second backup = %q, first = %q; must not overwrite", secondResult.BackupPath, firstResult.BackupPath)
	}
	original, err := os.ReadFile(firstResult.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != first {
		t.Fatalf("first backup was overwritten: %q", original)
	}
	latest, err := os.ReadFile(secondResult.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(latest) != second {
		t.Fatalf("second backup does not hold the second original: %q", latest)
	}
}

func TestPurgeCapabilityIsHarnessSpecific(t *testing.T) {
	if !ForHarness("claude").SupportsPurge() {
		t.Fatal("Claude native transcripts must support explicit purge")
	}
	if ForHarness("codex").SupportsPurge() {
		t.Fatal("Codex rollout must not be purged by the Claude empty-shape heuristic")
	}
}

func TestCleanImagesRewritesEveryJSONLRecordWithoutDroppingIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	raw := `{"type":"user","uuid":"u1","message":{"role":"user","content":"hello"}}
{"type":"assistant","uuid":"a1","parentUuid":"u1","message":{"role":"assistant","content":[{"type":"image","source":{"type":"base64","data":"AAAAAAAA"}}]}}
{"type":"assistant","uuid":"a2","parentUuid":"a1","message":{"role":"assistant","content":[{"type":"image","source":{"type":"base64","data":"BBBBBBBB"}}]}}
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := CleanImages(path)
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedImages != 2 {
		t.Fatalf("RemovedImages=%d, want 2", result.RemovedImages)
	}
	cleaned, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cleaned), `"type":"image"`) {
		t.Fatalf("image block survived: %s", cleaned)
	}
	if !strings.Contains(string(cleaned), `"uuid":"a2"`) || !strings.Contains(string(cleaned), `"parentUuid":"a1"`) {
		t.Fatalf("uuid chain was not preserved: %s", cleaned)
	}
	if lines := strings.Count(strings.TrimSpace(string(cleaned)), "\n") + 1; lines != 3 {
		t.Fatalf("record count changed: %d", lines)
	}
}

func TestWindowsPidAliveChecksProcessState(t *testing.T) {
	source, err := os.ReadFile("pid_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, token := range []string{"OpenProcess", "GetExitCodeProcess", "stillActive"} {
		if !strings.Contains(text, token) {
			t.Fatalf("Windows pidAlive lost %q; stale locks would never be cleaned there", token)
		}
	}
}
