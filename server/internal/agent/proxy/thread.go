package proxy

import (
	"log"
	"net/http"
	"strings"
	"time"
)

// Codex identifies its conversation by a thread id, sent on every request as
// `session-id` (the same value also appears as `thread-id` and
// `x-client-request-id`; confirmed against real captures). The pool registers
// thread → MindFS session key when the session opens, which is what lets a
// /c/{provider}/ request be attributed to a session and folded as one.
//
// The thread's state lives in the same `sessions` map as Claude's tokens, under
// a prefixed key: every helper that reads "the state for this key" then works
// for both transports unchanged, and a thread id can never collide with a
// generated token.
const threadKeyPrefix = "thread:"

func threadStateKey(threadID string) string { return threadKeyPrefix + threadID }

// RegisterThread makes a Codex thread foldable as the given MindFS session.
// The thread id is only known once the first turn starts (`thread/started`),
// so the codex adapter reports it through OpenOptions.OnThreadStarted and the
// pool registers it here; a resumed session already has the id at open time.
// Registering the same thread again (a resumed session) replaces the mapping,
// keeping the session key current.
func (s *Server) RegisterThread(threadID, sessionKey string) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	s.mu.Lock()
	s.sessions[threadStateKey(threadID)] = &sessionState{
		harness:     harnessCodex,
		sessionKey:  sessionKey,
		foldAllowed: true,
	}
	s.mu.Unlock()
}

// SignalCompact marks the thread's next fold decision as cache-cold. Codex
// compacts its own context and then resends the whole transcript, so the
// upstream prefix cache is gone and a fold costs nothing extra — the one
// moment worth folding below the rail. The flag is spent by the next decision
// that has something to fold (see live_compactState.pendingColdFold).
//
// An unknown thread is not an error: the compaction can be reported before the
// proxy has seen a request for it, and there would be nowhere to put the flag.
func (s *Server) SignalCompact(threadID string) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	s.mu.Lock()
	if st, ok := s.sessions[threadStateKey(threadID)]; ok {
		st.live.pendingColdFold = true
		st.live.pendingColdFoldAt = time.Now()
	}
	s.mu.Unlock()
}

// UnregisterThread drops the thread's state when its session closes. The
// commitment set goes with it: a new session re-folds from its own transcript.
func (s *Server) UnregisterThread(threadID string) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	s.mu.Lock()
	delete(s.sessions, threadStateKey(threadID))
	s.mu.Unlock()
}

// ThreadSessionKey reports the MindFS session a thread is registered as, for
// diagnostics and tests.
func (s *Server) ThreadSessionKey(threadID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.sessions[threadStateKey(threadID)]
	if !ok {
		return "", false
	}
	return st.sessionKey, true
}

// providerThreadID picks the header Codex uses to identify the thread. All
// three carried the same value in the captures; `session-id` is the documented
// one, the rest are fallbacks in case it is renamed.
func providerThreadID(r *http.Request) string {
	for _, name := range []string{"session-id", "thread-id", "x-client-request-id"} {
		if value := strings.TrimSpace(r.Header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

// warnUnknownThreadOnce records an unregistered thread. Such a request is
// forwarded byte-identically: without a session key there is nowhere to put a
// ledger line, and folding into an anonymous session would hide the savings
// from the chip it belongs to.
func (s *Server) warnUnknownThreadOnce(threadID string) {
	s.mu.Lock()
	if s.unknownThreads == nil {
		s.unknownThreads = make(map[string]struct{})
	}
	_, seen := s.unknownThreads[threadID]
	s.unknownThreads[threadID] = struct{}{}
	s.mu.Unlock()
	if !seen {
		log.Printf("[proxy] unknown codex thread %s: forwarded unobserved", ShortToken(threadID))
	}
}
