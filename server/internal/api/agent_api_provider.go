package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/apperr"
	configpkg "mindfs/server/internal/config"
	"mindfs/server/internal/preferences"
)

const (
	apiProviderProtocolOpenAICompatible    = "openai-compatible"
	apiProviderProtocolAnthropicCompatible = "anthropic-compatible"
	apiProviderProtocolGeminiCompatible    = "gemini-compatible"
)

type agentAPIProvider struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	BaseURL        string   `json:"baseUrl"`
	APIKey         string   `json:"apiKey,omitempty"`
	Protocols      []string `json:"protocols,omitempty"`
	ModelFamilies  []string `json:"modelFamilies"`
	Models         []string `json:"models"`
	CreatedAt      string   `json:"createdAt"`
	UpdatedAt      string   `json:"updatedAt"`
	activeProtocol string
}

type agentAPIProviderPublic struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	BaseURL       string   `json:"baseUrl"`
	Protocols     []string `json:"protocols,omitempty"`
	ModelFamilies []string `json:"modelFamilies"`
	Models        []string `json:"models,omitempty"`
	CreatedAt     string   `json:"createdAt"`
	UpdatedAt     string   `json:"updatedAt"`
}

type agentAPIProviderCreateRequest struct {
	Name    string `json:"name"`
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
}

type agentAPIProviderSyncRequest struct {
	Providers []agentAPIProviderCreateRequest `json:"providers"`
}

type agentAPIProviderSwitchRequest struct {
	Agent      string `json:"agent"`
	ProviderID string `json:"provider_id"`
}

type agentAPIProviderSyncAgentResult struct {
	Agent   string `json:"agent"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

type agentAPIProviderSyncAllResult struct {
	ID         string                            `json:"id"`
	Name       string                            `json:"name"`
	Success    bool                              `json:"success"`
	Error      string                            `json:"error,omitempty"`
	ModelCount int                               `json:"modelCount,omitempty"`
	Applied    []agentAPIProviderSyncAgentResult `json:"applied,omitempty"`
}

type agentAPIProviderTestRequest struct {
	ProviderID string `json:"provider_id"`
	Model      string `json:"model"`
	Prompt     string `json:"prompt,omitempty"`
}

type agentAPIProviderTestResult struct {
	Success   bool   `json:"success"`
	LatencyMS int64  `json:"latency_ms"`
	Model     string `json:"model"`
	Protocol  string `json:"protocol,omitempty"`
	Response  string `json:"response,omitempty"`
	Error     string `json:"error,omitempty"`
}

type agentAPIProviderProbeResult struct {
	Protocols     []string
	Models        []string
	ModelFamilies []string
}

func (h *HTTPHandler) handleAgentAPIProvidersList(w http.ResponseWriter, r *http.Request) {
	agentName := strings.TrimSpace(r.URL.Query().Get("agent"))
	providers, err := readAgentAPIProviders()
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, err)
		return
	}
	out := make([]agentAPIProviderPublic, 0, len(providers))
	for _, provider := range providers {
		if agentName != "" && !apiProviderCompatibleWithAgent(provider, agentName) {
			continue
		}
		out = append(out, publicAgentAPIProvider(provider))
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *HTTPHandler) handleAgentAPIProviderCreate(w http.ResponseWriter, r *http.Request) {
	var req agentAPIProviderCreateRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxUploadRequestBytes)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid request body"))
		return
	}
	provider, err := createAgentAPIProvider(r.Context(), req)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errAgentConfigConflict) {
			status = http.StatusConflict
		}
		respondError(w, status, err)
		return
	}
	respondJSON(w, http.StatusOK, publicAgentAPIProvider(provider))
}

func (h *HTTPHandler) handleAgentAPIProvidersSync(w http.ResponseWriter, r *http.Request) {
	var req agentAPIProviderSyncRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxUploadRequestBytes)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid request body"))
		return
	}
	providers, err := syncAgentAPIProviders(r.Context(), req.Providers)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	out := make([]agentAPIProviderPublic, 0, len(providers))
	for _, provider := range providers {
		out = append(out, publicAgentAPIProvider(provider))
	}
	respondJSON(w, http.StatusOK, map[string]any{"providers": out})
}

func (h *HTTPHandler) handleAgentAPIProvidersSyncAll(w http.ResponseWriter, r *http.Request) {
	providers, results, err := syncAllAgentAPIProviders(r.Context(), h.AppContext.GetPreferences())
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, err)
		return
	}
	out := make([]agentAPIProviderPublic, 0, len(providers))
	for _, provider := range providers {
		out = append(out, publicAgentAPIProvider(provider))
	}
	respondJSON(w, http.StatusOK, map[string]any{"providers": out, "results": results})
}

func (h *HTTPHandler) handleAgentAPIProviderTest(w http.ResponseWriter, r *http.Request) {
	var req agentAPIProviderTestRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxUploadRequestBytes)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid request body"))
		return
	}
	result, err := testAgentAPIProviderModel(r.Context(), req)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, result)
}

func (h *HTTPHandler) handleAgentAPIProviderDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("provider id required"))
		return
	}
	providers, err := deleteAgentAPIProvider(id)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	out := make([]agentAPIProviderPublic, 0, len(providers))
	for _, provider := range providers {
		out = append(out, publicAgentAPIProvider(provider))
	}
	respondJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id, "providers": out})
}

func (h *HTTPHandler) handleAgentAPIProviderSwitch(w http.ResponseWriter, r *http.Request) {
	var req agentAPIProviderSwitchRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxUploadRequestBytes)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid request body"))
		return
	}
	provider, err := switchAgentAPIProvider(req, h.AppContext)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"provider": publicAgentAPIProvider(provider),
	})
}

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

// testAgentAPIProviderModel 对单个模型发送最小测试请求，返回延迟、错误与简短响应。
func testAgentAPIProviderModel(ctx context.Context, req agentAPIProviderTestRequest) (agentAPIProviderTestResult, error) {
	providerID := strings.TrimSpace(req.ProviderID)
	if providerID == "" {
		return agentAPIProviderTestResult{}, errors.New("provider id required")
	}
	providers, err := readAgentAPIProviders()
	if err != nil {
		return agentAPIProviderTestResult{}, err
	}
	var target *agentAPIProvider
	for index := range providers {
		if providers[index].ID == providerID {
			target = &providers[index]
			break
		}
	}
	if target == nil {
		return agentAPIProviderTestResult{}, errors.New("provider not found")
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = firstModelOrDefault(target.Models, "")
	}
	if model == "" {
		return agentAPIProviderTestResult{}, errors.New("provider has no models to test")
	}
	chosen := apiProviderProtocolForTest(target)
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		prompt = "Reply with the single word: OK"
	}
	start := time.Now()
	response, err := runAPIProviderModelTest(ctx, chosen, target.BaseURL, target.APIKey, model, prompt)
	latency := time.Since(start).Milliseconds()
	result := agentAPIProviderTestResult{
		Success:   err == nil,
		LatencyMS: latency,
		Model:     model,
		Protocol:  chosen,
	}
	if err != nil {
		result.Error = sanitizeAPIProviderError(err.Error(), target.APIKey)
		return result, nil
	}
	result.Response = truncateAPIProviderResponse(response)
	return result, nil
}

func apiProviderProtocolForTest(provider *agentAPIProvider) string {
	if protocol := agentAPIProviderActiveProtocol(*provider); protocol != "" {
		return protocol
	}
	if len(provider.Protocols) > 0 {
		return provider.Protocols[0]
	}
	return apiProviderProtocolOpenAICompatible
}

func runAPIProviderModelTest(ctx context.Context, protocol, baseURL, apiKey, model, prompt string) (string, error) {
	switch protocol {
	case apiProviderProtocolAnthropicCompatible:
		return testAnthropicCompatibleModel(ctx, baseURL, apiKey, model, prompt)
	case apiProviderProtocolGeminiCompatible:
		return testGeminiCompatibleModel(ctx, baseURL, apiKey, model, prompt)
	default:
		return testOpenAICompatibleModel(ctx, baseURL, apiKey, model, prompt)
	}
}

const apiProviderTestTimeout = 30 * time.Second
const apiProviderTestMaxTokens = 32

func sanitizeAPIProviderError(message string, secrets ...string) string {
	// Redact before truncating so a key crossing the length limit cannot leak.
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		encoded, _ := json.Marshal(secret)
		for _, value := range []string{string(encoded[1 : len(encoded)-1]), url.QueryEscape(secret), url.PathEscape(secret), secret} {
			message = strings.ReplaceAll(message, value, "[REDACTED]")
		}
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return "unknown error"
	}
	if len(message) > 400 {
		message = message[:400] + "..."
	}
	return message
}

func truncateAPIProviderResponse(response string) string {
	response = strings.TrimSpace(response)
	if len(response) > 200 {
		response = response[:200] + "..."
	}
	return response
}

func testOpenAICompatibleModel(ctx context.Context, baseURL, apiKey, model, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, apiProviderTestTimeout)
	defer cancel()
	body := map[string]any{
		"model":      model,
		"max_tokens": apiProviderTestMaxTokens,
		"messages":   []map[string]string{{"role": "user", "content": prompt}},
	}
	data, err := postAPIProviderJSON(ctx, openAIChatURL(baseURL), body, map[string]string{
		"Authorization": "Bearer " + apiKey,
	})
	if err != nil {
		return "", err
	}
	var payload struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("invalid response: %w", err)
	}
	if payload.Error != nil && strings.TrimSpace(payload.Error.Message) != "" {
		return "", errors.New(strings.TrimSpace(payload.Error.Message))
	}
	if len(payload.Choices) == 0 {
		return "", errors.New("empty response")
	}
	return payload.Choices[0].Message.Content, nil
}

func testAnthropicCompatibleModel(ctx context.Context, baseURL, apiKey, model, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, apiProviderTestTimeout)
	defer cancel()
	body := map[string]any{
		"model":      model,
		"max_tokens": apiProviderTestMaxTokens,
		"messages":   []map[string]string{{"role": "user", "content": prompt}},
	}
	headers := map[string]string{
		"x-api-key":         apiKey,
		"anthropic-version": "2023-06-01",
	}
	data, err := postAPIProviderJSON(ctx, anthropicMessagesURL(baseURL), body, headers)
	if err != nil {
		return "", err
	}
	var payload struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("invalid response: %w", err)
	}
	if payload.Error != nil && strings.TrimSpace(payload.Error.Message) != "" {
		return "", errors.New(strings.TrimSpace(payload.Error.Message))
	}
	parts := make([]string, 0, len(payload.Content))
	for _, block := range payload.Content {
		if text := strings.TrimSpace(block.Text); text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return "", errors.New("empty response")
	}
	return strings.Join(parts, "\n"), nil
}

func testGeminiCompatibleModel(ctx context.Context, baseURL, apiKey, model, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, apiProviderTestTimeout)
	defer cancel()
	body := map[string]any{
		"contents": []map[string]any{{
			"role":  "user",
			"parts": []map[string]string{{"text": prompt}},
		}},
		"generationConfig": map[string]any{
			"maxOutputTokens": apiProviderTestMaxTokens,
		},
	}
	data, err := postAPIProviderJSON(ctx, geminiGenerateURL(baseURL, model), body, map[string]string{
		"x-goog-api-key": apiKey,
	})
	if err != nil {
		return "", err
	}
	var payload struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("invalid response: %w", err)
	}
	if payload.Error != nil && strings.TrimSpace(payload.Error.Message) != "" {
		return "", errors.New(strings.TrimSpace(payload.Error.Message))
	}
	parts := make([]string, 0)
	for _, candidate := range payload.Candidates {
		for _, part := range candidate.Content.Parts {
			if text := strings.TrimSpace(part.Text); text != "" {
				parts = append(parts, text)
			}
		}
	}
	if len(parts) == 0 {
		return "", errors.New("empty response")
	}
	return strings.Join(parts, "\n"), nil
}

func openAIChatURL(baseURL string) string {
	return openAIModelsBaseURL(baseURL) + "/chat/completions"
}

func anthropicMessagesURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/v1") {
		return base + "/messages"
	}
	return base + "/v1/messages"
}

func geminiGenerateURL(baseURL, model string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/v1beta") || strings.HasSuffix(base, "/v1") {
		return base + "/models/" + model + ":generateContent"
	}
	return base + "/v1beta/models/" + model + ":generateContent"
}

func postAPIProviderJSON(ctx context.Context, endpoint string, body map[string]any, extraHeaders ...map[string]string) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for _, headers := range extraHeaders {
		for key, value := range headers {
			req.Header.Set(key, value)
		}
	}
	client := &http.Client{Timeout: apiProviderTestTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d %s", resp.StatusCode, string(data))
	}
	return data, nil
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

func probeAgentAPIProvider(ctx context.Context, baseURL, apiKey string) (agentAPIProviderProbeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	attempts := []struct {
		protocol string
		fn       func(context.Context, string, string) ([]string, error)
	}{
		{apiProviderProtocolOpenAICompatible, probeOpenAICompatibleModels},
		{apiProviderProtocolAnthropicCompatible, probeAnthropicCompatibleModels},
		{apiProviderProtocolGeminiCompatible, probeGeminiCompatibleModels},
	}
	type probeAttemptResult struct {
		protocol string
		models   []string
		err      error
	}
	results := make(chan probeAttemptResult, len(attempts))
	for _, attempt := range attempts {
		attempt := attempt
		go func() {
			models, err := attempt.fn(ctx, baseURL, apiKey)
			results <- probeAttemptResult{protocol: attempt.protocol, models: models, err: err}
		}()
	}
	var failures []string
	modelsByProtocol := map[string][]string{}
	for range attempts {
		result := <-results
		if result.err != nil {
			failures = append(failures, result.protocol+": "+result.err.Error())
			continue
		}
		if len(result.models) == 0 {
			failures = append(failures, result.protocol+": no models returned")
			continue
		}
		modelsByProtocol[result.protocol] = result.models
	}
	protocols := successfulProbeProtocols(modelsByProtocol)
	if len(protocols) > 0 {
		models := mergeProtocolModels(modelsByProtocol, protocols)
		families := inferModelFamilies(models)
		return agentAPIProviderProbeResult{
			Protocols:     protocols,
			Models:        models,
			ModelFamilies: families,
		}, nil
	}
	return agentAPIProviderProbeResult{}, fmt.Errorf("unable to identify API protocol: %s", strings.Join(failures, "; "))
}

func probeOpenAICompatibleModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openAIModelsURL(baseURL), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := doModelProbe(req, &payload); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		if id := strings.TrimSpace(item.ID); id != "" {
			models = append(models, id)
		}
	}
	return normalizeModelIDs(models), nil
}

func probeAnthropicCompatibleModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openAIModelsURL(baseURL), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	var payload struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := doModelProbe(req, &payload); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		if id := strings.TrimSpace(item.ID); id != "" {
			models = append(models, id)
		}
	}
	return normalizeModelIDs(models), nil
}

func probeGeminiCompatibleModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	endpoint, err := url.Parse(geminiModelsURL(baseURL))
	if err != nil {
		return nil, err
	}
	q := endpoint.Query()
	q.Set("key", apiKey)
	endpoint.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Models []struct {
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
		} `json:"models"`
	}
	if err := doModelProbe(req, &payload); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(payload.Models))
	for _, item := range payload.Models {
		name := strings.TrimSpace(item.Name)
		name = strings.TrimPrefix(name, "models/")
		if name != "" {
			models = append(models, name)
		}
	}
	return normalizeModelIDs(models), nil
}

func doModelProbe(req *http.Request, target any) error {
	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("status %d %s", resp.StatusCode, sanitizeAPIProviderError(string(body),
			strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer "), req.Header.Get("x-api-key"), req.URL.Query().Get("key")))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(target); err != nil {
		return err
	}
	return nil
}

func readAgentAPIProviders() ([]agentAPIProvider, error) {
	path, err := agentAPIProvidersPath()
	if err != nil {
		return nil, err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []agentAPIProvider{}, nil
		}
		return nil, apperr.Wrap("read", path, err)
	}
	if len(strings.TrimSpace(string(payload))) == 0 {
		return []agentAPIProvider{}, nil
	}
	var providers []agentAPIProvider
	if err := json.Unmarshal(payload, &providers); err != nil {
		return nil, err
	}
	for i := range providers {
		providers[i].Protocols = agentAPIProviderProtocols(providers[i])
	}
	return providers, nil
}

func writeAgentAPIProviders(providers []agentAPIProvider) error {
	path, err := agentAPIProvidersPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return apperr.Wrap("mkdir", filepath.Dir(path), err)
	}
	payload, err := json.MarshalIndent(providers, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return apperr.Wrap("write", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(path)
		if retryErr := os.Rename(tmp, path); retryErr != nil {
			return apperr.Wrap("rename", path, err)
		}
	}
	return nil
}

func agentAPIProvidersPath() (string, error) {
	configDir, err := configpkg.MindFSConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "api-providers.json"), nil
}

func publicAgentAPIProvider(provider agentAPIProvider) agentAPIProviderPublic {
	return agentAPIProviderPublic{
		ID:            provider.ID,
		Name:          provider.Name,
		BaseURL:       provider.BaseURL,
		Protocols:     agentAPIProviderProtocols(provider),
		ModelFamilies: append([]string(nil), provider.ModelFamilies...),
		Models:        append([]string(nil), provider.Models...),
		CreatedAt:     provider.CreatedAt,
		UpdatedAt:     provider.UpdatedAt,
	}
}

func normalizeAPIProviderBaseURL(input string) (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(input), "/")
	if baseURL == "" {
		return "", errors.New("baseUrl required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("baseUrl must be an absolute URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("baseUrl scheme must be http or https")
	}
	return baseURL, nil
}

func openAIModelsURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/v1") {
		return base + "/models"
	}
	return base + "/v1/models"
}

func openAIModelsBaseURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/v1") {
		return base
	}
	return base + "/v1"
}

func geminiModelsURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/v1beta") {
		return base + "/models"
	}
	return base + "/v1beta/models"
}

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

func agentAPIProviderConfigName(provider agentAPIProvider) string {
	name := strings.TrimSpace(provider.Name)
	if name == "" {
		name = strings.TrimSpace(provider.ID)
	}
	if name == "" {
		return "api-provider"
	}
	return name
}

func tomlDottedTable(parent, key string) string {
	return "[" + parent + "." + tomlKey(key) + "]"
}

func tomlKey(key string) string {
	if isTOMLBareKey(key) {
		return key
	}
	return strconv.Quote(key)
}

func isTOMLBareKey(key string) bool {
	if key == "" {
		return false
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_' || r == '-':
			continue
		default:
			return false
		}
	}
	return true
}

func normalizedAPIProviderAgent(agentName string) string {
	name := strings.ToLower(strings.TrimSpace(agentName))
	switch name {
	case "claude", "claudecode", "claude-code":
		return "claude"
	case "github-copilot", "copilot-cli":
		return "copilot"
	case "pi", "pi-acp", "pi-coding-agent":
		return "pi"
	case "qwen-code", "qwencode":
		return "qwen"
	case "kimi-code":
		return "kimi"
	default:
		return name
	}
}

func slugifyAPIProviderName(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '.' || r == '_' || r == '-' || r == ' ':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func normalizeModelIDs(input []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(input))
	for _, item := range input {
		id := strings.TrimSpace(item)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func inferModelFamilies(models []string) []string {
	seen := map[string]bool{}
	var families []string
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			families = append(families, name)
		}
	}
	for _, model := range models {
		lower := strings.ToLower(model)
		switch {
		case strings.Contains(lower, "deepseek"):
			add("deepseek")
		case strings.Contains(lower, "claude"):
			add("anthropic")
		case strings.Contains(lower, "gemini"):
			add("gemini")
		case strings.Contains(lower, "qwen"):
			add("qwen")
		case strings.Contains(lower, "glm") || strings.Contains(lower, "zhipu") || strings.Contains(lower, "bigmodel"):
			add("glm")
		case strings.Contains(lower, "kimi") || strings.Contains(lower, "moonshot"):
			add("kimi")
		case strings.Contains(lower, "doubao") || strings.Contains(lower, "seed"):
			add("doubao")
		case strings.HasPrefix(lower, "gpt-") || strings.HasPrefix(lower, "o1") || strings.HasPrefix(lower, "o3") || strings.HasPrefix(lower, "o4"):
			add("openai")
		}
	}
	if len(families) == 0 {
		families = append(families, "unknown")
	}
	sort.Strings(families)
	return families
}
