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

// cutAtSentence 在 limit 以内尽量停在真正的边界上；找不到就退到最近换行，
// 再退到最近空白，最后才是硬截。
//
// 「真正的边界」有两种（S3）：
//   - 全角标点（。！？；）本身即句末。中文正文不在句末加空格，要求后随空白会让
//     中文一个边界都找不到，整段退化成硬截；
//   - ASCII 标点（.!?;）必须后随空白/换行/结尾。旧实现无条件认它，于是
//     `192.168.1.15:2222`、版本号、带点的文件名全在点号处被切开，线上卡片留下
//     的就是 `192.168.1.15:222` 这种半截地址。
//
// 结构化块（列表项、加粗标签、「没做」「未验证」段）不被切一半：块从下半段
// 起头时整块放弃 —— 「没做或未验」比少一段更误导。
func cutAtSentence(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	floor := limit / 2
	for i := limit - 1; i >= floor; i-- {
		if !isSentenceEnd(runes, i) {
			continue
		}
		cut := i + 1
		if start := structuralLineStart(runes, cut); start > floor && isStructuralBlockStart(runes[start:]) {
			return strings.TrimRight(string(runes[:start]), " \t\n") + "…"
		}
		return string(runes[:cut])
	}
	for i := limit - 1; i >= floor; i-- {
		if runes[i] == '\n' {
			return strings.TrimRight(string(runes[:i]), " \t") + "…"
		}
	}
	for i := limit - 1; i >= floor; i-- {
		if unicode.IsSpace(runes[i]) {
			return strings.TrimRight(string(runes[:i]), " \t") + "…"
		}
	}
	return string(runes[:limit]) + "…"
}

// isSentenceEnd 判断 runes[i] 能否作为句末。全角标点直接算；ASCII 标点要求
// 后随空白、换行或结尾，否则只是地址/版本号/文件名里的分隔符。
func isSentenceEnd(runes []rune, i int) bool {
	switch runes[i] {
	case '。', '！', '？', '；':
		return true
	case '.', '!', '?', ';':
		return i+1 >= len(runes) || unicode.IsSpace(runes[i+1])
	}
	return false
}

// structuralLineStart 返回 cut 所在行的行首（不跳过缩进，缩进也是行内容的一部分）。
func structuralLineStart(runes []rune, cut int) int {
	start := cut
	for start > 0 && runes[start-1] != '\n' {
		start--
	}
	return start
}

// isStructuralBlockStart 判断一行（已到行首）是不是结构化块：列表项、加粗标签，
// 或「没做」「未验证」这两种固定段。
func isStructuralBlockStart(runes []rune) bool {
	for len(runes) > 0 && (runes[0] == ' ' || runes[0] == '\t') {
		runes = runes[1:]
	}
	if len(runes) == 0 {
		return false
	}
	if runes[0] == '-' || runes[0] == '*' {
		return true
	}
	line := string(runes)
	return strings.HasPrefix(line, "没做") || strings.HasPrefix(line, "未验证")
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

// harnessFailureHeadRunes 是签名匹配的范围：首行之外最多再看这么多字。
const harnessFailureHeadRunes = 200

// harnessFailureText 把已知的 harness 错误行翻成一句用户能看懂的话。返回空串
// 表示这不是 harness 错误。
//
// 只认开头（首行 / 首 200 字），而不是整段文本。旧实现是「超过 400 字就不是错误」，
// 本意是别把正常回复里恰好出现的词判成错误，但反方向的代价更大：线上实测 3 条
// 以 `Prompt is too long` 开头、后面跟了 2000–2400 字正文的回合全部被记成
// completed，任务以 success 收尾。限制范围比限制长度更贴近意图 —— 正文中段顺带
// 提到「context length」的正常长回复仍然不判红，而错误行出现在开头就一定判红。
func harnessFailureText(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	head := trimmed
	if i := strings.IndexByte(head, '\n'); i >= 0 {
		head = head[:i]
	}
	if r := []rune(head); len(r) > harnessFailureHeadRunes {
		head = string(r[:harnessFailureHeadRunes])
	}
	lower := strings.ToLower(head)
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
