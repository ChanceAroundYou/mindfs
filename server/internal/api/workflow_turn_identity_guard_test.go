package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// 源码守卫：服务端发起的 workflow 轮次（AppContext.executeWorkflowTurn）的收尾 done
// **必须携带回合身份**。
//
// 为什么需要它：这条路径与定时任务（scheduled/tasks.go）是同一形状的服务端发起轮次，
// 而 G-BE 当年只补了 tasks.go —— `pending_identity_test.go` 的守卫扫的是 tasks.go，
// 管不到这里。身份缺失的表现是：灯不灭被保守清兜住，但**新轮的灯会被误抹**。
//
// 看板阶段执行路径删除后，可以置 pending 的服务端轮次只剩这一条（收尾回合、workflow
// 节点与留言投递都走它），所以守卫跟着搬到 appcontext_workflow.go。三条不变量（纯源码
// 扫描，不跑真实轮次 —— executeWorkflowTurn 要真 session）：
//  1. `BroadcastSessionUserMessageAt(` / `SetSessionPendingReply(` 的返回值必须被接收
//     （Go 允许丢返回值，编译器不挡）；
//  2. `EndSessionTurn(` 的**回合身份**参数（最后一个）不得传哨兵值（空串 "" 或 0）；
//  3. 收尾路径上不得再出现身份缺失的 `BroadcastSessionDone`。
func TestWorkflowTurnCarriesTurnIdentity(t *testing.T) {
	b, err := os.ReadFile("appcontext_workflow.go")
	if err != nil {
		t.Fatalf("读 appcontext_workflow.go 失败: %v", err)
	}
	src := string(b)
	// 只在**代码**里扫：注释（含解释「为什么不用 BroadcastSessionDone」的注释）不算。
	code := stripAPILineComments(src)

	if strings.Contains(code, "BroadcastSessionDone") {
		t.Fatal("appcontext_workflow.go 仍引用身份缺失的 BroadcastSessionDone —— " +
			"workflow 轮次的收尾 done 必须走 EndSessionTurn 并带本轮 mindfs_id")
	}

	// 1：置 pending 的返回值必须被接收。只看 executeWorkflowTurn 函数体（全局扫会误伤
	// 定义行与注释）。
	body := workflowTurnBody(src)
	if body == "" {
		t.Fatal("找不到 executeWorkflowTurn 的函数体 —— 轮次生命周期挪走了，先更新这条守卫")
	}
	// 只认真正的变量名：`_ =` 也是丢弃身份，不能让它混过去。
	assign := regexp.MustCompile(`^\s*[A-Za-z]\w*\s*:?=\s*s\.(BroadcastSessionUserMessageAt|SetSessionPendingReply)\(`)
	call := regexp.MustCompile(`s\.(BroadcastSessionUserMessageAt|SetSessionPendingReply)\(`)
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		if !call.MatchString(line) {
			continue
		}
		if !assign.MatchString(line) {
			t.Fatalf("executeWorkflowTurn 调用了置 pending 的方法却没接收返回的 mindfs_id: %q",
				strings.TrimSpace(line))
		}
	}
	// 3：子会话的回合身份必须来自**单一来源**（usecase.subagentTurnID），
	// 三条 attach 路径必须铸出同一种形状的 id，否则某个调用方会静默拿不到身份。
	if strings.Contains(code, "session.NewMindFSID(") {
		t.Fatal("appcontext_workflow.go 又在自己铸子会话回合身份：请改用 usecase.subagentTurnID()，" +
			"三处各自铸会让某条 attach 路径静默拿不到 id")
	}
	if !strings.Contains(code, "turnID") {
		t.Fatal("executeWorkflowTurn 没有接收子会话回合身份：请把它交给收尾路径")
	}

	// 2：收尾不得传哨兵身份。EndSessionTurn 的最后一个参数是回合身份；
	// 第三个参数是 requestID，服务端发起的轮次本来就没有 request id，传 "" 是对的。
	zeroID := regexp.MustCompile(`EndSessionTurn\([^)]*,\s*0\s*\)`)
	emptyID := regexp.MustCompile(`EndSessionTurn\([^)]*,\s*""\s*\)`)
	for i, line := range strings.Split(code, "\n") {
		if !zeroID.MatchString(line) && !emptyID.MatchString(line) {
			continue
		}
		t.Fatalf("appcontext_workflow.go:%d 把回合身份写成哨兵值传给 EndSessionTurn: %q",
			i+1, strings.TrimSpace(line))
	}
}

// workflowTurnBody 截出 executeWorkflowTurn 的函数体（到下一个顶层 func 之前）。
// 粗略但够用：只看方法内的置 pending 调用点，避免把定义行当调用点。
func workflowTurnBody(src string) string {
	start := strings.Index(src, "func (s *AppContext) executeWorkflowTurn(")
	if start < 0 {
		return ""
	}
	rest := src[start:]
	// 下一个顶层声明：空行后的 "func "。函数体内没有空行+func 的形状。
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