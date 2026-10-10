package session

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	rootfs "mindfs/server/internal/fs"
)

// writeExchangeLog 直接写主日志文件（绕过 manager 写入路径与内存缓存），
// 用于构造「冷缓存」场景：新 manager 既没有 exchanges 缓存也没有读盘游标。
func writeExchangeLog(t *testing.T, m *Manager, key string, records []Exchange) {
	t.Helper()
	path, err := m.exchangePath(key)
	if err != nil {
		t.Fatalf("exchange path: %v", err)
	}
	var buf bytes.Buffer
	for _, record := range records {
		payload, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("marshal exchange: %v", err)
		}
		buf.Write(payload)
		buf.WriteByte('\n')
	}
	if err := m.root.WriteMetaFile(path, buf.Bytes()); err != nil {
		t.Fatalf("write exchange log: %v", err)
	}
}

func sequentialExchanges(total int) []Exchange {
	records := make([]Exchange, 0, total)
	for i := 1; i <= total; i++ {
		role := "user"
		if i%2 == 0 {
			role = "agent"
		}
		records = append(records, Exchange{
			Seq:       i,
			Role:      role,
			Content:   fmt.Sprintf("line-%d", i),
			Timestamp: time.Unix(int64(i), 0).UTC(),
		})
	}
	return records
}

func newColdManager(t *testing.T, root rootfs.RootInfo) *Manager {
	t.Helper()
	return NewManager(root)
}

func seqRange(exchanges []Exchange) []int {
	out := make([]int, 0, len(exchanges))
	for _, item := range exchanges {
		out = append(out, item.Seq)
	}
	return out
}

// 冷缓存开窗必须只解析窗口所需的尾部记录，不能走 readExchangesFull 全量解析。
// 依据：全量解析在实测最重会话上 13.8ms/1.2MB，而窗口只要尾部 8 条。
func TestGetWindowColdCacheAvoidsFullParse(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	creator := NewManager(root)
	ctx := context.Background()
	created, err := creator.Create(ctx, CreateInput{Type: TypeChat, Name: "ColdWindow"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	const total = 40
	writeExchangeLog(t, creator, created.Key, sequentialExchanges(total))

	m := newColdManager(t, root)
	win, meta, err := m.GetWindow(ctx, created.Key, 0, 8, 0)
	if err != nil {
		t.Fatalf("cold GetWindow: %v", err)
	}
	if m.fullExchangeReads != 0 {
		t.Fatalf("cold window read called readExchangesFull %d times, want 0", m.fullExchangeReads)
	}
	if got := seqRange(win.Exchanges); len(got) != 8 || got[0] != 33 || got[7] != 40 {
		t.Fatalf("window seqs = %v, want 33..40", got)
	}
	if meta.Total != total || !meta.HasMore || meta.MinSeq != 33 || meta.MaxSeq != 40 {
		t.Fatalf("meta = %+v, want total=%d hasMore=true min=33 max=40", meta, total)
	}
}

// beforeSeq 取「seq<beforeSeq 的尾部 limit 条」，冷缓存同样不能全量解析。
func TestGetWindowColdCacheBeforeSeq(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	creator := NewManager(root)
	ctx := context.Background()
	created, err := creator.Create(ctx, CreateInput{Type: TypeChat, Name: "ColdBefore"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	writeExchangeLog(t, creator, created.Key, sequentialExchanges(40))

	m := newColdManager(t, root)
	win, meta, err := m.GetWindow(ctx, created.Key, 20, 5, 0)
	if err != nil {
		t.Fatalf("cold GetWindow before_seq: %v", err)
	}
	if m.fullExchangeReads != 0 {
		t.Fatalf("cold before_seq window read called readExchangesFull %d times, want 0", m.fullExchangeReads)
	}
	if got := seqRange(win.Exchanges); len(got) != 5 || got[0] != 15 || got[4] != 19 {
		t.Fatalf("window seqs = %v, want 15..19", got)
	}
	if meta.Total != 40 || !meta.HasMore || meta.MinSeq != 15 || meta.MaxSeq != 19 {
		t.Fatalf("meta = %+v, want total=40 hasMore=true min=15 max=19", meta)
	}
}

// latest 语义与 limit 解耦（既有行为：latest>0 时窗口就是尾部 latest 条）。
func TestGetWindowColdCacheLatestIgnoresLimit(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	creator := NewManager(root)
	ctx := context.Background()
	created, err := creator.Create(ctx, CreateInput{Type: TypeChat, Name: "ColdLatest"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	writeExchangeLog(t, creator, created.Key, sequentialExchanges(40))

	m := newColdManager(t, root)
	win, meta, err := m.GetWindow(ctx, created.Key, 0, 8, 3)
	if err != nil {
		t.Fatalf("cold GetWindow latest: %v", err)
	}
	if got := seqRange(win.Exchanges); len(got) != 3 || got[0] != 38 || got[2] != 40 {
		t.Fatalf("window seqs = %v, want 38..40", got)
	}
	if meta.MinSeq != 38 || meta.MaxSeq != 40 || !meta.HasMore {
		t.Fatalf("meta = %+v, want min=38 max=40 hasMore=true", meta)
	}
}

// 窗口覆盖整份日志时 hasMore=false，且 Total 取非空行数。
func TestGetWindowColdCacheCoversWholeFile(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	creator := NewManager(root)
	ctx := context.Background()
	created, err := creator.Create(ctx, CreateInput{Type: TypeChat, Name: "ColdAll"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	writeExchangeLog(t, creator, created.Key, sequentialExchanges(5))

	m := newColdManager(t, root)
	win, meta, err := m.GetWindow(ctx, created.Key, 0, 100, 0)
	if err != nil {
		t.Fatalf("cold GetWindow all: %v", err)
	}
	if got := seqRange(win.Exchanges); len(got) != 5 || got[0] != 1 || got[4] != 5 {
		t.Fatalf("window seqs = %v, want 1..5", got)
	}
	if meta.Total != 5 || meta.HasMore || meta.MinSeq != 1 || meta.MaxSeq != 5 {
		t.Fatalf("meta = %+v, want total=5 hasMore=false min=1 max=5", meta)
	}
}

// 单条记录大于一个读取分块（实测最大单行 308KB）时窗口仍必须完整取出。
func TestGetWindowColdCacheHandlesLineLargerThanChunk(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	creator := NewManager(root)
	ctx := context.Background()
	created, err := creator.Create(ctx, CreateInput{Type: TypeChat, Name: "ColdHugeLine"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	huge := make([]Exchange, 0, 3)
	huge = append(huge, Exchange{Seq: 1, Role: "user", Content: "q"})
	huge = append(huge, Exchange{Seq: 2, Role: "agent", Content: string(bytes.Repeat([]byte("正"), 200000))})
	huge = append(huge, Exchange{Seq: 3, Role: "user", Content: "tail"})
	writeExchangeLog(t, creator, created.Key, huge)

	m := newColdManager(t, root)
	win, meta, err := m.GetWindow(ctx, created.Key, 0, 2, 0)
	if err != nil {
		t.Fatalf("cold GetWindow huge line: %v", err)
	}
	if got := seqRange(win.Exchanges); len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("window seqs = %v, want 2..3", got)
	}
	if meta.Total != 3 || !meta.HasMore {
		t.Fatalf("meta = %+v, want total=3 hasMore=true", meta)
	}
}

// 日志文件不存在时冷开窗返回空窗口，且不报错（与全量读的既有语义一致）。
func TestGetWindowColdCacheMissingLog(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	creator := NewManager(root)
	ctx := context.Background()
	created, err := creator.Create(ctx, CreateInput{Type: TypeChat, Name: "ColdMissing"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	m := newColdManager(t, root)
	win, meta, err := m.GetWindow(ctx, created.Key, 0, 8, 0)
	if err != nil {
		t.Fatalf("cold GetWindow missing log: %v", err)
	}
	if len(win.Exchanges) != 0 || meta.Total != 0 || meta.HasMore || meta.MinSeq != 0 || meta.MaxSeq != 0 {
		t.Fatalf("window = %v meta = %+v, want empty", win.Exchanges, meta)
	}
}
