package notify

import (
	"regexp"
	"strings"
)

// BodyMaxRunes 是通知正文保留的最大字符数（按 rune 计）。
//
// 超出时从**尾部**截断、只留最后 N 个字符（前缀省略号），因为回复的结论通常在末尾；
// 截掉开头比截掉结尾更不容易让人误读。要调整通知长度改这一个值即可。
const BodyMaxRunes = 1000

// FallbackBodyMaxRunes 是 Web Push 载荷超过协议上限时的二次收紧值。正常情况下正文
// 已按 BodyMaxRunes 截断，只有 JSON 其它字段异常膨胀时才会走到这里。
const FallbackBodyMaxRunes = 300

// PlainText 把一段可能带 Markdown 的文本压成适合通知栏阅读的纯文本。
//
// 处理顺序：去围栏/行内代码标记（保留代码内容本身）→ 去图片/链接语法（保留文字）
// → 去标题/引用/列表/分隔线/表格分隔行标记 → 去强调标记 → 折叠所有连续空白 → 尾部截断。
// 代码块与普通正文走同一套规则，所以「代码内容再多处理一轮去格式」不需要单独分支。
func PlainText(value string, maxRunes int) string {
	text := stripMarkdown(value)
	if maxRunes > 0 {
		text = truncateRunes(text, maxRunes)
	}
	return text
}

var (
	reFenceLine  = regexp.MustCompile("^\\s*(?:```|~~~)")
	reHeading    = regexp.MustCompile(`^\s{0,3}#{1,6}\s*`)
	reBlockquote = regexp.MustCompile(`^\s{0,3}(?:>\s?)+`)
	reListMarker = regexp.MustCompile(`^\s{0,3}(?:[-*+]|\d{1,3}[.)])\s+`)
	reRuleLine   = regexp.MustCompile(`^\s{0,3}(?:[-*_])(?:\s*[-*_]){2,}\s*$`)
	// 表格分隔行（|---|:--:|）没有内容，整行丢弃；其余表格行把竖线换成空格。
	reTableSeparator = regexp.MustCompile(`^\s*\|?[\s:|-]*-[\s:|-]*\|?\s*$`)
	reTablePipe      = regexp.MustCompile(`\s*\|\s*`)
	reImage          = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	reLink           = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	reBoldStar       = regexp.MustCompile(`\*\*([^*]+?)\*\*`)
	reBoldUnderscore = regexp.MustCompile(`__([^_]+?)__`)
	reStrike         = regexp.MustCompile(`~~([^~]+?)~~`)
	reItalicStar     = regexp.MustCompile(`\*([^*\s][^*]*?)\*`)
	reWhitespace     = regexp.MustCompile(`\s+`)
)

func stripMarkdown(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")

	// 围栏行整行丢弃，围栏内的代码各保留原样；随后统一折叠空白，缩进自然消失。
	lines := strings.Split(value, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if reFenceLine.MatchString(line) {
			continue
		}
		if reRuleLine.MatchString(line) {
			continue
		}
		if strings.Contains(line, "|") && reTableSeparator.MatchString(line) {
			continue
		}
		line = reHeading.ReplaceAllString(line, "")
		line = reBlockquote.ReplaceAllString(line, "")
		line = reListMarker.ReplaceAllString(line, "")
		// 只把「首尾带竖线」的行当表格行；代码里的 `a | b`（shell 管道）保持原样。
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "|") || strings.HasSuffix(trimmed, "|") {
			line = reTablePipe.ReplaceAllString(line, " ")
		}
		kept = append(kept, line)
	}
	text := strings.Join(kept, "\n")

	// 行内代码：只去掉反引号，保留内容。
	text = strings.ReplaceAll(text, "`", "")
	text = reImage.ReplaceAllString(text, "$1")
	text = reLink.ReplaceAllString(text, "$1")
	text = reBoldStar.ReplaceAllString(text, "$1")
	text = reBoldUnderscore.ReplaceAllString(text, "$1")
	text = reStrike.ReplaceAllString(text, "$1")
	text = reItalicStar.ReplaceAllString(text, "$1")

	text = reWhitespace.ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}
