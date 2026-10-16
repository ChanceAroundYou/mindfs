package api

import (
	"strings"

	"mindfs/server/internal/agent"
)

func apiProviderCompatibleWithAgent(provider agentAPIProvider, agentName string) bool {
	_, ok := selectAPIProviderProtocolForAgent(provider, agentName)
	return ok
}

func selectAPIProviderProtocolForAgent(provider agentAPIProvider, agentName string) (string, bool) {
	providerProtocols := agentAPIProviderProtocols(provider)
	for _, supported := range agentSupportedAPIProtocols(agentName) {
		for _, protocol := range providerProtocols {
			if protocol == supported {
				return protocol, true
			}
		}
	}
	return "", false
}

func agentAPIProviderProtocols(provider agentAPIProvider) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(provider.Protocols))
	for _, protocol := range provider.Protocols {
		protocol = strings.TrimSpace(protocol)
		if protocol == "" || seen[protocol] {
			continue
		}
		seen[protocol] = true
		out = append(out, protocol)
	}
	return out
}

func agentAPIProviderActiveProtocol(provider agentAPIProvider) string {
	if protocol := strings.TrimSpace(provider.activeProtocol); protocol != "" {
		return protocol
	}
	protocols := agentAPIProviderProtocols(provider)
	if len(protocols) == 0 {
		return ""
	}
	return protocols[0]
}

func applyAgentAPIProviderCapabilities(statuses []agent.Status) []agent.Status {
	out := make([]agent.Status, len(statuses))
	for i, status := range statuses {
		protocols := agentSupportedAPIProtocols(status.Name)
		status.SupportedAPIProviderProtocols = append([]string(nil), protocols...)
		status.SupportsAPIProviderSwitch = len(protocols) > 0
		out[i] = status
	}
	return out
}

func agentSupportedAPIProtocols(agentName string) []string {
	switch normalizedAPIProviderAgent(agentName) {
	case "codex":
		return []string{apiProviderProtocolOpenAICompatible}
	case "claude":
		return []string{apiProviderProtocolAnthropicCompatible}
	case "gemini":
		return []string{apiProviderProtocolGeminiCompatible}
	case "copilot", "qwen", "kimi", "opencode", "openclaw", "omp":
		return []string{apiProviderProtocolOpenAICompatible, apiProviderProtocolAnthropicCompatible}
	case "pi":
		return []string{apiProviderProtocolOpenAICompatible, apiProviderProtocolAnthropicCompatible, apiProviderProtocolGeminiCompatible}
	case "hermes":
		return []string{apiProviderProtocolOpenAICompatible}
	default:
		return nil
	}
}

func successfulProbeProtocols(modelsByProtocol map[string][]string) []string {
	priority := []string{
		apiProviderProtocolOpenAICompatible,
		apiProviderProtocolAnthropicCompatible,
		apiProviderProtocolGeminiCompatible,
	}
	out := make([]string, 0, len(priority))
	for _, protocol := range priority {
		if len(modelsByProtocol[protocol]) > 0 {
			out = append(out, protocol)
		}
	}
	return out
}

func mergeProtocolModels(modelsByProtocol map[string][]string, protocols []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, protocol := range protocols {
		for _, model := range modelsByProtocol[protocol] {
			model = strings.TrimSpace(model)
			if model == "" || seen[model] {
				continue
			}
			seen[model] = true
			out = append(out, model)
		}
	}
	return out
}
