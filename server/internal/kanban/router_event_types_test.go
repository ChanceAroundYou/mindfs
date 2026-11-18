package kanban

import (
	"strconv"
	"testing"
	"time"
)

// T6 直接复用这些实例键规则，所以规则本身单独钉住。
func TestRouterEventInstanceKeyRules(t *testing.T) {
	at := time.Unix(600, 0).UTC() // 600/300 = bucket 2
	cases := []struct {
		name string
		kind RouterEventKind
		run  string
		node string
		att  int
		want string
	}{
		{"run_settled", RouterEventRunSettled, "run-1", "", 0, "run-1"},
		{"run_failed", RouterEventRunFailed, "run-1", "", 0, "run-1"},
		{"startup_reconcile", RouterEventStartupReconcile, "run-1", "", 0, "run-1"},
		{"gate attempt 0", RouterEventGateWaiting, "run-1", "node-1", 0, "node-1:0"},
		{"gate attempt 3", RouterEventGateWaiting, "run-1", "node-1", 3, "node-1:3"},
		{"stalled bucket", RouterEventRunStalled, "run-1", "", 0, "run-1:" + strconv.Itoa(int(at.Unix()/300))},
	}
	for _, tc := range cases {
		got, err := RouterEventInstanceKey(tc.kind, tc.run, tc.node, tc.att, at)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s: instance key = %q, want %q", tc.name, got, tc.want)
		}
	}

	sameBucket, err := RouterEventInstanceKey(RouterEventRunStalled, "run-1", "", 0, time.Unix(899, 0).UTC())
	if err != nil {
		t.Fatalf("stalled 899: %v", err)
	}
	firstBucket, err := RouterEventInstanceKey(RouterEventRunStalled, "run-1", "", 0, at)
	if err != nil {
		t.Fatalf("stalled 600: %v", err)
	}
	if sameBucket != firstBucket {
		t.Fatalf("600 and 899 must share the 5-minute bucket: %q vs %q", firstBucket, sameBucket)
	}
	nextBucket, err := RouterEventInstanceKey(RouterEventRunStalled, "run-1", "", 0, time.Unix(900, 0).UTC())
	if err != nil {
		t.Fatalf("stalled 900: %v", err)
	}
	if nextBucket == firstBucket {
		t.Fatalf("900 must fall into the next bucket, got %q", nextBucket)
	}

	if _, err := RouterEventInstanceKey(RouterEventKind("bogus"), "run-1", "node-1", 1, at); err == nil {
		t.Fatal("unknown kind must not produce an instance key")
	}
	if _, err := RouterEventInstanceKey(RouterEventGateWaiting, "run-1", "", 1, at); err == nil {
		t.Fatal("gate instance key requires a node run id")
	}
	if _, err := RouterEventInstanceKey(RouterEventRunSettled, "", "", 0, at); err == nil {
		t.Fatal("run instance key requires a run id")
	}
}

func TestRouterEventUniqueKeyFormat(t *testing.T) {
	got := RouterEventUniqueKey("task-1", "run-1", RouterEventGateWaiting, "node-1:2")
	want := "task-1\x00run-1\x00gate_waiting\x00node-1:2"
	if got != want {
		t.Fatalf("unique key = %q, want %q", got, want)
	}
	if RouterEventUniqueKey("task-1", "run-1", RouterEventRunSettled, "run-1") ==
		RouterEventUniqueKey("task-1", "run-1", RouterEventRunFailed, "run-1") {
		t.Fatal("unique key must include the event kind")
	}
}
