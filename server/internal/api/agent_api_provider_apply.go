package api

import (
)

func applyAgentAPIProvider(agentName string, provider agentAPIProvider, app *AppContext) error {
	switch normalizedAPIProviderAgent(agentName) {
	case "codex":
		if err := applyCodexAPIProvider(provider); err != nil {
			return err
		}
	case "claude":
		env, err := replaceAgentConfiguredEnv(agentName, map[string]string{
			"ANTHROPIC_BASE_URL":   provider.BaseURL,
			"ANTHROPIC_AUTH_TOKEN": provider.APIKey,
		})
		if err != nil {
			return err
		}
		if app != nil && app.GetAgentPool() != nil {
			if err := app.GetAgentPool().SetAgentEnv(agentName, env); err != nil {
				return err
			}
		}
		if app != nil && app.GetProber() != nil {
			if err := app.GetProber().SetAgentEnv(agentName, env); err != nil {
				return err
			}
		}
	case "gemini":
		if err := applyGeminiAPIProvider(provider); err != nil {
			return err
		}
	case "copilot":
		if err := applyAgentProviderEnv(agentName, copilotAPIProviderEnv(provider), app); err != nil {
			return err
		}
	case "qwen":
		if err := applyQwenAPIProvider(provider); err != nil {
			return err
		}
	case "kimi":
		if err := applyKimiAPIProvider(provider); err != nil {
			return err
		}
	case "opencode":
		if err := applyOpenCodeAPIProvider(provider); err != nil {
			return err
		}
	case "openclaw":
		if err := applyOpenClawAPIProvider(provider); err != nil {
			return err
		}
	case "omp":
		if err := applyOMPAPIProvider(provider); err != nil {
			return err
		}
	case "pi":
		if err := applyPiAPIProvider(provider); err != nil {
			return err
		}
	case "hermes":
		if err := applyHermesAPIProvider(provider); err != nil {
			return err
		}
	default:
		if err := applyAgentProviderEnv(agentName, map[string]string{
			"OPENAI_BASE_URL": provider.BaseURL,
			"OPENAI_API_KEY":  provider.APIKey,
		}, app); err != nil {
			return err
		}
	}
	return nil
}
