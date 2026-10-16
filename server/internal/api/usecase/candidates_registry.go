package usecase

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"

	rootfs "mindfs/server/internal/fs"
)

type CandidateType string

const (
	CandidateTypeFile         CandidateType = "file"
	CandidateTypePrompt       CandidateType = "prompt"
	CandidateTypeSkill        CandidateType = "skill"
	CandidateTypeSlashCommand CandidateType = "slash_command"
	CandidateTypeCommand      CandidateType = "command"
)

type CandidateItem struct {
	Type        CandidateType `json:"type"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
}

type SearchCandidatesInput struct {
	RootID string
	Type   CandidateType
	Query  string
	Agent  string
}

type SearchCandidatesOutput struct {
	Items []CandidateItem
}

const maxCandidateItems = 10

type CandidateProvider interface {
	Type() CandidateType
	Search(ctx context.Context, root rootfs.RootInfo, agent, query string) ([]CandidateItem, error)
}

type CandidateRegistry struct {
	providers map[CandidateType]CandidateProvider
}

func NewCandidateRegistry() *CandidateRegistry {
	return &CandidateRegistry{providers: make(map[CandidateType]CandidateProvider)}
}

func (r *CandidateRegistry) Register(provider CandidateProvider) {
	if r == nil || provider == nil {
		return
	}
	r.providers[provider.Type()] = provider
}

func (r *CandidateRegistry) Search(ctx context.Context, candidateType CandidateType, root rootfs.RootInfo, agent, query string) ([]CandidateItem, error) {
	if r == nil {
		return nil, errors.New("candidate registry not configured")
	}
	provider := r.providers[candidateType]
	if provider == nil {
		return nil, errors.New("candidate provider not found")
	}
	return provider.Search(ctx, root, agent, query)
}

func (s *Service) SearchCandidates(ctx context.Context, in SearchCandidatesInput) (SearchCandidatesOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return SearchCandidatesOutput{}, err
	}
	if err := validateSearchCandidatesInput(in); err != nil {
		return SearchCandidatesOutput{}, err
	}
	var root rootfs.RootInfo
	if in.Type != CandidateTypePrompt {
		var err error
		root, err = s.Registry.GetRoot(in.RootID)
		if err != nil {
			return SearchCandidatesOutput{}, err
		}
	}
	registry := s.Registry.GetCandidateRegistry()
	if in.Type == CandidateTypeSkill {
		skillItems, err := registry.Search(ctx, CandidateTypeSkill, root, in.Agent, in.Query)
		if err != nil {
			return SearchCandidatesOutput{}, err
		}
		slashItems, err := registry.Search(ctx, CandidateTypeSlashCommand, root, in.Agent, in.Query)
		if err != nil {
			return SearchCandidatesOutput{}, err
		}
		return SearchCandidatesOutput{Items: mergeCandidateItemsPreferSlash(slashItems, skillItems, in.Query)}, nil
	}
	items, err := registry.Search(ctx, in.Type, root, in.Agent, in.Query)
	if err != nil {
		return SearchCandidatesOutput{}, err
	}
	return SearchCandidatesOutput{Items: items}, nil
}

func mergeCandidateItemsPreferSlash(primary []CandidateItem, secondary []CandidateItem, query string) []CandidateItem {
	items := make([]CandidateItem, 0, len(primary)+len(secondary))
	seen := make(map[string]struct{}, len(primary)+len(secondary))
	appendUnique := func(list []CandidateItem) {
		for _, item := range list {
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
	appendUnique(primary)
	appendUnique(secondary)
	sortCandidateItems(items, query)
	return limitCandidateItems(items)
}

func validateSearchCandidatesInput(in SearchCandidatesInput) error {
	switch in.Type {
	case CandidateTypeFile:
		return nil
	case CandidateTypeCommand:
		return nil
	case CandidateTypePrompt:
		return nil
	case CandidateTypeSkill, CandidateTypeSlashCommand:
		if strings.TrimSpace(in.Agent) == "" {
			return errors.New("agent required for skill candidates")
		}
		return nil
	default:
		return errors.New("invalid candidate type")
	}
}

func normalizeCandidateName(name string) string {
	return strings.TrimSpace(strings.ToLower(name))
}

func limitCandidateItems(items []CandidateItem) []CandidateItem {
	if len(items) > maxCandidateItems {
		return items[:maxCandidateItems]
	}
	return items
}

func shouldIgnoreCandidatePath(relPath string, isDir bool) bool {
	base := filepath.Base(relPath)
	if base == ".DS_Store" || base == "Thumbs.db" {
		return true
	}
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	for _, part := range parts {
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, ".") {
			return true
		}
		switch part {
		case "node_modules", "dist", "build", "coverage", ".next", ".nuxt", ".turbo", ".cache":
			return true
		}
	}
	return false
}

func matchesCandidateName(name, query string) bool {
	if query == "" {
		return true
	}
	lower := strings.ToLower(name)
	return strings.HasPrefix(lower, query) || strings.Contains(lower, query)
}

func sortCandidateItems(items []CandidateItem, query string) {
	query = strings.ToLower(strings.TrimSpace(query))
	sort.Slice(items, func(i, j int) bool {
		left := strings.ToLower(items[i].Name)
		right := strings.ToLower(items[j].Name)
		leftPrefix := query != "" && strings.HasPrefix(left, query)
		rightPrefix := query != "" && strings.HasPrefix(right, query)
		if leftPrefix != rightPrefix {
			return leftPrefix
		}
		if len(items[i].Name) != len(items[j].Name) {
			return len(items[i].Name) < len(items[j].Name)
		}
		return items[i].Name < items[j].Name
	})
}
