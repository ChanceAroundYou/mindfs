package claude

import (
	"context"
	"errors"
	"log"
	"strings"

	types "mindfs/server/internal/agent/types"

)
func (s *session) resolveClaudeModelArg(model string) string {
	return ResolveClaudeModelArg(model, claudeEffectiveEnv(s.baseEnv, s.rootPath))
}

func isHiddenClaudeModel(value, displayName, description string) bool {
	joined := strings.ToLower(strings.TrimSpace(value) + " " + strings.TrimSpace(displayName) + " " + strings.TrimSpace(description))
	if strings.Contains(joined, "sonnet") && (strings.Contains(joined, "4.5") || strings.Contains(joined, "4-5")) {
		return true
	}
	return false
}

func (s *session) ListModels(ctx context.Context) (types.ModelList, error) {
	_ = ctx
	if s.client == nil {
		return types.ModelList{}, errors.New("claude session not initialized")
	}
	supported := s.client.SupportedModelsFromInit()
	models := make([]types.ModelInfo, 0, len(supported))
	for _, model := range supported {
		if isHiddenClaudeModel(model.Value, model.DisplayName, model.Description) {
			continue
		}
		models = append(models, claudeModelInfo(model))
	}
	log.Printf("[agent/claude] models.cached session=%s count=%d", s.sessionKey, len(models))
	currentModelID := ""
	if selected := strings.TrimSpace(s.model); selected != "" {
		for _, item := range models {
			if strings.TrimSpace(item.ID) == selected {
				currentModelID = strings.TrimSpace(item.ID)
				break
			}
			// Generic [1m] handling: probe advertises base without suffix, UI may hold base[1m].
			// For the Anthropic alias family compare canonical alias bases, otherwise compare stripped bases.
			if isClaudeAliasModel(selected) && isClaudeAliasModel(item.ID) {
				if resolveClaudeBaseAlias(strip1MSuffix(item.ID)) == resolveClaudeBaseAlias(strip1MSuffix(selected)) {
					currentModelID = strings.TrimSpace(item.ID)
					break
				}
			} else if strip1MSuffix(item.ID) == strip1MSuffix(selected) {
				currentModelID = strings.TrimSpace(item.ID)
				break
			}
		}
	}
	return types.ModelList{
		CurrentModelID: currentModelID,
		Models:         models,
	}, nil
}
