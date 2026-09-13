package claude

import (
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	agenttypes "mindfs/server/internal/agent/types"
)

// 真实转录验收（默认跳过，机器相关，不入 CI）。用于复查「同一轮被反复落库」类缺陷：
//
//	MIND_FS_REPLAY_TRANSCRIPT=~/.claude/projects/<slug>/<uuid>.jsonl \
//	  go test ./internal/agent/claude/ -run TestReplayRealTranscriptOpenTail -v
//
// 断言目标会话里那一轮恰好产出 1 条、内容为最终态、ask 卡只有一份；
// 并统计相邻 agent item 互为前缀的对数（同一轮被反复落库的指纹）。
func TestReplayRealTranscriptOpenTail(t *testing.T) {
	path := os.Getenv("MIND_FS_REPLAY_TRANSCRIPT")
	if path == "" {
		t.Skip("set MIND_FS_REPLAY_TRANSCRIPT")
	}
	items, committed, err := readClaudeImportedExchanges(path, 0, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if committed != info.Size() {
		t.Fatalf("完整转录应提交到末尾：committed=%d size=%d", committed, info.Size())
	}

	var hits []int
	for i, item := range items {
		if item.Role == "agent" && strings.Contains(item.Content, "开始只读核查。并行三路") {
			hits = append(hits, i)
		}
	}
	t.Logf("总 item 数=%d，命中该轮的=%d 条", len(items), len(hits))
	if len(hits) != 1 {
		t.Fatalf("该轮应恰好产出 1 条，得到 %d 条", len(hits))
	}
	item := items[hits[0]]
	asks := 0
	for _, aux := range item.Aux {
		if aux.ToolCall != nil && aux.ToolCall.Kind == agenttypes.ToolKindAskUser {
			asks++
		}
	}
	t.Logf("命中该轮：内容字符数=%d（%d 字节）aux 份数=%d 其中 ask=%d", utf8.RuneCountInString(item.Content), len(item.Content), len(item.Aux), asks)
	if utf8.RuneCountInString(item.Content) != 509 {
		t.Fatalf("内容应为最终态 509 字，得到 %d", utf8.RuneCountInString(item.Content))
	}
	if len(item.Aux) != 26 {
		t.Fatalf("aux 应为 26 份，得到 %d", len(item.Aux))
	}

	// 全库指纹：相邻 agent item 不得互为前缀（那是「同一轮被反复落库」的痕迹）。
	prev := -1
	dups := 0
	for i, item := range items {
		if item.Role != "agent" || strings.TrimSpace(item.Content) == "" {
			continue
		}
		if prev >= 0 {
			p, c := items[prev].Content, item.Content
			if strings.HasPrefix(c, p) || strings.HasPrefix(p, c) {
				dups++
				if dups <= 3 {
					t.Logf("疑似前缀重复：item[%d](%d 字) 与 item[%d](%d 字)", prev, len(p), i, len(c))
				}
			}
		}
		prev = i
	}
	t.Logf("相邻 agent item 互为前缀的对数=%d", dups)
}
