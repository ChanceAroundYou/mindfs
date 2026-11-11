package usecase

import (
	"fmt"
	"strings"

	agenttypes "mindfs/server/internal/agent/types"
)

func isClaudeAliasModelForValidate(model string) bool {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return false
	}
	has := len(trimmed) >= 4 && strings.EqualFold(trimmed[len(trimmed)-4:], "[1m]")
	base := trimmed
	if has {
		base = strings.TrimSpace(trimmed[:len(trimmed)-4])
	}
	lower := strings.ToLower(strings.TrimSpace(base))
	switch lower {
	case "fable", "opus", "sonnet", "haiku", "of", "op", "os", "ok", "default":
		return true
	default:
		return false
	}
}

func canonicalClaudeModelForValidate(model string) string {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return ""
	}
	if strings.EqualFold(trimmed, "default") {
		return trimmed
	}
	if !isClaudeAliasModelForValidate(trimmed) {
		return trimmed
	}
	has := len(trimmed) >= 4 && strings.EqualFold(trimmed[len(trimmed)-4:], "[1m]")
	base := trimmed
	if has {
		base = strings.TrimSpace(trimmed[:len(trimmed)-4])
	}
	lower := strings.ToLower(strings.TrimSpace(base))
	switch lower {
	case "fable":
		lower = "of"
	case "opus":
		lower = "op"
	case "sonnet":
		lower = "os"
	case "haiku":
		lower = "ok"
	}
	if has {
		return lower + "[1m]"
	}
	return lower
}

func claudeModelBaseForValidate(model string) string {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return ""
	}
	if strings.EqualFold(trimmed, "default") {
		return "default"
	}
	has := len(trimmed) >= 4 && strings.EqualFold(trimmed[len(trimmed)-4:], "[1m]")
	base := trimmed
	if has {
		base = strings.TrimSpace(trimmed[:len(trimmed)-4])
	}
	if !isClaudeAliasModelForValidate(trimmed) {
		return strings.TrimSpace(base)
	}
	lower := strings.ToLower(strings.TrimSpace(base))
	switch lower {
	case "fable":
		lower = "of"
	case "opus":
		lower = "op"
	case "sonnet":
		lower = "os"
	case "haiku":
		lower = "ok"
	}
	return lower
}

func (s *Service) validateAgentModel(agentName, model string) error {
	agentName = strings.TrimSpace(agentName)
	model = strings.TrimSpace(model)
	if agentName == "" || model == "" || s.Registry == nil {
		return nil
	}
	prober := s.Registry.GetProber()
	if prober == nil {
		return nil
	}
	status, ok := prober.GetStatus(agentName)
	if !ok || len(status.Models) == 0 {
		return nil
	}
	// 自定义模型只存在于 preferences 并合入 /api/agents 的目录，prober 状态里没有它们。
	// 校验必须看到同一份目录，否则界面能选中的模型会在发送时被这里拒绝。
	models := status.Models
	if prefs := s.Registry.GetPreferences(); prefs != nil {
		models = withCustomModels(models, prefs.AgentCustomModels(agentName))
	}
	return validateModelAgainstModels(agentName, model, models)
}

func withCustomModels(models []agenttypes.ModelInfo, custom []string) []agenttypes.ModelInfo {
	if len(custom) == 0 {
		return models
	}
	seen := make(map[string]struct{}, len(models)+len(custom))
	for _, item := range models {
		seen[strings.TrimSpace(item.ID)] = struct{}{}
	}
	out := append([]agenttypes.ModelInfo(nil), models...)
	for _, id := range custom {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, agenttypes.ModelInfo{ID: id, Name: id})
	}
	return out
}

func validateModelAgainstModels(agentName, model string, models []agenttypes.ModelInfo) error {
	isClaude := strings.EqualFold(agentName, "claude")
	if isClaude {
		model = canonicalClaudeModelForValidate(model)
	}
	for _, item := range models {
		candidate := strings.TrimSpace(item.ID)
		if isClaude {
			candidate = canonicalClaudeModelForValidate(candidate)
		}
		if candidate == model {
			return nil
		}
		// 1M suffix is an explicit user toggle, not a separate model entry.
		// Prober advertises base ids without [1m] (both Anthropic aliases and generic provider ids like glm-*/deepseek-*),
		// so base and base[1m] are the same model.
		if isClaude && claudeModelBaseForValidate(candidate) != "" && claudeModelBaseForValidate(candidate) == claudeModelBaseForValidate(model) {
			return nil
		}
	}
	supported := make([]string, 0, len(models))
	for i, item := range models {
		if i >= 12 {
			break
		}
		if id := strings.TrimSpace(item.ID); id != "" {
			supported = append(supported, id)
		}
	}
	return fmt.Errorf("model %q is not supported by agent %q (supported models: %s)", model, agentName, strings.Join(supported, ", "))
}
