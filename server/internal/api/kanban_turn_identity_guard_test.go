package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// 源码守卫：看板阶段执行路径（AppContext.RunAgentStage）的收尾 done **必须携带回合身份**。
//
// 为什么需要它：这条路径与定时任务（scheduled/tasks.go）是**同一形状的第三处**
// 服务端发起轮次，而 G-BE 当年只补了 tasks.go —— `pending_identity_test.go` 的守卫
// 扫的是 tasks.go，管不到这里。于是 RunAgentStage 一直是「置 pending 拿不到身份、
// 收尾按无身份走保守清」的状态：灯不灭被兜住（保守清），但**新轮的灯会被误抹**。
//
// 2026-10-11 S2e 换身份表示（TurnGen uint64 → mindfs_id string）时把它一并补齐，
// 让这条路径与 ws.go / tasks.go 遵守同一条纪律。三条不变量（纯源码扫描，
// 不跑真实阶段 —— RunAgentStage 要 kanban 的真 store）：
//  1. `BroadcastSessionUserMessageAt(` / `SetSessionPendingReply(` 的返回值必须被接收
//     （Go 允许丢返回值，编译器不挡）；
//  2. `EndSessionTurn(` 不得传哨兵身份（S2e 前是字面 0，现在是空串 ""）；
//  3. 收尾路径上不得再出现身份缺失的 `BroadcastSessionDone`。
func TestKanbanStagePathCarriesTurnIdentity(t *testing.T) {
	b, err := os.ReadFile("appcontext.go")
	if err != nil {
		t.Fatalf("读 appcontext.go 失败: %v", err)
	}
	src := string(b)
	// 只在**代码**里扫：注释（含解释「为什么不用 BroadcastSessionDone」的注释）不算。
	code := stripAPILineComments(src)

	if strings.Contains(code, "BroadcastSessionDone") {
		t.Fatal("appcontext.go 仍引用身份缺失的 BroadcastSessionDone —— " +
			"看板阶段的收尾 done 必须走 EndSessionTurn 并带本轮 mindfs_id")
	}

	// 1：置 pending 的返回值必须被接收。只看 RunAgentStage 函数体（全局扫会误伤
	// 定义行与注释）。
	body := runAgentStageBody(src)
	assign := regexp.MustCompile(`^\s*\w+\s*:?=\s*s\.(BroadcastSessionUserMessageAt|SetSessionPendingReply)\(`)
	call := regexp.MustCompile(`s\.(BroadcastSessionUserMessageAt|SetSessionPendingReply)\(`)
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		if !call.MatchString(line) {
			continue
		}
		if !assign.MatchString(line) {
			t.Fatalf("appcontext.go 的 RunAgentStage 调用了置 pending 的方法却没接收返回的 mindfs_id: %q",
				strings.TrimSpace(line))
		}
	}
	// 3：子会话的回合身份必须来自**单一来源** subagentTurnID()。
	//
	// 2026-10-16 之前这段 `MaxExchangeSeq+1 → NewMindFSID` 在 ws.go / appcontext.go /
	// scheduled/tasks.go 三处各抄一遍。收敛到 usecase.subagentTurnID 的理由不只是去重：
	// 三条 attach 路径必须铸出同一种形状的 id，否则某个调用方会静默拿不到身份 ——
	// 那正是 claude 认领分支此前的处境（子会话无人持有回合 ⇒ pending 永久残留）。
	// 所以这里从「这段字面必须出现在本文件」改成「本文件必须用那个共享函数」。
	if strings.Contains(code, "session.NewMindFSID(") {
		t.Fatal("appcontext.go 又在自己铸子会话回合身份：请改用 usecase.subagentTurnID()，" +
			"三处各自铸会让某条 attach 路径静默拿不到 id")
	}
	if !strings.Contains(code, "turnID") {
		t.Fatal("appcontext.go 的 RunAgentStage 没有接收子会话回合身份：请把它交给收尾路径")
	}

	// 2：收尾不得传哨兵身份。新旧哨兵都拦。
	zeroGen := regexp.MustCompile(`EndSessionTurn\([^)]*[^A-Za-z0-9_]0\s*\)`)
	emptyID := regexp.MustCompile(`EndSessionTurn\([^)]*,\s*""\s*\)`)
	for i, line := range strings.Split(code, "\n") {
		if !zeroGen.MatchString(line) && !emptyID.MatchString(line) {
			continue
		}
		t.Fatalf("appcontext.go:%d 把回合身份写成哨兵值传给 EndSessionTurn: %q",
			i+1, strings.TrimSpace(line))
	}
}

// runAgentStageBody 截出 RunAgentStage 的函数体（到下一个顶层 func 之前）。
// 粗略但够用：只看方法内的置 pending 调用点，避免把定义行当调用点。
func runAgentStageBody(src string) string {
	start := strings.Index(src, "func (s *AppContext) RunAgentStage(")
	if start < 0 {
		return ""
	}
	rest := src[start:]
	// 下一个顶层声明：空行后的 "func "。RunAgentStage 体内没有空行+func 的形状。
	if idx := strings.Index(rest[1:], "\nfunc "); idx >= 0 {
		return rest[:idx+1]
	}
	return rest
}

// stripAPILineComments 去掉行注释（保留行号），与 scheduled 的同名助手同一形状。
func stripAPILineComments(src string) string {
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		if idx := strings.Index(line, "//"); idx >= 0 {
			lines[i] = line[:idx]
		}
	}
	return strings.Join(lines, "\n")
}
