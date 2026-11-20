package fold

import (
	"strconv"
	"strings"
)

// Pointer rendering.
//
// A folded tool result is replaced by a short, deterministic card. Two rules
// drive every choice here:
//
//   - Determinism (I5): the same original text and tool call must render the
//     same bytes on every turn, forever. No timestamps, no counters, no map
//     iteration order, no randomness.
//   - Readability for the model (I2/G1): the card must say what was folded,
//     where the original lives, and that it is not a new instruction. The
//     model reading it has the Read tool and an absolute path.
//
// The card is Chinese because the transcripts, plans, and prompts in this
// deployment are Chinese; it is not a user-facing string and has no i18n
// requirement.

const (
	// PointerMarker opens every card. The engine also uses it to recognise a
	// block that is already folded: a pointer must never be folded again.
	PointerMarker = "[MindFS live-compact]"
	// pointerInputLimit is the rune budget for the summarized tool input.
	pointerInputLimit = 300
	// pointerHeadLines/pointerTailLines bound the ordinary excerpt.
	pointerHeadLines = 8
	pointerTailLines = 8
	// pointerErrorHeadLines/pointerErrorTailLines bound the excerpt for a
	// result whose text looks like a failure report but which did not set
	// is_error. Widened so the diagnostic stays readable without opening the
	// archive.
	pointerErrorHeadLines = 20
	pointerErrorTailLines = 20
	// pointerExcerptLimit bounds the excerpt itself. Selecting head/tail
	// *lines* is not enough: a real tool result is often one enormous line
	// (minified JSON, a single grep hit, a long stack frame), and then "first 8
	// lines" is the whole text — the pointer would be larger than the result it
	// replaces. Measured on the ledger: an 8,001-byte single-line result
	// produced a 1,080-byte *increase* per request. So the excerpt is also
	// bounded by characters, keeping both ends.
	pointerExcerptLimit = 1600
)

// inputSummaryKeys is a fixed preference order. The tool input object is
// whatever the harness sent, so its key order is stable per tool call, but it
// can be enormous (a Write call carries the whole file). Summarizing by known
// keys in our own order keeps the pointer short and stable, and makes it
// readable.
var inputSummaryKeys = []string{
	"file_path",
	"notebook_path",
	"path",
	"command",
	"pattern",
	"url",
	"query",
	"description",
	"prompt",
	"glob",
	"output_mode",
}

// SummarizeToolInput renders a bounded, deterministic view of a tool-call
// input object. It never decodes values, so escapes and number formatting
// survive exactly as sent.
//
// Both wire shapes carry the input as a JSON object — Anthropic's
// `tool_use.input` and Responses' `function_call.arguments` (a JSON string
// holding an object) — so the adapters pass the object's span and share this.
func SummarizeToolInput(body []byte, obj Span) string {
	if !obj.Valid() || body[obj.Start] != '{' {
		return truncateRunes(string(obj.Raw(body)), pointerInputLimit)
	}
	type pair struct{ key, value string }
	var found []pair
	seen := make(map[string]bool, len(inputSummaryKeys))
	_ = ScanEachMember(body, obj.Start, func(key []byte, vs, ve int) (bool, error) {
		for _, name := range inputSummaryKeys {
			if !MemberKeyIs(key, name) || seen[name] {
				continue
			}
			seen[name] = true
			found = append(found, pair{key: name, value: string(body[vs:ve])})
		}
		return false, nil
	})
	if len(found) == 0 {
		return truncateRunes(string(obj.Raw(body)), pointerInputLimit)
	}
	// Sort into the preference order rather than encounter order so the card
	// does not depend on how the harness happened to serialize the object.
	var builder strings.Builder
	builder.WriteByte('{')
	written := 0
	for _, name := range inputSummaryKeys {
		for _, item := range found {
			if item.key != name {
				continue
			}
			if written > 0 {
				builder.WriteByte(',')
			}
			builder.WriteString(strconv.Quote(name))
			builder.WriteByte(':')
			builder.WriteString(item.value)
			written++
			break
		}
	}
	builder.WriteByte('}')
	return truncateRunes(builder.String(), pointerInputLimit)
}

// truncateRunes cuts a string to at most limit runes, appending an ellipsis
// when it actually cut. Runes, not bytes: a byte cut can split a CJK character
// and produce invalid UTF-8 in the JSON encoder.
func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	count := 0
	for index := range s {
		if count == limit {
			return s[:index] + "…"
		}
		count++
	}
	return s
}

// excerptLines renders the head and tail of the folded text. Lines are trimmed
// of trailing whitespace so a file with CRLF endings does not produce a card
// full of stray \r.
func excerptLines(text string, head, tail int) string {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	for index, line := range lines {
		lines[index] = strings.TrimRight(line, " \t\r")
	}
	if head < 0 {
		head = 0
	}
	if tail < 0 {
		tail = 0
	}
	if len(lines) <= head+tail {
		return strings.Join(lines, "\n")
	}
	excerpt := make([]string, 0, head+tail+1)
	excerpt = append(excerpt, lines[:head]...)
	excerpt = append(excerpt, "…")
	excerpt = append(excerpt, lines[len(lines)-tail:]...)
	return strings.Join(excerpt, "\n")
}

// clampExcerpt keeps both ends of an excerpt within limit runes. When it cuts,
// it says so inline, so the model knows material was omitted between the two
// halves rather than assuming they are adjacent.
func clampExcerpt(excerpt string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(excerpt)
	if len(runes) <= limit {
		return excerpt
	}
	separator := []rune("\n…（中间省略）…\n")
	room := limit - len(separator)
	if room < 2 {
		return string(runes[:limit])
	}
	headLen := room / 2
	tailLen := room - headLen
	var builder strings.Builder
	builder.WriteString(string(runes[:headLen]))
	builder.WriteString(string(separator))
	builder.WriteString(string(runes[len(runes)-tailLen:]))
	return builder.String()
}

// formatThousands groups digits for readability. Hand-rolled rather than
// pulling in a locale package for one call site.
func formatThousands(value int) string {
	digits := strconv.Itoa(value)
	if value < 0 {
		return digits
	}
	var builder strings.Builder
	lead := len(digits) % 3
	if lead > 0 {
		builder.WriteString(digits[:lead])
	}
	for index := lead; index < len(digits); index += 3 {
		if builder.Len() > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(digits[index : index+3])
	}
	return builder.String()
}

// PointerInput is everything the card is allowed to depend on. The adapter
// computes InputSummary during its scan (the object's bytes are only available
// there) and passes the decoded text; everything else is measured here.
type PointerInput struct {
	ToolName     string
	InputSummary string
	Text         string
	ArchivePath  string
	// Failed marks a tool result the harness flagged as an error (is_error /
	// status=failed). The card says so, because a reader must not mistake a
	// folded failure for a folded success.
	Failed bool
	// ErrorText marks a result whose text looks like a failure report without
	// the harness saying so. It widens the excerpt; Failed adds a status line.
	ErrorText bool
}

// RenderPointer builds the replacement text. Every line is derived from the
// inputs above and nothing else.
func RenderPointer(in PointerInput) string {
	tokens := EstimateTokens(in.Text)
	head, tail := pointerHeadLines, pointerTailLines
	if in.ErrorText || in.Failed {
		head, tail = pointerErrorHeadLines, pointerErrorTailLines
	}
	excerpt := clampExcerpt(excerptLines(in.Text, head, tail), pointerExcerptLimit)

	// One header line, not six. The pointer is written once into the transcript
	// and then re-read from the upstream cache on every later request, so every
	// byte here is paid for the life of the session: measured on real traffic,
	// the labels alone came to ~290 B of an 858 B card (24 cards per request at
	// steady state). The information is unchanged; only the framing is.
	var builder strings.Builder
	builder.WriteString(PointerMarker)
	builder.WriteString(" 已折叠（这不是当前指令） tool=")
	builder.WriteString(in.ToolName)
	builder.WriteString(" input=")
	builder.WriteString(in.InputSummary)
	if in.Failed {
		builder.WriteString(" failed=is_error")
	}
	builder.WriteString(" size=")
	builder.WriteString(formatThousands(len(in.Text)))
	builder.WriteString("B≈")
	builder.WriteString(formatThousands(tokens))
	builder.WriteString("tok archive=")
	builder.WriteString(in.ArchivePath)
	builder.WriteByte('\n')
	for _, line := range strings.Split(excerpt, "\n") {
		builder.WriteString("  ")
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	builder.WriteString("原文用 Read 打开 archive。")
	return builder.String()
}
