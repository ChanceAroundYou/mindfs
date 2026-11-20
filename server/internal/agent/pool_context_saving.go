package agent

import "mindfs/server/internal/agent/fold"

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
