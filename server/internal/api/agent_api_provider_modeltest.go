package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

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
