package scheduled

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// 源码守卫（G-BE 建，S2e 换身份表示后修订）：定时任务路径的收尾 done **必须携带回合身份**。
//
// 背景：SessionActivityBroadcaster 接口原先只有 `BroadcastSessionDone(rootID, key, requestID)`
// —— 没有 mindfs_id。定时任务调它 ⇒ `AppContext.BroadcastSessionDone` → `EndSessionTurn(..., "")`
// → `ClearSessionPending(key, "")`；而门禁是 `if turnID != "" && state.MindFSID != "" && …`，
// 传空串时条件为假 ⇒ **无条件清**（G-BE 的「两侧都有身份才比对」在无身份调用点上失效）。
//
// 窗口窄但真实：`usecase.SendMessage` 有 per-key sendLock + register/unregister defer，
// 同 key 回合被串行化；窗口在「SendMessage 返回 → 广播 done」之间 —— 此刻 active turn 已注销，
// 用户消息若在此刻起跑并置 pending（新 mindfs_id），会被这条空身份的 done 抹掉，
// 即症状「正在运行但会话列表灯不亮」。
//
// 本测试钉四条不变量（纯源码扫描，不跑真实回合 —— runTask 需要真 session manager）：
//  1. `SessionActivityBroadcaster` 接口里不得再出现 `BroadcastSessionDone`（身份缺失的入口必须不存在）；
//  2. tasks.go 里不得出现 `BroadcastSessionDone(` 调用；
//  3. tasks.go 里 `BroadcastSessionUserMessageAt(` / `SetSessionPendingReply(` 的返回值必须被接收
//     （Go 允许丢弃返回值，所以编译器不会替我们挡），且 `EndSessionTurn(` 不得传哨兵身份；
//  4. 接口/调用里不得再出现不带 `At` 的 `BroadcastSessionUserMessage(` —— 它是同一类
//     「置 pending 但拿不到 mindfs_id」的入口，留着等于给下一个调用点挖坑。
func TestScheduledPathCarriesTurnIdentity(t *testing.T) {
	b, err := os.ReadFile("tasks.go")
	if err != nil {
		t.Fatalf("读 tasks.go 失败: %v", err)
	}
	src := string(b)
	// 只在**代码**里扫：注释（含解释「为什么不用 BroadcastSessionDone」的注释）不算。
	code := stripLineComments(src)

	// 1 + 2：身份缺失的入口必须彻底消失（接口声明 + 调用）。
	if strings.Contains(code, "BroadcastSessionDone") {
		t.Fatalf("tasks.go 仍引用身份缺失的 BroadcastSessionDone —— " +
			"定时任务的收尾 done 必须走 EndSessionTurn 并带本轮 mindfs_id")
	}
	// 4：不带 At 的那个置 pending 入口同样拿不到 mindfs_id，不许回到接口上。
	// 正则要求 `Message` 后直接跟 `(`，因此不会误伤 `BroadcastSessionUserMessageAt(`。
	if identityless := regexp.MustCompile(`BroadcastSessionUserMessage\s*\(`); identityless.MatchString(code) {
		t.Fatalf("tasks.go 又出现了不带 At 的 BroadcastSessionUserMessage( —— " +
			"它置 pending 时拿不到 mindfs_id，收尾清 pending 会退化成无条件清")
	}

	// 3a：mindfs_id 必须被接收（丢弃返回值在 Go 里是合法的，编译器不挡）。
	// 只看**调用点**（`broadcaster.` 前缀）—— 接口声明行没有该前缀，不该被算进来。
	assign := regexp.MustCompile(`^\s*\w+\s*:?=\s*broadcaster\.(BroadcastSessionUserMessageAt|SetSessionPendingReply)\(`)
	call := regexp.MustCompile(`broadcaster\.(BroadcastSessionUserMessageAt|SetSessionPendingReply)\(`)
	for i, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if !call.MatchString(line) {
			continue
		}
		if !assign.MatchString(line) {
			t.Fatalf("tasks.go:%d 调用了置 pending 的方法却没接收返回的 mindfs_id: %q", i+1, trimmed)
		}
	}

	// 3b：收尾不能把身份写成哨兵值（S2e 前是字面 0，现在是空串 ""）——
	// 哨兵走「无身份」，而 EndSessionTurn 的门禁在两侧都有身份时才比对，
	// 于是哨兵 == 退回无条件清。新旧两个哨兵都要拦：将来再换表示时同理。
	zeroGen := regexp.MustCompile(`EndSessionTurn\([^)]*[^A-Za-z0-9_]0\s*\)`)
	emptyID := regexp.MustCompile(`EndSessionTurn\([^)]*,\s*""\s*\)`)
	for i, line := range strings.Split(code, "\n") {
		if zeroGen.MatchString(line) || emptyID.MatchString(line) {
			t.Fatalf("tasks.go:%d 把回合身份写成哨兵值传给 EndSessionTurn: %q",
				i+1, strings.TrimSpace(line))
		}
	}

	if !strings.Contains(code, "EndSessionTurn(") {
		t.Fatalf("tasks.go 找不到 EndSessionTurn 调用 —— 收尾没走带身份的终结器")
	}

	if !strings.Contains(code, "session.MaxExchangeSeq(created.Exchanges) + 1") ||
		!strings.Contains(code, "session.NewMindFSID(\"\", fmt.Sprintf(\"%s|agent|%d\", created.Key, nextSeq))") {
		t.Fatal("tasks.go 子会话 pending 必须按下一条 assistant seq 铸与落盘一致的 mindfs_id")
	}
}

// stripLineComments 去掉行注释，保留行号（注释行置空），供源码扫描忽略解释性文字。
// 只处理 `//`：Go 源码里不会在字符串字面量里出现裸 `//`（本文件被扫的 tasks.go 无 URL 字面量）。
func stripLineComments(src string) string {
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		if idx := strings.Index(line, "//"); idx >= 0 {
			lines[i] = line[:idx]
		}
	}
	return strings.Join(lines, "\n")
}
