package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 增量读按「已提交字节偏移」续读，只应产出新增内容。
// 判据从「item.Timestamp 是否比上次新」换成「item.StartOffset 是否在已提交位置之后」：
// 前者会被 applyClaudeToolResults 对每条 tool_result 持续改写，导致同一轮被反复当成
// 新内容落库（实测受损会话 seq 317-321 是同一轮的 6 份副本）。
func TestReadClaudeImportedExchangesIncremental(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	head := `{"type":"user","uuid":"u1","timestamp":"2026-08-27T02:15:00Z","message":{"content":[{"type":"text","text":"q1"}]}}
{"type":"assistant","uuid":"a1","timestamp":"2026-08-27T02:15:08Z","message":{"content":[{"type":"text","text":"r1"}]}}
`
	if err := os.WriteFile(path, []byte(head), 0o600); err != nil {
		t.Fatal(err)
	}
	first, committed, err := readClaudeImportedExchanges(path, 0, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 {
		t.Fatalf("首轮应产出 2 条，得到 %d", len(first))
	}
	if committed != int64(len(head)) {
		t.Fatalf("尾部已收尾时应提交到文件末尾，得到 %d（文件 %d 字节）", committed, len(head))
	}

	// 追加一轮，然后从已提交位置续读：只应拿到新增那一轮。
	tail := `{"type":"user","uuid":"u2","timestamp":"2026-08-27T02:16:00Z","message":{"content":[{"type":"text","text":"q2"}]}}
{"type":"assistant","uuid":"a2","timestamp":"2026-08-27T02:16:05Z","message":{"content":[{"type":"text","text":"r2"}]}}
`
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(tail); err != nil {
		t.Fatal(err)
	}
	f.Close()

	inc, committed2, err := readClaudeImportedExchanges(path, committed, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	// 新判据下不存在「回看窗口内重复产出上一轮」：已提交的条目一律不再产出。
	var sawQ2, sawR2 int
	for _, item := range inc {
		switch strings.TrimSpace(item.Content) {
		case "q1", "r1":
			t.Fatalf("已提交过的条目不得再次产出：%q", item.Content)
		case "q2":
			sawQ2++
		case "r2":
			sawR2++
		}
	}
	if sawQ2 != 1 || sawR2 != 1 {
		t.Fatalf("增量应恰好产出新增一轮（q2=%d r2=%d），共 %d 条", sawQ2, sawR2, len(inc))
	}

	// 稳态：文件无新增时续读应为空——「不会重复追加」的依据。
	eof, committed3, err := readClaudeImportedExchanges(path, committed2, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(eof) != 0 {
		t.Fatalf("无新增时续读应为空，得到 %d 条", len(eof))
	}
	if committed3 != committed2 {
		t.Fatalf("无新增时提交位置不应变化：%d → %d", committed2, committed3)
	}
}

// 尾轮还在进行（最后一条相关条目带 tool_use，正等工具结果）时不得提交：它的内容会继续
// 变，落了就是半成品，而且下次同步会把它当成新内容再落一遍。这是「同一轮落库 6 次」的
// 直接回归测试。
func TestReadClaudeImportedExchangesHoldsOpenTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	head := `{"type":"user","uuid":"u1","timestamp":"2026-08-27T02:15:00Z","message":{"content":[{"type":"text","text":"q1"}]}}
{"type":"assistant","uuid":"a1","timestamp":"2026-08-27T02:15:08Z","message":{"content":[{"type":"text","text":"先查一下"}]}}
{"type":"assistant","uuid":"a2","timestamp":"2026-08-27T02:15:09Z","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}
`
	if err := os.WriteFile(path, []byte(head), 0o600); err != nil {
		t.Fatal(err)
	}
	open, committed, err := readClaudeImportedExchanges(path, 0, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	// 用户条目照常提交；未收尾的助手轮留在原地。
	if len(open) != 1 || strings.TrimSpace(open[0].Content) != "q1" {
		t.Fatalf("未收尾的尾轮不应提交，只应产出用户条目，得到 %d 条", len(open))
	}
	if committed >= int64(len(head)) {
		t.Fatalf("提交位置应停在未收尾轮的起点，得到 %d（文件 %d 字节）", committed, len(head))
	}

	// 工具结果回来、助手收尾后：这一轮被完整提交一次，内容为最终态。
	done := head + `{"type":"user","uuid":"u2","timestamp":"2026-08-27T02:15:20Z","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}
{"type":"assistant","uuid":"a3","timestamp":"2026-08-27T02:15:30Z","message":{"content":[{"type":"text","text":"查完了"}]}}
`
	if err := os.WriteFile(path, []byte(done), 0o600); err != nil {
		t.Fatal(err)
	}
	closed, committed2, err := readClaudeImportedExchanges(path, committed, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(closed) != 1 {
		t.Fatalf("收尾后应恰好提交 1 条，得到 %d 条", len(closed))
	}
	got := strings.TrimSpace(closed[0].Content)
	if !strings.Contains(got, "先查一下") || !strings.Contains(got, "查完了") {
		t.Fatalf("提交内容应为该轮最终态，得到 %q", got)
	}
	if len(closed[0].Aux) != 1 {
		t.Fatalf("该轮的 aux 应只含 1 份工具调用，得到 %d 份", len(closed[0].Aux))
	}

	// 再同步一次不得重复提交。
	again, _, err := readClaudeImportedExchanges(path, committed2, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("已提交的轮次不得重复产出，得到 %d 条", len(again))
	}
}

// 复现 2026-10-09「点同步几乎百分百丢最新几轮」的导入器一侧：Full 同步是
// committedOffset==0 且 bootstrapAfter 为零的组合，旧代码在这里直接 return items，
// 把 TimestampFloor 静默跳过 —— live-owned 会话的 Full 同步于是导不到「比库里最新
// 一条更新」的尾段（usecase 再按 ctx_seq 切片，delta 恒为空）。修复：只要地板非零
// 就必须过滤，与有没有游标/引导时间戳无关（见 types.go 的 TimestampFloor 契约）。
func TestReadClaudeImportedExchangesAppliesFloorWithoutCursorOrBootstrap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := `{"type":"user","uuid":"u1","timestamp":"2026-10-09T15:00:00Z","message":{"content":[{"type":"text","text":"已入库的一轮"}]}}
{"type":"assistant","uuid":"a1","timestamp":"2026-10-09T15:00:05Z","message":{"content":[{"type":"text","text":"已入库的回复"}]}}
{"type":"user","uuid":"u2","timestamp":"2026-10-09T15:31:42Z","message":{"content":[{"type":"text","text":"最新一轮"}]}}
{"type":"assistant","uuid":"a2","timestamp":"2026-10-09T15:32:24Z","message":{"content":[{"type":"text","text":"最新回复"}]}}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// 库里最新一条是 15:14:26；Full 同步只传地板，不传游标也不传引导时间戳
	floor := time.Date(2026, 10, 9, 15, 14, 26, 0, time.UTC)
	items, _, err := readClaudeImportedExchanges(path, 0, time.Time{}, floor)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("地板应滤掉 15:00 的两条，只留 15:31/15:32 的两条，得到 %d 条: %+v", len(items), items)
	}
	for _, item := range items {
		if !item.Timestamp.After(floor) {
			t.Fatalf("地板以下的条目不该被导出: %+v", item)
		}
	}
}

// 时间地板（TimestampFloor）：live-owned 会话的游标冻结已久，重启后的兜底补齐若只按偏移读，
// 会把早已落库的旧回合整段重导一遍——那些重导行因为「相邻同角色合并」内容并不逐字相同，
// 写入侧判重都折叠不掉。加了地板之后：游标决定从哪开始读，地板决定读到的东西
// 算不算数，兜底补齐只拿「比库里最新一条更新」的尾轮。
func TestReadClaudeImportedExchangesHonorsTimestampFloor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := `{"type":"user","uuid":"u1","timestamp":"2026-09-14T01:00:00Z","message":{"content":[{"type":"text","text":"老旧的一轮"}]}}
{"type":"assistant","uuid":"a1","timestamp":"2026-09-14T01:00:05Z","message":{"content":[{"type":"text","text":"老旧的回复"}]}}
{"type":"user","uuid":"u2","timestamp":"2026-09-16T01:04:16Z","message":{"content":[{"type":"text","text":"刚被中断的那一轮"}]}}
{"type":"assistant","uuid":"a2","timestamp":"2026-09-16T01:04:20Z","message":{"content":[{"type":"text","text":"尾轮回复"}]}}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// 游标停在文件开头（模拟冻结的旧游标），地板设为 09-15
	floor := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	items, _, err := readClaudeImportedExchanges(path, 1, time.Time{}, floor)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("地板应滤掉 09-14 的两条，只留 09-16 的两条，得到 %d 条: %+v", len(items), items)
	}
	for _, item := range items {
		if item.Timestamp.Before(floor) {
			t.Fatalf("地板以下的条目不该被导出: %+v", item)
		}
	}
	// 没有地板时行为不变（既有的导入器路径不受影响）：老条目会照常出现在结果里。
	// 注意末尾那条可能被「尾轮未收尾就先不落库」扣住，所以这里只断言"比有地板时多，
	// 且确实带上了 09-14 的老条目"，不去咬死具体条数。
	all, _, err := readClaudeImportedExchanges(path, 1, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) <= len(items) {
		t.Fatalf("不给地板时应带上老条目（多于 %d 条），得到 %d 条", len(items), len(all))
	}
	var sawOld bool
	for _, item := range all {
		if item.Timestamp.Before(floor) {
			sawOld = true
		}
	}
	if !sawOld {
		t.Fatalf("不给地板时应能看到 09-14 的老条目: %+v", all)
	}
}
