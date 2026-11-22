package session

import (
	"context"
	"strings"
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
)

// 块流是单个 appends-only 文件，长会话实测能到 70 MB / 4.5 万行。任何「读一遍」
// 都必须流式：先把整份文件 ReadMetaFile 进内存再 strings.Split，会同时产生
// 70 MB 副本与 4.5 万个行字符串，发送路径上每轮都要付一次（实测 685 ms /
// 157 MB 分配；流式 + 前缀解析 59 ms）。

func TestBlockSeqFromLineReadsLeadingSeqWithoutFullDecode(t *testing.T) {
	cases := []struct {
		name string
		line string
		want int
		ok   bool
	}{
		{"prefix", `{"seq":42,"pos":0,"revision":0,"type":"text","text":"x"}`, 42, true},
		{"neg-zero", `{"seq":0,"pos":1}`, 0, true},
		{"leading-space-falls-back", `  {"seq": 7,"pos":0}`, 7, true},
		{"field-order-falls-back", `{"pos":0,"seq":9}`, 9, true},
		{"wrong-type", `{"seq":"9"}`, 0, false},
		{"truncated", `{"seq":12`, 0, false},
		{"torn-body", `{"seq":4,"pos":0,"trunc`, 0, false},
		{"empty", ``, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := blockSeqFromLine([]byte(tc.line))
			if ok != tc.ok || (ok && got != tc.want) {
				t.Fatalf("blockSeqFromLine(%q) = (%d, %v), want (%d, %v)", tc.line, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestForEachBlockLineHandlesLongLinesAndTruncatedTail(t *testing.T) {
	long := `{"seq":2,"pos":0,"revision":0,"type":"text","text":"` + strings.Repeat("x", 2<<20) + `"}`
	body := `{"seq":1,"pos":0,"revision":0,"type":"text","text":"a"}` + "\n" +
		long + "\n" +
		`{"seq":3,"pos":0,"trunc` // 崩溃留下的半行：没有换行结尾，不算一行
	var lines, longest int
	var last string
	if err := forEachBlockLine(strings.NewReader(body), func(line []byte) bool {
		lines++
		last = string(line[:min(len(line), 16)])
		if len(line) > longest {
			longest = len(line)
		}
		return true
	}); err != nil {
		t.Fatalf("forEachBlockLine: %v", err)
	}
	if lines != 2 {
		t.Fatalf("lines = %d, want 2（截断尾行不是完整行，不得回调）", lines)
	}
	if strings.HasPrefix(last, `{"seq":3`) {
		t.Fatalf("末尾回调了截断行: %q", last)
	}
	if longest < 2<<20 {
		t.Fatalf("longest line = %d bytes, want > 2 MiB", longest)
	}
}

// CountBlocksAfterSeq 在发送路径上每轮都会跑；超长行与坏尾行都不能改变计数。
func TestCountBlocksAfterSeqStreamsLongLines(t *testing.T) {
	m := newBlockTestManager(t)
	key := newBlockTestSession(t, m)
	ctx := context.Background()
	longText := strings.Repeat("y", 600<<10)
	blocks := []Block{
		{Seq: 1, Pos: 0, Type: BlockTypeText, Text: "one", Step: 1},
		{Seq: 2, Pos: 1, Type: BlockTypeText, Text: longText, Step: 2},
		{Seq: 3, Pos: 0, Type: BlockTypeToolCall, ToolCall: &agenttypes.ToolCall{CallID: "c1", Title: "Bash"}, Step: 3},
	}
	if err := m.AppendBlocks(ctx, key, blocks); err != nil {
		t.Fatalf("AppendBlocks: %v", err)
	}
	// 模拟崩溃留下的半行：计数字段会被跳过，但前面的行一个都不能少。
	path, err := m.blocksPath(key)
	if err != nil {
		t.Fatalf("blocksPath: %v", err)
	}
	if err := m.root.WriteMetaFile(path, append(mustReadMeta(t, m, path), []byte(`{"seq":4,"pos":0,"trunc`+"\n")...)); err != nil {
		t.Fatalf("append partial line: %v", err)
	}

	for _, tc := range []struct{ after, want int }{{0, 3}, {1, 2}, {2, 1}, {3, 0}} {
		got, err := m.CountBlocksAfterSeq(key, tc.after)
		if err != nil {
			t.Fatalf("CountBlocksAfterSeq(%d): %v", tc.after, err)
		}
		if got != tc.want {
			t.Fatalf("CountBlocksAfterSeq(%d) = %d, want %d", tc.after, got, tc.want)
		}
	}
}

func mustReadMeta(t *testing.T, m *Manager, path string) []byte {
	t.Helper()
	data, err := m.root.ReadMetaFile(path)
	if err != nil {
		t.Fatalf("ReadMetaFile: %v", err)
	}
	return data
}
