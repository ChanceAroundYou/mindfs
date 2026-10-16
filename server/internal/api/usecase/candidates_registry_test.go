package usecase

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/agent"
	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

func TestSearchCommandCandidatesMergesMindFSAndShellHistory(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := session.NewManager(root)
	if err := UpsertCommandSuggestion(manager, CommandSuggestion{
		Command:      "git status",
		Cwd:          ".",
		Shell:        "zsh",
		RootID:       root.ID,
		LastExitCode: 0,
		LastUsedAt:   time.Now(),
	}); err != nil {
		t.Fatalf("UpsertCommandSuggestion: %v", err)
	}

	historyFile := filepath.Join(rootDir, "zsh_history")
	if err := os.WriteFile(historyFile, []byte(": 1710000000:0;git status\n: 1710000001:0;git stash\n"), 0o644); err != nil {
		t.Fatalf("write zsh history: %v", err)
	}
	t.Setenv("HISTFILE", historyFile)

	candidates, err := SearchCommandCandidates(context.Background(), manager, root.ID, "git st", 10, ShellHistorySpec{Command: "zsh"})
	if err != nil {
		t.Fatalf("SearchCommandCandidates: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates = %#v, want 2", candidates)
	}
	if candidates[0].Name != "git status" || candidates[1].Name != "git stash" {
		t.Fatalf("candidates = %#v", candidates)
	}
}

func TestSearchCommandCandidatesCleansMindFSControlHistory(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := session.NewManager(root)
	historyFile := filepath.Join(rootDir, "zsh_history")
	history := strings.Join([]string{
		": 1710000000:0;command printf '\\n%s\\n' '__MINDFS_CMD_START_abc__'",
		": 1710000001:0;eval 'git status' </dev/null",
		": 1710000002:0;__mindfs_status=$?",
		": 1710000003:0;command printf '\\n%s%s\\n' '__MINDFS_CMD_END_abc__:' \"$__mindfs_status\"",
		": 1710000004:0;eval 'printf '\\''x y'\\''' </dev/null",
		": 1710000005:0;git stash",
	}, "\n") + "\n"
	if err := os.WriteFile(historyFile, []byte(history), 0o644); err != nil {
		t.Fatalf("write zsh history: %v", err)
	}
	t.Setenv("HISTFILE", historyFile)

	candidates, err := SearchCommandCandidates(context.Background(), manager, root.ID, "", 10, ShellHistorySpec{Command: "zsh"})
	if err != nil {
		t.Fatalf("SearchCommandCandidates: %v", err)
	}
	names := make([]string, 0, len(candidates))
	for _, item := range candidates {
		names = append(names, item.Name)
	}
	want := []string{"git stash", "printf 'x y'", "git status"}
	if strings.Join(names, "\n") != strings.Join(want, "\n") {
		t.Fatalf("candidate names = %#v, want %#v", names, want)
	}
}

func TestCommandCandidatesFromStatus(t *testing.T) {
	provider := NewSlashCommandCandidateProvider(func(agentName string) (agent.Status, bool) {
		if agentName != "claude" {
			return agent.Status{}, false
		}
		return agent.Status{
			Name: "claude",
			Commands: []agenttypes.CommandInfo{
				{Name: "review", Description: "Review current changes"},
				{Name: "memory", Description: "Manage memory"},
			},
		}, true
	})
	items, err := provider.Search(context.Background(), rootfs.RootInfo{}, "claude", "re")
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 command candidate, got %d: %#v", len(items), items)
	}
	if items[0].Type != CandidateTypeSlashCommand {
		t.Fatalf("expected slash command candidate, got %#v", items[0])
	}
	if items[0].Name != "review" {
		t.Fatalf("expected review command, got %#v", items[0])
	}
}

func TestCodexSlashCandidatesIncludeTransientCommands(t *testing.T) {
	provider := NewSlashCommandCandidateProvider(func(agentName string) (agent.Status, bool) {
		return agent.Status{Name: agentName}, true
	})
	items, err := provider.Search(context.Background(), rootfs.RootInfo{}, "codex", "lo")
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(items) != 1 || items[0].Name != "login" {
		t.Fatalf("items = %#v, want login", items)
	}
}

func TestMergeCandidateItemsPreferSlash(t *testing.T) {
	items := mergeCandidateItemsPreferSlash([]CandidateItem{
		{Type: CandidateTypeSlashCommand, Name: "review", Description: "Slash review"},
	}, []CandidateItem{
		{Type: CandidateTypeSkill, Name: "review", Description: "Skill review"},
		{Type: CandidateTypeSkill, Name: "refactor", Description: "Skill refactor"},
	}, "")
	if len(items) != 2 {
		t.Fatalf("expected 2 unique candidates, got %d: %#v", len(items), items)
	}
	if items[0].Type != CandidateTypeSlashCommand || items[0].Name != "review" {
		t.Fatalf("expected slash command to win dedupe, got %#v", items[0])
	}
	if items[1].Name != "refactor" {
		t.Fatalf("expected refactor to remain, got %#v", items[1])
	}
}

func TestPromptStoreAppendMovesExistingToLatestAndLimits(t *testing.T) {
	store := &PromptStore{filePath: filepath.Join(t.TempDir(), "prompts.json")}
	for i := 0; i < maxPromptItems; i++ {
		if _, err := store.Append("prompt-" + strconv.Itoa(i)); err != nil {
			t.Fatalf("Append(%d) returned error: %v", i, err)
		}
	}
	items, err := store.Append("prompt-10")
	if err != nil {
		t.Fatalf("Append(existing) returned error: %v", err)
	}
	if len(items) != maxPromptItems {
		t.Fatalf("expected %d prompts after move, got %d", maxPromptItems, len(items))
	}
	if items[len(items)-1] != "prompt-10" {
		t.Fatalf("expected moved prompt at end, got %q", items[len(items)-1])
	}
	items, err = store.Append("prompt-new")
	if err != nil {
		t.Fatalf("Append(new) returned error: %v", err)
	}
	if len(items) != maxPromptItems {
		t.Fatalf("expected %d prompts after limit, got %d", maxPromptItems, len(items))
	}
	for _, item := range items {
		if item == "prompt-0" {
			t.Fatalf("expected oldest prompt to be trimmed, got %#v", items)
		}
	}
	if items[len(items)-1] != "prompt-new" {
		t.Fatalf("expected newest prompt at end, got %q", items[len(items)-1])
	}
}

func TestPromptCandidateProviderSearchReturnsNewestFirst(t *testing.T) {
	store := &PromptStore{filePath: filepath.Join(t.TempDir(), "prompts.json")}
	for _, item := range []string{"first prompt", "second prompt", "another"} {
		if _, err := store.Append(item); err != nil {
			t.Fatalf("Append(%q) returned error: %v", item, err)
		}
	}
	provider := NewPromptCandidateProvider(store)
	items, err := provider.Search(context.Background(), rootfs.RootInfo{}, "", "prompt")
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 prompt matches, got %d: %#v", len(items), items)
	}
	if items[0].Type != CandidateTypePrompt || items[0].Name != "second prompt" {
		t.Fatalf("expected newest prompt first, got %#v", items[0])
	}
	if items[1].Name != "first prompt" {
		t.Fatalf("expected older prompt second, got %#v", items[1])
	}
}

func TestPromptStoreDeleteRemovesOnlyMatchingPrompt(t *testing.T) {
	store := &PromptStore{filePath: filepath.Join(t.TempDir(), "prompts.json")}
	for _, item := range []string{"first prompt", "second prompt", "third prompt"} {
		if _, err := store.Append(item); err != nil {
			t.Fatalf("Append(%q) returned error: %v", item, err)
		}
	}
	items, err := store.Delete(" second prompt ")
	if err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if want := []string{"first prompt", "third prompt"}; !slices.Equal(items, want) {
		t.Fatalf("Delete items = %#v, want %#v", items, want)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !slices.Equal(items, loaded) {
		t.Fatalf("persisted items = %#v, want %#v", loaded, items)
	}
}
