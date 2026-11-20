package fold

import (
	"sync"
	"time"
)

// Per-session record of blocks that have already been folded.
//
// Why this is separate from pointerCache: the CLI resends the original
// transcript on every turn, so a fold is recomputed from scratch each request.
// Without a commitment record, "do not fold this turn" would mean "unfold
// everything that was folded before" — which both restores the context we just
// paid to remove and changes bytes the upstream has already cached. The
// pointerCache cannot serve as that record: it resets wholesale at 4096 entries
// (live_compact_pointer_cache.go), and losing it mid-session would silently
// unfold the whole prefix.
//
// So: pointerCache answers "how do I render this block" (cheap to recompute),
// and this set answers "have we committed to folding it" (never evicted).
type Commits struct {
	mu   sync.Mutex
	keys map[PointerCacheKey]struct{}
	// names holds the archive file name of every committed block. The key is a
	// per-process maphash and cannot name a file; the sweep needs the name to
	// keep a referenced archive alive.
	names map[string]struct{}
	// restored holds archive names read back from this session's commit log
	// that have not yet been matched to a candidate in the current process.
	// Matching needs the decoded text, so it happens during the first fold that
	// renders those candidates; whatever is still unmatched afterwards belongs
	// to a transcript this session no longer has and is dropped.
	restored map[string]struct{}
	// log persists new commitments, so a restart can restore them. Nil means
	// persistence is off (shadow mode, no archive directory, no ledger).
	log *CommitLog
}

func NewCommits() *Commits {
	return &Commits{
		keys:     make(map[PointerCacheKey]struct{}),
		names:    make(map[string]struct{}),
		restored: make(map[string]struct{}),
	}
}

// NewCommitsFor restores the session's persisted commitments. The records
// are only adopted once a candidate's archive name matches (see adopt), because
// the transcript may have been forked or truncated since.
func NewCommitsFor(log *CommitLog) *Commits {
	commits := NewCommits()
	if log == nil {
		return commits
	}
	commits.log = log
	for _, record := range log.Load() {
		if record.Name != "" {
			commits.restored[record.Name] = struct{}{}
		}
	}
	return commits
}

// has reports whether the block identified by key was committed in this
// session. A nil set means "nothing is committed yet".
func (c *Commits) Has(key PointerCacheKey) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.keys[key]
	return ok
}

// add records a commitment without persistence. Preview (shadow) uses it: a
// rehearsal must not write a commit log, or a later mode=on turn would treat
// blocks it never folded as already folded.
func (c *Commits) Add(key PointerCacheKey) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keys[key] = struct{}{}
}

// commitAll records the blocks this request actually folded: the in-memory
// keys, the archive names for sweep protection, and one persisted record each
// for restore. The batch is written with a single append — a deep fold commits
// up to eight blocks, and one open per block would be eight small writes on the
// request path.
func (c *Commits) CommitAll(keys []PointerCacheKey, names []string, sizes []int) {
	if c == nil || len(keys) == 0 {
		return
	}
	now := time.Now().Unix()
	records := make([]CommitRecord, 0, len(keys))
	c.mu.Lock()
	for index, key := range keys {
		c.keys[key] = struct{}{}
		name := ""
		if index < len(names) {
			name = names[index]
		}
		if name != "" {
			c.names[name] = struct{}{}
		}
		size := 0
		if index < len(sizes) {
			size = sizes[index]
		}
		records = append(records, CommitRecord{TS: now, Name: name, Bytes: size})
	}
	log := c.log
	c.mu.Unlock()
	if log != nil {
		log.Append(records)
	}
}

// adopt promotes a restored commitment back to a live one when the candidate's
// archive name matches it. Returns false for a name that was never restored.
func (c *Commits) Adopt(key PointerCacheKey, name string) bool {
	if c == nil || name == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.restored[name]; !ok {
		return false
	}
	delete(c.restored, name)
	c.keys[key] = struct{}{}
	c.names[name] = struct{}{}
	return true
}

// restoredCount reports how many persisted commitments still await a match.
func (c *Commits) RestoredCount() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.restored)
}

// dropUnrestored discards persisted commitments this process could not match.
// A committed block is always an eligible candidate on every later request (the
// transcript only grows, and the keep window only covers newer rounds), so an
// unmatched name means the block is gone from this session's history.
func (c *Commits) DropUnrestored() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.restored = make(map[string]struct{})
}

// ProtectedNames returns every archive this session still points at: live
// commitments and not-yet-matched restored ones.
func (c *Commits) ProtectedNames() []string {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.names)+len(c.restored))
	for name := range c.names {
		out = append(out, name)
	}
	for name := range c.restored {
		out = append(out, name)
	}
	return out
}

// len is used by tests and diagnostics.
func (c *Commits) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.keys)
}
