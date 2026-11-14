// Package proxy provides a lightweight per-session HTTP proxy that strips
// over-injected skills and file-restore blocks from the first request after
// Claude Code performs a context compaction.
//
// Flow:
//
//	mindfs opens CC session
//	  → injects ANTHROPIC_BASE_URL=http://127.0.0.1:{port}/p/{token}
//	  → injects MINDFS_COMPACT_SIGNAL_URL=http://127.0.0.1:{port}/compact-signal
//	  → injects MINDFS_PROXY_TOKEN={token}
//
//	PostCompact hook fires (only in mindfs-managed sessions)
//	  → POST /compact-signal  body: {"token":"<token>"}
//	  → proxy marks that token's next request for stripping
//
//	CLI sends next request to /p/{token}/v1/messages
//	  → proxy strips invoked_skills beyond budget and excess file-restore blocks
//	  → forwards modified request to the real ANTHROPIC_BASE_URL
//	  → clears the strip flag
//
//	All other requests: transparent pass-through, no buffering.
package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// DefaultSkillBudgetBytes is the maximum total byte size kept for invoked_skills
// content after compaction. The CLI cap is ~100 KB (25k tokens × 4 bytes/token);
// we default to ~20 KB (~5k tokens) which is usually enough for skill names and
// entry-point hints without the full bodies.
const DefaultSkillBudgetBytes = 20 * 1024

// DefaultMaxFileRestoreBlocks is the maximum number of file-restore blocks kept
// after compaction. The CLI restores up to 5; we default to 2.
const DefaultMaxFileRestoreBlocks = 2

// Server is the proxy server.  One instance is shared across all Claude sessions
// managed by a single Pool.
type Server struct {
	mu       sync.Mutex
	sessions map[string]*sessionState // keyed by token
	listener net.Listener
	srv      *http.Server

	SkillBudgetBytes     int
	MaxFileRestoreBlocks int
}

type sessionState struct {
	realBaseURL string // e.g. "https://openwrt.xiaokubao.space/llmux/v1"
	stripNext   bool
}

// NewServer creates and starts the proxy server on a random loopback port.
func NewServer() (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("proxy: listen: %w", err)
	}
	s := &Server{
		sessions:             make(map[string]*sessionState),
		listener:             ln,
		SkillBudgetBytes:     DefaultSkillBudgetBytes,
		MaxFileRestoreBlocks: DefaultMaxFileRestoreBlocks,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/compact-signal", s.handleCompactSignal)
	mux.HandleFunc("/p/", s.handleProxied)
	s.srv = &http.Server{Handler: mux}
	go func() {
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("[proxy] serve error: %v", err)
		}
	}()
	return s, nil
}

// Port returns the local port the proxy is listening on.
func (s *Server) Port() int {
	return s.listener.Addr().(*net.TCPAddr).Port
}

// RegisterSession registers a new session token with its real upstream base URL.
// Called by the Pool when opening a Claude session.
func (s *Server) RegisterSession(token, realBaseURL string) {
	s.mu.Lock()
	s.sessions[token] = &sessionState{realBaseURL: realBaseURL}
	s.mu.Unlock()
}

// UnregisterSession removes a session from the proxy state.
// Called by the Pool when a Claude session is closed.
func (s *Server) UnregisterSession(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

// Close shuts down the proxy server.
func (s *Server) Close() error {
	return s.srv.Close()
}

// handleCompactSignal is called by the PostCompact hook to flag the next request
// for stripping.  Body: {"token":"<token>"}
func (s *Server) handleCompactSignal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 512)).Decode(&body); err != nil || body.Token == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	st, ok := s.sessions[body.Token]
	if ok {
		st.stripNext = true
	}
	s.mu.Unlock()
	if !ok {
		http.Error(w, "unknown token", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleProxied handles all requests from the CLI.
// URL pattern: /p/{token}/v1/...
func (s *Server) handleProxied(w http.ResponseWriter, r *http.Request) {
	// Extract token and remainder path from /p/{token}/...
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/p/"), "/", 2)
	if len(parts) < 2 {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	token := parts[0]
	remainder := "/" + parts[1]

	s.mu.Lock()
	st, ok := s.sessions[token]
	strip := ok && st.stripNext
	if strip {
		st.stripNext = false
	}
	realBaseURL := ""
	if ok {
		realBaseURL = st.realBaseURL
	}
	s.mu.Unlock()

	if !ok {
		http.Error(w, "unknown session token", http.StatusBadGateway)
		return
	}

	// Build target URL.
	target, err := url.Parse(strings.TrimRight(realBaseURL, "/") + remainder)
	if err != nil {
		http.Error(w, "bad upstream url", http.StatusInternalServerError)
		return
	}
	if r.URL.RawQuery != "" {
		target.RawQuery = r.URL.RawQuery
	}

	// Read request body.
	body, err := io.ReadAll(io.LimitReader(r.Body, 32*1024*1024))
	if err != nil {
		http.Error(w, "read body", http.StatusInternalServerError)
		return
	}

	// Strip post-compact bloat if flagged.
	if strip && len(body) > 0 {
		if stripped, err := stripCompactPayload(body, s.SkillBudgetBytes, s.MaxFileRestoreBlocks); err == nil {
			body = stripped
		} else {
			log.Printf("[proxy] strip error (using original): %v", err)
		}
	}

	// Forward request.
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		http.Error(w, "build request", http.StatusInternalServerError)
		return
	}
	req.Header = r.Header.Clone()
	req.Header.Del("Host")

	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		http.Error(w, "upstream error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers.
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
