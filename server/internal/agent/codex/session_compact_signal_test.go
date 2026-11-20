package codex

import (
	"testing"

	codexsdk "github.com/fanwenlin/codex-go-sdk/codex"
	codextypes "github.com/fanwenlin/codex-go-sdk/types"
)

// fakeCompactItem is any item the SDK did not model: the handler falls back to
// its type discriminator, which is how a `contextCompaction` event arrives on
// some app-server versions.
type fakeCompactItem struct{ typ string }

func (f fakeCompactItem) GetType() string { return f.typ }

// Codex compacts on its own schedule. Each of the three event shapes that
// report it must reach the proxy, because the request that follows a
// compaction carries the whole transcript and is the one moment a fold is
// free.
func TestCodexCompactEventInvokesOnCompact(t *testing.T) {
	cases := []struct {
		name string
		item codexsdk.ThreadItem
	}{
		{"compacted item", &codexsdk.CompactedItem{ID: "c1", Summary: "summary"}},
		{"unknown contextCompaction", &codextypes.UnknownItem{Type: "contextCompaction"}},
		{"typed contextCompaction", fakeCompactItem{typ: "contextCompaction"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			s := &session{threadID: "thread-1", onCompact: func(id string) { got = append(got, id) }}
			if !s.handleNonToolItem(tc.item, false) {
				t.Fatal("item was not handled")
			}
			if len(got) != 1 || got[0] != "thread-1" {
				t.Fatalf("onCompact calls = %v, want [thread-1]", got)
			}
		})
	}
}

// A session opened without the hook (older wiring, tests) must still emit its
// normal events.
func TestCodexCompactEventWithoutHookIsSafe(t *testing.T) {
	s := &session{threadID: "thread-1"}
	if !s.handleNonToolItem(&codexsdk.CompactedItem{ID: "c1"}, false) {
		t.Fatal("item was not handled")
	}
}
