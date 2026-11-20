package responses

import (
	"bytes"
	"encoding/json"

	"mindfs/server/internal/agent/fold"
)

// Token accounting for a Responses request.
//
// Responses reports usage at the end of the turn: in the response object for a
// non-streaming call, or in the `response.completed` event of a stream. Both
// carry the same block, so one parser covers both — the caller hands Usage the
// bytes it proxied, whole.
//
// `input_tokens` is the whole context the model saw, cache included;
// `input_tokens_details.cached_tokens` is the part it read at the cache price.
// That is the opposite of Messages, where the three counters are disjoint.
type usagePayload struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	InputDetails struct {
		CachedTokens int `json:"cached_tokens"`
		// CacheWriteTokens is only sent by providers that bill cache writes
		// separately; absent means zero.
		CacheWriteTokens int `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
}

// normalize maps a Responses usage block onto the kernel's accounting.
//
// RewriteTokens is what a moved cache boundary costs at full price: the input
// the model did not read from cache. Clamped at zero so a detail block that
// lags behind input_tokens cannot make a rewrite look like a gain.
func (p usagePayload) normalize() fold.Usage {
	cached := p.InputDetails.CachedTokens
	rewrite := p.InputTokens - cached
	if rewrite < 0 {
		rewrite = 0
	}
	return fold.Usage{
		ContextTokens: p.InputTokens,
		CacheRead:     cached,
		CacheWrite:    p.InputDetails.CacheWriteTokens,
		Output:        p.OutputTokens,
		RewriteTokens: rewrite,
		Source:        fold.UsageSourceResponse,
	}
}

// usageEnvelope finds the block in either shape: at the top level (a usage
// object on its own) or inside a response object (`response.completed`, and
// the body of a non-streaming call, which is itself the response).
type usageEnvelope struct {
	Usage    *usagePayload `json:"usage"`
	Response *struct {
		Usage *usagePayload `json:"usage"`
	} `json:"response"`
}

func (e usageEnvelope) payload() *usagePayload {
	if e.Response != nil && e.Response.Usage != nil {
		return e.Response.Usage
	}
	return e.Usage
}

// maxUsageLine bounds one SSE line: a usage event is small, and a line past
// this is not one.
const maxUsageLine = 1 << 20

// UsageTotals is the counters a stream has reported so far.
type UsageTotals struct {
	payload usagePayload
	seen    bool
}

// Usage normalizes what the stream reported. A stream that ended without a
// usage event reports Source=missing rather than a zero-cost turn.
func (t UsageTotals) Usage() (fold.Usage, bool) {
	if !t.seen {
		return fold.Usage{Source: fold.UsageSourceMissing, RewriteTokens: -1}, false
	}
	return t.payload.normalize(), true
}

// UsageScanner reads an SSE stream as a side effect of proxying it: the caller
// wraps the upstream body in io.TeeReader, so bytes reach the client while this
// observes a copy. It never delays or buffers the response.
type UsageScanner struct {
	pending []byte
	maxLine int
	totals  UsageTotals
}

// NewUsageScanner returns a scanner ready for Write.
func NewUsageScanner() *UsageScanner {
	return &UsageScanner{maxLine: maxUsageLine}
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
	// A line past the cap is not a usage event; drop it instead of growing the
	// buffer without bound.
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
	var envelope usageEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return
	}
	if payload := envelope.payload(); payload != nil {
		s.totals.payload = *payload
		s.totals.seen = true
	}
}

// Totals returns what the stream has reported so far.
func (s *UsageScanner) Totals() UsageTotals { return s.totals }

// Usage reads a complete response body: a plain JSON object, or an SSE stream
// replayed in one piece (the last usage block wins). No usage block reports
// Source=missing explicitly — a turn that ended without counters must not be
// read as "saved zero".
func (Adapter) Usage(resp []byte) (fold.Usage, bool) {
	trimmed := bytes.TrimSpace(resp)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var envelope usageEnvelope
		if err := json.Unmarshal(trimmed, &envelope); err == nil {
			if payload := envelope.payload(); payload != nil {
				return payload.normalize(), true
			}
		}
	}
	scanner := NewUsageScanner()
	_, _ = scanner.Write(resp)
	return scanner.Totals().Usage()
}

// Guard never blocks. The prefix guard exists to protect preserved-thinking
// blocks that a rewrite would invalidate; Responses sends reasoning as an
// opaque `encrypted_content` item, and a fold only ever replaces tool outputs,
// so there is nothing for a guard to save.
func (Adapter) Guard(string, fold.Config) fold.GuardVerdict { return fold.GuardVerdict{} }
