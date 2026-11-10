package codex

import (
	"context"
	"encoding/json"
	"testing"

	codexsdk "github.com/fanwenlin/codex-go-sdk/codex"
)

func TestIsCodexProviderMismatch(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "provider switched",
			err:  errString(`app server error (-32600): This chat uses probe-provider, but your current provider is openai. Switch to probe-provider to continue.`),
			want: true,
		},
		{
			name: "provider no longer configured",
			err:  errString(`app server error (-32600): This chat uses model provider "llmux", which is no longer configured. Restore this provider's configuration to continue.`),
			want: true,
		},
		{
			name: "unrelated app server error",
			err:  errString(`app server error (-32600): no rollout found for thread id 01a1`),
			want: false,
		},
		{name: "nil error", err: nil, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCodexProviderMismatch(tc.err); got != tc.want {
				t.Fatalf("isCodexProviderMismatch() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCodexModelProviderFromConfig(t *testing.T) {
	cases := []struct {
		name string
		resp *codexsdk.ConfigReadResponse
		want string
	}{
		{name: "nil response", resp: nil, want: "openai"},
		{name: "provider unset", resp: &codexsdk.ConfigReadResponse{}, want: "openai"},
		{name: "blank provider", resp: configWithProvider("  "), want: "openai"},
		{name: "explicit provider", resp: configWithProvider("llmux"), want: "llmux"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := codexModelProviderFromConfig(tc.resp); got != tc.want {
				t.Fatalf("codexModelProviderFromConfig() = %q, want %q", got, tc.want)
			}
		})
	}
}

func configWithProvider(provider string) *codexsdk.ConfigReadResponse {
	resp := &codexsdk.ConfigReadResponse{}
	resp.Config.ModelProvider = &provider
	return resp
}

// 探测参数必须始终带 modelProvider：不带它时 app-server 会按 rollout 里的旧供应商
// 加载线程，后续带正确供应商的 resume 变成空操作（2026-11-11 冰晶智能）。
func TestCodexResumeProbeParamsAlwaysCarryProvider(t *testing.T) {
	params := codexResumeProbeParams("01a1bf60", " /home/nnb/projects/x ", "llmux")
	if got := params["modelProvider"]; got != "llmux" {
		t.Fatalf("probe modelProvider = %v, want llmux", got)
	}
	if got := params["threadId"]; got != "01a1bf60" {
		t.Fatalf("probe threadId = %v, want 01a1bf60", got)
	}
	if got := params["cwd"]; got != "/home/nnb/projects/x" {
		t.Fatalf("probe cwd = %v, want trimmed cwd", got)
	}
	if _, ok := codexResumeProbeParams("t", "", "")["modelProvider"]; !ok {
		t.Fatal("provider key must be present even when empty")
	}
}

func TestCodexResumeNeedsFork(t *testing.T) {
	cases := []struct {
		name     string
		result   string
		provider string
		err      error
		want     bool
	}{
		{name: "same provider", result: `{"modelProvider":"llmux"}`, provider: "llmux", want: false},
		{name: "same provider different case", result: `{"modelProvider":"LLMUX"}`, provider: "llmux", want: false},
		{name: "loaded on persisted provider", result: `{"modelProvider":"openai"}`, provider: "llmux", want: true},
		{name: "response without provider", result: `{"model":"gpt-6-sol"}`, provider: "llmux", want: false},
		{name: "empty result", result: ``, provider: "llmux", want: false},
		{name: "malformed result", result: `not-json`, provider: "llmux", want: false},
		{
			name:     "mismatch error",
			provider: "llmux",
			err:      errString(`This chat uses model provider "openai", but your current provider is llmux. Switch to openai to continue.`),
			want:     true,
		},
		{name: "unrelated error", provider: "llmux", err: errString("no rollout found"), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := codexResumeNeedsFork(json.RawMessage(tc.result), tc.provider, tc.err); got != tc.want {
				t.Fatalf("codexResumeNeedsFork() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRethreadCodexResumeWithoutClientIsNoop(t *testing.T) {
	forked, ok := rethreadCodexResumeOnProviderMismatch(context.Background(), nil, "01a1bf60", codexsdk.ThreadOptions{}, "/tmp")
	if ok || forked != "" {
		t.Fatalf("rethread without client = (%q, %v), want empty/false", forked, ok)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
