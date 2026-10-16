package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rootfs "mindfs/server/internal/fs"
)

func writeExchangeFile(t *testing.T, root rootfs.RootInfo, key string, lines []string) string {
	t.Helper()
	path := filepath.Join(root.MetaDir(), "sessions", key+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write exchanges: %v", err)
	}
	return path
}

func exchangeJSON(t *testing.T, seq int, content string) string {
	t.Helper()
	payload, err := json.Marshal(Exchange{Seq: seq, Role: "user", LegacyContent: content, Timestamp: time.Date(2026, 9, 14, 10, 0, seq, 0, time.UTC)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(payload)
}

// seq 有洞时按「最大 seq + 1」分配，不按缓存长度：否则下一次写入会撞上已存在的 seq，
// 前端按 seq 合并 → 消息直接不显示。
func TestNextSeqUsesMaxSeqNotLength(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Gap"})
	if err != nil {
		t.Fatal(err)
	}
	writeExchangeFile(t, root, created.Key, []string{
		exchangeJSON(t, 1, "a"), exchangeJSON(t, 2, "b"), exchangeJSON(t, 3, "c"), exchangeJSON(t, 5, "e"),
	})
	loaded, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Exchanges) != 4 || maxExchangeSeq(loaded.Exchanges) != 5 {
		t.Fatalf("setup = %d rows max=%d", len(loaded.Exchanges), maxExchangeSeq(loaded.Exchanges))
	}
	if err := manager.AddExchangeForAgent(context.Background(), loaded, "user", "new", "claude", "", "", ""); err != nil {
		t.Fatal(err)
	}
	after, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if last := after.Exchanges[len(after.Exchanges)-1]; last.Seq != 6 {
		t.Fatalf("new seq = %d, want 6（旧的 len+1 会给 5，与已有行撞号）", last.Seq)
	}
}

// 行首被 NUL 零填充的损坏行（外部/并发写入留下的空洞）必须能被还原，不能当坏行丢掉。
func TestReadExchangesRepairsNulFilledLine(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Nul"})
	if err != nil {
		t.Fatal(err)
	}
	writeExchangeFile(t, root, created.Key, []string{
		exchangeJSON(t, 1, "a"),
		strings.Repeat("\x00", 64) + exchangeJSON(t, 2, "b"),
	})
	loaded, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Exchanges) != 2 {
		t.Fatalf("rows = %d, want 2（NUL 行应被还原）", len(loaded.Exchanges))
	}
	if loaded.Exchanges[1].LegacyContent != "b" {
		t.Fatalf("repaired content = %q, want %q", loaded.Exchanges[1].LegacyContent, "b")
	}
}

// 文件在盘上被外部改动后，缓存不能当真相继续用：下一次读必须看到新行。
func TestGetSessionRefreshesStaleCache(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Name: "External"})
	if err != nil {
		t.Fatal(err)
	}
	path := writeExchangeFile(t, root, created.Key, []string{exchangeJSON(t, 1, "a")})
	if _, err := manager.Get(context.Background(), created.Key, 0); err != nil {
		t.Fatal(err)
	}
	// 模拟外部写入者（另一个进程 / 手工修文件）追加一行
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(exchangeJSON(t, 2, "b") + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	loaded, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Exchanges) != 2 {
		t.Fatalf("rows = %d, want 2（陈旧缓存必须被刷新）", len(loaded.Exchanges))
	}
}
