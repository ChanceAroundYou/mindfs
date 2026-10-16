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
	"strings"
)

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
