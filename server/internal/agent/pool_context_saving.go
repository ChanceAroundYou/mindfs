package agent

import (
	"time"

	"mindfs/server/internal/agent/fold"
)

// SessionContextSaving returns the live-compact ledger summary for a session.
//
// The key the proxy records is the pool-level session key (agent prefix
// included), which is exactly the string callers already use with Get /
// BeginSessionUse, so no extra translation is needed here.
//
// A node with live-compact off has no proxy at all; a session that never
// folded has an empty summary. Neither is an error: the chip simply shows
// nothing.
func (p *Pool) SessionContextSaving(sessionKey string) fold.ContextSaving {
	if p == nil || p.compactProxy == nil {
		return fold.ContextSaving{}
	}
	return p.compactProxy.SessionContextSaving(sessionKey)
}

// FoldUsageRow is one ledger row reduced to what a usage sum needs. It exists
// (instead of handing out fold.LedgerEntry) so callers outside the agent
// packages do not grow a dependency on the ledger's on-disk shape.
type FoldUsageRow struct {
	At     time.Time
	Input  int
	Output int
	Cached int
	// Missing marks a row whose turn ended without an upstream usage sample
	// (or was written before usage_source existed). Its counters mean "not
	// measured", not 0, and a sum must skip it.
	Missing bool
}

// SessionFoldUsage returns the raw ledger rows for a pool-level session key.
// The completion record (Plan 2026-11-20 Task 13) needs per-row timestamps to
// cut a run window, which ContextSaving's summary no longer carries.
func (p *Pool) SessionFoldUsage(sessionKey string) []FoldUsageRow {
	if p == nil || p.compactProxy == nil {
		return nil
	}
	entries := p.compactProxy.SessionFoldUsage(sessionKey)
	if len(entries) == 0 {
		return nil
	}
	rows := make([]FoldUsageRow, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, FoldUsageRow{
			At:      time.Unix(entry.TS, 0),
			Input:   entry.UsageInput,
			Output:  entry.UsageOutput,
			Cached:  entry.UsageCacheRead,
			Missing: entry.UsageSource != fold.UsageSourceResponse,
		})
	}
	return rows
}
