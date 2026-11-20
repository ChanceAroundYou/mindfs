package codex

import "testing"

// A new thread has no id at open time — the app server assigns it during the
// first turn. The pool cannot bind that thread to a MindFS session (and so the
// proxy cannot fold it) until the adapter reports it, and it must report it
// exactly once per id rather than on every event that carries the same id.
func TestSetThreadIDReportsStartOncePerThread(t *testing.T) {
	var seen []string
	s := &session{onThreadStart: func(id string) { seen = append(seen, id) }}

	if got := s.SessionID(); got != "" {
		t.Fatalf("fresh session has id %q", got)
	}
	s.setThreadID("thread-1")
	s.setThreadID("thread-1")
	if len(seen) != 1 || seen[0] != "thread-1" {
		t.Fatalf("callbacks = %v, want one thread-1", seen)
	}
	if got := s.SessionID(); got != "thread-1" {
		t.Fatalf("session id = %q", got)
	}

	// A resumed thread reuses the same session and reports its id once more:
	// the pool re-registers, and the proxy keeps the mapping current.
	s.setThreadID("thread-2")
	if len(seen) != 2 || seen[1] != "thread-2" {
		t.Fatalf("callbacks = %v, want thread-2 appended", seen)
	}
}

// No callback, no crash: the adapter is used without the proxy in tests and in
// configurations where live compact is off.
func TestSetThreadIDWithoutCallback(t *testing.T) {
	s := &session{}
	s.setThreadID("thread-1")
	if got := s.SessionID(); got != "thread-1" {
		t.Fatalf("session id = %q", got)
	}
	s.setThreadID("   ")
	if got := s.SessionID(); got != "thread-1" {
		t.Fatalf("blank id must be ignored, got %q", got)
	}
}
