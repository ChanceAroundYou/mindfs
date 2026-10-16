package claude

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
)

func TestClaudeProjectDirNameMatchesClaudeCodeOnDiskEncoding(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"ascii path keeps letters and digits", "/Users/Ye/Databases/L000", "-Users-Ye-Databases-L000"},
		{"cjk and space chars become dashes", "/Users/Ye/HOBBIES/260817Claude Code远程访问第三方方案", "-Users-Ye-HOBBIES-260817Claude-Code---------"},
		{"dot becomes dash", "/Users/Ye/.claude", "-Users-Ye--claude"},
		{"underscore becomes dash", "/a_b/c", "-a-b-c"},
		{"emoji becomes two dashes", "/Users/test/😀", "-Users-test---"},
		{"supplementary cjk becomes two dashes", "/Users/test/𠀀", "-Users-test---"},
		{"tailing slash stripped", "/Users/Ye/L000/", "-Users-Ye-L000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := claudeProjectDirName(tt.path); got != tt.want {
				t.Fatalf("claudeProjectDirName(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestSanitizeClaudeProjectPathTruncatesAndHashesLongPaths(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"ascii", strings.Repeat("a", 201), strings.Repeat("a", 200) + "-rkvsv5"},
		{"utf16 hash", strings.Repeat("a", 199) + "😀", strings.Repeat("a", 199) + "--rlxqg4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeClaudeProjectPath(tt.path); got != tt.want {
				t.Fatalf("sanitizeClaudeProjectPath(long path) = %q, want %q", got, tt.want)
			}
		})
	}
}

func writeClaudeSessionJSONL(t *testing.T, dir, id, cwd string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir transcript dir: %v", err)
	}
	line := fmt.Sprintf(`{"sessionId":%q,"cwd":%q}`, id, cwd)
	content := line + "\n" + `{"type":"user","message":{"content":"hello from fixture"}}` + "\n"
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

// worktree 会话的转录按 spawn cwd 归档到各自的 slug 目录；导入必须扫描 <root>/.worktree/*
// 的转录目录才能命中，否则同步报 external session not found（2026-09-09 手机端同步失败根因）。
func TestImportExternalSessionScansWorktreeTranscriptDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	imp := NewImporter(ImporterOptions{AgentName: "claude"})
	mainPath := filepath.Join(home, "proj")
	wtPath := filepath.Join(mainPath, ".worktree", "task-5")
	if err := os.MkdirAll(wtPath, 0o755); err != nil {
		t.Fatal(err)
	}
	writeClaudeSessionJSONL(t, filepath.Join(home, ".claude", "projects", claudeProjectDirName(mainPath)), "main-id", mainPath)
	writeClaudeSessionJSONL(t, filepath.Join(home, ".claude", "projects", claudeProjectDirName(wtPath)), "wt-id", wtPath)

	out, err := imp.ImportExternalSession(context.Background(), agenttypes.ImportExternalSessionInput{
		RootPath:       mainPath,
		AgentSessionID: "wt-id",
	})
	if err != nil {
		t.Fatalf("ImportExternalSession(worktree session) failed: %v", err)
	}
	if out.AgentSessionID != "wt-id" {
		t.Fatalf("AgentSessionID = %q, want wt-id", out.AgentSessionID)
	}

	// 首次导入已把 worktree 条目写入索引；二次导入走 lookupSessionFile 快路径，
	// 宽松 cwd 匹配（worktree cwd 归属根目录）必须同样命中
	out2, err := imp.ImportExternalSession(context.Background(), agenttypes.ImportExternalSessionInput{
		RootPath:       mainPath,
		AgentSessionID: "wt-id",
	})
	if err != nil {
		t.Fatalf("ImportExternalSession(index fast path) failed: %v", err)
	}
	if out2.AgentSessionID != "wt-id" {
		t.Fatalf("AgentSessionID(index) = %q, want wt-id", out2.AgentSessionID)
	}
}

// 非 .worktree 归属的外部 cwd（其它项目）不得命中：宽松匹配仅限根目录及其 .worktree/*。
func TestImportExternalSessionStillFailsForForeignRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	imp := NewImporter(ImporterOptions{AgentName: "claude"})
	mainPath := filepath.Join(home, "proj")
	otherPath := filepath.Join(home, "other")
	writeClaudeSessionJSONL(t, filepath.Join(home, ".claude", "projects", claudeProjectDirName(otherPath)), "other-id", otherPath)
	_, err := imp.ImportExternalSession(context.Background(), agenttypes.ImportExternalSessionInput{
		RootPath:       mainPath,
		AgentSessionID: "other-id",
	})
	if err == nil || !strings.Contains(err.Error(), "external session not found") {
		t.Fatalf("expected external session not found, got err=%v", err)
	}
}

func TestCwdMatchesRoot(t *testing.T) {
	root := "/data/proj"
	cases := []struct {
		cwd  string
		want bool
	}{
		{root, true},
		{root + "/.worktree/task-5", true},
		{root + "/.worktree/session-0901-01/sub", true},
		{root + "/.worktree-evil/x", false},
		{"/elsewhere/proj", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := cwdMatchesRoot(tc.cwd, root); got != tc.want {
			t.Fatalf("cwdMatchesRoot(%q, %q) = %v, want %v", tc.cwd, root, got, tc.want)
		}
	}
}
