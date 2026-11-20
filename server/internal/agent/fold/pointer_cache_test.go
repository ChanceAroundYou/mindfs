package fold

import "testing"

func TestPointerCacheNilIsUsable(t *testing.T) {
	// planFold takes the cache as an optional dependency; a nil must read as a
	// permanent miss rather than a panic.
	var cache *PointerCache
	key := NewPointerCacheKey("Read", `{"file_path":"/x"}`, []byte(`"text"`))
	if _, ok := cache.Get(key); ok {
		t.Fatal("nil cache reported a hit")
	}
	cache.Put(key, CachedPointer{Literal: "x"}) // must not panic
}

func TestPointerCacheIsKeyedByContent(t *testing.T) {
	cache := NewPointerCache()
	key := NewPointerCacheKey("Read", `{"file_path":"/a"}`, []byte(`"same text"`))
	cache.Put(key, CachedPointer{Literal: "one", Path: "/tmp/1.txt", Tokens: 10, Bytes: 100})

	if entry, ok := cache.Get(NewPointerCacheKey("Read", `{"file_path":"/a"}`, []byte(`"same text"`))); !ok || entry.Literal != "one" {
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
		if _, ok := cache.Get(NewPointerCacheKey(other.tool, other.input, []byte(other.span))); ok {
			t.Fatalf("%s: distinct block hit the cached entry", name)
		}
	}
}

// A long session has an upper bound on memory: the map is dropped wholesale at
// the limit rather than growing without bound. Every entry is cheap to
// recompute, so a reset costs one extra rebuild, not correctness.
func TestPointerCacheResetsAtLimit(t *testing.T) {
	cache := NewPointerCache()
	for i := 0; i < PointerCacheLimit; i++ {
		cache.Put(NewPointerCacheKey("Read", "/x", []byte{byte(i), byte(i >> 8)}), CachedPointer{Literal: "l"})
	}
	if got := len(cache.entries); got != PointerCacheLimit {
		t.Fatalf("entries = %d, want %d", got, PointerCacheLimit)
	}
	key := NewPointerCacheKey("Read", "/x", []byte{0, 0})
	cache.Put(key, CachedPointer{Literal: "overflow"})
	if got := len(cache.entries); got != 1 {
		t.Fatalf("a full cache did not reset: %d entries", got)
	}
	if entry, ok := cache.Get(key); !ok || entry.Literal != "overflow" {
		t.Fatal("the entry written after the reset is missing")
	}
}
