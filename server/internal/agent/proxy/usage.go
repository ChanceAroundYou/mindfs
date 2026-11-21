package proxy

import (
	"bytes"
	"encoding/json"

	"mindfs/server/internal/agent/fold"
)

// usageScanner reads an SSE stream as a side effect of proxying it and keeps
// the last usage block it saw. It never delays or buffers the response: the
// caller wraps the upstream body in io.TeeReader, so bytes reach the client
// while this scanner observes a copy.
//
// Anthropic reports context size in `message_start` (message.usage) and the
// final output count in `message_delta` (usage). Both shapes are accepted.
// Non-streaming JSON responses are not scanned; Claude Code always streams.
type usageScanner struct {
	pending []byte
	maxLine int
	totals  usageTotals
}

type usageTotals struct {
	InputTokens              int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
	OutputTokens             int
	Seen                     bool
}

type usagePayload struct {
	InputTokens              int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	OutputTokens             int `json:"output_tokens"`
}

const defaultUsageLineLimit = 1 << 20 // 1 MiB

func newUsageScanner() *usageScanner {
	return &usageScanner{maxLine: defaultUsageLineLimit}
}

func (s *usageScanner) Write(p []byte) (int, error) {
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

func (s *usageScanner) consumeLine(line []byte) {
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
		s.merge(*event.Message.Usage)
	}
	if event.Usage != nil {
		s.merge(*event.Usage)
	}
}

// merge keeps any non-zero field. message_delta carries only output_tokens, so
// overwriting the whole struct would erase the input counts from
// message_start.
func (s *usageScanner) merge(payload usagePayload) {
	s.totals.Seen = true
	if payload.InputTokens > 0 {
		s.totals.InputTokens = payload.InputTokens
	}
	if payload.CacheCreationInputTokens > 0 {
		s.totals.CacheCreationInputTokens = payload.CacheCreationInputTokens
	}
	if payload.CacheReadInputTokens > 0 {
		s.totals.CacheReadInputTokens = payload.CacheReadInputTokens
	}
	if payload.OutputTokens > 0 {
		s.totals.OutputTokens = payload.OutputTokens
	}
}

func (s *usageScanner) Totals() usageTotals {
	return s.totals
}

// ContextTokens is what the model actually saw on the wire: fresh input plus
// both cache paths.
func (t usageTotals) ContextTokens() int {
	return t.InputTokens + t.CacheCreationInputTokens + t.CacheReadInputTokens
}

// foldUsageTotals maps the kernel's neutral accounting onto the
// Messages-shaped counters the calibration pair stores.
//
// Messages reports three disjoint counters; Responses reports one input total
// that already includes the cache. Splitting it back out (fresh = total −
// cached − cache write) keeps ContextTokens() — the number the rail and the
// cold-cache rule read — equal to the upstream's input_tokens. A missing
// sample stays missing instead of arriving as a real zero.
func foldUsageTotals(usage fold.Usage) usageTotals {
	if usage.Source != fold.UsageSourceResponse {
		return usageTotals{}
	}
	cached, written := usage.CacheRead, usage.CacheWrite
	if cached < 0 {
		cached = 0
	}
	if written < 0 {
		written = 0
	}
	fresh := usage.ContextTokens - cached - written
	if fresh < 0 {
		fresh = 0
	}
	return usageTotals{
		InputTokens:              fresh,
		CacheCreationInputTokens: written,
		CacheReadInputTokens:     cached,
		OutputTokens:             usage.Output,
		Seen:                     true,
	}
}
