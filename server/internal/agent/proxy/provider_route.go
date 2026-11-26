package proxy

import (
	"bytes"
	"fmt"
	"io"
	"log"
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
	threadID := providerThreadID(r)
	if threadID != "" {
		if _, known := s.ThreadSessionKey(threadID); known {
			key = threadStateKey(threadID)
		} else {
			s.warnUnknownThreadOnce(threadID)
		}
	}
	scanned := key != "" && r.Method == http.MethodPost
	// A thread that crossed a model provider still carries the previous
	// provider's encrypted reasoning, which the new provider cannot decrypt.
	// Drop that ciphertext before folding so the upstream never sees it. The
	// first post-switch request snapshots which values were old; later requests
	// only strip those, so the new provider's own ciphertext survives.
	if scanned && s.providerSwitchedThread(threadID) {
		allowed, snapshotted := s.providerSwitchCiphertextIDs(threadID)
		if !snapshotted {
			allowed = nil // first pass: the whole history is from the old provider
		}
		if stripped, removed, changed, found := stripEncryptedInput(body, allowed); changed {
			body = stripped
			if !snapshotted {
				s.recordProviderSwitchCiphertextIDs(threadID, found)
			}
			log.Printf("[proxy] provider-switch.strip thread=%s items=%d scoped=%v bytes=%d",
				ShortToken(threadID), removed, snapshotted, len(body))
		} else if !snapshotted {
			// Nothing to strip: initialise an empty snapshot so later passes are
			// value-scoped instead of stripping the new provider's ciphertext.
			s.recordProviderSwitchCiphertextIDs(threadID, nil)
		}
		// Item ids are provider-scoped references. Codex replays them with
		// store:false, so an id minted by the previous upstream is not
		// resolvable at the new one and the turn 404s with "Item with id …
		// not found". Codex sends the full item, so dropping the reference is
		// the stateless shape every provider accepts; see stripInputItemIDs.
		if stripped, removed, changed := stripInputItemIDs(body); changed {
			body = stripped
			log.Printf("[proxy] provider-switch.strip-ids thread=%s items=%d bytes=%d",
				ShortToken(threadID), removed, len(body))
		}
	}
	// The official Responses API rejects message items whose id does not begin
	// with `msg_`, and Codex gives the messages it creates locally (the
	// pre-sampling compact prompt) `item_<hex>` ids. Drop those ids before
	// folding; see normalizeInputMessageIDs.
	if scanned {
		if normalized, removed, changed := normalizeInputMessageIDs(body); changed {
			body = normalized
			log.Printf("[proxy] responses.id-normalize thread=%s items=%d bytes=%d",
				ShortToken(threadID), removed, len(body))
		}
	}
	// Usage scanning is part of the observation pipeline: with live compact off
	// this route stays a byte-for-byte pass-through and pays nothing.
	observed := scanned && s.harnessRuntimeFor(harnessForAdapter(adapterResponses)).cfg.Observes()
	var pending *foldLedgerPending
	var gate liveCompactGate
	var result foldResult
	if scanned {
		body, result, pending, gate = s.foldRequestLedgerFor(key, "responses", body)
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
		// A turn that never reached the upstream still leaves its fold decision
		// in the log; only the usage half is missing.
		if scanned && gate.decision != "" {
			logLiveCompactProvider(key, gate, result, fold.Usage{
				Source:        fold.UsageSourceMissing,
				RewriteTokens: -1,
				MissingReason: fold.UsageMissingUpstreamUnreachable,
			})
		}
		http.Error(w, "upstream error: "+err.Error(), http.StatusBadGateway)
		return
	}
	// Safety net for a thread that crossed providers before the marker existed:
	// retry once without the undecryptable ciphertext and remember the thread.
	if scanned && threadID != "" {
		resp = s.recoverInvalidEncryptedContent(resp, r, targetURL, threadID, body)
		resp = s.recoverUnresolvableItemID(resp, r, targetURL, threadID, body)
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
	// The usage scanner runs on every registered POST, not only on a folded
	// turn. The usage block is the only place a Codex session reports its real
	// context size (the body is largely base64 reasoning, so the byte estimator
	// reads 1.4-2.6x high), and both the rail and the cold-cache rule need it on
	// every turn — a session that spends several turns below the rail would
	// otherwise reach its first fold with no calibration sample at all. A
	// compressed body is not scanned (the bytes are not readable text) and the
	// sample stays missing rather than becoming a fabricated zero.
	var scanner *responses.UsageScanner
	reader := io.Reader(resp.Body)
	if observed && resp.Header.Get("Content-Encoding") == "" {
		scanner = responses.NewUsageScanner()
		reader = io.TeeReader(resp.Body, scanner)
	}
	_, _ = io.Copy(&flushWriter{w: w}, reader)
	// A missing sample keeps a reason: "no usage" is not the same failure as a
	// compressed body or a stream that ended early, and the log is the only
	// place that difference is visible.
	missingReason := fold.UsageMissingNotObserved
	if observed {
		missingReason = fold.UsageMissingNoUsageBlock
		if resp.Header.Get("Content-Encoding") != "" {
			missingReason = fold.UsageMissingContentEncoding
		}
	}
	usage, seen := fold.Usage{Source: fold.UsageSourceMissing, RewriteTokens: -1, MissingReason: missingReason}, false
	if scanner != nil {
		usage, seen = scanner.Totals().Usage()
		if !seen {
			// The scanner owns the "missing" shape but not why it is missing;
			// keep the reason this path observed.
			usage.MissingReason = missingReason
		}
	}
	// The ledger line belongs to a folded turn only: its bytes describe a
	// rewrite. The calibration pair belongs to the session and is updated by
	// every response that carried a usage block.
	if pending != nil {
		s.writeFoldLedgerUsage(key, pending, usage, target.base)
	}
	if seen {
		s.noteActualContext(key, foldUsageTotals(usage))
	}
	if scanned && gate.decision != "" {
		logLiveCompactProvider(key, gate, result, usage)
	}
}
