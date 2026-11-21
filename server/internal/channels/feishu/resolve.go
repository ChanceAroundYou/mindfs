package feishu

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Resolution is the deterministic result of mapping an inbound message to a
// task destination. Exactly one of Project and Candidates is meaningful: a
// "project" result carries a resolved destination, "picker" asks the owner to
// choose from Candidates, and "answer" means the text is not a project request.
type Resolution struct {
	Kind       string
	Project    ProjectRef
	Candidates []ProjectRef
}

const (
	ResolutionProject = "project"
	ResolutionPicker  = "picker"
	ResolutionAnswer  = "answer"

	pickerHardCap = 5
)

// ResolveProject follows the documented order: explicit chat binding, an
// explicit #/@ prefix, a unique project name in the text, a bounded picker,
// then a plain answer. It never guesses a project unrelated to the text.
func ResolveProject(text, chatID string, bindings []Binding, catalog ProjectCatalog,
	defaultRoot string, max int) (Resolution, error) {
	refs, catalogErr := listRefs(catalog)
	if binding, ok := findBinding(bindings, chatID); ok {
		return Resolution{Kind: ResolutionProject, Project: bindingProject(binding, refs)}, nil
	}
	if catalogErr != nil {
		return Resolution{}, catalogErr
	}

	if prefix := prefixToken(text); prefix != "" {
		matches := matchPrefix(refs, prefix)
		if len(matches) == 0 {
			return Resolution{Kind: ResolutionAnswer}, nil
		}
		return finishMatches(matches, max), nil
	}

	if defaultRoot != "" {
		matches := make([]ProjectRef, 0, 1)
		for _, ref := range refs {
			if ref.RootID == defaultRoot {
				matches = append(matches, ref)
			}
		}
		if len(matches) != 0 {
			return finishMatches(matches, max), nil
		}
	}

	matches := make([]ProjectRef, 0, len(refs))
	for _, ref := range refs {
		if mentions(text, ref) {
			matches = append(matches, ref)
		}
	}
	if len(matches) == 0 {
		return Resolution{Kind: ResolutionAnswer}, nil
	}
	return finishMatches(matches, max), nil
}

func listRefs(catalog ProjectCatalog) ([]ProjectRef, error) {
	if catalog == nil {
		return nil, nil
	}
	refs, err := catalog.List(context.Background())
	if err != nil {
		return nil, fmt.Errorf("feishu project catalog: %w", err)
	}
	return refs, nil
}

func findBinding(bindings []Binding, chatID string) (Binding, bool) {
	if chatID == "" {
		return Binding{}, false
	}
	for _, b := range bindings {
		if b.ChatID == chatID && b.NodeID != "" && b.RootID != "" {
			return b, true
		}
	}
	return Binding{}, false
}

// A binding is explicit configuration: it stays usable even when the remote
// catalog is down, with the root id as the display name fallback.
func bindingProject(binding Binding, refs []ProjectRef) ProjectRef {
	for _, ref := range refs {
		if ref.NodeID == binding.NodeID && ref.RootID == binding.RootID {
			return ref
		}
	}
	return ProjectRef{NodeID: binding.NodeID, RootID: binding.RootID, Name: binding.RootID}
}

func prefixToken(text string) string {
	s := strings.TrimSpace(text)
	if s == "" || (s[0] != '#' && s[0] != '@') {
		return ""
	}
	fields := strings.Fields(s[1:])
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func matchPrefix(refs []ProjectRef, token string) []ProjectRef {
	parts := strings.SplitN(token, "/", 2)
	explicitNode := len(parts) == 2 && parts[0] != "" && parts[1] != ""
	matches := make([]ProjectRef, 0, len(refs))
	for _, ref := range refs {
		if explicitNode && !nodeMatches(ref, parts[0]) {
			continue
		}
		name := token
		if explicitNode {
			name = parts[1]
		}
		if exactName(ref, name) {
			matches = append(matches, ref)
		}
	}
	return matches
}

func nodeMatches(ref ProjectRef, node string) bool {
	if strings.EqualFold(ref.NodeID, node) {
		return true
	}
	if i := strings.LastIndex(ref.Name, " · "); i >= 0 {
		return strings.EqualFold(strings.TrimSpace(ref.Name[i+3:]), node)
	}
	return false
}

func exactName(ref ProjectRef, name string) bool {
	return name == ref.RootID || name == ref.Name || name == baseName(ref)
}

func baseName(ref ProjectRef) string {
	if i := strings.LastIndex(ref.Name, " · "); i >= 0 {
		return strings.TrimSpace(ref.Name[:i])
	}
	return ref.Name
}

func mentions(text string, ref ProjectRef) bool {
	lower := strings.ToLower(text)
	for _, name := range []string{ref.RootID, baseName(ref)} {
		if name != "" && strings.Contains(lower, strings.ToLower(name)) {
			return true
		}
	}
	return false
}

func finishMatches(matches []ProjectRef, max int) Resolution {
	sorted := append([]ProjectRef(nil), matches...)
	sort.Slice(sorted, func(i, j int) bool {
		left := sorted[i].NodeID + "\x00" + sorted[i].RootID
		right := sorted[j].NodeID + "\x00" + sorted[j].RootID
		return left < right
	})
	if len(sorted) == 1 {
		return Resolution{Kind: ResolutionProject, Project: sorted[0]}
	}
	limit := pickerHardCap
	if max > 0 && max < limit {
		limit = max
	}
	if len(sorted) > limit {
		sorted = sorted[:limit]
	}
	return Resolution{Kind: ResolutionPicker, Candidates: sorted}
}
