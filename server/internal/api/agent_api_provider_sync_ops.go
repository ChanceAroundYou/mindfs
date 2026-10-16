package api

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/preferences"
)

func createAgentAPIProvider(ctx context.Context, req agentAPIProviderCreateRequest) (agentAPIProvider, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return agentAPIProvider{}, errors.New("provider name required")
	}
	if strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		return agentAPIProvider{}, errors.New("provider name must not contain path separators")
	}
	baseURL, err := normalizeAPIProviderBaseURL(req.BaseURL)
	if err != nil {
		return agentAPIProvider{}, err
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		return agentAPIProvider{}, errors.New("api key required")
	}
	probe, err := probeAgentAPIProvider(ctx, baseURL, apiKey)
	if err != nil {
		return agentAPIProvider{}, err
	}
	providers, err := readAgentAPIProviders()
	if err != nil {
		return agentAPIProvider{}, err
	}
	id := "api-" + slugifyAPIProviderName(name)
	if id == "api-" {
		id = fmt.Sprintf("api-%d", time.Now().UnixNano())
	}
	for _, provider := range providers {
		if provider.ID == id {
			return agentAPIProvider{}, errAgentConfigConflict
		}
	}
	now := time.Now().Format(time.RFC3339)
	provider := agentAPIProvider{
		ID:            id,
		Name:          name,
		BaseURL:       baseURL,
		APIKey:        apiKey,
		Protocols:     probe.Protocols,
		ModelFamilies: probe.ModelFamilies,
		Models:        probe.Models,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	providers = append(providers, provider)
	if err := writeAgentAPIProviders(providers); err != nil {
		return agentAPIProvider{}, err
	}
	return provider, nil
}

func syncAgentAPIProviders(ctx context.Context, requests []agentAPIProviderCreateRequest) ([]agentAPIProvider, error) {
	if len(requests) == 0 {
		return nil, errors.New("providers required")
	}
	providers, err := readAgentAPIProviders()
	if err != nil {
		return nil, err
	}
	byName := make(map[string]int, len(providers))
	for i, provider := range providers {
		byName[strings.TrimSpace(provider.Name)] = i
	}
	now := time.Now().Format(time.RFC3339)
	changed := make([]agentAPIProvider, 0, len(requests))
	seenNames := map[string]struct{}{}
	for _, req := range requests {
		name := strings.TrimSpace(req.Name)
		if name == "" {
			return nil, errors.New("provider name required")
		}
		if strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
			return nil, errors.New("provider name must not contain path separators")
		}
		if _, ok := seenNames[name]; ok {
			continue
		}
		seenNames[name] = struct{}{}
		baseURL, err := normalizeAPIProviderBaseURL(req.BaseURL)
		if err != nil {
			return nil, err
		}
		apiKey := strings.TrimSpace(req.APIKey)
		if apiKey == "" {
			return nil, errors.New("api key required")
		}
		probe, err := probeAgentAPIProvider(ctx, baseURL, apiKey)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if index, ok := byName[name]; ok {
			provider := providers[index]
			provider.Name = name
			provider.BaseURL = baseURL
			provider.APIKey = apiKey
			provider.Protocols = probe.Protocols
			provider.ModelFamilies = probe.ModelFamilies
			provider.Models = probe.Models
			provider.UpdatedAt = now
			providers[index] = provider
			changed = append(changed, provider)
			continue
		}
		id := "api-" + slugifyAPIProviderName(name)
		if id == "api-" {
			id = fmt.Sprintf("api-%d", time.Now().UnixNano())
		}
		if providerIDExists(providers, id) {
			id = fmt.Sprintf("%s-%d", id, time.Now().UnixNano())
		}
		provider := agentAPIProvider{
			ID:            id,
			Name:          name,
			BaseURL:       baseURL,
			APIKey:        apiKey,
			Protocols:     probe.Protocols,
			ModelFamilies: probe.ModelFamilies,
			Models:        probe.Models,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		providers = append(providers, provider)
		byName[name] = len(providers) - 1
		changed = append(changed, provider)
	}
	if len(changed) == 0 {
		return nil, errors.New("no valid providers")
	}
	if err := writeAgentAPIProviders(providers); err != nil {
		return nil, err
	}
	return changed, nil
}

func providerIDExists(providers []agentAPIProvider, id string) bool {
	for _, provider := range providers {
		if provider.ID == id {
			return true
		}
	}
	return false
}

// syncAllAgentAPIProviders 一键同步：使用已保存的 API Key 逐个重新拉取模型目录。
// 单个供应商同步失败时保留旧模型列表并记录错误，不影响其他供应商。
func syncAllAgentAPIProviders(ctx context.Context, prefs *preferences.Store) ([]agentAPIProvider, []agentAPIProviderSyncAllResult, error) {
	providers, err := readAgentAPIProviders()
	if err != nil {
		return nil, nil, err
	}
	if len(providers) == 0 {
		return providers, nil, nil
	}
	results := make([]agentAPIProviderSyncAllResult, 0, len(providers))
	changed := false
	now := time.Now().Format(time.RFC3339)
	for index := range providers {
		provider := providers[index]
		probe, err := probeAgentAPIProvider(ctx, provider.BaseURL, provider.APIKey)
		if err != nil {
			results = append(results, agentAPIProviderSyncAllResult{
				ID:         provider.ID,
				Name:       provider.Name,
				Success:    false,
				Error:      sanitizeAPIProviderError(err.Error(), provider.APIKey),
				ModelCount: len(provider.Models),
			})
			continue
		}
		provider.Protocols = probe.Protocols
		provider.ModelFamilies = probe.ModelFamilies
		provider.Models = probe.Models
		provider.UpdatedAt = now
		providers[index] = provider
		changed = true
		results = append(results, agentAPIProviderSyncAllResult{
			ID:         provider.ID,
			Name:       provider.Name,
			Success:    true,
			ModelCount: len(provider.Models),
			Applied:    reapplySyncedProvider(prefs, provider),
		})
	}
	if changed {
		if err := writeAgentAPIProviders(providers); err != nil {
			return nil, nil, err
		}
	}
	return providers, results, nil
}

// reapplySyncedProvider 在一键同步成功后，把最新模型列表静默写入所有上次
// 手动应用过该供应商的 Agent 配置；不重启任何 Agent 进程，改动在 Agent
// 下次启动时生效。失败只记录在结果里，不影响其它 Agent。
func reapplySyncedProvider(prefs *preferences.Store, provider agentAPIProvider) []agentAPIProviderSyncAgentResult {
	if prefs == nil {
		return nil
	}
	selections := prefs.AgentLastConfigSelections()
	if len(selections) == 0 {
		return nil
	}
	names := make([]string, 0, len(selections))
	for name := range selections {
		names = append(names, name)
	}
	sort.Strings(names)
	results := make([]agentAPIProviderSyncAgentResult, 0)
	for _, name := range names {
		selection := selections[name]
		if selection.Type != "api_provider" || selection.ID != provider.ID {
			continue
		}
		err := applyAgentAPIProvider(name, provider, nil)
		result := agentAPIProviderSyncAgentResult{Agent: name, Success: err == nil}
		if err != nil {
			result.Error = sanitizeAPIProviderError(err.Error(), provider.APIKey)
		}
		results = append(results, result)
	}
	if len(results) == 0 {
		return nil
	}
	return results
}

func deleteAgentAPIProvider(id string) ([]agentAPIProvider, error) {
	providers, err := readAgentAPIProviders()
	if err != nil {
		return nil, err
	}
	next := make([]agentAPIProvider, 0, len(providers))
	found := false
	for _, provider := range providers {
		if provider.ID == id {
			found = true
			continue
		}
		next = append(next, provider)
	}
	if !found {
		return nil, errors.New("provider not found")
	}
	if err := writeAgentAPIProviders(next); err != nil {
		return nil, err
	}
	return next, nil
}

func switchAgentAPIProvider(req agentAPIProviderSwitchRequest, app *AppContext) (agentAPIProvider, error) {
	agentName := strings.TrimSpace(req.Agent)
	if agentName == "" {
		return agentAPIProvider{}, errors.New("agent required")
	}
	cfg, err := agent.LoadConfig("")
	if err != nil {
		return agentAPIProvider{}, err
	}
	if _, ok := cfg.GetAgent(agentName); !ok {
		return agentAPIProvider{}, fmt.Errorf("agent not configured: %s", agentName)
	}
	providerID := strings.TrimSpace(req.ProviderID)
	if providerID == "" {
		return agentAPIProvider{}, errors.New("provider id required")
	}
	providers, err := readAgentAPIProviders()
	if err != nil {
		return agentAPIProvider{}, err
	}
	var provider agentAPIProvider
	for _, item := range providers {
		if item.ID == providerID {
			provider = item
			break
		}
	}
	if provider.ID == "" {
		return agentAPIProvider{}, errors.New("provider not found")
	}
	selectedProtocol, ok := selectAPIProviderProtocolForAgent(provider, agentName)
	if !ok {
		return agentAPIProvider{}, fmt.Errorf("provider protocols %s are not compatible with agent %s", strings.Join(agentAPIProviderProtocols(provider), ", "), agentName)
	}
	provider.activeProtocol = selectedProtocol
	if err := applyAgentAPIProvider(agentName, provider, app); err != nil {
		return agentAPIProvider{}, err
	}
	if app != nil && app.GetPreferences() != nil {
		if err := app.GetPreferences().UpdateAgentLastConfigSelection(agentName, preferences.LastConfigSelection{
			Type: "api_provider",
			ID:   provider.ID,
			Name: provider.Name,
		}); err != nil {
			return agentAPIProvider{}, err
		}
	}
	if app != nil && app.GetAgentPool() != nil {
		app.GetAgentPool().KillAgentProcess(agentName, 0)
	}
	triggerAgentConfigSwitchProbe(app, agentName)
	return provider, nil
}
