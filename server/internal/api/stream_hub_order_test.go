package api

import "testing"

func TestListReplyingSessionsSortedBySessionKey(t *testing.T) {
	hub := NewStreamHub()
	for _, key := range []string{"s-c", "s-a", "s-b"} {
		hub.SetPendingReply("root-1", key, key)
	}
	first := hub.ListReplyingSessions()
	second := hub.ListReplyingSessions()
	if len(first) != 3 {
		t.Fatalf("len=%d", len(first))
	}
	for i, want := range []string{"s-a", "s-b", "s-c"} {
		if first[i].SessionKey != want {
			t.Fatalf("first[%d]=%q want %q", i, first[i].SessionKey, want)
		}
	}
	for i := range first {
		if first[i].SessionKey != second[i].SessionKey {
			t.Fatalf("顺序不稳定：%v vs %v", first, second)
		}
	}
}
