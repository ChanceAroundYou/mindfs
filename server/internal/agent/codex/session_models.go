package codex

import (
	"context"
	"errors"
	"log"
	"strings"

	types "mindfs/server/internal/agent/types"

	codexsdk "github.com/fanwenlin/codex-go-sdk/codex"
)

func (s *session) CurrentModel() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.threadOpts.Model)
}

func (s *session) SetModel(_ context.Context, model string) error {
	if s == nil || s.client == nil {
		return errors.New("codex session not initialized")
	}
	threadID := strings.TrimSpace(s.SessionID())
	if threadID == "" {
		s.updateThreadIDFromThread()
		threadID = strings.TrimSpace(s.SessionID())
	}
	if threadID == "" {
		return errors.New("codex thread id unavailable")
	}
	opts := s.threadOpts
	opts.Model = strings.TrimSpace(model)
	opts.CollaborationMode = codexCollaborationMode(s.planMode)
	thread := s.client.ResumeThread(threadID, opts)
	s.mu.Lock()
	s.thread = thread
	s.threadOpts = opts
	s.threadID = threadID
	s.mu.Unlock()
	return nil
}

func (s *session) SetPlanMode(ctx context.Context, enabled bool) error {
	if s == nil || s.client == nil {
		return errors.New("codex session not initialized")
	}
	threadID := strings.TrimSpace(s.SessionID())
	if threadID == "" {
		s.updateThreadIDFromThread()
		threadID = strings.TrimSpace(s.SessionID())
	}
	if threadID == "" {
		return errors.New("codex thread id unavailable")
	}
	opts := s.threadOpts
	opts.CollaborationMode = codexCollaborationMode(enabled)
	thread := s.client.ResumeThread(threadID, opts)
	if err := thread.SetCollaborationMode(ctx, opts.CollaborationMode); err != nil {
		return err
	}
	s.mu.Lock()
	s.thread = thread
	s.threadOpts = opts
	s.threadID = threadID
	s.planMode = enabled
	s.mu.Unlock()
	return nil
}

func (s *session) ListModels(ctx context.Context) (types.ModelList, error) {
	if s == nil || s.client == nil {
		return types.ModelList{}, errors.New("codex session not initialized")
	}
	resp, err := s.client.ListModels(ctx, codexListModelsParams())
	if err != nil {
		return types.ModelList{}, err
	}
	models := make([]types.ModelInfo, 0, len(resp.Data))
	currentModelID := ""
	defaults, _ := s.RuntimeDefaults(ctx)
	for _, model := range resp.Data {
		name := strings.TrimSpace(model.DisplayName)
		if name == "" {
			name = strings.TrimSpace(model.Model)
		}
		models = append(models, types.ModelInfo{
			ID:            model.Model,
			Name:          name,
			Description:   model.Description,
			Hidden:        model.Hidden,
			SupportEffort: true,
			Efforts:       codexModelEfforts(model.SupportedReasoningEfforts),
		})
		if model.IsDefault && currentModelID == "" {
			currentModelID = model.Model
		}
	}
	if strings.TrimSpace(defaults.Model) != "" {
		currentModelID = strings.TrimSpace(defaults.Model)
	}
	return types.ModelList{
		CurrentModelID: currentModelID,
		Models:         models,
	}, nil
}

func codexListModelsParams() codexsdk.ModelListParams {
	includeHidden := true
	return codexsdk.ModelListParams{IncludeHidden: &includeHidden}
}

func codexModelEfforts(options []codexsdk.ReasoningEffortOption) []string {
	efforts := make([]string, 0, len(options))
	seen := make(map[string]struct{}, len(options))
	for _, option := range options {
		effort := strings.TrimSpace(string(option.ReasoningEffort))
		if effort == "" || effort == "none" {
			continue
		}
		if _, ok := seen[effort]; ok {
			continue
		}
		seen[effort] = struct{}{}
		efforts = append(efforts, effort)
	}
	return efforts
}

func (s *session) RuntimeDefaults(ctx context.Context) (types.RuntimeDefaults, error) {
	if s == nil || s.client == nil {
		return types.RuntimeDefaults{}, errors.New("codex session not initialized")
	}
	params := codexsdk.ConfigReadParams{
		IncludeLayers: false,
		Cwd:           strings.TrimSpace(s.threadOpts.WorkingDirectory),
	}
	resp, err := s.client.ReadConfig(ctx, params)
	if err != nil {
		log.Printf("[codex/runtime_defaults] config.read.error cwd=%q err=%v", params.Cwd, err)
		return types.RuntimeDefaults{}, err
	}
	defaults := types.RuntimeDefaults{}
	if resp == nil {
		log.Printf("[codex/runtime_defaults] config.read.empty cwd=%q", params.Cwd)
		return defaults, nil
	}
	if resp.Config.Model != nil {
		defaults.Model = strings.TrimSpace(*resp.Config.Model)
	}
	if resp.Config.ModelReasoningEffort != nil {
		defaults.Effort = strings.TrimSpace(string(*resp.Config.ModelReasoningEffort))
	}
	if resp.Config.ServiceTier != nil {
		if strings.TrimSpace(*resp.Config.ServiceTier) == "fast" {
			defaults.FastService = "on"
		} else {
			defaults.FastService = "off"
		}
	}
	return defaults, nil
}

func (s *session) SetMode(_ context.Context, _ string) error {
	return nil
}

func (s *session) ListModes(_ context.Context) (types.ModeList, error) {
	return types.ModeList{}, nil
}

func (s *session) ListCommands(ctx context.Context) (types.CommandList, error) {
	_ = ctx
	if s == nil || s.client == nil {
		return types.CommandList{}, errors.New("codex session not initialized")
	}
	supported := s.client.SupportedSlashCommands()
	commands := make([]types.CommandInfo, 0, len(supported))
	for _, command := range supported {
		name := strings.TrimSpace(command.Name)
		if name == "" {
			continue
		}
		commands = append(commands, types.CommandInfo{
			Name:         name,
			Description:  strings.TrimSpace(command.Description),
			ArgumentHint: strings.TrimSpace(command.ArgumentHint),
		})
	}
	log.Printf("[agent/codex] commands.cached session=%s count=%d", s.sessionKey, len(commands))
	return types.CommandList{Commands: commands}, nil
}
