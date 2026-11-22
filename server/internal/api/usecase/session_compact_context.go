package usecase

import agenttypes "mindfs/server/internal/agent/types"

// contextWindowAfterCompact 把 harness 报的 postTokens 写回本回合的上下文窗口。
//
// Claude 的 /compact 回合没有新的 message_delta，MessageDone 仍会带着压缩前的
// 旧 TotalTokens；不覆盖的话读口和 hygiene 都会认为压缩没发生。Codex 的 compact
// item 不带 postTokens（传 0），保留 token_count 事件给出的新值。
func contextWindowAfterCompact(done agenttypes.ContextWindow, postTokens int) agenttypes.ContextWindow {
	if postTokens > 0 {
		done.TotalTokens = postTokens
	}
	return done
}
