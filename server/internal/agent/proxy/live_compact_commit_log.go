package proxy

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Per-session log of committed folds, so a restart does not unfold the prefix.
//
// The in-memory commitment set is keyed by a maphash pointer key, and maphash
// seeds are per process: those keys cannot be written to disk and compared
// later. The stable identity is the archive file name — sha256 of the folded
// text, truncated, which is already content-addressed and survives restarts.
//
// Shape: <archive-dir>/commits/<sha256(session)[:8]>.jsonl, one line per
// committed block (size and content hash only, never the text, tool arguments,
// or the session key). A fold event commits a handful of blocks, so the cost
// is about 1–2 KB per event; the file is rotated to its own tail when it grows
// past commitLogMaxBytes.
const (
	commitLogDirName   = "commits"
	commitLogMaxBytes  = 1 << 20
	commitLogKeepLines = 4000
)

// foldCommitRecord is one committed fold. Name is the archive file name and is
// the only field restore matching uses; Bytes is diagnostic.
type foldCommitRecord struct {
	TS    int64  `json:"ts"`
	Name  string `json:"name"`
	Bytes int    `json:"bytes"`
}

type commitLog struct {
	dir  string
	path string
	mu   sync.Mutex
}

// newCommitLog returns nil when persistence is not possible (no archive
// directory, or no session key), which makes every caller a no-op.
func newCommitLog(archiveDir, sessionKey string) *commitLog {
	if archiveDir == "" || sessionKey == "" {
		return nil
	}
	dir := filepath.Join(archiveDir, commitLogDirName)
	return &commitLog{dir: dir, path: filepath.Join(dir, ledgerSessionName(sessionKey))}
}

// append writes the records of one fold event. Like the ledger it never
// returns an error to the request path: losing the log only costs one unfolded
// turn after the next restart.
func (l *commitLog) append(records []foldCommitRecord) {
	if l == nil || len(records) == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return
	}
	_ = os.Chmod(l.dir, 0o700)
	file, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	encoder := json.NewEncoder(file)
	for _, record := range records {
		if record.Name == "" {
			continue
		}
		if err := encoder.Encode(record); err != nil {
			break
		}
	}
	info, statErr := file.Stat()
	_ = file.Close()
	if statErr == nil && info.Size() > commitLogMaxBytes {
		l.compactLocked()
	}
}

// compactLocked keeps the tail of an oversized log. Called with l.mu held.
func (l *commitLog) compactLocked() {
	records, err := readCommitLog(l.path, commitLogKeepLines)
	if err != nil {
		return
	}
	if err := writeCommitLog(l.path, records); err != nil {
		return
	}
}

// load returns every record in the session's log. A missing log is empty.
func (l *commitLog) load() []foldCommitRecord {
	if l == nil {
		return nil
	}
	records, err := readCommitLog(l.path, 0)
	if err != nil {
		return nil
	}
	return records
}

// readCommitLog reads records, keeping only the newest `keep` (0 = all).
// Malformed lines are skipped: a torn append must not make the whole log
// unreadable.
func readCommitLog(path string, keep int) ([]foldCommitRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var records []foldCommitRecord
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record foldCommitRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return records, err
	}
	if keep > 0 && len(records) > keep {
		records = records[len(records)-keep:]
	}
	return records, nil
}

// writeCommitLog replaces the log with records, using temp+rename so a crash
// leaves either the old file or the new one, never a half-written log.
func writeCommitLog(path string, records []foldCommitRecord) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".commit-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer func() { _ = os.Remove(name) }()
	encoder := json.NewEncoder(temp)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			_ = temp.Close()
			return err
		}
	}
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// commitLogFiles lists the per-session commit logs. Used by the archive sweep
// for its protection set and to drop logs of long-idle sessions.
func (a *archiveStore) commitLogFiles() []string {
	if a == nil || a.dir == "" {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(a.dir, commitLogDirName))
	if err != nil {
		return nil
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		paths = append(paths, filepath.Join(a.dir, commitLogDirName, entry.Name()))
	}
	return paths
}

// referencedAfter collects the archive names referenced by persisted commit
// logs, ignoring records older than cutoff (their sessions are long gone and
// the sweep's own horizon is the same window).
func (a *archiveStore) referencedAfter(cutoff time.Time) map[string]struct{} {
	out := make(map[string]struct{})
	for _, path := range a.commitLogFiles() {
		records, err := readCommitLog(path, 0)
		if err != nil {
			continue
		}
		for _, record := range records {
			if record.Name == "" || record.TS < cutoff.Unix() {
				continue
			}
			out[record.Name] = struct{}{}
		}
	}
	return out
}
