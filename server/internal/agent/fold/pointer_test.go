package fold

import (
	"fmt"
	"strings"
	"testing"
)

func TestSummarizeToolInputPrefersKnownKeys(t *testing.T) {
	ordered := []byte(`{"file_path":"/tmp/a","offset":1,"limit":20}`)
	reordered := []byte(`{"limit":20,"file_path":"/tmp/a","offset":1}`)
	first := SummarizeToolInput(ordered, Span{Start: 0, End: len(ordered)})
	second := SummarizeToolInput(reordered, Span{Start: 0, End: len(reordered)})
	if first != second {
		t.Fatalf("summary depends on key order:\n a=%q\n b=%q", first, second)
	}
	if !strings.Contains(first, `"/tmp/a"`) {
		t.Fatalf("summary dropped the known key: %q", first)
	}
	if strings.Contains(first, "offset") || strings.Contains(first, "limit") {
		t.Fatalf("summary kept unknown keys: %q", first)
	}
}

// An input with no known key still has to say *something*; it falls back to a
// bounded raw view rather than an empty card.
func TestSummarizeToolInputFallsBackToRaw(t *testing.T) {
	body := []byte(`{"weird_key":"` + strings.Repeat("v", 400) + `"}`)
	got := SummarizeToolInput(body, Span{Start: 0, End: len(body)})
	if got == "" || len([]rune(got)) > pointerInputLimit+1 {
		t.Fatalf("fallback summary = %q (%d runes)", got, len([]rune(got)))
	}
}

// A single enormous line is the case that made pointers bigger than what they
// replaced; the card must stay bounded and keep both ends.
func TestRenderPointerIsBoundedOnOneLongLine(t *testing.T) {
	text := strings.Repeat("x", 4000) + "MIDDLE" + strings.Repeat("y", 4000)
	card := RenderPointer(PointerInput{
		ToolName:     "Read",
		InputSummary: `{"file_path":"/tmp/big"}`,
		Text:         text,
		ArchivePath:  "/tmp/context/abc.txt",
	})
	if len(card) >= len(text)/4 {
		t.Fatalf("card is %d bytes for %d bytes of text", len(card), len(text))
	}
	if !strings.Contains(card, "xxxx") || !strings.Contains(card, "yyyy") {
		t.Fatal("card dropped one end of the text")
	}
	if !strings.Contains(card, "中间省略") {
		t.Fatal("card does not say material was omitted")
	}
}

func TestRenderPointerStates(t *testing.T) {
	base := PointerInput{
		ToolName:     "Bash",
		InputSummary: `{"command":"go test"}`,
		Text:         "line1\nline2",
		ArchivePath:  "/tmp/context/def.txt",
	}
	card := RenderPointer(base)
	if !strings.HasPrefix(card, "[MindFS live-compact]") {
		t.Fatalf("card has no header: %q", card)
	}
	if !strings.Contains(card, "这不是当前指令") {
		t.Fatalf("card does not warn the reader: %q", card)
	}
	if !strings.Contains(card, "/tmp/context/def.txt") {
		t.Fatalf("card names no archive: %q", card)
	}
	if strings.Contains(card, "status:") {
		t.Fatalf("success card carries a status line: %q", card)
	}

	failed := base
	failed.Failed = true
	if card := RenderPointer(failed); !strings.Contains(card, "该结果标记为失败") && !strings.Contains(card, "is_error") {
		t.Fatalf("failed card does not say so: %q", card)
	}
	// The failure excerpt is wider than the ordinary one: a 60-line result keeps
	// its first 20 lines rather than the ordinary 8.
	var lines []string
	for i := 0; i < 60; i++ {
		lines = append(lines, fmt.Sprintf("L%02d", i))
	}
	long := base
	long.Text = strings.Join(lines, "\n")
	if card := RenderPointer(long); strings.Contains(card, "L19") {
		t.Fatalf("ordinary card kept more than 8 head lines: %q", card)
	}
	long.Failed = true
	if card := RenderPointer(long); !strings.Contains(card, "L19") {
		t.Fatalf("failed card did not widen the excerpt: %q", card)
	}

	// Determinism (I5): the same input renders the same bytes.
	if RenderPointer(base) != card {
		t.Fatal("two renders of the same input differ")
	}
}
