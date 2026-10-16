package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type agentAPIProviderProbeResult struct {
	Protocols     []string
	Models        []string
	ModelFamilies []string
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
