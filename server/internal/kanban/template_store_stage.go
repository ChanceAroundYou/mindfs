package kanban

import (
	"errors"
	"sort"
	"strings"
)
func (s *TemplateStore) ListStageTemplates() ([]StageTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.loadStageTemplatesLocked()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(items, func(i, j int) bool {
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	return items, nil
}


func (s *TemplateStore) SaveStageTemplate(in StageTemplate) (StageTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.loadStageTemplatesLocked()
	if err != nil {
		return StageTemplate{}, err
	}
	now := s.now().UTC()
	in = normalizeStageTemplate(in)
	if in.Name == "" {
		return StageTemplate{}, errors.New("stage template name required")
	}
	if in.Role != RoleUser && in.Role != RoleAgent {
		return StageTemplate{}, errors.New("invalid stage role")
	}
	if in.Role == RoleAgent {
		if strings.TrimSpace(in.Agent) == "" {
			return StageTemplate{}, errors.New("agent stage requires agent")
		}
	}
	if in.ID == "" {
		in.ID = newID("stage")
		in.CreatedAt = now
	}
	in.UpdatedAt = now
	if in.CreatedAt.IsZero() {
		in.CreatedAt = now
	}
	replaced := false
	for i := range items {
		if items[i].ID == in.ID {
			items[i] = in
			replaced = true
			break
		}
	}
	if !replaced {
		items = append(items, in)
	}
	return in, s.saveJSONLocked(stageTemplateFile, items)
}


func (s *TemplateStore) DeleteStageTemplate(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("stage template id required")
	}
	items, err := s.loadStageTemplatesLocked()
	if err != nil {
		return err
	}
	next := items[:0]
	removed := false
	for _, item := range items {
		if item.ID == id {
			removed = true
			continue
		}
		next = append(next, item)
	}
	if !removed {
		return errors.New("stage template not found")
	}
	return s.saveJSONLocked(stageTemplateFile, next)
}


func (s *TemplateStore) loadStageTemplatesLocked() ([]StageTemplate, error) {
	var items []StageTemplate
	if err := s.loadJSONLocked(stageTemplateFile, &items); err != nil {
		return nil, err
	}
	if items == nil {
		items = []StageTemplate{}
	}
	return items, nil
}


func normalizeStageTemplate(in StageTemplate) StageTemplate {
	in.ID = strings.TrimSpace(in.ID)
	in.Name = strings.TrimSpace(in.Name)
	in.Role = strings.TrimSpace(in.Role)
	if in.Role == "" {
		in.Role = RoleUser
	}
	in.Agent = strings.TrimSpace(in.Agent)
	in.Model = strings.TrimSpace(in.Model)
	in.Mode = strings.TrimSpace(in.Mode)
	in.Effort = strings.TrimSpace(in.Effort)
	in.FastService = strings.TrimSpace(in.FastService)
	in.SessionReusePolicy = strings.TrimSpace(in.SessionReusePolicy)
	if in.SessionReusePolicy == "" {
		in.SessionReusePolicy = SessionReuseTaskMain
	}
	in.PromptTemplate = strings.TrimSpace(in.PromptTemplate)
	return in
}

