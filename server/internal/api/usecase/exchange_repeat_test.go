package usecase

import (
	"context"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

// 同一轮的用户条目有两个写入者：外部转录同步的导入器，和回合结束时的 SendMessage。
// 判据不对称时同一句话会落两条（2026-09-13 实测 seq=57 带 Z 的导入条目、
// seq=58 由 SendMessage 写的条目，时间戳差 21ms）。
func TestExchangeAlreadyRecorded(t *testing.T) {
	base := time.Date(2026, 9, 13, 7, 14, 55, 0, time.UTC)
	target := &session.Session{
		Exchanges: []session.Exchange{
			{Role: "user", Content: "把那条决定性信息给我", Timestamp: base},
			{Role: "agent", Content: "好", Timestamp: base.Add(time.Second)},
			{Role: "user", Content: "没有时间戳的条目"},
		},
	}

	cases := []struct {
		name    string
		target  *session.Session
		role    string
		content string
		ts      time.Time
		want    bool
	}{
		{"双写：同内容 21ms 后落第二条", target, "user", "把那条决定性信息给我", base.Add(21 * time.Millisecond), true},
		{"窗内（4s）仍算重复", target, "user", "把那条决定性信息给我", base.Add(4 * time.Second), true},
		{"窗外（10s）是真实重发，必须保留", target, "user", "把那条决定性信息给我", base.Add(10 * time.Second), false},
		{"窗外（-10s）同上", target, "user", "把那条决定性信息给我", base.Add(-10 * time.Second), false},
		{"role 不同不算重复", target, "agent", "把那条决定性信息给我", base, false},
		{"内容不同不算重复", target, "user", "继续", base, false},
		{"零时间戳不参与判定", target, "user", "把那条决定性信息给我", time.Time{}, false},
		{"nil target", nil, "user", "x", base, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exchangeAlreadyRecorded(tc.target, tc.role, tc.content, tc.ts); got != tc.want {
				t.Fatalf("exchangeAlreadyRecorded(role=%q content=%q ts=%v) = %v, want %v",
					tc.role, tc.content, tc.ts, got, tc.want)
			}
		})
	}
}

// 助手侧的双写现场（2026-09-14 实测）：同一轮助手消息由实时路径与转录导入各写一次，
// 两边抓到的快照长度不同 —— 实时写 21 字（seq=128，纳秒 ts），转录导入 556 字
// （seq=129，毫秒 ts）＝ 前者 + "\n\nAPI Error: 502 Provider returned 429 …"。
// 只按完全相等判会留下一模一样的重复气泡。
func TestExchangeAlreadyRecordedToleratesAgentPrefixSnapshot(t *testing.T) {
	base := time.Date(2026, 9, 14, 6, 51, 35, 0, time.UTC)
	live := "重启确认，全库也干净了。现在做浏览器实测。"
	full := live + "\n\nAPI Error: 502 Provider returned 429 Too Many Requests"

	agent := func(content string) *session.Session {
		return &session.Session{Exchanges: []session.Exchange{{Role: "agent", Content: content, Timestamp: base}}}
	}
	user := func(content string) *session.Session {
		return &session.Session{Exchanges: []session.Exchange{{Role: "user", Content: content, Timestamp: base}}}
	}

	cases := []struct {
		name    string
		target  *session.Session
		role    string
		content string
		ts      time.Time
		want    bool
	}{
		{"库里是实时短版，导入的长版应判重", agent(live), "agent", full, base.Add(330 * time.Millisecond), true},
		{"反向：库里是长版，导入短版也判重", agent(full), "agent", live, base.Add(-330 * time.Millisecond), true},
		{"前缀关系超出容忍窗口仍是两条", agent(live), "agent", full, base.Add(10 * time.Second), false},
		// 真实形状（2026-09-14 16:18 实测）：实时路径把 \n\n 插进了 Markdown 粗体标记内部，
		// 导入版是连着的。折叠成单空格会留下一个多余空格，必须抹掉空白才能对齐。
		{"只差空行（实测实时 1765 字 vs 导入 1761 字）", agent("治疗端给**支具\n\n+康复总市场**。\n\n模型跑通了。"), "agent",
			"治疗端给**支具+康复总市场**。\n\n模型跑通了。", base.Add(360 * time.Millisecond), true},
		{"表格行内的空行差异", agent("| **3.04 亿**\n\n |\n| 基准 |"), "agent", "| **3.04 亿** |\n| 基准 |",
			base.Add(360 * time.Millisecond), true},
		{"用户侧同样按空白归一化判重", user("第一段\n\n第二段"), "user", "第一段\n第二段", base.Add(time.Millisecond), true},
		{"用户侧不吃前缀容忍：「继续」vs「继续吧」", user("继续"), "user", "继续吧", base.Add(time.Millisecond), false},
		{"助手侧短应答不做前缀判定：「好」vs「好的，我这就去处理这件事」",
			agent("好"), "agent", "好的，我这就去处理这件事", base.Add(time.Millisecond), false},
		{"空内容不参与前缀判定", agent(""), "agent", live, base.Add(time.Millisecond), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exchangeAlreadyRecorded(tc.target, tc.role, tc.content, tc.ts); got != tc.want {
				t.Fatalf("exchangeAlreadyRecorded(role=%q content=%q) = %v, want %v", tc.role, tc.content, got, tc.want)
			}
		})
	}
}

// 端到端复刻 2026-09-13 的双写现场（真实 manager + 真实落盘）：
//
//	15:14:55.826Z  转录同步的导入器先写 seq=57（用户行在转录里先出现）
//	15:15:11       回合结束时 SendMessage 又要写同一条（旧代码 → seq=58，差 21ms）
//
// 两个写入者现在共用一个判据，所以第二个必须被拦下；而隔久了的真实重发照旧放行。
func TestSendPathSkipsExchangeAlreadyImported(t *testing.T) {
	ctx := context.Background()
	manager := session.NewManager(rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir()))
	created, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "dup"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	target, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}

	const content = "把那条决定性信息给我"
	imported := time.Date(2026, 9, 13, 7, 14, 55, 826000000, time.UTC)
	exchange := agenttypes.ImportedExchange{Role: "user", Content: content, Timestamp: imported}

	added, err := appendImportedExchange(ctx, manager, target, "claude", exchange)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if !added {
		t.Fatal("the first import should add the exchange")
	}
	target, err = manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatalf("reload after import: %v", err)
	}
	if n := len(target.Exchanges); n != 1 {
		t.Fatalf("after first import: %d exchanges, want 1", n)
	}

	// 同一条再导一次 → 幂等，不新增
	added, err = appendImportedExchange(ctx, manager, target, "claude", exchange)
	if err != nil {
		t.Fatalf("repeated import: %v", err)
	}
	if added {
		t.Fatal("re-importing the same exchange should be a no-op")
	}
	target, err = manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatalf("reload after repeated import: %v", err)
	}
	if n := len(target.Exchanges); n != 1 {
		t.Fatalf("after repeated import: %d exchanges, want 1", n)
	}

	// 第二个写入者（SendMessage 的回合结束写入）：21ms 后，必须被拦下
	if !exchangeAlreadyRecorded(target, "user", content, imported.Add(21*time.Millisecond)) {
		t.Fatal("the send path must suppress a user exchange already written by the importer")
	}
	// 窗外（30s）是用户真实重发，必须放行 —— 红线：不得按内容批量去重
	if exchangeAlreadyRecorded(target, "user", content, imported.Add(30*time.Second)) {
		t.Fatal("a genuine repeat outside the tolerance window must not be suppressed")
	}
}
