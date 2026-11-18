package proxy

import "testing"

func TestPointerCacheNilIsUsable(t *testing.T) {
	// planFold takes the cache as an optional dependency; a nil must read as a
	// permanent miss rather than a panic.
	var cache *pointerCache
	key := pointerCacheKeyFor("Read", `{"file_path":"/x"}`, []byte(`"text"`))
	if _, ok := cache.get(key); ok {
		t.Fatal("nil cache reported a hit")
	}
	cache.put(key, cachedPointer{literal: "x"}) // must not panic
}

func TestPointerCacheIsKeyedByContent(t *testing.T) {
	cache := newPointerCache()
	key := pointerCacheKeyFor("Read", `{"file_path":"/a"}`, []byte(`"same text"`))
	cache.put(key, cachedPointer{literal: "one", path: "/tmp/1.txt", tokens: 10, bytes: 100})

	if entry, ok := cache.get(pointerCacheKeyFor("Read", `{"file_path":"/a"}`, []byte(`"same text"`))); !ok || entry.literal != "one" {
		t.Fatalf("identical content did not hit: %+v %v", entry, ok)
	}
	for name, other := range map[string]struct {
		tool  string
		input string
		span  string
	}{
		"different tool":  {"Bash", `{"file_path":"/a"}`, `"same text"`},
		"different input": {"Read", `{"file_path":"/b"}`, `"same text"`},
		"different text":  {"Read", `{"file_path":"/a"}`, `"other text"`},
		"escape spelling": {"Read", `{"file_path":"/a"}`, `"same\u0020text"`},
	} {
		if _, ok := cache.get(pointerCacheKeyFor(other.tool, other.input, []byte(other.span))); ok {
			t.Fatalf("%s: distinct block hit the cached entry", name)
		}
	}
}

// A long session has an upper bound on memory: the map is dropped wholesale at
// the limit rather than growing without bound. Every entry is cheap to
// recompute, so a reset costs one extra rebuild, not correctness.
func TestPointerCacheResetsAtLimit(t *testing.T) {
	cache := newPointerCache()
	for i := 0; i < pointerCacheLimit; i++ {
		cache.put(pointerCacheKeyFor("Read", "/x", []byte{byte(i), byte(i >> 8)}), cachedPointer{literal: "l"})
	}
	if got := len(cache.entries); got != pointerCacheLimit {
		t.Fatalf("entries = %d, want %d", got, pointerCacheLimit)
	}
	key := pointerCacheKeyFor("Read", "/x", []byte{0, 0})
	cache.put(key, cachedPointer{literal: "overflow"})
	if got := len(cache.entries); got != 1 {
		t.Fatalf("a full cache did not reset: %d entries", got)
	}
	if entry, ok := cache.get(key); !ok || entry.literal != "overflow" {
		t.Fatal("the entry written after the reset is missing")
	}
}
