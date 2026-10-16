package api

import (
	"strings"

	"mindfs/server/internal/agent"
)

func applyAgentProviderEnv(agentName string, updates map[string]string, app *AppContext) error {
	env, err := mergeAgentEnvConfig(agentName, updates)
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
	return nil
}

func copilotAPIProviderEnv(provider agentAPIProvider) map[string]string {
	env := map[string]string{
		"COPILOT_PROVIDER_BASE_URL": provider.BaseURL,
		"COPILOT_PROVIDER_API_KEY":  provider.APIKey,
		"COPILOT_MODEL":             firstModelOrDefault(provider.Models, ""),
	}
	switch agentAPIProviderActiveProtocol(provider) {
	case apiProviderProtocolAnthropicCompatible:
		env["COPILOT_PROVIDER_TYPE"] = "anthropic"
	default:
		env["COPILOT_PROVIDER_TYPE"] = "openai"
	}
	return env
}

func mergeAgentEnvConfig(agentName string, updates map[string]string) (map[string]string, error) {
	return writeAgentEnvConfig(agentName, updates, false)
}

func replaceAgentConfiguredEnv(agentName string, updates map[string]string) (map[string]string, error) {
	return writeAgentEnvConfig(agentName, updates, true)
}

func writeAgentEnvConfig(agentName string, updates map[string]string, clearConfigured bool) (map[string]string, error) {
	// clearConfigured 要删的是「配置档记过账的 key」，那份记录在 ConfigBackup 里。
	// 这里读生效配置（安装自带 ⊕ 用户层）只是**只读查表**，不会再被写回用户层。
	clearKeys, err := configuredEnvKeys(agentName)
	if err != nil {
		return nil, err
	}

	var merged map[string]string
	err = mutateUserAgent(agentName, func(def *agent.Definition) {
		merged = cloneStringMap(def.Env)
		if merged == nil {
			merged = map[string]string{}
		}
		if clearConfigured {
			for _, key := range clearKeys {
				delete(merged, key)
			}
		}
		for key, value := range updates {
			if strings.TrimSpace(value) == "" {
				delete(merged, key)
				continue
			}
			merged[key] = value
		}
		def.Env = cloneStringMap(merged)
	})
	if err != nil {
		return nil, err
	}

	// 调用方拿这个 env 去 SetAgentEnv，而它是**整份替换**：必须是生效 env，
	// 用户层没写的键还得靠安装自带那份供给，只回传用户层会当场把它们抹掉。
	effective, err := agent.LoadConfig("")
	if err != nil {
		return nil, err
	}
	if def, ok := effective.GetAgent(agentName); ok && len(def.Env) > 0 {
		return cloneStringMap(def.Env), nil
	}
	return merged, nil
}

// configuredEnvKeys 取配置档记过账的 env key。用户层没记就回退到上游那份。
func configuredEnvKeys(agentName string) ([]string, error) {
	cfg, err := agent.LoadConfig("")
	if err != nil {
		return nil, err
	}
	if def, ok := cfg.GetAgent(agentName); ok {
		return def.ConfigBackup.EnvKeys, nil
	}
	return nil, nil
}
