package session

import (
	"context"
	"strings"
	"testing"
	"time"

	rootfs "mindfs/server/internal/fs"
)

// 搜索的 scanner 上限必须与主日志解析一致（64MB），否则单行超过 1MB 的会话会
// 整个搜不到（bufio.Scanner 返回 ErrTooLong，Search 直接失败）。
func TestSearchSessionContentMatchesLineLargerThanOneMegabyte(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	m := NewManager(root)
	ctx := context.Background()
	created, err := m.Create(ctx, CreateInput{Type: TypeChat, Name: "BigLine"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// 单行 > 1MB（历史上限），正文里埋一个唯一关键词。
	big := strings.Repeat("填", 400000) + " needle " + strings.Repeat("充", 400000)
	writeExchangeLog(t, m, created.Key, []Exchange{
		{Seq: 1, Role: "user", Content: "hello"},
		{Seq: 2, Role: "agent", Content: big, Timestamp: time.Unix(2, 0).UTC()},
	})

	hits, err := m.Search(ctx, SearchOptions{Query: "needle", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1 (single line >1MB must still be searchable)", len(hits))
	}
	if hits[0].Key != created.Key {
		t.Fatalf("hit key = %q, want %q", hits[0].Key, created.Key)
	}
}
