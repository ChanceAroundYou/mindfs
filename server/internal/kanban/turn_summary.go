package kanban

import (
	"context"
	"strings"
	"time"
	"unicode"
)

// 直连回合与没有 Run 的结算回合都需要一段「没有 Router 总结时」的兜底摘要。
// 兜底不是把 Agent 的逐字叙述塞进卡片（那正是 #100 不可读的原因），而是从
// 用户诉求、最后一段可见进展与终态拼出固定三段。
//
// 只用于成功回合：失败回合的结论就是「没跑成」，兜底写进 result_summary 会把
// 系统提示当成任务结论（见 executeDirectActivity 的失败分支）。
const (
	turnSummaryFallbackHeader  = "本轮未生成总结（Router 未在收尾回合给出）"
	turnSummaryUserLimit       = 100
	turnSummaryProgressLimit   = 160
	turnSummaryReasonLimit     = 120
	turnSummaryFallbackStatus  = "已完成，等待你审核"
	turnSummaryProgressMissing = "本轮没有可见产出"
)

// deterministicTurnSummary 生成结构化兜底摘要。它只使用任务里已有的数据，
// 不再调模型；Router 随后写出的 purpose=summary 会覆盖它。
func (s *ActivityService) deterministicTurnSummary(ctx context.Context, taskID, text, statusLine string) string {
	var b strings.Builder
	b.WriteString(turnSummaryFallbackHeader)
	if s != nil && s.activities != nil {
		if user, err := s.activities.PrecedingUserMessage(ctx, taskID, time.Now().UTC().Add(time.Hour), ""); err == nil && user != nil {
			if request := firstReadableSentence(user.Body, turnSummaryUserLimit); request != "" {
				b.WriteString("\n· 用户诉求：")
				b.WriteString(request)
			}
		}
	}
	progress := firstReadableSentence(text, turnSummaryProgressLimit)
	if progress == "" {
		progress = turnSummaryProgressMissing
	}
	b.WriteString("\n· 最后进展：")
	b.WriteString(progress)
	if line := strings.TrimSpace(statusLine); line != "" {
		b.WriteString("\n· 状态：")
		b.WriteString(truncateRunes(line, turnSummaryReasonLimit))
	}
	return truncateSummary(b.String())
}

// firstReadableSentence 从一段可能是 Markdown / 工具噪声的文本里取第一句可读
// 内容：跳过空行、代码围栏与纯符号行，折叠空白，再按句末标点截断。
func firstReadableSentence(text string, limit int) string {
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			continue
		}
		line = strings.TrimLeft(line, "#>*-+ \t")
		line = strings.Join(strings.Fields(line), " ")
		if line == "" || isSymbolOnly(line) {
			continue
		}
		return truncateRunes(cutAtSentence(line, limit), limit)
	}
	return ""
}

func isSymbolOnly(line string) bool {
	for _, r := range line {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// cutAtSentence 在 limit 以内尽量停在句末标点上；没有句末标点就硬截。
func cutAtSentence(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	for i := limit - 1; i >= limit/2; i-- {
		switch runes[i] {
		case '。', '！', '？', '；', '.', '!', '?', ';':
			return string(runes[:i+1])
		}
	}
	return string(runes[:limit]) + "…"
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

// harnessFailureText 把已知的 harness 错误行翻成一句用户能看懂的话。返回空串
// 表示这不是 harness 错误。只认短文本，避免把正常回复里恰好出现的词判成错误。
func harnessFailureText(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || len([]rune(trimmed)) > 400 {
		return ""
	}
	lower := strings.ToLower(trimmed)
	switch {
	case strings.Contains(lower, "prompt is too long"),
		strings.Contains(lower, "context length"),
		strings.Contains(lower, "context_length_exceeded"),
		strings.Contains(lower, "maximum context"):
		return "本轮没有正常返回：会话上下文已超出模型上限，请先压缩会话再重试。"
	case strings.HasPrefix(lower, "api error"), strings.HasPrefix(lower, "error:"):
		return "本轮没有正常返回：" + cutAtSentence(trimmed, turnSummaryReasonLimit)
	}
	return ""
}

// directOutputFailure 判定一次「没有报错的」直连输出其实是不是失败。返回空串
// 表示输出可用。
func directOutputFailure(text string) string {
	if strings.TrimSpace(text) == "" {
		return "本轮没有产生任何可见输出（harness 未返回内容），已记为失败。"
	}
	return harnessFailureText(text)
}

// readableDirectFailure 把失败原因翻成用户能读的一句话。原始错误行只作最后
// 兜底，并且要截断——它可能带上整段请求体或堆栈。
func readableDirectFailure(reason string) string {
	if msg := harnessFailureText(reason); msg != "" {
		return msg
	}
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return "本轮没有产生任何可见输出（harness 未返回内容），已记为失败。"
	}
	return cutAtSentence(trimmed, 400)
}
