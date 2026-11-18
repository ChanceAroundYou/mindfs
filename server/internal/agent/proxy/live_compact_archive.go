package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Content-addressed archive for folded tool output.
//
// Layout: <config-dir>/context/<sha256(text)[:24]>.txt
//
// No session segment, deliberately. The session key changes on worktree
// repoint, fork, and idle-close-reopen; putting it in the path would make every
// existing pointer reference a different path the moment the key changes,
// which is a full prefix-cache break plus orphaned archives. Content addressing
// is also what makes folded text deduplicated for free: the same file read
// twice in two sessions is stored once.
//
// The archive lives in the same trust domain as the transcript (the account is
// a configuration profile, not a security boundary — AGENTS.md fact 4). It is
// still 0700/0600 so other local users cannot read it.
const (
	archiveDirName    = "context"
	archiveMinHash    = 24
	archiveSuffix     = ".txt"
	archiveTempPrefix = ".tmp-"
	archiveRetention  = 7 * 24 * time.Hour
	// archiveMaxBytes caps the directory. Folding stops writing new archives
	// when the cap is hit; already-folded pointers keep working because their
	// files stay until the GC removes them.
	archiveMaxBytes = int64(2 << 30)
	// archiveScanInterval is the minimum gap between GC runs. The proxy may
	// restart often; a startup scan plus one per hour is enough.
	archiveScanInterval = time.Hour
)

type archiveStore struct {
	dir        string
	retention  time.Duration
	maxBytes   int64
	sweepEvery time.Duration

	mu        sync.Mutex
	lastSweep time.Time
	// failedKeys memoizes texts that could not be written, so the same block is
	// not retried on every turn of a long session. A disk-full node would
	// otherwise retry a multi-megabyte write on every request.
	failed map[[archiveMinHash]byte]struct{}
}

func newArchiveStore(dir string, retention time.Duration, maxBytes int64) *archiveStore {
	if retention <= 0 {
		retention = archiveRetention
	}
	if maxBytes <= 0 {
		maxBytes = archiveMaxBytes
	}
	return &archiveStore{
		dir:        dir,
		retention:  retention,
		maxBytes:   maxBytes,
		sweepEvery: archiveScanInterval,
		failed:     make(map[[archiveMinHash]byte]struct{}),
	}
}

func archiveName(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:12]) + archiveSuffix
}

func (a *archiveStore) path(text string) string {
	return filepath.Join(a.dir, archiveName(text))
}

// write stores text and returns its absolute path. It is idempotent: a second
// write of the same text is a stat, not a copy.
//
// A write failure is reported to the caller so the block stays unfolded; the
// pointer would otherwise reference a file that does not exist, which is worse
// than not folding at all (the model would be told to Read a missing file).
func (a *archiveStore) write(text string) (string, error) {
	if a == nil || a.dir == "" {
		return "", fmt.Errorf("archive: no directory configured")
	}
	name := archiveName(text)
	path := filepath.Join(a.dir, name)

	a.mu.Lock()
	if a.failed != nil {
		key := sha256.Sum256([]byte(text))
		var prefix [archiveMinHash]byte
		copy(prefix[:], key[:archiveMinHash])
		if _, bad := a.failed[prefix]; bad {
			a.mu.Unlock()
			return "", fmt.Errorf("archive: write previously failed")
		}
	}
	a.mu.Unlock()

	if info, err := os.Stat(path); err == nil && info.Size() == int64(len(text)) {
		return path, nil
	}
	if err := os.MkdirAll(a.dir, 0o700); err != nil {
		a.recordFailure(text)
		return "", err
	}
	// MkdirAll leaves an existing directory's mode alone, and <config-dir> is
	// user-controlled (a permissive umask or a hand-made directory would leave
	// 0755). Tighten it; a failure here is not fatal for the write.
	_ = os.Chmod(a.dir, 0o700)
	temp, err := os.CreateTemp(a.dir, archiveTempPrefix+"*")
	if err != nil {
		a.recordFailure(text)
		return "", err
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	if _, err := io.WriteString(temp, text); err != nil {
		_ = temp.Close()
		a.recordFailure(text)
		return "", err
	}
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		a.recordFailure(text)
		return "", err
	}
	if err := temp.Close(); err != nil {
		a.recordFailure(text)
		return "", err
	}
	if err := os.Rename(tempName, path); err != nil {
		a.recordFailure(text)
		return "", err
	}
	return path, nil
}

func (a *archiveStore) recordFailure(text string) {
	sum := sha256.Sum256([]byte(text))
	var prefix [archiveMinHash]byte
	copy(prefix[:], sum[:archiveMinHash])
	a.mu.Lock()
	if a.failed == nil {
		a.failed = make(map[[archiveMinHash]byte]struct{})
	}
	a.failed[prefix] = struct{}{}
	a.mu.Unlock()
}

// sweep enforces the retention window and the byte cap. It runs at most once
// per archiveScanInterval; callers may call it on every fold without paying for
// a directory walk each time.
//
// Order of deletion: files older than the retention window first, then oldest
// first until the total fits under the cap. A file that is still pointed at by
// a live conversation is not tracked here — the pointer would then reference a
// deleted file. That trade-off is why the retention window is a full week: a
// conversation older than that has almost certainly left the CLI's context
// window already, and the pointer is a hint, not a guarantee.
func (a *archiveStore) sweep(now time.Time) {
	if a == nil || a.dir == "" {
		return
	}
	a.mu.Lock()
	if !a.lastSweep.IsZero() && now.Sub(a.lastSweep) < a.sweepEvery {
		a.mu.Unlock()
		return
	}
	a.lastSweep = now
	a.mu.Unlock()

	entries, err := os.ReadDir(a.dir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[live-compact] archive-sweep read=%v", err)
		}
		return
	}
	type candidate struct {
		path    string
		size    int64
		modTime time.Time
	}
	var files []candidate
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), archiveSuffix) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, candidate{
			path:    filepath.Join(a.dir, entry.Name()),
			size:    info.Size(),
			modTime: info.ModTime(),
		})
		total += info.Size()
	}
	removed, freed := 0, int64(0)
	drop := func(item candidate) {
		if err := os.Remove(item.path); err != nil {
			return
		}
		removed++
		freed += item.size
		total -= item.size
	}
	kept := files[:0]
	for _, item := range files {
		if now.Sub(item.modTime) > a.retention {
			drop(item)
			continue
		}
		kept = append(kept, item)
	}
	files = kept
	if total > a.maxBytes {
		sort.Slice(files, func(i, j int) bool { return files[i].modTime.Before(files[j].modTime) })
		for _, item := range files {
			if total <= a.maxBytes {
				break
			}
			drop(item)
		}
	}
	if removed > 0 {
		log.Printf("[live-compact] archive-sweep removed=%d freed_bytes=%d remaining_bytes=%d", removed, freed, total)
	}
}
