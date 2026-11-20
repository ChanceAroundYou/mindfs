package fold

import "time"

// Adapter is one harness's wire protocol, as the engine needs it.
//
// The engine owns every policy decision; an adapter only parses, decodes,
// encodes, and splices. That split is what lets one implementation serve
// Anthropic Messages, OpenAI Responses, and anything else that resends its
// history: the policy sees rounds and results, never a request schema.
type Adapter interface {
	// Name identifies the adapter in logs and in the engine map.
	Name() string
	// Scan parses a request body into rounds in wire order. An unknown or
	// malformed structure must return an error: the engine forwards the
	// original body rather than guess.
	Scan(body []byte) (*Conversation, error)
	// Text decodes one result's text. The engine calls it only for a candidate
	// it is about to render, so an adapter must not decode during Scan.
	Text(body []byte, r Result) (string, bool)
	// Encode renders pointer text as the exact literal Splice will write for
	// that span. The engine measures savings on these bytes rather than on the
	// decoded text, because escaping can turn a fold into an expansion.
	Encode(text string) string
	// Splice writes each replacement's literal into its span and copies every
	// other byte verbatim. An empty replacement list must return the body
	// unchanged.
	Splice(body []byte, reps []Replacement) ([]byte, error)
	// Usage extracts and normalizes a response's token accounting. The second
	// return is false when the response carries no usage block.
	Usage(resp []byte) (Usage, bool)
	// Guard decides whether a model may be rewritten by a proxy at all.
	Guard(model string, cfg Config) GuardVerdict
	// Window returns the model's context window, or 0 when unknown; the
	// engine then falls back to the configured value.
	Window(model string) int
}

// Session is the per-session state the engine reads and updates.
//
// The caller owns the lock: it fills Request.Session from its own state under
// whatever mutex protects it, calls Fold without holding it, and writes back
// the fields the engine changed.
type Session struct {
	Commits *Commits
	// PendingColdFold is the CLI-compacted signal. It is spent only when the
	// request actually had candidates, because the request right after a
	// compaction often has nothing foldable.
	PendingColdFold bool
	// CompactedAt is when that signal arrived. A signal older than the cache
	// TTL is dropped: by then the cache is cold anyway and the idle rule covers
	// it.
	CompactedAt  time.Time
	LastUsageAt  time.Time
	NoReadStreak int
	LastContext  int
	// PrefixWarned records that the "unknown model, allowing rewrite" warning
	// was already logged for this session.
	PrefixWarned bool
}

// Request is one request as the engine sees it.
type Request struct {
	Body    []byte
	Session *Session
	// Now is the clock; the engine reads it once so one decision is consistent.
	Now time.Time
	// FoldAllowed is the caller's session-level gate (for example an isolated
	// ConfigDir session, which must never be rewritten).
	FoldAllowed bool
	// LastEstimate is the previous request's pre-fold estimate, used to bound
	// this request's growth. Zero means "no previous sample".
	LastEstimate int
	// LastActual and LastForwardEst are the previous request's calibration
	// pair: the upstream-reported context size and the estimate the proxy
	// forwarded. Zero means "not measured yet".
	LastActual     int
	LastForwardEst int
}
