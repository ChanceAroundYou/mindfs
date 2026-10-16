package claude

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"

	"mindfs/server/internal/agent/types"
)

func TestClaudeModelInfoSupportsEffortWithoutModelNameWhitelist(t *testing.T) {
	want := []string{"low", "medium", "high", "xhigh", "max"}
	got := claudeModelInfo(claudeagent.ModelInfo{
		Value:       "custom-provider-model",
		DisplayName: "Custom Model",
	})
	if !got.SupportEffort {
		t.Fatal("custom model should support CLI effort selection")
	}
	if !reflect.DeepEqual(got.Efforts, want) {
		t.Fatalf("custom model efforts = %v, want %v", got.Efforts, want)
	}
}

func TestClaudeTokenUsageIncludesCacheReadAndCreationInLogicalInput(t *testing.T) {
	got := claudeTokenUsage(claudeagent.ResultMessage{
		Usage: &claudeagent.NonNullableUsage{
			InputTokens:              50,
			OutputTokens:             1_100,
			CacheReadInputTokens:     10_000,
			CacheCreationInputTokens: 2_350,
		},
	})
	if got == nil || got.InputTokens != 12_400 || got.OutputTokens != 1_100 {
		t.Fatalf("usage = %#v", got)
	}
	if got.CacheReadTokens == nil || *got.CacheReadTokens != 10_000 {
		t.Fatalf("cache read = %#v", got.CacheReadTokens)
	}
	if got.CacheWriteTokens == nil || *got.CacheWriteTokens != 2_350 {
		t.Fatalf("cache write = %#v", got.CacheWriteTokens)
	}
}

func TestAppendClaudeDeveloperInstructionsUsesCLIAppendSystemPrompt(t *testing.T) {
	options := claudeagent.DefaultOptions()
	for _, apply := range appendClaudeDeveloperInstructions(nil, "render markdown") {
		apply(&options)
	}
	if options.SystemPrompt != "" {
		t.Fatalf("custom system prompt = %q, want empty", options.SystemPrompt)
	}
	value, ok := options.ExtraArgs["append-system-prompt"]
	if !ok || value == nil || *value != "render markdown" {
		t.Fatalf("append-system-prompt extra arg = %#v", value)
	}
}

func TestClaudeListModelsResolvesOneMModelAlias(t *testing.T) {
	if got := resolveClaudeBaseAlias(strip1MSuffix("of[1m]")); got != "of" {
		t.Fatalf("base alias = %q, want of", got)
	}
	if got := resolveClaudeBaseAlias(strip1MSuffix("fable")); got != "of" {
		t.Fatalf("advertised alias = %q, want of", got)
	}
}

func TestClaudeContextWindowPrefersCurrentModelUsage(t *testing.T) {
	s := &session{model: "of[1m]"}

	s.updateContextWindow(claudeagent.ResultMessage{ModelUsage: map[string]claudeagent.ModelUsage{
		"of":     {InputTokens: 130053, OutputTokens: 1645, ContextWindow: 200000},
		"of[1m]": {InputTokens: 66991, OutputTokens: 344, ContextWindow: 1000000},
	}})

	contextWindow, err := s.ContextWindow(nil)
	if err != nil {
		t.Fatal(err)
	}
	if contextWindow.ModelContextWindow != 1000000 {
		t.Fatalf("context window = %d, want 1000000", contextWindow.ModelContextWindow)
	}
}

func TestClaudeCompactBoundaryEmitsCompactNotice(t *testing.T) {
	var got types.Event
	s := &session{sessionID: "claude-session", onUpdate: func(event types.Event) { got = event }}

	s.handleCompactBoundaryMessage(claudeagent.CompactBoundaryMessage{
		UUID:      "compact-1",
		SessionID: "claude-session",
		CompactMetadata: claudeagent.CompactMetadata{
			Trigger:   "auto",
			PreTokens: 1200,
		},
	})

	if got.Type != types.EventTypeCompact {
		t.Fatalf("event type = %q, want compact", got.Type)
	}
	notice, ok := got.Data.(types.CompactNotice)
	if !ok {
		t.Fatalf("event data = %T, want CompactNotice", got.Data)
	}
	if notice.ID != "compact-1" || notice.Status != "auto" || !strings.Contains(notice.Summary, "1200") {
		t.Fatalf("notice = %#v", notice)
	}
}

func TestClaudeAuthStatusEmitsToolUpdate(t *testing.T) {
	var got types.Event
	s := &session{sessionID: "claude-session", onUpdate: func(event types.Event) { got = event }}

	s.handleAuthStatusMessage(claudeagent.AuthStatusMessage{
		UUID:             "auth-1",
		IsAuthenticating: true,
		Output:           []string{"open browser"},
	})

	if got.Type != types.EventTypeToolUpdate {
		t.Fatalf("event type = %q, want tool update", got.Type)
	}
	toolCall, ok := got.Data.(types.ToolCall)
	if !ok {
		t.Fatalf("event data = %T, want ToolCall", got.Data)
	}
	if toolCall.RawType != "auth_status" || toolCall.Status != "running" {
		t.Fatalf("toolCall = %#v", toolCall)
	}
}

func TestClaudePlainStreamEventFallsBackToMessageChunk(t *testing.T) {
	var got types.Event
	s := &session{sessionID: "claude-session", onUpdate: func(event types.Event) { got = event }}

	s.handleStreamEvent(claudeagent.StreamEvent{Type: "stream_event", Event: "delta", Delta: "hello"})

	if got.Type != types.EventTypeMessageChunk {
		t.Fatalf("event type = %q, want message chunk", got.Type)
	}
	chunk, ok := got.Data.(types.MessageChunk)
	if !ok || chunk.Content != "hello" {
		t.Fatalf("chunk = %#v", got.Data)
	}
}

func TestClaudePartialInputJSONDeltaIsKnownNonDisplayEvent(t *testing.T) {
	raw := json.RawMessage(`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"具"}}`)

	textDelta, thinkingDelta := extractDeltas(raw)
	if textDelta != "" || thinkingDelta != "" {
		t.Fatalf("extractDeltas = %q, %q; want no display deltas", textDelta, thinkingDelta)
	}
	if !isKnownNonDisplayPartialEvent(raw) {
		t.Fatalf("input_json_delta should be recognized as non-display partial event")
	}
}

func TestClaudeThinkingTokensMessageUpdatesSessionIDOnly(t *testing.T) {
	events := make([]types.Event, 0, 1)
	s := &session{onUpdate: func(event types.Event) {
		events = append(events, event)
	}}

	msg := claudeagent.ThinkingTokensMessage{
		Type:                 "system",
		Subtype:              "thinking_tokens",
		EstimatedTokens:      43,
		EstimatedTokensDelta: 2,
		SessionID:            "claude-session",
	}
	s.updateSessionID(msg)

	if s.SessionID() != "claude-session" {
		t.Fatalf("session id = %q, want claude-session", s.SessionID())
	}
	if len(events) != 0 {
		t.Fatalf("events = %#v, want none", events)
	}
}
