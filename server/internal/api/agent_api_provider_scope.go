package api

import (
	"strings"

	"mindfs/internal/codexsync"
)

const (
	// codexOfficialProviderID 是「官方订阅」的合成条目：default profile 没有
	// model_providers 表，OpenAI 官方供应商由 Codex 隐式提供，配置文件里没有
	// 可枚举的条目。用固定 ID 让供应商面板始终能把它列出来并切回 default。
	codexOfficialProviderID      = "shared:codex-default"
	agentAPIProviderKindOfficial = "official"
)

func officialCodexAPIProvider() agentAPIProvider {
	return agentAPIProvider{
		ID:            codexOfficialProviderID,
		Name:          "官方订阅",
		Kind:          agentAPIProviderKindOfficial,
		Protocols:     []string{apiProviderProtocolOpenAICompatible},
		ModelFamilies: []string{"openai"},
	}
}

// scopeCodexAPIProvidersAt 让 Codex 的供应商列表跟随当前活动配置文件：
//   - 官方订阅条目恒定存在，代表 default profile 的隐式 OpenAI 供应商；
//   - 映射到某个 shared profile 的供应商只在该 profile 活动时出现，因此切回
//     default 后 llmux provider 不再显示，也不会被误认为仍在生效；
//   - 未映射到 shared profile 的自定义元数据条目保持可见，否则用户无法管理
//     自己添加的条目（它们对 Codex 仍是 metadata-only）。
func scopeCodexAPIProvidersAt(paths codexsync.Paths, providers []agentAPIProvider, activeProfile string) ([]agentAPIProvider, error) {
	active := strings.ToLower(strings.TrimSpace(activeProfile))
	if active == "" {
		active = "default"
	}
	out := make([]agentAPIProvider, 0, len(providers)+1)
	out = append(out, officialCodexAPIProvider())
	for _, provider := range providers {
		profile, matched, err := codexsync.SharedProfileForProviderAt(paths, provider.Name, provider.BaseURL)
		if err != nil {
			return nil, err
		}
		if matched && profile != active {
			continue
		}
		out = append(out, provider)
	}
	return out, nil
}
