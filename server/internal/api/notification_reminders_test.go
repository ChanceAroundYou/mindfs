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
