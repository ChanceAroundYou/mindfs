package fold

import (
	"hash/maphash"
	"sync"
)

// Rendered-pointer cache.
//
// Why this exists: a fold is recomputed from scratch on every request, because
// the CLI resends the full original history every turn (that is what makes the
// feature stateless and cache-stable). Recomputing means unescaping each
// candidate's text so the pointer can carry a byte count, a token estimate, and
// a head/tail excerpt. Measured on a 2.9 MB body with 37 eligible results:
// scanning is 2.4 ms, but decode + render + archive is 95 ms — and in a long
// session the number of eligible blocks only grows.
//
// The pointer for a block is a pure function of that block's content, so it can
// be cached by content. After the first turn every already-folded block is a
// hash plus a stat; only the round that just left the keep window is rendered.
//
// The key is a hash of the raw (still escaped) text span plus the tool name and
// input summary. Hashing the escaped form avoids the decode this cache exists
// to skip. Two different escape spellings of the same text land on different
// keys and simply render twice; that costs nothing in correctness, and the wire
// encoder is deterministic.
//
// It is deliberately not SHA-256: on this node's CPU (Celeron N5105, no SHA
// extensions) crypto/sha256 runs at ~97 MB/s, which would spend 22 ms per turn
// hashing candidate text — as expensive as the work the cache saves. maphash
// runs at ~6.8 GB/s (0.35 ms for the same payload) and is seeded per process,
// so its collisions cannot be steered by input. Two independent lanes give a
// 128-bit key; an accidental collision would hand one block another block's
// pointer, which is a silent wrong answer, so the second lane is worth the
// extra 0.35 ms.
const (
	PointerCacheLimit = 4096
)

type PointerCacheKey struct {
	a, b uint64
}

var (
	pointerKeySeedA = maphash.MakeSeed()
	pointerKeySeedB = maphash.MakeSeed()
)

type CachedPointer struct {
	Literal string
	Path    string
	Tokens  int
	Bytes   int
}

type PointerCache struct {
	mu      sync.Mutex
	entries map[PointerCacheKey]CachedPointer
}

func NewPointerCache() *PointerCache {
	return &PointerCache{entries: make(map[PointerCacheKey]CachedPointer)}
}

// PointerVariantFailed separates the card for an is_error result from the card
// for identical text that succeeded. The card text differs (it says the result
// failed), so the cache key must differ too.
const PointerVariantFailed = "is_error"

func NewPointerCacheKey(toolName, inputSummary string, textSpan []byte) PointerCacheKey {
	return NewPointerCacheKeyVariant(toolName, inputSummary, "", textSpan)
}

func NewPointerCacheKeyVariant(toolName, inputSummary, variant string, textSpan []byte) PointerCacheKey {
	sum := func(seed maphash.Seed) uint64 {
		var hash maphash.Hash
		hash.SetSeed(seed)
		_, _ = hash.WriteString(toolName)
		_ = hash.WriteByte(0)
		_, _ = hash.WriteString(inputSummary)
		_ = hash.WriteByte(0)
		_, _ = hash.WriteString(variant)
		_ = hash.WriteByte(0)
		_, _ = hash.Write(textSpan)
		return hash.Sum64()
	}
	return PointerCacheKey{a: sum(pointerKeySeedA), b: sum(pointerKeySeedB)}
}

func (c *PointerCache) Get(key PointerCacheKey) (CachedPointer, bool) {
	if c == nil {
		return CachedPointer{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	return entry, ok
}

func (c *PointerCache) Put(key PointerCacheKey, entry CachedPointer) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// A plain reset rather than an LRU: every entry is cheap to recompute, and
	// a long session that blows past the limit will have re-rendered all of its
	// still-eligible blocks within one turn anyway.
	if len(c.entries) >= PointerCacheLimit {
		c.entries = make(map[PointerCacheKey]CachedPointer)
	}
	c.entries[key] = entry
}
