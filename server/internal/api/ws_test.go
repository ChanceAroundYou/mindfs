package api

import (
	"testing"
	"time"
)

func TestStreamHubFrozenQueueBlocksAutomaticPopUntilUnfrozen(t *testing.T) {
	hub := NewStreamHub()
	rootID := "root"
	sessionKey := "session"

	hub.EnqueueSessionMessage(rootID, sessionKey, "Session", QueuedUserMessage{
		ID: "first",
		PendingUserMessage: PendingUserMessage{
			Content:   "first message",
			Timestamp: time.Now().UTC(),
		},
	})
	hub.EnqueueSessionMessage(rootID, sessionKey, "Session", QueuedUserMessage{
		ID: "second",
		PendingUserMessage: PendingUserMessage{
			Content:   "second message",
			Timestamp: time.Now().UTC(),
		},
	})

	frozenQueue, frozen := hub.FreezeQueuedSessionMessages(sessionKey)
	if !frozen {
		t.Fatal("expected queue freeze to succeed")
	}
	if len(frozenQueue) != 2 {
		t.Fatalf("expected frozen queue snapshot to contain 2 items, got %d", len(frozenQueue))
	}
	if _, queue, ok := hub.PopQueuedSessionMessage(sessionKey, ""); ok {
		t.Fatal("expected frozen queue to block automatic pop")
	} else if len(queue) != 2 {
		t.Fatalf("expected frozen queue to remain intact, got %d items", len(queue))
	}

	queue, ok := hub.PromoteQueuedSessionMessage(sessionKey, "second")
	if !ok {
		t.Fatal("expected promote to succeed")
	}
	if len(queue) != 2 || queue[0].ID != "second" {
		t.Fatalf("expected promoted item at queue head, got %#v", queue)
	}

	item, queue, ok := hub.PopQueuedSessionMessage(sessionKey, "")
	if !ok {
		t.Fatal("expected promoted queue to be unfrozen")
	}
	if item.ID != "second" {
		t.Fatalf("expected promoted item to pop first, got %q", item.ID)
	}
	if len(queue) != 1 || queue[0].ID != "first" {
		t.Fatalf("expected remaining queue to contain first item, got %#v", queue)
	}
}

func TestStreamHubUnfreezeQueueAllowsAutomaticPop(t *testing.T) {
	hub := NewStreamHub()
	sessionKey := "session"
	hub.EnqueueSessionMessage("root", sessionKey, "Session", QueuedUserMessage{
		ID: "first",
		PendingUserMessage: PendingUserMessage{
			Content:   "first message",
			Timestamp: time.Now().UTC(),
		},
	})
	_, frozen := hub.FreezeQueuedSessionMessages(sessionKey)
	if !frozen {
		t.Fatal("expected queue freeze to succeed")
	}

	unfrozenQueue, changed := hub.UnfreezeQueuedSessionMessages(sessionKey)
	if !changed {
		t.Fatal("expected queue unfreeze to report changed")
	}
	if len(unfrozenQueue) != 1 {
		t.Fatalf("expected unfreeze queue snapshot to contain 1 item, got %d", len(unfrozenQueue))
	}
	item, queue, ok := hub.PopQueuedSessionMessage(sessionKey, "")
	if !ok {
		t.Fatal("expected automatic pop after unfreeze")
	}
	if item.ID != "first" {
		t.Fatalf("expected first item, got %q", item.ID)
	}
	if len(queue) != 0 {
		t.Fatalf("expected empty queue, got %#v", queue)
	}
}

func TestStreamHubSetPendingUserAtUsesProvidedTimestamp(t *testing.T) {
	hub := NewStreamHub()
	want := time.Date(2026, 7, 29, 10, 0, 0, int(456*time.Millisecond), time.UTC)

	pending, _ := hub.SetPendingUserAt("root", "session", "Session", "codex", "gpt-test", "", "", "", "", false, "hello", want)

	if pending == nil {
		t.Fatal("SetPendingUserAt returned nil")
	}
	if !pending.Timestamp.Equal(want) {
		t.Fatalf("pending timestamp = %s, want %s", pending.Timestamp.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
}
