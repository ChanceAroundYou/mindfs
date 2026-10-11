package session

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rootfs "mindfs/server/internal/fs"
)

// appendExchangeLogLine 模拟「外部写入者」直接往主日志追加一行：
// 绕过 manager 的写路径，因此读盘游标不前进，下次读取必然走尾部增量读。
// 显式推进 mtime，避免同一次测试内两次写入落在同一时间戳上而被判为「文件未变」。
func appendExchangeLogLine(t *testing.T, m *Manager, key string, record Exchange) {
	t.Helper()
	path, err := m.exchangePath(key)
	if err != nil {
		t.Fatalf("exchange path: %v", err)
	}
	file, err := m.root.OpenMetaFileAppend(path)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	payload, err := json.Marshal(record)
	if err != nil {
		_ = file.Close()
		t.Fatalf("marshal: %v", err)
	}
	if _, err := file.Write(append(payload, '\n')); err != nil {
		_ = file.Close()
		t.Fatalf("append: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	abs := filepath.Join(m.root.MetaDir(), path)
	stamp := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(abs, stamp, stamp); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
}

// 增量快路径（loadExchanges → readExchangeTail）必须看得见「同 seq 的后续版本」。
// 原实现按 `seq <= baseSeq` 直接跳过，导致补全后的记录永远不可见（正文停在占位空串）。
// S1 用「字段完备度」表达这次更新；S2a 起由 revision 承担同一职责。
func TestLoadExchangesIncrementalAppliesSameSeqUpdate(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	m := NewManager(root)
	ctx := context.Background()
	created, err := m.Create(ctx, CreateInput{Type: TypeChat, Name: "SameSeqUpdate"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	writeExchangeLog(t, m, created.Key, []Exchange{
		{Seq: 1, Role: "user", LegacyContent: "q"},
		{Seq: 2, Role: "agent"},
	})

	// 首次读取建立缓存 + 读盘游标。
	first, err := m.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatalf("first get: %v", err)
	}
	if len(first.Exchanges) != 2 || first.Exchanges[1].LegacyContent != "" {
		t.Fatalf("first exchanges = %#v, want placeholder empty agent row", first.Exchanges)
	}

	// 外部写入者补全同一条 seq=2。
	appendExchangeLogLine(t, m, created.Key, Exchange{Seq: 2, Role: "agent", LegacyContent: "complete reply"})

	got, err := m.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatalf("incremental get: %v", err)
	}
	if len(got.Exchanges) != 2 {
		t.Fatalf("incremental exchanges = %d rows, want 2 (no duplicate seq)", len(got.Exchanges))
	}
	if got.Exchanges[1].LegacyContent != "complete reply" {
		t.Fatalf("seq=2 content = %q, want %q (incremental path must see same-seq update)",
			got.Exchanges[1].LegacyContent, "complete reply")
	}
}

// 尾部同一 seq 出现多行时，只保留一条（原实现会把两行都 append 进缓存，产生重复 seq）。
func TestLoadExchangesIncrementalDedupsSameSeqWithinTail(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	m := NewManager(root)
	ctx := context.Background()
	created, err := m.Create(ctx, CreateInput{Type: TypeChat, Name: "TailDedup"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	writeExchangeLog(t, m, created.Key, []Exchange{
		{Seq: 1, Role: "user", LegacyContent: "q"},
		{Seq: 2, Role: "agent", LegacyContent: "first"},
	})
	if _, err := m.Get(ctx, created.Key, 0); err != nil {
		t.Fatalf("first get: %v", err)
	}

	appendExchangeLogLine(t, m, created.Key, Exchange{Seq: 3, Role: "agent", LegacyContent: "draft"})
	appendExchangeLogLine(t, m, created.Key, Exchange{Seq: 3, Role: "agent", LegacyContent: "final"})

	got, err := m.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatalf("incremental get: %v", err)
	}
	if len(got.Exchanges) != 3 {
		t.Fatalf("exchanges = %d rows, want 3 (same-seq tail rows must merge)", len(got.Exchanges))
	}
	if got.Exchanges[2].LegacyContent != "final" {
		t.Fatalf("seq=3 content = %q, want %q (last non-empty wins)", got.Exchanges[2].LegacyContent, "final")
	}
}

// 冲突告警只在「真冲突」（两行都有正文且不同）时打；字段补全属于增量更新，不该打日志。
func TestLoadExchangesDoesNotWarnOnCompletenessUpgrade(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	m := NewManager(root)
	ctx := context.Background()
	created, err := m.Create(ctx, CreateInput{Type: TypeChat, Name: "NoWarn"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	writeExchangeLog(t, m, created.Key, []Exchange{
		{Seq: 1, Role: "user", LegacyContent: "q"},
		{Seq: 2, Role: "agent"},
	})

	var captured bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&captured)
	defer log.SetOutput(previous)

	appendExchangeLogLine(t, m, created.Key, Exchange{Seq: 2, Role: "agent", LegacyContent: "reply", Model: "gpt-5"})
	if _, err := m.Get(ctx, created.Key, 0); err != nil {
		t.Fatalf("incremental get: %v", err)
	}
	if strings.Contains(captured.String(), "conflicting duplicate seq") {
		t.Fatalf("completeness upgrade logged as conflict:\n%s", captured.String())
	}

	// 真冲突：两行都有非空且不同的正文 → 必须告警。
	captured.Reset()
	appendExchangeLogLine(t, m, created.Key, Exchange{Seq: 3, Role: "agent", LegacyContent: "aaa"})
	appendExchangeLogLine(t, m, created.Key, Exchange{Seq: 3, Role: "agent", LegacyContent: "bbb"})
	if _, err := m.Get(ctx, created.Key, 0); err != nil {
		t.Fatalf("incremental get after conflict: %v", err)
	}
	if !strings.Contains(captured.String(), "conflicting duplicate seq") {
		t.Fatalf("real conflict was not logged:\n%s", captured.String())
	}
}
