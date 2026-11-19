package proxy

import "sync"

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
type foldCommits struct {
	mu   sync.Mutex
	keys map[pointerCacheKey]struct{}
}

func newFoldCommits() *foldCommits {
	return &foldCommits{keys: make(map[pointerCacheKey]struct{})}
}

// has reports whether the block identified by key was committed in this
// session. A nil set means "nothing is committed yet".
func (c *foldCommits) has(key pointerCacheKey) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.keys[key]
	return ok
}

func (c *foldCommits) add(key pointerCacheKey) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keys[key] = struct{}{}
}

// len is used by tests and diagnostics.
func (c *foldCommits) len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.keys)
}
