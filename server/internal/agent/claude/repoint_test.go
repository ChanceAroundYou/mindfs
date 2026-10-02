package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 写一条最小的、claude 转录形状的记录。真实转录的字段多，但 ForkSession 只重写
// sessionId/uuid/parentUuid，其余原样搬运，所以这里不需要完整形状。
func writeTranscriptLine(t *testing.T, path, sessionID, uuid, parentUUID, text string) {
	t.Helper()
	entry := map[string]any{
		"type":      "user",
		"sessionId": sessionID,
		"uuid":      uuid,
		"timestamp": "2026-09-27T00:00:00.000Z",
		"cwd":       "/home/xiaokubao/projects/mindfs/.worktree/task-99",
		"message":   map[string]any{"role": "user", "content": text},
	}
	if parentUUID != "" {
		entry["parentUuid"] = parentUUID
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	defer fh.Close()
	if _, err := fh.Write(append(data, '\n')); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

// 隔离 HOME：CLAUDE_CONFIG_DIR 决定 SDK 和我们各自往哪儿写，必须指到临时目录，
// 否则测试会去动用户真实的 ~/.claude/projects。
func withIsolatedClaudeHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	return filepath.Join(dir, "projects")
}

func TestRepointTranscriptMovesToMainSlugDir(t *testing.T) {
	projectsDir := withIsolatedClaudeHome(t)
	rootPath := "/home/xiaokubao/projects/mindfs"
	worktreePath := rootPath + "/.worktree/task-99"

	// 转录先落在 worktree 的 slug 目录里（Claude Code 按 spawn cwd 归档）。
	srcDir := filepath.Join(projectsDir, claudeProjectDirName(worktreePath))
	oldID := "11111111-2222-3333-4444-555555555555"
	srcFile := filepath.Join(srcDir, oldID+".jsonl")
	writeTranscriptLine(t, srcFile, oldID, "u1", "", "第一轮")
	writeTranscriptLine(t, srcFile, oldID, "u2", "u1", "第二轮")
	// 附属目录也该一起搬，且要落成**新 id** 的名字。
	if err := os.MkdirAll(filepath.Join(srcDir, oldID), 0o755); err != nil {
		t.Fatalf("mkdir sidecar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, oldID, "tool-result.txt"), []byte("payload"), 0o600); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	out, err := RepointTranscript(rootPath, oldID)
	if err != nil {
		t.Fatalf("repoint: %v", err)
	}

	// 新 id 必须换掉，否则两个 slug 目录各一份同名转录，mindfs 和用户侧会各写各的。
	if out.AgentSessionID == oldID {
		t.Fatalf("agent session id unchanged (%s); two slug dirs would diverge", out.AgentSessionID)
	}
	if out.PreviousAgentSessionID != oldID {
		t.Fatalf("previous id = %q, want %q", out.PreviousAgentSessionID, oldID)
	}

	// 关键：文件必须落在**主 checkout** 的 slug 目录，不是 worktree 的。
	wantDir := filepath.Join(projectsDir, claudeProjectDirName(rootPath))
	if filepath.Dir(out.TranscriptPath) != wantDir {
		t.Fatalf("transcript landed in %s, want main slug dir %s", filepath.Dir(out.TranscriptPath), wantDir)
	}
	if _, err := os.Stat(out.TranscriptPath); err != nil {
		t.Fatalf("new transcript missing: %v", err)
	}
	// 源目录不能留下新 id 的副本（搬走不是复制）。
	if _, err := os.Stat(filepath.Join(srcDir, out.AgentSessionID+".jsonl")); err == nil {
		t.Fatalf("forked transcript still present in old slug dir %s", srcDir)
	}
	// 旧 id 的转录必须留着 —— 那是回滚保险。
	if _, err := os.Stat(srcFile); err != nil {
		t.Fatalf("previous transcript must be preserved for rollback: %v", err)
	}
	// 附属目录也要到位。
	if _, err := os.Stat(filepath.Join(wantDir, out.AgentSessionID, "tool-result.txt")); err != nil {
		t.Fatalf("sidecar not moved: %v", err)
	}
}

// 历史必须一条不少地搬过去 —— 这是整个功能的意义。
func TestRepointTranscriptPreservesAllHistory(t *testing.T) {
	projectsDir := withIsolatedClaudeHome(t)
	rootPath := "/home/xiaokubao/projects/mindfs"
	srcDir := filepath.Join(projectsDir, claudeProjectDirName(rootPath+"/.worktree/task-99"))
	oldID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	srcFile := filepath.Join(srcDir, oldID+".jsonl")
	for i := 1; i <= 5; i++ {
		writeTranscriptLine(t, srcFile, oldID, "uuid-"+string(rune('a'+i)), "", "第"+string(rune('0'+i))+"轮")
	}

	out, err := RepointTranscript(rootPath, oldID)
	if err != nil {
		t.Fatalf("repoint: %v", err)
	}
	data, err := os.ReadFile(out.TranscriptPath)
	if err != nil {
		t.Fatalf("read moved transcript: %v", err)
	}
	lines := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines++
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("line %d is not valid json after move: %v", lines, err)
		}
		if got := entry["sessionId"]; got != out.AgentSessionID {
			t.Fatalf("line %d sessionId = %v, want new id %s", lines, got, out.AgentSessionID)
		}
	}
	if lines != 5 {
		t.Fatalf("moved transcript has %d lines, want 5", lines)
	}
}

// TranscriptBytes 是游标的来源。它必须等于新文件的真实大小 —— 写成 0 会让下次同步
// 从 0 整份重读转录，重导的行与实时写的并不逐字相同，判重折叠不掉，正是 2026-09-16
// 「ask 下面又渲染了一轮出现过的文字」的成因。
func TestRepointTranscriptReportsTrueEOFForCursor(t *testing.T) {
	projectsDir := withIsolatedClaudeHome(t)
	rootPath := "/home/xiaokubao/projects/mindfs"
	srcDir := filepath.Join(projectsDir, claudeProjectDirName(rootPath+"/.worktree/task-99"))
	oldID := "dddddddd-eeee-ffff-0000-111111111111"
	srcFile := filepath.Join(srcDir, oldID+".jsonl")
	for i := 0; i < 3; i++ {
		writeTranscriptLine(t, srcFile, oldID, "u"+string(rune('a'+i)), "", "消息")
	}

	out, err := RepointTranscript(rootPath, oldID)
	if err != nil {
		t.Fatalf("repoint: %v", err)
	}
	info, err := os.Stat(out.TranscriptPath)
	if err != nil {
		t.Fatalf("stat moved transcript: %v", err)
	}
	if out.TranscriptBytes == 0 {
		t.Fatalf("TranscriptBytes = 0; the sync cursor would restart at 0 and re-import the whole transcript")
	}
	if out.TranscriptBytes != info.Size() {
		t.Fatalf("TranscriptBytes = %d, want real EOF %d", out.TranscriptBytes, info.Size())
	}
	if out.TranscriptModTimeUnixNano != info.ModTime().UnixNano() {
		t.Fatalf("TranscriptModTimeUnixNano = %d, want %d (cursor compares size+mtime together; a mismatched pair forces a needless full re-read)",
			out.TranscriptModTimeUnixNano, info.ModTime().UnixNano())
	}
}

func TestRepointTranscriptRejectsExistingTarget(t *testing.T) {
	projectsDir := withIsolatedClaudeHome(t)
	rootPath := "/home/xiaokubao/projects/mindfs"
	srcDir := filepath.Join(projectsDir, claudeProjectDirName(rootPath+"/.worktree/task-99"))
	oldID := "99999999-8888-7777-6666-555555555555"
	writeTranscriptLine(t, filepath.Join(srcDir, oldID+".jsonl"), oldID, "u1", "", "内容")

	if _, err := RepointTranscript(rootPath, oldID); err != nil {
		t.Fatalf("first repoint: %v", err)
	}
	// 再搬一次同一个 id 会 fork 出新 id，不会撞目标；真正要验的是「目标已存在就停手」。
	dstDir := filepath.Join(projectsDir, claudeProjectDirName(rootPath))
	if _, err := os.Stat(dstDir); err != nil {
		t.Fatalf("main slug dir should exist after repoint: %v", err)
	}
}

func TestRepointTranscriptRejectsEmptyInput(t *testing.T) {
	withIsolatedClaudeHome(t)
	if _, err := RepointTranscript("", "some-id"); err == nil {
		t.Fatalf("empty root path must be rejected")
	}
	if _, err := RepointTranscript("/home/xiaokubao/projects/mindfs", "  "); err == nil {
		t.Fatalf("blank agent session id must be rejected")
	}
}

// 源转录**已经在主 slug 目录**时（会话早先搬过一次，或 worktree 目录被手工清掉之后
// 才来收尾），fork 必然落在 dstDir —— 没有「dstDir 之外的副本」可搬，原地就是终点。
//
// 这是 2026-10-02 实测踩到的那个坑：旧写法在这里硬报 "not found outside %s"，
// 而 RepointSession 在调它之前已经 pool.Close 掐掉了 agent 进程，用户拿到的是
// 「会话没了、状态却一点没改」——最坏的一种失败。
func TestRepointTranscriptUsesInPlaceWhenSourceIsAlreadyInMainSlug(t *testing.T) {
	projectsDir := withIsolatedClaudeHome(t)
	rootPath := "/home/xiaokubao/projects/mindfs"
	dstDir := filepath.Join(projectsDir, claudeProjectDirName(rootPath))
	oldID := "12345678-1234-1234-1234-123456789abc"
	srcFile := filepath.Join(dstDir, oldID+".jsonl")
	for i := 0; i < 4; i++ {
		writeTranscriptLine(t, srcFile, oldID, "u"+string(rune('a'+i)), "", "第"+string(rune('0'+i))+"轮")
	}
	// 附属目录和转录同处一目录（它跟着转录走）。
	if err := os.MkdirAll(filepath.Join(dstDir, oldID), 0o755); err != nil {
		t.Fatalf("mkdir sidecar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, oldID, "tool-result.txt"), []byte("payload"), 0o600); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	out, err := RepointTranscript(rootPath, oldID)
	if err != nil {
		t.Fatalf("source already in the main slug must be handled in place, got: %v", err)
	}
	if out.AgentSessionID == oldID {
		t.Fatalf("agent session id unchanged (%s)", out.AgentSessionID)
	}
	if filepath.Dir(out.TranscriptPath) != dstDir {
		t.Fatalf("transcript landed in %s, want %s", filepath.Dir(out.TranscriptPath), dstDir)
	}
	data, err := os.ReadFile(out.TranscriptPath)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	lines := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) != "" {
			lines++
		}
	}
	if lines != 4 {
		t.Fatalf("in-place transcript has %d lines, want 4 (history must survive)", lines)
	}
	// 旧 id 的转录仍留着 —— 回滚保险。
	if _, err := os.Stat(srcFile); err != nil {
		t.Fatalf("previous transcript must be preserved for rollback: %v", err)
	}
	// 附属目录要落成新 id 的名字。
	if _, err := os.Stat(filepath.Join(dstDir, out.AgentSessionID, "tool-result.txt")); err != nil {
		t.Fatalf("sidecar not moved to the new id: %v", err)
	}
}

// HasTranscript 是「掐进程之前」的前置校验：它报 true 而实际搬不动，代价是一次
// 无谓的自杀；报 false 而实际搬得动，会把能收的尾挡在门外。两个方向都要准。
func TestHasTranscriptReportsFindability(t *testing.T) {
	projectsDir := withIsolatedClaudeHome(t)
	id := "abcdefab-0000-1111-2222-333344445555"
	if HasTranscript(id) {
		t.Fatalf("HasTranscript(%s) = true before any transcript exists", id)
	}
	writeTranscriptLine(t, filepath.Join(projectsDir, claudeProjectDirName("/tmp/x"), id+".jsonl"), id, "u1", "", "hi")
	if !HasTranscript(id) {
		t.Fatalf("HasTranscript(%s) = false right after writing its transcript", id)
	}
	if HasTranscript("   ") {
		t.Fatalf("a blank id must report false")
	}
}
