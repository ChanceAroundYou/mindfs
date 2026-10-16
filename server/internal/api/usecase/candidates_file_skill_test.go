package usecase

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	rootfs "mindfs/server/internal/fs"
)

func TestFileCandidateProviderSearch(t *testing.T) {
	rootDir := t.TempDir()
	mustWriteFile(t, filepath.Join(rootDir, "design", "18-view-plugin.md"), "a")
	mustWriteFile(t, filepath.Join(rootDir, "design", "14-json-render-refactoring.md"), "a")
	mustWriteFile(t, filepath.Join(rootDir, "node_modules", "pkg", "index.js"), "a")
	mustWriteFile(t, filepath.Join(rootDir, ".git", "config"), "a")
	mustWriteFile(t, filepath.Join(rootDir, ".mindfs", "state.json"), "a")
	mustWriteFile(t, filepath.Join(rootDir, ".DS_Store"), "a")
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)

	provider := NewFileCandidateProvider()
	items, err := provider.Search(context.Background(), root, "", "design")
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d: %#v", len(items), items)
	}
	if items[0].Name != "design/18-view-plugin.md" {
		t.Fatalf("expected shorter matching path first, got %q", items[0].Name)
	}
	for _, item := range items {
		switch item.Name {
		case "node_modules/pkg/index.js", ".git/config", ".mindfs/state.json", ".DS_Store":
			t.Fatalf("unexpected filtered path in results: %q", item.Name)
		}
	}
}

func TestSkillCandidateProviderSearch(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	rootDir := t.TempDir()
	mustWriteFile(t, filepath.Join(homeDir, ".codex", "skills", "status", "SKILL.md"), "---\nname: status\ndescription: Home status skill\n---\n")
	mustWriteFile(t, filepath.Join(homeDir, ".agents", "skills", "review", "SKILL.md"), "---\nname: review\ndescription: Shared review skill\n---\n")
	mustWriteFile(t, filepath.Join(rootDir, ".codex", "skills", "status", "SKILL.md"), "---\nname: status\ndescription: Root status skill\n---\n")
	mustWriteFile(t, filepath.Join(rootDir, ".agents", "skills", "trellis-start", "SKILL.md"), "---\nname: trellis-start\ndescription: Start Trellis\n---\n")
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)

	provider := NewSkillCandidateProvider()
	items, err := provider.Search(context.Background(), root, "codex", "")
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 unique items, got %d: %#v", len(items), items)
	}
	if items[0].Name != "review" && items[0].Name != "status" && items[0].Name != "trellis-start" {
		t.Fatalf("unexpected first item: %#v", items[0])
	}
	descriptionByName := make(map[string]string, len(items))
	for _, item := range items {
		descriptionByName[item.Name] = item.Description
	}
	if got := descriptionByName["status"]; got != "Home status skill" {
		t.Fatalf("expected first scanned status skill to win, got %q", got)
	}
	if got := descriptionByName["review"]; got != "Shared review skill" {
		t.Fatalf("unexpected review description: %q", got)
	}
	if got := descriptionByName["trellis-start"]; got != "Start Trellis" {
		t.Fatalf("unexpected trellis-start description: %q", got)
	}
}

func TestSkillCandidateProviderSearchIncludesCodexPluginCacheSkills(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	rootDir := t.TempDir()
	mustWriteFile(t, filepath.Join(homeDir, ".codex", "plugins", "cache", "openai-primary-runtime", "documents", "26.1.0", "skills", "documents", "SKILL.md"), "---\nname: documents\ndescription: Old documents skill\n---\n")
	mustWriteFile(t, filepath.Join(homeDir, ".codex", "plugins", "cache", "openai-primary-runtime", "documents", "26.10.0", "skills", "documents", "SKILL.md"), "---\nname: documents\ndescription: Current documents skill\n---\n")
	mustWriteFile(t, filepath.Join(homeDir, ".codex", "plugins", "cache", "openai-curated-remote", "product-design", "0.1.47", "skills", "audit", "SKILL.md"), "---\nname: audit\ndescription: Audit product flows\n---\n")
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)

	provider := NewSkillCandidateProvider()
	items, err := provider.Search(context.Background(), root, "codex", "")
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	descriptionByName := make(map[string]string, len(items))
	for _, item := range items {
		descriptionByName[item.Name] = item.Description
	}
	if got := descriptionByName["documents"]; got != "Current documents skill" {
		t.Fatalf("documents description = %q, want Current documents skill; items=%#v", got, items)
	}
	if got := descriptionByName["audit"]; got != "Audit product flows" {
		t.Fatalf("audit description = %q, want Audit product flows; items=%#v", got, items)
	}
}

func TestSkillCandidateProviderSearchFollowsSymlinkedSkillDir(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	rootDir := t.TempDir()
	ssotDir := t.TempDir()
	targetDir := filepath.Join(ssotDir, "linked")
	mustWriteFile(t, filepath.Join(targetDir, "SKILL.md"), "---\nname: linked\ndescription: Linked skill\n---\n")
	skillsDir := filepath.Join(homeDir, ".codex", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatalf("mkdir skills dir: %v", err)
	}
	if err := os.Symlink(targetDir, filepath.Join(skillsDir, "linked")); err != nil {
		t.Skipf("symlink not available: %v", err)
	}
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)

	provider := NewSkillCandidateProvider()
	items, err := provider.Search(context.Background(), root, "codex", "linked")
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 linked skill, got %d: %#v", len(items), items)
	}
	if items[0].Name != "linked" {
		t.Fatalf("skill name = %q, want linked", items[0].Name)
	}
	if items[0].Description != "Linked skill" {
		t.Fatalf("skill description = %q, want Linked skill", items[0].Description)
	}
}

func TestSkillCandidateProviderSearchExpandsNamespacedSkillBundle(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	rootDir := t.TempDir()
	ssotDir := t.TempDir()
	targetDir := filepath.Join(ssotDir, "aegis-skills")
	mustWriteFile(t, filepath.Join(targetDir, "brainstorming", "SKILL.md"), "---\nname: brainstorming\ndescription: Aegis brainstorm\n---\n")
	mustWriteFile(t, filepath.Join(targetDir, "using-aegis", "SKILL.md"), "---\nname: using-aegis\ndescription: Aegis router\n---\n")
	skillsDir := filepath.Join(homeDir, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatalf("mkdir skills dir: %v", err)
	}
	if err := os.Symlink(targetDir, filepath.Join(skillsDir, "aegis")); err != nil {
		t.Skipf("symlink not available: %v", err)
	}
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)

	provider := NewSkillCandidateProvider()
	items, err := provider.Search(context.Background(), root, "codex", "aegis")
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 namespaced skills, got %d: %#v", len(items), items)
	}
	descriptionByName := make(map[string]string, len(items))
	for _, item := range items {
		descriptionByName[item.Name] = item.Description
	}
	if _, ok := descriptionByName["aegis"]; ok {
		t.Fatalf("did not expect bare namespace item: %#v", items)
	}
	if got := descriptionByName["aegis:brainstorming"]; got != "Aegis brainstorm" {
		t.Fatalf("unexpected aegis:brainstorming description: %q", got)
	}
	if got := descriptionByName["aegis:using-aegis"]; got != "Aegis router" {
		t.Fatalf("unexpected aegis:using-aegis description: %q", got)
	}
}

func TestSkillCandidateProviderSearchMatchesNamespacedChildName(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	rootDir := t.TempDir()
	mustWriteFile(t, filepath.Join(homeDir, ".agents", "skills", "aegis", "brainstorming", "SKILL.md"), "---\nname: brainstorming\ndescription: Aegis brainstorm\n---\n")
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)

	provider := NewSkillCandidateProvider()
	items, err := provider.Search(context.Background(), root, "codex", "brain")
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 namespaced skill, got %d: %#v", len(items), items)
	}
	if items[0].Name != "aegis:brainstorming" {
		t.Fatalf("skill name = %q, want aegis:brainstorming", items[0].Name)
	}
}

func TestSkillCandidateProviderSearchSkipsNonDirectoryScanPath(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	rootDir := t.TempDir()
	mustWriteFile(t, filepath.Join(homeDir, ".codex"), "not a directory")
	mustWriteFile(t, filepath.Join(homeDir, ".agents", "skills", "review", "SKILL.md"), "---\nname: review\ndescription: Shared review skill\n---\n")
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)

	provider := NewSkillCandidateProvider()
	items, err := provider.Search(context.Background(), root, "codex", "")
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 skill, got %d: %#v", len(items), items)
	}
	if items[0].Name != "review" {
		t.Fatalf("skill name = %q, want review", items[0].Name)
	}
}
