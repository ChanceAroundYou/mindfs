package usecase

import (
	"path/filepath"
	"strings"

	agenttypes "mindfs/server/internal/agent/types"
)

func buildImportedSessionName(imported agenttypes.ImportedExternalSession) string {
	if title := strings.TrimSpace(imported.Title); title != "" {
		runes := []rune(title)
		if len(runes) > 80 {
			title = string(runes[:80])
		}
		return title
	}
	// Use last user message (剥离 REPLY_TIPS) 的前20，满足导入后短标题需求；无内容回退到 Imported agent
	var last string
	for i := len(imported.Exchanges) - 1; i >= 0; i-- {
		if strings.TrimSpace(imported.Exchanges[i].Role) != "user" {
			continue
		}
		c := strings.TrimSpace(imported.Exchanges[i].Content)
		if c == "" {
			continue
		}
		last = c
		break
	}
	if short := shortExternalSessionTitle(last); short != "" {
		return short
	}
	if preview := strings.TrimSpace(last); preview != "" {
		runes := []rune(preview)
		if len(runes) > 20 {
			preview = string(runes[:20])
		}
		return preview
	}
	return "Imported " + strings.TrimSpace(imported.Agent)
}

func normalizeExternalSessionPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	clean := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil && strings.TrimSpace(resolved) != "" {
		clean = resolved
	}
	if abs, err := filepath.Abs(clean); err == nil {
		clean = abs
	}
	return filepath.Clean(clean)
}

func stripReplyTipsPrefix(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "[REPLY_TIPS]") {
		if idx := strings.Index(text, "[USER_PROMPT]"); idx >= 0 {
			return strings.TrimSpace(text[idx+len("[USER_PROMPT]"):])
		}
		// No USER_PROMPT marker: fallback to tail after replyTips block
		if idx := strings.Index(text, "\n\n"); idx >= 0 {
			return strings.TrimSpace(text[idx:])
		}
	}
	return text
}

func shortExternalSessionTitle(text string) string {
	text = strings.TrimSpace(stripReplyTipsPrefix(stripExternalSessionPrefix(text)))
	if text == "" {
		return ""
	}
	// 取首行/首段，避免跨行长尾
	if idx := strings.Index(text, "\n"); idx >= 0 {
		text = strings.TrimSpace(text[:idx])
	}
	runes := []rune(text)
	if len(runes) > 20 {
		text = string(runes[:20])
	}
	return strings.TrimSpace(text)
}

func stripExternalSessionPrefix(text string) string {
	text = strings.TrimSpace(text)
	const prefix = "This session was migrated from elsewhere. Your context may lag behind this session;"
	const tail = "Only if reading fails, output a brief error and stop."
	normalized := strings.ReplaceAll(text, "\\n", "\n")
	if !strings.HasPrefix(normalized, prefix) {
		return text
	}
	idx := strings.Index(normalized, tail)
	if idx < 0 {
		return text
	}
	return strings.TrimSpace(normalized[idx+len(tail):])
}
