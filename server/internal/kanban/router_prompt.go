package kanban

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"strings"
)

//go:embed router_prompt.md
var routerPromptText string

// RouterPromptHash 取提示词内容哈希的前 12 位。Router 会话记录 prompt_hash，
// 内容变化时先关闭旧 runtime 再 resume —— 追加式 system prompt 不会自动替换，
// 不重建会让旧会话继续用旧规则。
func RouterPromptHash() string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(routerPromptText)))
	return hex.EncodeToString(sum[:6])
}

// RouterDeveloperInstructions 是 Router 每一轮都注入的开发者指令。
// 必须是纯文本：final 文本作为 append-system-prompt 传给 CLI。
func RouterDeveloperInstructions() string {
	return strings.TrimSpace(routerPromptText)
}
