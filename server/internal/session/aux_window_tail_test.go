package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rootfs "mindfs/server/internal/fs"
)

// 窗口 aux 改尾部读后，必须与「全量读再按 seqSet 过滤」等价——这是正确性的底线。
func TestLoadExchangeAuxWindowTailEqualsFullRead(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := NewManager(root)
	key := "s1"

	dir := filepath.Join(root.MetaDir(), "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, key+".aux.jsonl")
	var b strings.Builder
	for seq := 1; seq <= 300; seq++ {
		// 含 plan 才能通过 CompactExchangeAux（仅 thought 会被丢弃）。
		fmt.Fprintf(&b, `{"seq":%d,"line":0,"plan":{"id":"p%d","content":"c%d"}}`+"\n", seq, seq, seq)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	// 全量读的基准
	full, err := manager.loadExchangeAuxEntries(key, 0)
	if err != nil {
		t.Fatal(err)
	}
	fullBySeq := map[int]string{}
	for _, entry := range full {
		if entry.Plan != nil {
			fullBySeq[entry.Seq] = entry.Plan.ID
		}
	}

	cases := []map[int]bool{
		{300: true},                     // 只取最后一条
		{295: true, 300: true},          // 尾部两条
		{1: true},                       // 头部（需扫到文件开头）
		{1: true, 150: true, 300: true}, // 跨全文件
	}
	for _, seqSet := range cases {
		got, err := manager.loadExchangeAuxWindow(key, seqSet)
		if err != nil {
			t.Fatalf("seqSet=%v err=%v", seqSet, err)
		}
		for seq := range seqSet {
			want := fullBySeq[seq]
			items := got[seq]
			if want == "" {
				if len(items) != 0 {
					t.Fatalf("seqSet=%v seq=%d 期望空，得到 %d 条", seqSet, seq, len(items))
				}
				continue
			}
			if len(items) != 1 || items[0].Plan == nil || items[0].Plan.ID != want {
				t.Fatalf("seqSet=%v seq=%d 期望 plan=%s，得到 %#v", seqSet, seq, want, items)
			}
		}
		// 不得混入窗口外的 seq
		for seq := range got {
			if !seqSet[seq] {
				t.Fatalf("seqSet=%v 混入窗口外 seq=%d", seqSet, seq)
			}
		}
	}
}

// aux 文件缺失时尾部读必须返回「空集且成功」，由调用方继续走正常路径。
func TestLoadExchangeAuxWindowMissingFile(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	got, err := manager.loadExchangeAuxWindow("absent", map[int]bool{5: true})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(got) != 0 {
		t.Fatalf("期望空集，得到 %#v", got)
	}
}
