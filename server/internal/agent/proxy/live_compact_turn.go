package proxy

import "sync"

// A live-compact turn is one observed POST /v1/messages request. Two things
// arrive at different times and both are needed for a meaningful log line:
//
//   - the analysis result, produced by an observation worker;
//   - the upstream usage totals, only known after the response stream ends.
//
// Before this type the analysis read the session's last recorded usage, which
// on real traffic is the *previous* turn's number: an analysis finishes in
// milliseconds while a streamed turn takes seconds, so the worker for turn N
// almost always ran before turn N's usage was recorded. Phase A exists to
// compare the local estimate against the upstream's own count for the same
// request, so an off-by-one here silently invalidated the whole measurement.
//
// Whichever half finishes last emits the line; the other only stores its half.
type liveCompactTurn struct {
	mu sync.Mutex

	analysis    liveCompactObservation
	hasAnalysis bool

	totals   usageTotals
	hasUsage bool

	emitted bool
}

// completeAnalysis records the analysis half and reports whether this call
// should emit (the response half already arrived).
func (t *liveCompactTurn) completeAnalysis(observation liveCompactObservation) (liveCompactObservation, usageTotals, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.analysis = observation
	t.hasAnalysis = true
	return t.emitIfReady()
}

// completeTurn records the response half and reports whether this call should
// emit (the analysis half already arrived).
//
// Totals with Seen=false mean "the turn is over and we do not know its count":
// a compressed stream, a non-streaming error response, or a failed upstream
// call. The line is still emitted with actual_in=-1 rather than withheld —
// dropping it would put a hole in the per-turn series, and the local estimate
// is the primary metric anyway.
func (t *liveCompactTurn) completeTurn(totals usageTotals) (liveCompactObservation, usageTotals, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.totals = totals
	t.hasUsage = true
	return t.emitIfReady()
}

func (t *liveCompactTurn) emitIfReady() (liveCompactObservation, usageTotals, bool) {
	if !t.hasAnalysis || !t.hasUsage || t.emitted {
		return liveCompactObservation{}, usageTotals{}, false
	}
	t.emitted = true
	return t.analysis, t.totals, true
}
