package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/notify"
	"mindfs/server/internal/notifyscript"
	"mindfs/server/internal/session"
)

// captureNotifyScript 用真实 notifyscript.Service 把每条通知落到文件：
// 走的是生产路径（EndSessionTurn → shouldNotifySessionDone → 载荷构造 → 脚本），
// 比单独测 shouldNotifySessionDone 更能证明「这一轮到底有没有发出去」。
func captureNotifyScript(t *testing.T) (string, *notifyscript.Service) {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "notifications.jsonl")
	script := filepath.Join(dir, "capture.sh")
	body := "#!/bin/sh\ncat >> \"$MINDFS_NOTIFY_OUT\"\nprintf '\\n' >> \"$MINDFS_NOTIFY_OUT\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	t.Setenv("MINDFS_NOTIFY_OUT", out)
	return out, notifyscript.NewService(notifyscript.Config{Script: script, Timeout: 5 * time.Second})
}

func waitNotificationLines(t *testing.T, path string, want int) []notify.Payload {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var payloads []notify.Payload
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil {
			payloads = payloads[:0]
			for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				var payload notify.Payload
				if err := json.Unmarshal([]byte(line), &payload); err != nil {
					t.Fatalf("notify payload is not JSON: %v (%s)", err, line)
				}
				payloads = append(payloads, payload)
			}
			if len(payloads) >= want {
				return payloads
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return payloads
}

// startTopLevelTurn 走顶层用户留言的入口：这条 pending 不是子会话，完成时应当提醒。
func startTopLevelTurn(app *AppContext, rootID, sessionKey, title, content, turnID string) string {
	return app.BroadcastSessionUserMessageAt(rootID, sessionKey, session.TypeChat, title, "claude", "model", "Model", "mode", "effort", "fast", false, content, time.Now().UTC(), 1, turnID)
}

func appendPendingSummary(app *AppContext, sessionKey, summary string) {
	app.GetSessionStreamHub().AppendReplyEvent(sessionKey, StreamEvent{
		Type: "message_chunk",
		Data: agenttypes.MessageChunk{Content: summary},
	})
}

func TestEndSessionTurnSilentCompletionStaysQuiet(t *testing.T) {
	app, rootID, sessionKey := newTaskQuestionTestApp(t, "task-silent")
	out, service := captureNotifyScript(t)
	app.Notify = service

	turnID := startTopLevelTurn(app, rootID, sessionKey, "任务节点", "跑一个节点", "user-silent")
	appendPendingSummary(app, sessionKey, "内部步骤跑完了")
	app.MarkPendingCompletionSilent(rootID, sessionKey, turnID)
	app.EndSessionTurn(rootID, sessionKey, "req-silent", turnID)

	// 正常回合作为对照：同一个 AppContext / 同一个脚本，必须能收到一条。
	normalTurn := startTopLevelTurn(app, rootID, sessionKey, "普通会话", "普通留言", "user-normal")
	appendPendingSummary(app, sessionKey, "普通回合跑完了")
	app.EndSessionTurn(rootID, sessionKey, "req-normal", normalTurn)

	payloads := waitNotificationLines(t, out, 1)
	if len(payloads) != 1 {
		t.Fatalf("silent 回合不应产生通知，实际收到 %d 条：%+v", len(payloads), payloads)
	}
	if got := payloads[0].Title; got != "普通会话 · 完成" {
		t.Fatalf("通知标题 = %q；提醒不应带项目 / mindfs 前缀", got)
	}
}

func TestEndSessionTurnSubSessionStaysQuiet(t *testing.T) {
	app, rootID, sessionKey := newTaskQuestionTestApp(t, "task-sub")
	out, service := captureNotifyScript(t)
	app.Notify = service

	appendPendingSummary(app, sessionKey, "子任务跑完了")
	// SetPendingReply 就是子会话 attach 的入口：这条 pending 自带 SubSession。
	subTurn := app.SetSessionPendingReply(rootID, sessionKey, "子任务", "mindfs-sub")
	app.EndSessionTurn(rootID, sessionKey, "req-sub", subTurn)

	normalTurn := startTopLevelTurn(app, rootID, sessionKey, "主会话", "普通留言", "user-main")
	appendPendingSummary(app, sessionKey, "主会话跑完了")
	app.EndSessionTurn(rootID, sessionKey, "req-main", normalTurn)

	payloads := waitNotificationLines(t, out, 1)
	if len(payloads) != 1 || payloads[0].Title != "主会话 · 完成" {
		t.Fatalf("子会话完成不应通知，实际 %+v", payloads)
	}
}

// readNotificationLines 只读一次、不等待：用来断言「过一会儿也没有新通知」。
func readNotificationLines(t *testing.T, path string) []notify.Payload {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var payloads []notify.Payload
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var payload notify.Payload
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			t.Fatalf("notify payload is not JSON: %v (%s)", err, line)
		}
		payloads = append(payloads, payload)
	}
	return payloads
}

// TestEndSessionTurnRepeatedDoneStaysQuiet 钉住「同一轮的第二次收尾必须彻底静默」。
//
// 现场（2026-11-22 11:53:53，wsl 的 llm-deploy 一轮）：子代理各自先由
// OnSubAgentTurnFinished 收尾，pending 被 ClearSessionPending 整条删掉；回合结束时
// executeWorkflowTurn 的 defer 又逐个再收一次 —— 第二次拿到的是**零值快照**，
// SubSession / SilentCompletion 全变成 false，闸门放行，桌面 PWA 一次收到 4 条
// 「子会话标题 · 完成」。完成账本有 RootID 守卫（所以轮询侧看不见），Web Push 没有。
func TestEndSessionTurnRepeatedDoneStaysQuiet(t *testing.T) {
	app, rootID, sessionKey := newTaskQuestionTestApp(t, "task-repeat")
	out, service := captureNotifyScript(t)
	app.Notify = service

	appendPendingSummary(app, sessionKey, "子任务跑完了")
	subTurn := app.SetSessionPendingReply(rootID, sessionKey, "子任务", "mindfs-sub-repeat")
	app.EndSessionTurn(rootID, sessionKey, "", subTurn)
	app.EndSessionTurn(rootID, sessionKey, "", subTurn) // 重放：pending 已清

	// 对照：普通回合的真实收尾要发一条，重放不得再发第二条。
	normalTurn := startTopLevelTurn(app, rootID, sessionKey, "主会话", "普通留言", "user-main-repeat")
	appendPendingSummary(app, sessionKey, "主会话跑完了")
	app.EndSessionTurn(rootID, sessionKey, "", normalTurn)
	app.EndSessionTurn(rootID, sessionKey, "", normalTurn)

	if payloads := waitNotificationLines(t, out, 1); len(payloads) < 1 {
		t.Fatal("普通回合的完成提醒没有发出来，对照失效")
	}
	// 通知脚本是异步打的：等到一条之后再看一眼，重放产生的那条会在这段时间落地。
	time.Sleep(300 * time.Millisecond)
	payloads := readNotificationLines(t, out)
	if len(payloads) != 1 {
		t.Fatalf("同一轮的重放收尾不应再发通知，实际 %d 条：%+v", len(payloads), payloads)
	}
	if got := payloads[0].Title; got != "主会话 · 完成" {
		t.Fatalf("通知标题 = %q，want 主会话 · 完成", got)
	}
}

// TestEndSessionTurnWithoutPendingStaysQuiet 钉住「没有 pending 的收尾不得凭空推一条」。
//
// 这类调用连回合标题都没有，只能回落到会话库里的名字 —— 正是截图里那条
// 「Implement shared prompt expansion core · 完成」的来源。
func TestEndSessionTurnWithoutPendingStaysQuiet(t *testing.T) {
	app, rootID, sessionKey := newTaskQuestionTestApp(t, "task-nopending")
	out, service := captureNotifyScript(t)
	app.Notify = service

	app.EndSessionTurn(rootID, sessionKey, "", "")
	time.Sleep(300 * time.Millisecond)
	if payloads := readNotificationLines(t, out); len(payloads) != 0 {
		t.Fatalf("没有 pending 的收尾不应发通知，实际 %+v", payloads)
	}
}

func TestEndSessionTurnNotificationBodyIsPlainText(t *testing.T) {
	app, rootID, sessionKey := newTaskQuestionTestApp(t, "task-body")
	out, service := captureNotifyScript(t)
	app.Notify = service

	turnID := startTopLevelTurn(app, rootID, sessionKey, "可读性", "跑一下", "user-body")
	appendPendingSummary(app, sessionKey, "结果：\n```go\nfmt.Println(\"x\")\n```\n**结论** 已写入 `main.go`\n- 第一项")
	app.EndSessionTurn(rootID, sessionKey, "req-body", turnID)

	payloads := waitNotificationLines(t, out, 1)
	if len(payloads) != 1 {
		t.Fatalf("通知条数 = %d，want 1", len(payloads))
	}
	body := payloads[0].Body
	for _, marker := range []string{"```", "**", "`", "- "} {
		if strings.Contains(body, marker) {
			t.Fatalf("正文仍含 Markdown 标记 %q：%q", marker, body)
		}
	}
	if !strings.Contains(body, "fmt.Println") {
		t.Fatalf("代码内容应保留（只去标记），实际 %q", body)
	}
	if !strings.Contains(body, "第一项") {
		t.Fatalf("列表内容应保留，实际 %q", body)
	}
}

func TestEndSessionTurnNotificationKeepsTailWhenTooLong(t *testing.T) {
	app, rootID, sessionKey := newTaskQuestionTestApp(t, "task-tail")
	out, service := captureNotifyScript(t)
	app.Notify = service

	// 先塞进超过上限的内容再收尾：客户端看到的必须是「... + 结尾」，不是从头截断。
	turnID := startTopLevelTurn(app, rootID, sessionKey, "截断", "跑一下", "user-tail")
	summary := strings.Repeat("前", notify.BodyMaxRunes+50) + "最后一句"
	appendPendingSummary(app, sessionKey, summary)
	app.EndSessionTurn(rootID, sessionKey, "req-tail", turnID)

	payloads := waitNotificationLines(t, out, 1)
	if len(payloads) != 1 {
		t.Fatalf("通知条数 = %d，want 1", len(payloads))
	}
	body := payloads[0].Body
	if !strings.HasPrefix(body, "...") || !strings.HasSuffix(body, "最后一句") {
		t.Fatalf("正文应按尾部截断（... + 结尾），实际 %q", body)
	}
	if n := len([]rune(body)); n > notify.BodyMaxRunes+3 {
		t.Fatalf("正文长度 = %d，超过上限 %d", n, notify.BodyMaxRunes+3)
	}
}
