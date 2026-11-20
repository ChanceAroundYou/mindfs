package fold

// Neutral wire shapes.
//
// Everything below describes a conversation the way the fold policy needs it,
// with no reference to any harness's request format. An adapter's Scan parses
// its own wire format into these values; the engine never sees a JSON body
// structure beyond the byte spans it splices.

// Result is one paired tool result.
//
// Parts are the byte spans of the text literals that make up the result, in
// wire order and including their quotes. Folding in place replaces exactly one
// literal, so a result with any other number of parts is never a candidate
// (replacing several text blocks with one pointer would change the block
// structure the harness sent back to the model).
type Result struct {
	CallID string
	// ToolName and InputSummary come from the paired tool call. They are card
	// metadata only; nothing in the policy depends on them beyond the render
	// cache key, which must distinguish two calls with identical output.
	ToolName     string
	InputSummary string
	Parts        []Span
	// Bytes is the wire width of Parts and Tokens its byte-based estimate.
	// The adapter fills both during the scan so the planner does not have to
	// decode anything it is not about to fold.
	Bytes  int
	Tokens int
	// ErrorText marks text that looks like a failure report without the harness
	// saying so. Failed marks a result the harness flagged as an error.
	ErrorText bool
	Failed    bool
	// NonText marks a result carrying an image, document, or block type the
	// adapter does not understand. Such a result keeps its bytes verbatim.
	NonText bool
}

// Round is one tool call and every result that answered it.
//
// An open round — a call whose result has not arrived yet — is represented by
// zero Results. It is never foldable: the model is still waiting for it.
type Round struct{ Results []Result }

// Conversation is a scanned request body.
type Conversation struct {
	Model  string
	Rounds []Round
	// Estimated is the adapter's pre-fold size estimate for the whole body.
	Estimated int
	// Refuse, when non-empty, is the adapter's judgement that this body must
	// not be rewritten at all — for example a Responses request that stores
	// state server-side, where a client-side rewrite would fork the stored
	// conversation. The engine turns it into Verdict{Block: true}.
	Refuse string
}

// Verdict is the engine's decision for one request, for the log and the caller.
type Verdict struct {
	Block  bool
	Reason string
}

// Replacement is one splice: the adapter writes Literal in place of Span and
// leaves every other byte untouched.
//
// Literal is already encoded for the wire (see Adapter.Encode): the engine
// measures savings in wire bytes, because JSON escaping can turn a pointer
// that is shorter as text into one that is longer as a literal.
type Replacement struct {
	Span    Span
	Literal string
}

// Usage is one response's token accounting, normalized across harnesses.
type Usage struct {
	// ContextTokens is the full context the upstream billed for this request:
	// new input plus cache creation plus cache read.
	ContextTokens int
	CacheRead     int
	CacheWrite    int
	Output        int
	// RewriteTokens is the part of the context billed at full price because a
	// fold moved the cache boundary.
	RewriteTokens int
	// Source is Response or Missing; a missing block must not be silently read
	// as zero.
	Source string
}

// GuardVerdict is the adapter's answer to "may this model be rewritten at all".
type GuardVerdict struct {
	Block  bool
	Reason string
	// UnknownModel reports a model id the adapter cannot classify. The guard
	// does not block on it — an alias has to keep folding — but the caller logs
	// one line per session so an operator can register the alias in
	// prefix_unsafe_models when it turns out to map to a guarded model.
	UnknownModel bool
}
