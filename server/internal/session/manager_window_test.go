package session

import (
	"context"
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
)

func createSessionWithExchanges(t *testing.T, manager *Manager, n int) *Session {
	t.Helper()
	ctx := context.Background()
	s, err := manager.Create(ctx, CreateInput{Type: TypeChat, Name: "Window"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	for i := 1; i <= n; i++ {
		content := "msg"
		if err := manager.AddExchangeForAgent(ctx, s, "user", content, "claude", "", "", ""); err != nil {
			t.Fatalf("add exchange %d: %v", i, err)
		}
		if err := manager.AddExchangeForAgent(ctx, s, "agent", content, "claude", "", "", ""); err != nil {
			t.Fatalf("add exchange agent %d: %v", i, err)
		}
	}
	loaded, err := manager.Get(ctx, s.Key, 0)
	if err != nil {
		t.Fatalf("get after create: %v", err)
	}
	return loaded
}

func TestGetWindowLatest(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	m := NewManager(root)
	ctx := context.Background()
	s := createSessionWithExchanges(t, m, 3) // 6 exchanges seq 1..6
	total := len(s.Exchanges)
	if total != 6 {
		t.Fatalf("total exchanges = %d, want 6", total)
	}
	// latest=2 should return last 2
	win, meta, err := m.GetWindow(ctx, s.Key, 0, 0, 2)
	if err != nil {
		t.Fatalf("GetWindow latest: %v", err)
	}
	if len(win.Exchanges) != 2 {
		t.Fatalf("window len = %d, want 2", len(win.Exchanges))
	}
	if meta.Total != total {
		t.Fatalf("meta.Total = %d, want %d", meta.Total, total)
	}
	if !meta.HasMore {
		t.Fatalf("HasMore = false, want true (6 total, latest 2)")
	}
	if meta.MinSeq != win.Exchanges[0].Seq || meta.MaxSeq != win.Exchanges[len(win.Exchanges)-1].Seq {
		t.Fatalf("MinSeq/MaxSeq mismatch: %+v vs exchanges", meta)
	}
	// latest larger than total → full window, HasMore false
	win2, meta2, err := m.GetWindow(ctx, s.Key, 0, 0, 100)
	if err != nil {
		t.Fatalf("GetWindow latest overflow: %v", err)
	}
	if len(win2.Exchanges) != total || meta2.HasMore {
		t.Fatalf("overflow window len=%d hasMore=%v, want %d false", len(win2.Exchanges), meta2.HasMore, total)
	}
}

func TestGetWindowBeforeSeq(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	m := NewManager(root)
	ctx := context.Background()
	s := createSessionWithExchanges(t, m, 5) // seq 1..10
	// beforeSeq=6, limit=3 → should return seq 3,4,5 (idx of first >=6 is 5 (0-based), window [2:5])
	win, meta, err := m.GetWindow(ctx, s.Key, 6, 3, 0)
	if err != nil {
		t.Fatalf("GetWindow beforeSeq: %v", err)
	}
	if len(win.Exchanges) != 3 {
		t.Fatalf("window len = %d, want 3", len(win.Exchanges))
	}
	for i, ex := range win.Exchanges {
		want := 3 + i
		if ex.Seq != want {
			t.Fatalf("ex[%d].Seq = %d, want %d", i, ex.Seq, want)
		}
	}
	if !meta.HasMore {
		t.Fatalf("HasMore = false, want true (still items before)")
	}
	// beforeSeq at minSeq (seq 1 has no before) → empty? first index is 0, window [0:0] empty, HasMore false
	win2, meta2, err := m.GetWindow(ctx, s.Key, 1, 3, 0)
	if err != nil {
		t.Fatalf("GetWindow beforeSeq min: %v", err)
	}
	if len(win2.Exchanges) != 0 || meta2.HasMore {
		t.Fatalf("min beforeSeq window len=%d hasMore=%v, want 0 false", len(win2.Exchanges), meta2.HasMore)
	}
	// beforeSeq beyond max → window is last limit items before end
	win3, meta3, err := m.GetWindow(ctx, s.Key, 100, 3, 0)
	if err != nil {
		t.Fatalf("GetWindow beforeSeq beyond max: %v", err)
	}
	if len(win3.Exchanges) != 3 {
		t.Fatalf("beyond max len=%d, want 3", len(win3.Exchanges))
	}
	if meta3.MaxSeq != 10 || meta3.MinSeq != 8 {
		t.Fatalf("beyond max meta = %+v, want Min 8 Max 10", meta3)
	}
}

func TestGetWindowLimitClamp(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	m := NewManager(root)
	ctx := context.Background()
	s := createSessionWithExchanges(t, m, 2) // 4 exchanges
	// limit 0 defaults to 50 → returns all (4)
	win, meta, err := m.GetWindow(ctx, s.Key, 0, 0, 0)
	if err != nil {
		t.Fatalf("GetWindow default limit: %v", err)
	}
	if len(win.Exchanges) != 4 || meta.Total != 4 {
		t.Fatalf("default limit window len=%d total=%d, want 4 4", len(win.Exchanges), meta.Total)
	}
	// limit >200 clamps to 200 → still returns all for small session
	win2, _, err := m.GetWindow(ctx, s.Key, 0, 999, 0)
	if err != nil {
		t.Fatalf("GetWindow clamp: %v", err)
	}
	if len(win2.Exchanges) != 4 {
		t.Fatalf("clamp window len=%d, want 4", len(win2.Exchanges))
	}
}

func TestCountExchanges(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	m := NewManager(root)
	ctx := context.Background()
	s, err := m.Create(ctx, CreateInput{Type: TypeChat, Name: "Count"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if n, err := m.CountExchanges(s.Key); err != nil || n != 0 {
		t.Fatalf("Count empty = %d err=%v, want 0 nil", n, err)
	}
	for i := 0; i < 3; i++ {
		if err := m.AddExchangeForAgent(ctx, s, "user", "hi", "claude", "", "", ""); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	// after adding, count via cache fast path and via file
	if n, err := m.CountExchanges(s.Key); err != nil || n != 3 {
		t.Fatalf("Count after 3 = %d err=%v, want 3", n, err)
	}
	// invalid key
	if _, err := m.CountExchanges(""); err == nil {
		t.Fatalf("Count empty key should error")
	}
}

func TestGetExchangeAuxWindow(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	m := NewManager(root)
	ctx := context.Background()
	s, err := m.Create(ctx, CreateInput{Type: TypeChat, Name: "AuxWindow"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// need exchanges first to have seqs
	for i := 0; i < 3; i++ {
		if err := m.AddExchangeForAgent(ctx, s, "user", "hi", "claude", "", "", ""); err != nil {
			t.Fatalf("add exchange: %v", err)
		}
	}
	loaded, _ := m.Get(ctx, s.Key, 0)
	_ = loaded
	// add aux for seq 1,2,3
	for seq := 1; seq <= 3; seq++ {
		if err := m.AddExchangeAux(ctx, s.Key, ExchangeAux{
			Seq:     seq,
			Line:    0,
			Thought: "thought",
		}); err != nil {
			t.Fatalf("add aux seq %d: %v", seq, err)
		}
		if err := m.AddExchangeAux(ctx, s.Key, ExchangeAux{
			Seq:  seq,
			Line: 1,
			ToolCall: &agenttypes.ToolCall{
				CallID: "call",
				Title:  "t",
				Kind:   agenttypes.ToolKindSearch,
			},
		}); err != nil {
			t.Fatalf("add aux tool seq %d: %v", seq, err)
		}
	}
	// window only seq 2
	seqSet := map[int]bool{2: true}
	winAux, err := m.GetExchangeAuxWindow(ctx, s.Key, seqSet)
	if err != nil {
		t.Fatalf("GetExchangeAuxWindow: %v", err)
	}
	if len(winAux) != 1 || len(winAux[2]) == 0 {
		t.Fatalf("window aux = %#v, want only seq 2", winAux)
	}
	if _, ok := winAux[1]; ok {
		t.Fatalf("seq 1 should be filtered")
	}
	if _, ok := winAux[3]; ok {
		t.Fatalf("seq 3 should be filtered")
	}
	// empty set returns empty without error
	empty, err := m.GetExchangeAuxWindow(ctx, s.Key, map[int]bool{})
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty set = %#v err=%v, want empty", empty, err)
	}
	if _, err := m.GetExchangeAuxWindow(ctx, "", seqSet); err == nil {
		// key empty may still go through load but should not panic; allow either error or empty
	}
}

func TestFirstIndexWhereSeq(t *testing.T) {
	cases := []struct {
		seqs   []int
		target int
		want   int
	}{
		{[]int{1, 2, 3, 5, 8}, 5, 3},
		{[]int{1, 2, 3}, 1, 0},
		{[]int{1, 2, 3}, 4, 3},
		{[]int{}, 1, 0},
		{[]int{2, 4, 6}, 3, 1},
	}
	for _, c := range cases {
		exs := make([]Exchange, len(c.seqs))
		for i, s := range c.seqs {
			exs[i] = Exchange{Seq: s}
		}
		got := firstIndexWhereSeq(exs, c.target)
		if got != c.want {
			t.Fatalf("firstIndexWhereSeq(%v, %d) = %d, want %d", c.seqs, c.target, got, c.want)
		}
	}
}

func TestGetWindowHasMoreTruthTable(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	m := NewManager(root)
	ctx := context.Background()
	s := createSessionWithExchanges(t, m, 10) // 20 exchanges 1..20
	// latest 5 → hasMore true (20-5=15>0)
	_, meta, _ := m.GetWindow(ctx, s.Key, 0, 0, 5)
	if !meta.HasMore {
		t.Fatalf("latest 5 hasMore false, want true")
	}
	// latest 20 → hasMore false (exact)
	_, meta2, _ := m.GetWindow(ctx, s.Key, 0, 0, 20)
	if meta2.HasMore {
		t.Fatalf("latest 20 hasMore true, want false")
	}
	// beforeSeq 10 limit 5 → idx of 10 is 9 (0-based), start 4, hasMore true
	_, meta3, _ := m.GetWindow(ctx, s.Key, 10, 5, 0)
	if !meta3.HasMore {
		t.Fatalf("beforeSeq 10 limit5 hasMore false, want true")
	}
	// beforeSeq 6 limit 10 → start 0, hasMore false (at beginning)
	_, meta4, _ := m.GetWindow(ctx, s.Key, 6, 10, 0)
	if meta4.HasMore {
		t.Fatalf("beforeSeq 6 limit10 hasMore true, want false")
	}
}
