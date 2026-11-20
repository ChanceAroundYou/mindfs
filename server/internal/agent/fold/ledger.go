package fold

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
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
	// LedgerDirName is the subdirectory of the archive directory that holds
	// the per-session ledgers.
	LedgerDirName   = "ledger"
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
	// RewriteTokens is what this fold cost: the part of the fold request billed
	// at full price instead of the 0.1x cache read. That is input_tokens plus
	// cache_creation_input_tokens — the second term was missing before
	// 2026-11-20, so rows written earlier under-report and must not be summed
	// with newer ones as if they measured the same thing. It is only known
	// after the response stream ends; -1 means the turn ended without a usage
	// sample (see usage_source, which says so explicitly).
	RewriteTokens int `json:"rewrite_tokens"`
	// UsageInput / UsageCacheCreation / UsageCacheRead are the raw counters the
	// rewrite cost is derived from, kept so the number can be re-derived
	// without re-reading the response stream.
	UsageInput int `json:"usage_input"`
	// UsageOutput is the upstream output_tokens counter. It is what the
	// completion record reports as "this run's output"; without it the run
	// reads as pure input.
	UsageOutput        int `json:"usage_output"`
	UsageCacheCreation int `json:"usage_cache_creation"`
	UsageCacheRead     int `json:"usage_cache_read"`
	// UsageSource is "response" when the counters above came from the upstream
	// usage block and "missing" when the turn ended without one. It replaces
	// inferring "no sample" from a negative rewrite_tokens.
	UsageSource string `json:"usage_source"`
}

const (
	// UsageSourceResponse marks a ledger row whose counters came from the
	// upstream usage block.
	UsageSourceResponse = "response"
	// UsageSourceMissing marks a ledger row whose turn ended without a usage
	// sample; its rewrite cost is unknown, not zero.
	UsageSourceMissing = "missing"
)

// LedgerStore appends one line per folded request under dir.
type LedgerStore struct {
	dir string
	mu  sync.Mutex
}

// NewLedgerStore returns a store writing into dir, or nil when dir is empty so
// callers can treat "no archive directory" as "no ledger".
func NewLedgerStore(dir string) *LedgerStore {
	if dir == "" {
		return nil
	}
	return &LedgerStore{dir: dir}
}

func (l *LedgerStore) path(sessionKey string) string {
	return filepath.Join(l.dir, SessionFileName(sessionKey))
}

// Write appends one line. The caller's request path must not fail because a
// diagnostic aid could not be written, so the error is returned for logging
// rather than propagated into the fold. It never fsyncs.
func (l *LedgerStore) Write(sessionKey string, entry LedgerEntry) error {
	if l == nil || sessionKey == "" {
		return nil
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(l.dir, 0o700)
	path := l.path(sessionKey)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(line); err != nil {
		_ = file.Close()
		return err
	}
	info, statErr := file.Stat()
	_ = file.Close()
	if statErr == nil && info.Size() > ledgerMaxBytes {
		// Rotation is maintenance on the file that was just written; failing it
		// leaves an oversized but readable ledger, so it does not fail the write.
		l.compactLocked(path)
	}
	return nil
}

// compactLocked keeps the tail of an oversized ledger. Called with l.mu held.
func (l *LedgerStore) compactLocked(path string) {
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

// Read returns up to ledgerReadLimit of the most recent entries, oldest first.
// Read errors are folded into "nothing to show": the chip is a diagnostic.
func (l *LedgerStore) Read(sessionKey string) []LedgerEntry {
	entries, err := l.read(sessionKey, ledgerReadLimit)
	if err != nil {
		return nil
	}
	return entries
}

// read returns up to limit of the most recent entries, oldest first. limit <= 0
// means no limit.
func (l *LedgerStore) read(sessionKey string, limit int) ([]LedgerEntry, error) {
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
	TotalRewriteTokens int `json:"total_rewrite_tokens"`
	// RewriteSamples counts the known samples the total is made of, so "0 paid"
	// and "nothing measured" are distinguishable.
	RewriteSamples int `json:"rewrite_samples"`
	// UsageMissing counts folded turns whose response carried no usage block.
	UsageMissing int           `json:"usage_missing"`
	Recent       []LedgerEntry `json:"recent"`
}

// SummarizeLedger folds the retained history into the chip's numbers.
func SummarizeLedger(entries []LedgerEntry) ContextSaving {
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
		// Legacy rows (written before usage_source existed) have an empty
		// source; they are still counted when their rewrite is known.
		if entry.UsageSource == UsageSourceMissing {
			summary.UsageMissing++
			continue
		}
		if entry.RewriteTokens >= 0 {
			summary.TotalRewriteTokens += entry.RewriteTokens
			summary.RewriteSamples++
		}
	}
	return summary
}
