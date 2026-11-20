package proxy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"mindfs/server/internal/agent/fold"
	"mindfs/server/internal/agent/fold/responses"
)

// Provider routing lets a harness that reads its endpoints from its own config
// (Codex: `[model_providers.*]` in config.toml) be pointed at this proxy
// without touching that file: the pool passes the proxy address as a
// command-line override, and the proxy forwards to the real base URL recorded
// here.
//
// Route shape: /c/{provider}/v1/... — the same `/v1/...` suffix the provider
// itself would have received. A base URL that carries no version segment (the
// ChatGPT backend, `https://chatgpt.com/backend-api/codex`) keeps that shape
// too: Codex appends the API path to whatever base it is handed, so inserting
// a `/v1` the upstream does not have would 404 every request.
func (s *Server) RegisterProvider(name, realBaseURL string) {
	name = strings.TrimSpace(name)
	trimmed := strings.TrimRight(strings.TrimSpace(realBaseURL), "/")
	base := strings.TrimSuffix(trimmed, "/v1")
	if name == "" || base == "" {
		return
	}
	s.providerMu.Lock()
	if s.providers == nil {
		s.providers = make(map[string]providerTarget)
	}
	s.providers[name] = providerTarget{base: base, v1Suffix: trimmed != base}
	s.providerMu.Unlock()
}

// ProviderProxyURL is the base URL a provider should be configured with so its
// traffic reaches this proxy.
func (s *Server) ProviderProxyURL(name string) string {
	s.providerMu.Lock()
	target, found := s.providers[name]
	s.providerMu.Unlock()
	// An unregistered name keeps the historical /v1 shape; it is only used for
	// the override, and forwarding will answer 404 for it anyway.
	if found && !target.v1Suffix {
		return fmt.Sprintf("http://127.0.0.1:%d/c/%s", s.Port(), name)
	}
	return fmt.Sprintf("http://127.0.0.1:%d/c/%s/v1", s.Port(), name)
}

// providerTarget is one registered upstream. v1Suffix records whether the
// configured base URL ended in `/v1`, which decides the shape of the proxy
// address Codex is pointed at.
type providerTarget struct {
	base     string
	v1Suffix bool
}

// handleProvider forwards /c/{name}/... to the registered real base URL.
func (s *Server) handleProvider(w http.ResponseWriter, r *http.Request) {
	name, tail, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/c/"), "/")
	if !ok || name == "" || tail == "" {
		http.Error(w, "bad path", http.StatusNotFound)
		return
	}
	s.providerMu.Lock()
	target, found := s.providers[name]
	s.providerMu.Unlock()
	if !found {
		// 404, not 502: an unregistered name is a wiring mistake on our side,
		// and a bad-gateway status would send the operator looking at upstream.
		http.Error(w, "unknown provider", http.StatusNotFound)
		return
	}

	targetURL, err := url.Parse(target.base + "/" + tail)
	if err != nil {
		http.Error(w, "bad upstream url", http.StatusInternalServerError)
		return
	}
	if r.URL.RawQuery != "" {
		targetURL.RawQuery = r.URL.RawQuery
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 32*1024*1024))
	if err != nil {
		http.Error(w, "read body", http.StatusInternalServerError)
		return
	}

	// A registered thread is folded as the MindFS session that opened it; an
	// unregistered one is forwarded untouched with one log line (see
	// thread.go). The adapter is fixed: everything on this route is Responses.
	key := ""
	if threadID := providerThreadID(r); threadID != "" {
		if _, known := s.ThreadSessionKey(threadID); known {
			key = threadStateKey(threadID)
		} else {
			s.warnUnknownThreadOnce(threadID)
		}
	}
	var pending *foldLedgerPending
	if key != "" && r.Method == http.MethodPost {
		var result foldResult
		var gate liveCompactGate
		body, result, pending, gate = s.foldRequestLedgerFor(key, "responses", body)
		if gate.decision != "" {
			logLiveCompactProvider(key, gate, result)
		}
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL.String(), bytes.NewReader(body))
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

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	// Status and body pass through untouched: a provider error must stay
	// readable by the CLI instead of being re-wrapped here.
	//
	// The usage scanner only runs for a folded turn: it is the rewrite cost of
	// that turn, which the ledger pairs with the fold. A compressed body is not
	// scanned (the bytes are not readable text); the line is then written with
	// the source marked missing rather than a fabricated zero.
	var scanner *responses.UsageScanner
	reader := io.Reader(resp.Body)
	if pending != nil && resp.Header.Get("Content-Encoding") == "" {
		scanner = responses.NewUsageScanner()
		reader = io.TeeReader(resp.Body, scanner)
	}
	_, _ = io.Copy(&flushWriter{w: w}, reader)
	if pending != nil {
		usage := fold.Usage{Source: fold.UsageSourceMissing, RewriteTokens: -1}
		if scanner != nil {
			usage, _ = scanner.Totals().Usage()
		}
		s.writeFoldLedgerUsage(key, pending, usage)
	}
}
