package proxy

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Per-session fold ledger.
//
// Purpose: the session page shows one chip — "this conversation is sending N
// bytes instead of M". That number has to survive a proxy restart, so it
// cannot live only in memory, and it must not be reconstructed by re-parsing
// history (the whole point of the fold is to stop doing that).
//
// Shape: one JSON line per folded request, appended, never rewritten while it
// fits. Fields are sizes and counters only — never message text, tool
// arguments, paths, or the session key itself (the key is hashed into the file
// name). The ledger lives next to the archive under <config-dir>/context/ and
// has the same trust boundary as the transcript.
//
// Rotation: a session that folds every turn writes a line every turn. At
// ledgerMaxBytes the file is replaced with its own tail, so the chip keeps
// showing recent history instead of growing without bound.
const (
	ledgerDirName   = "ledger"
	ledgerMaxBytes  = 4 << 20
	ledgerKeepLines = 2000
	ledgerReadLimit = 200
)

// LedgerEntry is one folded request. saved_bytes is the difference between
// what would have been sent and what was sent, for this request only; it is a
// snapshot, not something to sum across turns of the same fold.
type LedgerEntry struct {
	TS            int64  `json:"ts"`
	Mode          string `json:"mode"`
	ReqBytes      int    `json:"req_bytes"`
	FoldedBytes   int    `json:"folded_bytes"`
	SavedBytes    int    `json:"saved_bytes"`
	Blocks        int    `json:"blocks"`
	TokensEst     int    `json:"tokens_est"`
	Skipped       int    `json:"skipped"`
	ArchiveErrors int    `json:"archive_errors"`
	FoldMS        int64  `json:"fold_ms"`
	// RewriteTokens is what this fold cost: the fold request's own
	// input_tokens, i.e. the part of it billed at 1x instead of the 0.1x cache
	// read. It is only known after the response stream ends. -1 means the turn
	// ended without a usage sample; 0 is a real full cache hit.
	RewriteTokens int `json:"rewrite_tokens"`
}

type ledgerStore struct {
	dir string
	mu  sync.Mutex
}

func newLedgerStore(dir string) *ledgerStore {
	if dir == "" {
		return nil
	}
	return &ledgerStore{dir: dir}
}

// ledgerSessionName hashes the session key. Session keys are slugs today, but
// hashing makes the file name independent of whatever the key becomes and
// removes any path-traversal surface in one step.
func ledgerSessionName(sessionKey string) string {
	sum := sha256.Sum256([]byte(sessionKey))
	return hex.EncodeToString(sum[:8]) + ".jsonl"
}

func (l *ledgerStore) path(sessionKey string) string {
	return filepath.Join(l.dir, ledgerSessionName(sessionKey))
}

// append writes one line. It never fsyncs and never returns an error to the
// caller's request path: this is a diagnostic aid, and a full disk must not
// turn into a failed request.
func (l *ledgerStore) append(sessionKey string, entry LedgerEntry) {
	if l == nil || sessionKey == "" {
		return
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	line = append(line, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return
	}
	_ = os.Chmod(l.dir, 0o700)
	path := l.path(sessionKey)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	if _, err := file.Write(line); err != nil {
		_ = file.Close()
		return
	}
	info, statErr := file.Stat()
	_ = file.Close()
	if statErr == nil && info.Size() > ledgerMaxBytes {
		l.compactLocked(path)
	}
}

// compactLocked keeps the tail of an oversized ledger. Called with l.mu held.
func (l *ledgerStore) compactLocked(path string) {
	entries, err := readLedgerFile(path, ledgerKeepLines)
	if err != nil || len(entries) == 0 {
		return
	}
	temp, err := os.CreateTemp(l.dir, ".compact-*")
	if err != nil {
		return
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	writer := bufio.NewWriter(temp)
	for _, entry := range entries {
		line, err := json.Marshal(entry)
		if err != nil {
			_ = temp.Close()
			return
		}
		if _, err := writer.Write(append(line, '\n')); err != nil {
			_ = temp.Close()
			return
		}
	}
	if err := writer.Flush(); err != nil {
		_ = temp.Close()
		return
	}
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return
	}
	if err := temp.Close(); err != nil {
		return
	}
	_ = os.Rename(tempName, path)
}

// read returns up to limit of the most recent entries, oldest first.
func (l *ledgerStore) read(sessionKey string, limit int) ([]LedgerEntry, error) {
	if l == nil || sessionKey == "" {
		return nil, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return readLedgerFile(l.path(sessionKey), limit)
}

func readLedgerFile(path string, limit int) ([]LedgerEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	// Only the tail is interesting; a compacted file is a couple of thousand
	// lines, well within one pass.
	entries := make([]LedgerEntry, 0, 64)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry LedgerEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			// A truncated final line is expected after a crash. Skip it rather
			// than failing the whole read.
			continue
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return entries, err
	}
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}

// ContextSaving is what the read-only API returns: the newest snapshot plus
// aggregate counts over the recent history.
type ContextSaving struct {
	Latest LedgerEntry `json:"latest"`
	// Requests counts folded requests in the retained history.
	Requests int `json:"requests"`
	// MaxSavedBytes is the largest single-request saving seen, which is the
	// number that answers "how much is this worth at peak".
	MaxSavedBytes int `json:"max_saved_bytes"`
	// TotalRewriteTokens sums the known rewrite samples: what folding paid
	// against what it saved. Unknown samples (-1) are excluded.
	TotalRewriteTokens int           `json:"total_rewrite_tokens"`
	Recent             []LedgerEntry `json:"recent"`
}

func summarizeLedger(entries []LedgerEntry) ContextSaving {
	summary := ContextSaving{Recent: entries}
	if len(entries) == 0 {
		return summary
	}
	summary.Latest = entries[len(entries)-1]
	summary.Requests = len(entries)
	for _, entry := range entries {
		if entry.SavedBytes > summary.MaxSavedBytes {
			summary.MaxSavedBytes = entry.SavedBytes
		}
		if entry.RewriteTokens >= 0 {
			summary.TotalRewriteTokens += entry.RewriteTokens
		}
	}
	return summary
}

// foldLedgerPending is one folded request whose ledger line is not written yet:
// the rewrite cost is only known once the response stream has ended.
type foldLedgerPending struct {
	mode           string
	reqBytes       int
	forwardedBytes int
	result         foldResult
	elapsed        time.Duration
}

// newFoldLedgerPending captures the sizes a fold event will report. Returns nil
// when no ledger is configured, so the request path pays nothing for it.
func (s *Server) newFoldLedgerPending(mode string, originalBytes, forwardedBytes int, result foldResult, elapsed time.Duration) *foldLedgerPending {
	if s.ledger == nil {
		return nil
	}
	return &foldLedgerPending{
		mode:           mode,
		reqBytes:       originalBytes,
		forwardedBytes: forwardedBytes,
		result:         result,
		elapsed:        elapsed,
	}
}

// writeFoldLedger appends the pending line with this turn's rewrite cost. It
// reads the session key from the proxy's session state rather than taking it as
// a parameter, so the fold path cannot accidentally log a key it does not own.
//
// Observe-only sessions never reach here (they never fold), which is what the
// design requires: isolated sessions must not inflate the user-facing saving.
func (s *Server) writeFoldLedger(token string, pending *foldLedgerPending, totals usageTotals) {
	if s.ledger == nil || pending == nil {
		return
	}
	s.mu.Lock()
	sessionKey := ""
	if st, ok := s.sessions[token]; ok {
		sessionKey = st.sessionKey
	}
	s.mu.Unlock()
	if sessionKey == "" {
		return
	}
	rewrite := -1
	if totals.Seen {
		rewrite = totals.InputTokens
	}
	entry := LedgerEntry{
		TS:            time.Now().Unix(),
		Mode:          pending.mode,
		ReqBytes:      pending.reqBytes,
		FoldedBytes:   pending.forwardedBytes,
		SavedBytes:    pending.reqBytes - pending.forwardedBytes,
		Blocks:        pending.result.foldedBlocks,
		TokensEst:     pending.result.foldedTokens,
		Skipped:       pending.result.skipped,
		ArchiveErrors: pending.result.archiveFailures,
		FoldMS:        pending.elapsed.Milliseconds(),
		RewriteTokens: rewrite,
	}
	s.ledger.append(sessionKey, entry)
}

// SessionContextSaving returns the ledger summary for a session. It is the
// read path behind the "context" chip, and it is safe to call for a session
// that has never folded (returns a zero summary).
func (s *Server) SessionContextSaving(sessionKey string) ContextSaving {
	if s.ledger == nil {
		return ContextSaving{}
	}
	entries, err := s.ledger.read(sessionKey, ledgerReadLimit)
	if err != nil {
		return ContextSaving{}
	}
	return summarizeLedger(entries)
}
