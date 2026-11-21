package feishu

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeLarkWithEvent 是一个能扮演两种角色的 lark-cli：普通调用（send / patch）
// 返回发送成功；`event consume` 打出一行真实的卡片点击事件后挂住不退出 ——
// 与线上长连接的行为一致，也让监听循环不必靠反复重启来喂事件。
func fakeLarkWithEvent(t *testing.T, logPath, readyPath, eventJSON string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "lark-cli")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + logPath + "\n" +
		"if [ \"$1\" = \"event\" ]; then\n" +
		"  i=0\n" +
		"  while [ ! -f " + readyPath + " ]; do i=$((i+1)); [ $i -ge 400 ] && exit 0; sleep 0.05; done\n" +
		"  printf '%s\\n' '" + eventJSON + "'\n" +
		"  sleep 30\n" +
		"  exit 0\n" +
		"fi\n" +
		"printf '%s\\n' '{\"ok\":true,\"data\":{\"message_id\":\"om_1\"}}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// 一次点击要真的走完「长连接收到事件 → 认出是 owner → 先回执 → 再交给 MindFS」
// 这整条路。分段的单测都过了，也不能说明它们接起来是通的。
func TestClickFromEventStreamReachesAnswerHandler(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "calls.txt")
	event := `{"type":"card.action.trigger","event_id":"e1","operator_id":"ou_owner","message_id":"om_1","action_tag":"button","action_name":"card_button_click","action_value":"{\"card\":\"gate-nr-1\",\"choice\":0}"}`
	readyPath := filepath.Join(t.TempDir(), "ready")
	bin := fakeLarkWithEvent(t, logPath, readyPath, event)

	st := NewStore(filepath.Join(t.TempDir(), "cards.json"))
	if err := st.Load(); err != nil {
		t.Fatal(err)
	}
	svc := New(Config{
		OwnerOpenID:     "ou_owner",
		LarkCLIPath:     bin,
		CardTTL:         time.Hour,
		DeliverInterval: 10 * time.Millisecond,
		Now:             fixedNow,
		Logf:            noLog,
	}, st, &CLI{Path: bin, Timeout: 2 * time.Second})

	applied := make(chan Answer, 1)
	svc.SetAnswerHandler(func(_ context.Context, a Answer) error {
		applied <- a
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := svc.Notify(ctx, gateNotice("gate-nr-1")); err != nil {
		t.Fatal(err)
	}
	svc.Start(ctx)

	waitFor(t, 10*time.Second, func() bool {
		calls, err := os.ReadFile(logPath)
		return err == nil && strings.Contains(string(calls), "mindfs-card-gate-nr-1")
	})
	// 真实顺序是「卡片先发出去，用户才可能点」。放行假事件流，让它按真实时序到达。
	if err := os.WriteFile(readyPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	var answer Answer
	select {
	case answer = <-applied:
	case <-time.After(10 * time.Second):
		t.Fatalf("点击没有到达应用层：\n%s", readText(t, logPath))
	}
	if answer.CardID != "gate-nr-1" || answer.Kind != KindGate || answer.Decision != "approve" || answer.TaskID != "task-1" {
		t.Fatalf("answer = %+v", answer)
	}

	waitFor(t, 5*time.Second, func() bool {
		c, ok := st.Get("gate-nr-1")
		return ok && c.State == StateDone
	})

	joined := readText(t, logPath)
	ackAt := strings.Index(joined, "messages patch")
	eventAt := strings.Index(joined, "event")
	if ackAt < 0 || eventAt < 0 || eventAt > ackAt {
		t.Fatalf("回执必须发生在收到点击之后：\n%s", joined)
	}
}
