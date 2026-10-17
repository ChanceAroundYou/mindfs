package codex

import (
	"encoding/json"
	"strings"

	types "mindfs/server/internal/agent/types"

	codexsdk "github.com/fanwenlin/codex-go-sdk/codex"
	codextypes "github.com/fanwenlin/codex-go-sdk/types"
)

func mapUnknownToolItem(item *codextypes.UnknownItem, started bool) (types.ToolCall, bool) {
	if item == nil {
		return types.ToolCall{}, false
	}
	switch item.GetType() {
	case "dynamicToolCall":
		return mapDynamicToolCall(item.Raw, started)
	case "hookPrompt":
		return mapHookPrompt(item.Raw, started)
	case "imageGeneration":
		return mapImageGeneration(item.Raw, started)
	default:
		return types.ToolCall{}, false
	}
}

func mapDynamicToolCall(raw json.RawMessage, started bool) (types.ToolCall, bool) {
	var payload struct {
		ID           string          `json:"id"`
		Namespace    *string         `json:"namespace"`
		Tool         string          `json:"tool"`
		Arguments    json.RawMessage `json:"arguments"`
		Status       string          `json:"status"`
		ContentItems []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			ImageURL string `json:"imageUrl"`
		} `json:"contentItems"`
		Success    *bool `json:"success"`
		DurationMs *int  `json:"durationMs"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return types.ToolCall{}, false
	}
	meta := map[string]any{
		"rawType": "dynamicToolCall",
		"tool":    payload.Tool,
	}
	if payload.Namespace != nil && strings.TrimSpace(*payload.Namespace) != "" {
		meta["namespace"] = strings.TrimSpace(*payload.Namespace)
	}
	if len(payload.Arguments) > 0 && string(payload.Arguments) != "null" {
		meta["arguments"] = string(payload.Arguments)
	}
	if payload.Success != nil {
		meta["success"] = *payload.Success
	}
	if payload.DurationMs != nil {
		meta["durationMs"] = *payload.DurationMs
	}
	content := make([]types.ToolCallContentItem, 0, len(payload.ContentItems))
	for _, item := range payload.ContentItems {
		if strings.TrimSpace(item.Text) != "" {
			content = append(content, types.ToolCallContentItem{Type: "text", Text: item.Text})
		} else if strings.TrimSpace(item.ImageURL) != "" {
			content = append(content, types.ToolCallContentItem{Type: "text", Text: item.ImageURL})
		}
	}
	return types.ToolCall{
		CallID:  payload.ID,
		Title:   firstNonEmpty(payload.Tool, "dynamic tool"),
		Status:  normalizeStatus(payload.Status, started),
		Kind:    types.ToolKindOther,
		Content: content,
		RawType: "dynamicToolCall",
		Meta:    meta,
	}, true
}

func mapHookPrompt(raw json.RawMessage, started bool) (types.ToolCall, bool) {
	var payload struct {
		ID        string `json:"id"`
		Fragments []struct {
			Text      string `json:"text"`
			HookRunID string `json:"hookRunId"`
		} `json:"fragments"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return types.ToolCall{}, false
	}
	parts := make([]string, 0, len(payload.Fragments))
	hookRunIDs := make([]string, 0, len(payload.Fragments))
	for _, fragment := range payload.Fragments {
		if text := strings.TrimSpace(fragment.Text); text != "" {
			parts = append(parts, text)
		}
		if id := strings.TrimSpace(fragment.HookRunID); id != "" {
			hookRunIDs = append(hookRunIDs, id)
		}
	}
	meta := map[string]any{"rawType": "hookPrompt"}
	if len(hookRunIDs) > 0 {
		meta["hookRunIds"] = hookRunIDs
	}
	content := []types.ToolCallContentItem{}
	if len(parts) > 0 {
		content = append(content, types.ToolCallContentItem{Type: "text", Text: strings.Join(parts, "\n\n")})
	}
	return types.ToolCall{
		CallID:  payload.ID,
		Title:   "hook prompt",
		Status:  normalizeStatus("", started),
		Kind:    types.ToolKindOther,
		Content: content,
		RawType: "hookPrompt",
		Meta:    meta,
	}, true
}

func mapImageGeneration(raw json.RawMessage, started bool) (types.ToolCall, bool) {
	var payload struct {
		ID            string  `json:"id"`
		Status        string  `json:"status"`
		RevisedPrompt *string `json:"revisedPrompt"`
		Result        string  `json:"result"`
		SavedPath     string  `json:"savedPath"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return types.ToolCall{}, false
	}
	content := []types.ToolCallContentItem{}
	if prompt := stringPtrValue(payload.RevisedPrompt); prompt != "" {
		content = append(content, types.ToolCallContentItem{Type: "text", Text: prompt})
	}
	if strings.TrimSpace(payload.Result) != "" {
		content = append(content, types.ToolCallContentItem{Type: "text", Text: payload.Result})
	}
	if strings.TrimSpace(payload.SavedPath) != "" {
		content = append(content, types.ToolCallContentItem{Type: "text", Text: payload.SavedPath})
	}
	return types.ToolCall{
		CallID:  payload.ID,
		Title:   "image generation",
		Status:  normalizeStatus(payload.Status, started),
		Kind:    types.ToolKindOther,
		Content: content,
		RawType: "imageGeneration",
		Meta: map[string]any{
			"rawType": "imageGeneration",
		},
	}, true
}

func isToolItem(item codexsdk.ThreadItem) bool {
	switch item.(type) {
	case *codexsdk.CommandExecutionItem, *codexsdk.FileChangeItem, *codexsdk.McpToolCallItem, *codexsdk.WebSearchItem, *codexsdk.ErrorItem, *codexsdk.CollabToolCallItem:
		return true
	case *codextypes.UnknownItem:
		switch item.GetType() {
		case "dynamicToolCall", "hookPrompt", "imageGeneration":
			return true
		}
		return false
	default:
		return false
	}
}

func errorContent(err *codexsdk.McpToolCallError) []types.ToolCallContentItem {
	if err == nil || strings.TrimSpace(err.Message) == "" {
		return []types.ToolCallContentItem{}
	}
	return []types.ToolCallContentItem{{Type: "text", Text: err.Message}}
}

func collabToolContent(item *codexsdk.CollabToolCallItem) []types.ToolCallContentItem {
	if item == nil {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(item.Tool), "wait") {
		return nil
	}
	if prompt := stringPtrValue(item.Prompt); prompt != "" {
		return []types.ToolCallContentItem{{Type: "text", Text: prompt}}
	}
	return nil
}

func collabToolTitle(item *codexsdk.CollabToolCallItem) string {
	if item == nil {
		return "collab_tool"
	}
	receiver := firstNonEmpty(item.ReceiverThreadIDs...)
	tool := strings.TrimSpace(item.Tool)
	switch tool {
	case "spawnAgent":
		parts := make([]string, 0, 2)
		if model := stringPtrValue(item.Model); model != "" {
			parts = append(parts, model)
		}
		if effort := stringPtrValue(item.ReasoningEffort); effort != "" {
			parts = append(parts, effort)
		}
		settings := ""
		if len(parts) > 0 {
			settings = " (" + strings.Join(parts, " ") + ")"
		}
		prompt := truncateRunes(stringPtrValue(item.Prompt), 80)
		return strings.TrimSpace("Spawn " + receiver + settings + "  " + prompt)
	case "wait":
		return strings.TrimSpace("Waiting for " + receiver)
	default:
		return firstNonEmpty(tool, "collab_tool")
	}
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func truncateRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if limit <= 0 || len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "..."
}
