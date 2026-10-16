package usecase

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"

	"mindfs/server/internal/agent"
	rootfs "mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

func (p *FileCandidateProvider) Type() CandidateType {
	return CandidateTypeFile
}

func (p *FileCandidateProvider) Search(ctx context.Context, root rootfs.RootInfo, _ string, query string) ([]CandidateItem, error) {
	rootDir, err := root.RootDir()
	if err != nil {
		return nil, err
	}
	query = strings.TrimSpace(strings.ToLower(query))
	items := make([]CandidateItem, 0, 32)
	err = filepath.WalkDir(rootDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if path == rootDir {
			return nil
		}
		relPath, err := root.NormalizePath(path)
		if err != nil {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if shouldIgnoreCandidatePath(relPath, entry.IsDir()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !matchesCandidateName(relPath, query) {
			return nil
		}
		items = append(items, CandidateItem{
			Type: CandidateTypeFile,
			Name: relPath,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortCandidateItems(items, query)
	return limitCandidateItems(items), nil
}

func (p *SkillCandidateProvider) Type() CandidateType {
	return CandidateTypeSkill
}

func (p *SkillCandidateProvider) Search(ctx context.Context, root rootfs.RootInfo, agent, query string) ([]CandidateItem, error) {
	query = strings.TrimSpace(strings.ToLower(query))
	items := make([]CandidateItem, 0, 16)
	seen := make(map[string]struct{})
	for _, dir := range skillScanDirs(root, agent) {
		discovered, err := discoverSkillCandidates(ctx, dir, query)
		if err != nil {
			return nil, err
		}
		for _, item := range discovered {
			normalizedName := normalizeCandidateName(item.Name)
			if normalizedName == "" {
				continue
			}
			if _, ok := seen[normalizedName]; ok {
				continue
			}
			seen[normalizedName] = struct{}{}
			items = append(items, item)
		}
	}
	sortCandidateItems(items, query)
	return limitCandidateItems(items), nil
}

type SlashCommandCandidateProvider struct {
	getStatus func(agentName string) (agent.Status, bool)
}

func NewSlashCommandCandidateProvider(getStatus func(agentName string) (agent.Status, bool)) *SlashCommandCandidateProvider {
	return &SlashCommandCandidateProvider{getStatus: getStatus}
}

func (p *SlashCommandCandidateProvider) Type() CandidateType {
	return CandidateTypeSlashCommand
}

func (p *SlashCommandCandidateProvider) Search(ctx context.Context, _ rootfs.RootInfo, agentName, query string) ([]CandidateItem, error) {
	if p == nil || p.getStatus == nil {
		return nil, nil
	}
	query = strings.TrimSpace(strings.ToLower(query))
	items := make([]CandidateItem, 0, 1)
	appendSlash := func(name, description string) {
		if !matchesCandidateName(name, query) {
			return
		}
		for _, item := range items {
			if item.Name == name {
				return
			}
		}
		items = append(items, CandidateItem{
			Type:        CandidateTypeSlashCommand,
			Name:        name,
			Description: description,
		})
	}
	appendSlash("plan", "open Plan mode")
	if strings.TrimSpace(agentName) == "codex" {
		appendSlash("status", "show Codex status")
		appendSlash("login", "sign in to ChatGPT")
	}
	status, ok := p.getStatus(strings.TrimSpace(agentName))
	if !ok || len(status.Commands) == 0 {
		sortCandidateItems(items, query)
		return limitCandidateItems(items), nil
	}
	for _, command := range status.Commands {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		name := strings.TrimSpace(command.Name)
		if name == "plan" {
			continue
		}
		if name == "" || !matchesCandidateName(name, query) {
			continue
		}
		items = append(items, CandidateItem{
			Type:        CandidateTypeSlashCommand,
			Name:        name,
			Description: strings.TrimSpace(command.Description),
		})
	}
	sortCandidateItems(items, query)
	return limitCandidateItems(items), nil
}

type PromptCandidateProvider struct {
	store *PromptStore
}

func NewPromptCandidateProvider(store *PromptStore) *PromptCandidateProvider {
	return &PromptCandidateProvider{store: store}
}

func (p *PromptCandidateProvider) Type() CandidateType {
	return CandidateTypePrompt
}

func (p *PromptCandidateProvider) Search(ctx context.Context, _ rootfs.RootInfo, _ string, query string) ([]CandidateItem, error) {
	if p == nil || p.store == nil {
		return nil, nil
	}
	prompts, err := p.store.Search(ctx, query, maxCandidateItems)
	if err != nil {
		return nil, err
	}
	items := make([]CandidateItem, 0, len(prompts))
	for _, prompt := range prompts {
		items = append(items, CandidateItem{
			Type: CandidateTypePrompt,
			Name: prompt,
		})
	}
	return items, nil
}

type CommandCandidateProvider struct {
	shellProvider func() ShellHistorySpec
}

func NewCommandCandidateProvider(shellProvider ...func() ShellHistorySpec) *CommandCandidateProvider {
	provider := &CommandCandidateProvider{}
	if len(shellProvider) > 0 {
		provider.shellProvider = shellProvider[0]
	}
	return provider
}

func (p *CommandCandidateProvider) Type() CandidateType {
	return CandidateTypeCommand
}

func (p *CommandCandidateProvider) Search(ctx context.Context, root rootfs.RootInfo, _ string, query string) ([]CandidateItem, error) {
	manager := session.NewManager(root)
	var shell ShellHistorySpec
	if p.shellProvider != nil {
		shell = p.shellProvider()
	}
	return SearchCommandCandidates(ctx, manager, root.ID, query, maxCandidateItems, shell)
}
