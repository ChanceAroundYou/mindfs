// Package proxy provides a lightweight per-session HTTP proxy that trims
// post-compaction attachment bloat (restored skill bodies and plan content)
// from Claude Code requests.
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
//	  → proxy records the signal (observability only; see below)
//
//	CLI sends requests to /p/{token}/v1/messages
//	  → proxy trims restored skill bodies / plan content when the post-compact
//	    markers are present, forwards to the real ANTHROPIC_BASE_URL
//
//	All other requests: transparent pass-through, no buffering.
//
// Stripping is marker-driven and idempotent rather than gated on the signal:
// the restored attachments stay in the conversation history until the next
// compaction, so they are re-sent on every turn and must be trimmed every time.
// The signal is still accepted (the hook is installed globally) and logged, but
// it does not decide whether to strip.
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
	"time"
)

// Server is the proxy server. One instance is shared across all Claude sessions
// managed by a single Pool.
type Server struct {
	mu          sync.Mutex
	sessions    map[string]*sessionState // keyed by token
	listener    net.Listener
	srv         *http.Server
	liveCompact LiveCompactConfig

	// Emergency brake: the presence of DisableFile turns live-compact off for
	// the running process. Checked at most once per second.
	disableMu        sync.Mutex
	disableCheckedAt time.Time
	disableCache     bool

	// Observation queue. Parsing a multi-megabyte body costs hundreds of
	// milliseconds, so it must never run on the request path. See
	// live_compact_worker.go.
	workMu       sync.Mutex
	workQueues   map[string][]liveCompactJob
	workReady    chan string
	workStop     chan struct{}
	workClosed   bool
	workInFlight int
	workRetained int
	workDropped  int
	workerWG     sync.WaitGroup
}

type sessionState struct {
	realBaseURL string // e.g. "https://openwrt.xiaokubao.space/llmux/v1"
	compactions int    // PostCompact signals seen (observability only)
	requests    int    // proxied requests (first one is logged as evidence)
	sessionKey  string // MindFS session key, for the live-compact ledger
	foldAllowed bool   // false for isolated-ConfigDir (Router/workflow) sessions
	live        liveCompactState
}

// NewServer creates and starts the proxy server on a random loopback port.
func NewServer() (*Server, error) {
	return NewServerWithLiveCompact(LiveCompactConfig{})
}

// NewServerWithLiveCompact starts the proxy with the given context-management
// configuration. The configuration is node-local and defaults to off.
func NewServerWithLiveCompact(liveCompact LiveCompactConfig) (*Server, error) {
	cfg := liveCompact.Normalized()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("proxy: listen: %w", err)
	}
	s := &Server{
		sessions:    make(map[string]*sessionState),
		liveCompact: cfg,
		listener:    ln,
		workQueues:  make(map[string][]liveCompactJob),
	}
	s.initLiveCompactWorkers()
	if cfg.Mode == LiveCompactOn {
		// Phase A ships observability only. Be explicit rather than silently
		// wrapping a request differently than the operator asked for.
		log.Printf("[live-compact] mode=on is not implemented in this build; observing only")
	}
	if cfg.Observes() && s.liveCompactDisabled() {
		log.Printf("[live-compact] disabled by brake file %s", cfg.DisableFile)
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
	s.RegisterSessionWithKey(token, realBaseURL, "")
}

// RegisterSessionWithKey also records the MindFS session key so live-compact
// observations can be attributed without exposing the key in logs.
func (s *Server) RegisterSessionWithKey(token, realBaseURL, sessionKey string) {
	s.mu.Lock()
	s.sessions[token] = &sessionState{realBaseURL: realBaseURL, sessionKey: sessionKey, foldAllowed: true}
	s.mu.Unlock()
}

// RegisterSessionObserveOnly registers a session that may be measured but must
// never be folded. Isolated-ConfigDir sessions (Router, workflow recommenders,
// result transformers) inherit none of the user's tool permissions, so a
// pointer to an archive file could be unreadable for them; folding there would
// silently become lossy.
func (s *Server) RegisterSessionObserveOnly(token, realBaseURL, sessionKey string) {
	s.mu.Lock()
	s.sessions[token] = &sessionState{realBaseURL: realBaseURL, sessionKey: sessionKey, foldAllowed: false}
	s.mu.Unlock()
}

// UnregisterSession removes a session from the proxy state.
// Called by the Pool when a Claude session is closed.
func (s *Server) UnregisterSession(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

// SessionFoldAllowed reports whether the given token may be folded. Unknown
// tokens return false. Used by diagnostics and tests; folding decisions inside
// the proxy use the same session state.
func (s *Server) SessionFoldAllowed(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.sessions[token]
	return ok && st.foldAllowed
}

// Close shuts down the proxy server.
func (s *Server) Close() error {
	err := s.srv.Close()
	s.closeLiveCompactWorkers()
	return err
}

// handleCompactSignal accepts the PostCompact hook's notification.
// Body: {"token":"<token>"}
//
// Stripping no longer depends on this signal (see the package comment), but the
// hook is installed globally, so the endpoint must stay accepted and must not
// fail the hook when the session is unknown.
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
	if st, ok := s.sessions[body.Token]; ok {
		st.compactions++
		st.live.compactionEpoch++
	}
	s.mu.Unlock()
	log.Printf("[proxy] post-compact signal token=%s", ShortToken(body.Token))
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
	realBaseURL := ""
	firstRequest := false
	if ok {
		realBaseURL = st.realBaseURL
		st.requests++
		firstRequest = st.requests == 1
	}
	s.mu.Unlock()

	if !ok {
		http.Error(w, "unknown session token", http.StatusBadGateway)
		return
	}

	if firstRequest {
		// 只在首个请求打一行：这是「CLI 真的走了代理」的线上证据（曾经
		// proxy 因为没有上游地址整条链路静默不生效，日志里一行都没有）。
		log.Printf("[proxy] first request token=%s path=%s", ShortToken(token), remainder)
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

	// Trim post-compact attachments. stripCompactPayload short-circuits with a
	// substring check when no marker is present, so ordinary requests pay only
	// one scan of the body.
	if len(body) > 0 {
		if stripped := stripCompactPayload(body); len(stripped) != len(body) {
			// 只记尺寸，不记内容：这是「压缩后剥离真的生效」的唯一线上证据。
			log.Printf("[proxy] compact-strip token=%s before=%d after=%d",
				ShortToken(token), len(body), len(stripped))
			body = stripped
		}
	}

	// Phase A: observe only, off the request path. The analysis runs on a
	// worker after the response is done; the forwarded body is unchanged.
	//
	// Only the main Messages endpoint is in scope. Folding count_tokens would
	// change the context size the CLI itself sees, which is a different
	// behavior change and not approved by the design.
	//
	// The worker shares this body slice instead of copying it. That is safe
	// only because everything below reads it: stripCompactPayload rebuilds
	// from decoded maps rather than editing in place, and the forward request
	// gets bytes.NewReader. Keep it that way — an in-place rewrite here would
	// race the worker.
	var turn *liveCompactTurn
	if r.Method == http.MethodPost && isMessagesPath(remainder) {
		turn = s.observeLiveCompact(token, body)
	}

	// Forward request.
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		s.recordLiveCompactUsage(token, turn, usageTotals{})
		http.Error(w, "build request", http.StatusInternalServerError)
		return
	}
	req.Header = r.Header.Clone()
	req.Header.Del("Host")

	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		s.recordLiveCompactUsage(token, turn, usageTotals{})
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
	// SSE 必须逐块下发：CLI 的流式事件只有几十字节，攒满 net/http 的 4KB
	// 缓冲才发出去会把「逐字输出」变成「一阵一阵」的。
	//
	// usage 扫描只在未压缩响应上启用：带 Content-Encoding 的体是压缩字节，
	// 逐块解压只为观测不值得，宁可把 actual 记成未知。
	var scanner *usageScanner
	reader := io.Reader(resp.Body)
	if turn != nil && resp.Header.Get("Content-Encoding") == "" {
		scanner = newUsageScanner()
		reader = io.TeeReader(resp.Body, scanner)
	}
	_, _ = io.Copy(&flushWriter{w: w}, reader)
	if turn != nil {
		var totals usageTotals
		if scanner != nil {
			// This turn's own totals: the log line pairs them with this
			// request's estimate. A stream with no usage block leaves Seen
			// false, and the turn still closes with actual_in=-1.
			totals = scanner.Totals()
		}
		s.recordLiveCompactUsage(token, turn, totals)
	}
}

// flushWriter 在每次 Write 之后 flush，保持上游 SSE 的实时性。
// 底层 Writer 不支持 http.Flusher 时退化成普通缓冲，不影响正确性。
type flushWriter struct {
	w io.Writer
}

func (f *flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if flusher, ok := f.w.(http.Flusher); ok {
		flusher.Flush()
	}
	return n, err
}

func ShortToken(token string) string {
	if len(token) <= 8 {
		return token
	}
	return token[:8]
}
