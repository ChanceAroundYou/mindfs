package scheduled

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// scheduledSourceFiles 是本包的源码文件集合。runTask 落在哪一个是拆分细节，
// 守卫必须跟着它走而不是钉死一个文件名。
func scheduledSourceFiles(t *testing.T) map[string]string {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	out := map[string]string{}
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("读 %s 失败: %v", p, err)
		}
		out[p] = string(b)
	}
	return out
}

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
	// 逐文件扫，但错误信息带上文件名 —— 拆分后不钉死 tasks.go。
	assign := regexp.MustCompile(`^\s*\w+\s*:?=\s*broadcaster\.(BroadcastSessionUserMessageAt|SetSessionPendingReply)\(`)
	call := regexp.MustCompile(`broadcaster\.(BroadcastSessionUserMessageAt|SetSessionPendingReply)\(`)
	identityless := regexp.MustCompile(`BroadcastSessionUserMessage\s*\(`)
	zeroGen := regexp.MustCompile(`EndSessionTurn\([^)]*[^A-Za-z0-9_]0\s*\)`)
	emptyID := regexp.MustCompile(`EndSessionTurn\([^)]*,\s*""\s*\)`)

	files := scheduledSourceFiles(t)
	var joinedCode strings.Builder
	sawEndSessionTurn := false
	for path, src := range files {
		code := stripLineComments(src)
		joinedCode.WriteString(code)
		joinedCode.WriteString("\n")

		// 1 + 2：身份缺失的入口必须彻底消失（接口声明 + 调用）。
		if strings.Contains(code, "BroadcastSessionDone") {
			t.Fatalf("%s 仍引用身份缺失的 BroadcastSessionDone —— "+
				"定时任务的收尾 done 必须走 EndSessionTurn 并带本轮 mindfs_id", path)
		}
		// 4：不带 At 的那个置 pending 入口同样拿不到 mindfs_id，不许回到接口上。
		if identityless.MatchString(code) {
			t.Fatalf("%s 又出现了不带 At 的 BroadcastSessionUserMessage( —— "+
				"它置 pending 时拿不到 mindfs_id，收尾清 pending 会退化成无条件清", path)
		}

		// 3a：mindfs_id 必须被接收（丢弃返回值在 Go 里是合法的，编译器不挡）。
		// 只看**调用点**（`broadcaster.` 前缀）—— 接口声明行没有该前缀，不该被算进来。
		for i, line := range strings.Split(src, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if !call.MatchString(line) {
				continue
			}
			if !assign.MatchString(line) {
				t.Fatalf("%s:%d 调用了置 pending 的方法却没接收返回的 mindfs_id: %q", path, i+1, trimmed)
			}
		}

		// 3b：收尾不能把身份写成哨兵值（S2e 前是字面 0，现在是空串 ""）——
		// 哨兵走「无身份」，而 EndSessionTurn 的门禁在两侧都有身份时才比对，
		// 于是哨兵 == 退回无条件清。新旧两个哨兵都要拦：将来再换表示时同理。
		for i, line := range strings.Split(code, "\n") {
			if zeroGen.MatchString(line) || emptyID.MatchString(line) {
				t.Fatalf("%s:%d 把回合身份写成哨兵值传给 EndSessionTurn: %q",
					path, i+1, strings.TrimSpace(line))
			}
		}

		if strings.Contains(code, "EndSessionTurn(") {
			sawEndSessionTurn = true
		}
	}

	if !sawEndSessionTurn {
		t.Fatal("本包源码里找不到 EndSessionTurn 调用 —— 收尾没走带身份的终结器")
	}

	// 子会话的回合身份必须来自**单一来源** usecase.subagentTurnID()。
	// 2026-10-16 之前这段 `MaxExchangeSeq+1 → NewMindFSID` 在 ws.go / appcontext.go /
	// tasks.go 三处各抄一遍。收敛到共享函数的理由是：三条 attach 路径必须铸出同一种
	// 形状的 id，否则某条路径会静默拿不到身份 —— 那正是 claude 认领分支此前的处境
	// （子会话无人持有回合 ⇒ pending 永久残留，父会话被判成 delegating）。
	// 所以断言从「这段字面必须出现在本文件」改为「本文件不得自己铸，且必须接收 id」。
	// 这里用的是跨文件拼接后的 joinedCode 而不是循环末的 code：铸在哪一个文件是拆分
	// 细节，只查最后一个文件会让守卫在多数文件上形同虚设。
	if strings.Contains(joinedCode.String(), "session.NewMindFSID(") {
		t.Fatal("本包又在自己铸子会话回合身份：请改用 usecase.subagentTurnID()，" +
			"三处各自铸会让某条 attach 路径静默拿不到 id")
	}
	if !strings.Contains(joinedCode.String(), "turnID") {
		t.Fatal("本包没有接收子会话回合身份：请把它交给置 pending 与收尾路径")
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
