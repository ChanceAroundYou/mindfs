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
// itself would have received.
func (s *Server) RegisterProvider(name, realBaseURL string) {
	name = strings.TrimSpace(name)
	base := normalizeProviderBase(realBaseURL)
	if name == "" || base == "" {
		return
	}
	s.providerMu.Lock()
	if s.providers == nil {
		s.providers = make(map[string]string)
	}
	s.providers[name] = base
	s.providerMu.Unlock()
}

// ProviderProxyURL is the base URL a provider should be configured with so its
// traffic reaches this proxy.
func (s *Server) ProviderProxyURL(name string) string {
	return fmt.Sprintf("http://127.0.0.1:%d/c/%s/v1", s.Port(), name)
}

// normalizeProviderBase drops a trailing `/v1`: the route carries the full
// `/v1/...` path, so keeping it would duplicate the segment.
func normalizeProviderBase(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	return strings.TrimSuffix(base, "/v1")
}

// handleProvider forwards /c/{name}/... to the registered real base URL.
func (s *Server) handleProvider(w http.ResponseWriter, r *http.Request) {
	name, tail, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/c/"), "/")
	if !ok || name == "" || tail == "" {
		http.Error(w, "bad path", http.StatusNotFound)
		return
	}
	s.providerMu.Lock()
	base, found := s.providers[name]
	s.providerMu.Unlock()
	if !found {
		// 404, not 502: an unregistered name is a wiring mistake on our side,
		// and a bad-gateway status would send the operator looking at upstream.
		http.Error(w, "unknown provider", http.StatusNotFound)
		return
	}

	target, err := url.Parse(base + "/" + tail)
	if err != nil {
		http.Error(w, "bad upstream url", http.StatusInternalServerError)
		return
	}
	if r.URL.RawQuery != "" {
		target.RawQuery = r.URL.RawQuery
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
		body, _, pending, _ = s.foldRequestLedgerFor(key, "responses", body)
	}

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
