package anthropic

import (
	"bytes"
	"encoding/json"

	"mindfs/server/internal/agent/fold"
)

// Token accounting for a Messages request.
//
// Anthropic reports context size in `message_start` (message.usage) and the
// final output count in `message_delta` (usage). Claude Code always streams, so
// the scanner reads SSE as a side effect of proxying: the caller wraps the
// upstream body in io.TeeReader, and bytes reach the client while this observes
// a copy. Non-streaming JSON bodies are parsed whole by Adapter.Usage.

// UsageTotals is one response's raw counters. Fields are merged, never
// replaced wholesale: message_delta carries only output_tokens.
type UsageTotals struct {
	InputTokens              int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
	OutputTokens             int
	Seen                     bool
}

// ContextTokens is what the model actually saw on the wire: fresh input plus
// both cache paths.
func (t UsageTotals) ContextTokens() int {
	return t.InputTokens + t.CacheCreationInputTokens + t.CacheReadInputTokens
}

// FoldUsage normalizes the counters for the fold ledger. RewriteTokens is what
// a fold costs at full price: the new input plus everything written to cache,
// because moving the fold boundary invalidates the cache from there on.
func (t UsageTotals) FoldUsage() fold.Usage {
	return fold.Usage{
		ContextTokens: t.ContextTokens(),
		CacheRead:     t.CacheReadInputTokens,
		CacheWrite:    t.CacheCreationInputTokens,
		Output:        t.OutputTokens,
		RewriteTokens: t.InputTokens + t.CacheCreationInputTokens,
		Source:        fold.UsageSourceResponse,
	}
}

type usagePayload struct {
	InputTokens              int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	OutputTokens             int `json:"output_tokens"`
}

const defaultUsageLineLimit = 1 << 20 // 1 MiB

// UsageScanner reads an SSE stream and keeps the last usage block it saw. It
// never delays or buffers the response.
type UsageScanner struct {
	pending []byte
	maxLine int
	totals  UsageTotals
}

// NewUsageScanner returns a scanner ready for Write.
func NewUsageScanner() *UsageScanner {
	return &UsageScanner{maxLine: defaultUsageLineLimit}
}

func (s *UsageScanner) Write(p []byte) (int, error) {
	s.pending = append(s.pending, p...)
	for {
		index := bytes.IndexByte(s.pending, '\n')
		if index < 0 {
			break
		}
		s.consumeLine(s.pending[:index])
		s.pending = s.pending[index+1:]
	}
	// A single SSE line larger than the cap is not a usage event we care
	// about. Drop it rather than growing the buffer without bound.
	if len(s.pending) > s.maxLine {
		s.pending = nil
	}
	return len(p), nil
}

func (s *UsageScanner) consumeLine(line []byte) {
	line = bytes.TrimRight(line, "\r")
	if !bytes.HasPrefix(line, []byte("data:")) {
		return
	}
	data := bytes.TrimSpace(line[len("data:"):])
	if len(data) == 0 || !bytes.Contains(data, []byte(`"usage"`)) {
		return
	}
	var event struct {
		Usage   *usagePayload `json:"usage"`
		Message *struct {
			Usage *usagePayload `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return
	}
	if event.Message != nil && event.Message.Usage != nil {
		s.totals.merge(*event.Message.Usage)
	}
	if event.Usage != nil {
		s.totals.merge(*event.Usage)
	}
}

func (t *UsageTotals) merge(payload usagePayload) {
	t.Seen = true
	if payload.InputTokens > 0 {
		t.InputTokens = payload.InputTokens
	}
	if payload.CacheCreationInputTokens > 0 {
		t.CacheCreationInputTokens = payload.CacheCreationInputTokens
	}
	if payload.CacheReadInputTokens > 0 {
		t.CacheReadInputTokens = payload.CacheReadInputTokens
	}
	if payload.OutputTokens > 0 {
		t.OutputTokens = payload.OutputTokens
	}
}

// Totals returns what the stream has reported so far.
func (s *UsageScanner) Totals() UsageTotals { return s.totals }

// Usage reads a complete response body: a plain JSON object carrying `usage`,
// or an SSE stream replayed in one piece.
func (Adapter) Usage(resp []byte) (fold.Usage, bool) {
	trimmed := bytes.TrimSpace(resp)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var payload struct {
			Usage *usagePayload `json:"usage"`
		}
		if err := json.Unmarshal(trimmed, &payload); err == nil && payload.Usage != nil {
			totals := UsageTotals{Seen: true}
			totals.merge(*payload.Usage)
			return totals.FoldUsage(), true
		}
	}
	scanner := NewUsageScanner()
	_, _ = scanner.Write(resp)
	totals := scanner.Totals()
	if !totals.Seen {
		return fold.Usage{}, false
	}
	return totals.FoldUsage(), true
}
