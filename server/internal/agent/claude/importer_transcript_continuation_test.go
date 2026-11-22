package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 时间地板必须是**条目级**的。相邻同角色条目会合并成一条 item，合并后的时间戳取
// 最后一条条目、NativeID 也取最后一条的 uuid —— 只在地板处拦 item，「已落库那一轮的
// 尾段 + 之后的续写」就会并成一条时间戳更新的 item，身份判重必然失手，重导一次把
// 旧正文再落一遍。
//
// 现场（2026-11-22，llm-deploy 会话）：回合在 06:33:11 收尾并落盘；06:35:28 CLI 因
// 后台任务回注（task-notification，被 isMeta/噪声过滤挡掉，不产生 item）自己又跑了
// 一整段循环到 06:39:27。两条助手条目之间没有任何 item 把它们隔开，于是并成一条。
func TestReadClaudeImportedExchangesFloorSplitsMergedAssistantGroup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := `{"type":"user","uuid":"u1","timestamp":"2026-11-22T06:32:00.000Z","message":{"content":[{"type":"text","text":"已落库的问题"}]}}
{"type":"assistant","uuid":"a1","timestamp":"2026-11-22T06:33:10.000Z","message":{"content":[{"type":"text","text":"已落库的回复"}]}}
{"type":"user","uuid":"n1","timestamp":"2026-11-22T06:35:28.000Z","message":{"content":[{"type":"text","text":"<task-notification> 后台任务 b8jh7lfpw 完成"}]}}
{"type":"assistant","uuid":"a2","timestamp":"2026-11-22T06:35:35.000Z","message":{"content":[{"type":"text","text":"续写一"}]}}
{"type":"assistant","uuid":"a3","timestamp":"2026-11-22T06:39:27.000Z","message":{"content":[{"type":"text","text":"续写二"}]}}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// 地板取「已落库那一行」的落盘时刻：06:33:11.365（比转录里最后一条助手条目晚）。
	floor := time.Date(2026, 11, 22, 6, 33, 11, 365_000_000, time.UTC)
	items, committed, err := readClaudeImportedExchanges(path, 0, time.Time{}, floor)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("地板之后只该剩续写那一条 item，得到 %d 条: %+v", len(items), items)
	}
	item := items[0]
	if item.Role != "agent" {
		t.Fatalf("续写应归到助手侧，得到 role=%q", item.Role)
	}
	if strings.Contains(item.Content, "已落库的回复") {
		t.Fatalf("已落库那一轮的正文不得被并进续写行: %q", item.Content)
	}
	if !strings.Contains(item.Content, "续写一") || !strings.Contains(item.Content, "续写二") {
		t.Fatalf("续写内容缺失: %q", item.Content)
	}
	if item.NativeID != "a3" {
		t.Fatalf("NativeID 应取最后一条条目（a3），得到 %q", item.NativeID)
	}
	if want := time.Date(2026, 11, 22, 6, 39, 27, 0, time.UTC); !item.Timestamp.Equal(want) {
		t.Fatalf("item 时间戳应取最后一条条目 %s，得到 %s", want, item.Timestamp)
	}
	// 尾部已收尾（最后一条是纯文本助手条目） ⇒ 整份文件都算提交过。
	if committed != int64(len(body)) {
		t.Fatalf("收尾后应提交到文件末尾，committed=%d size=%d", committed, len(body))
	}

	// 幂等：冻结游标（committedOffset=0，live-owned 会话的真实形态）再配这道地板，
	// 同一条续写不会被再读一遍 —— 只剩地板挡不住的真人用户条目。
	again, _, err := readClaudeImportedExchanges(path, 0, time.Time{}, item.Timestamp)
	if err != nil {
		t.Fatal(err)
	}
	for _, extra := range again {
		if extra.Role == "agent" {
			t.Fatalf("同一段续写不该被读第二次，得到 %+v", extra)
		}
	}
}

// 没有地板时行为不变：合并组照旧从已落库那一条开始 —— 这条钉住「地板只影响设了
// 地板的调用方」，别把日常导入（import-owned 增量）也改了。
func TestReadClaudeImportedExchangesWithoutFloorKeepsMergedGroup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := `{"type":"assistant","uuid":"a1","timestamp":"2026-11-22T06:33:10.000Z","message":{"content":[{"type":"text","text":"已落库的回复"}]}}
{"type":"user","uuid":"n1","timestamp":"2026-11-22T06:35:28.000Z","message":{"content":[{"type":"text","text":"<task-notification> 后台任务完成"}]}}
{"type":"assistant","uuid":"a2","timestamp":"2026-11-22T06:39:27.000Z","message":{"content":[{"type":"text","text":"续写"}]}}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	items, _, err := readClaudeImportedExchanges(path, 0, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("没有地板时相邻助手条目仍应合并成一条，得到 %d 条: %+v", len(items), items)
	}
	if !strings.Contains(items[0].Content, "已落库的回复") || !strings.Contains(items[0].Content, "续写") {
		t.Fatalf("没有地板时合并内容保持不变: %q", items[0].Content)
	}
}
