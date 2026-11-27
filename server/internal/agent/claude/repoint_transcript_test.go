package claude

import (
	"os"
	"path/filepath"
	"testing"
)

// resume 前的转录校验：CLI 对缺失的 --resume 会立即退出且不回答 initialize，
// 校验不到就直接判失败，交给上层改开新会话。
func TestHasTranscriptForRespectsConfigDir(t *testing.T) {
	configDir := t.TempDir()
	slug := filepath.Join(configDir, "projects", "-home-user-proj")
	if err := os.MkdirAll(slug, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "11111111-2222-3333-4444-555555555555"
	if err := os.WriteFile(filepath.Join(slug, id+".jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !hasTranscriptFor(id, configDir) {
		t.Fatal("existing transcript not found under config dir")
	}
	if hasTranscriptFor("99999999-0000-0000-0000-000000000000", configDir) {
		t.Fatal("missing transcript reported as present")
	}
	if hasTranscriptFor("", configDir) {
		t.Fatal("empty session id must be false")
	}

	// 同名 session 在另一个配置目录里不存在：不能跨目录误判。
	if hasTranscriptFor(id, t.TempDir()) {
		t.Fatal("transcript leaked across config dirs")
	}

	// 同 id 的转录在任意 slug 目录下都算存在（worktree repoint 会换目录）。
	otherSlug := filepath.Join(configDir, "projects", "-home-user-worktree")
	if err := os.MkdirAll(otherSlug, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(slug, id+".jsonl"), filepath.Join(otherSlug, id+".jsonl")); err != nil {
		t.Fatal(err)
	}
	if !hasTranscriptFor(id, configDir) {
		t.Fatal("transcript in another slug dir must still count")
	}
}
