package codex

import (
	"context"
	"sync"
	"testing"

	codexsdk "github.com/fanwenlin/codex-go-sdk/codex"

	agenttypes "mindfs/server/internal/agent/types"
)

// compactTurnExec 复刻真 AppServerExec 在 `/compact` 上的事件形状：收到
// item/completed(contextCompaction) 就返回，**永远不会再发 turn.completed**
// （SDK 的 runCompact 就在这条通知上返回，见 codex/app_server_exec.go）。
// 只有不支持 RunCompact 的 exec 才会走 syntheticMessageStream 补一条 turn.completed。
type compactTurnExec struct {
	mu       sync.Mutex
	compacts int
	lines    []string
}

func (e *compactTurnExec) Run(codexsdk.CodexExecArgs) <-chan codexsdk.ExecResult {
	// 真 exec 的普通回合路径。与 /compact 一样按脚本吐事件 —— 对照用例需要它
	// 「中途自动压缩过、随后流断掉」这个形状。它是普通回合不是靠 RunCompact 区分，
	// 而是靠下面的 compactCalls 计数。
	return e.scripted()
}

func (e *compactTurnExec) RunCompact(codexsdk.CodexExecArgs) <-chan codexsdk.ExecResult {
	e.mu.Lock()
	e.compacts++
	e.mu.Unlock()
	return e.scripted()
}

func (e *compactTurnExec) scripted() <-chan codexsdk.ExecResult {
	e.mu.Lock()
	lines := append([]string(nil), e.lines...)
	e.mu.Unlock()
	ch := make(chan codexsdk.ExecResult, len(lines))
	for _, line := range lines {
		ch <- codexsdk.ExecResult{Line: line}
	}
	close(ch)
	return ch
}

func (e *compactTurnExec) compactCalls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.compacts
}

func newCompactTurnSession(exec *compactTurnExec) *session {
	client := codexsdk.NewCodexWithExec(exec, codexsdk.CodexOptions{})
	return &session{
		client:   client,
		thread:   client.ResumeThread("thread-1", codexsdk.ThreadOptions{}),
		threadID: "thread-1",
	}
}

func contextCompactionLines() []string {
	return []string{
		`{"type":"thread.started","threadId":"thread-1"}`,
		`{"type":"item.started","item":{"id":"cmp-1","type":"contextCompaction","status":"running"}}`,
		`{"type":"item.completed","item":{"id":"cmp-1","type":"contextCompaction","status":"complete"}}`,
	}
}

// 现场（pc-wsl，2026-11-24 16:57）：用户手动 /compact，日志里没有 output.done
// （没有 TurnCompletedEvent），紧接着 turn.send.no_response +
// `codex response stream ended unexpectedly`，界面上于是弹错误 —— 但压缩其实成功了。
// 根因在 SDK：/compact 走 RunCompact，收到 item/completed(contextCompaction) 就返回，
// 协议里根本没有 turn.completed。适配器把「没见到 TurnCompleted」一律当流意外结束，
// 所以每次手动压缩都报一次。压缩完成的证据就是那条 completed 的 contextCompaction。
func TestSendMessageCompactTurnSucceedsWithoutTurnCompleted(t *testing.T) {
	exec := &compactTurnExec{lines: contextCompactionLines()}
	s := newCompactTurnSession(exec)

	var seen []agenttypes.Event
	s.OnUpdate(func(event agenttypes.Event) { seen = append(seen, event) })

	if err := s.SendMessage(context.Background(), "/compact"); err != nil {
		t.Fatalf("手动压缩回合不该报错，got %v", err)
	}
	if got := exec.compactCalls(); got != 1 {
		// 走到 syntheticMessageStream 也能「成功」，但那就不是真实路径了 ——
		// 这条断言保证用例钉的是 RunCompact。
		t.Fatalf("RunCompact calls = %d, want 1", got)
	}
	complete := false
	for _, event := range seen {
		if event.Type != agenttypes.EventTypeCompact {
			continue
		}
		if notice, ok := event.Data.(agenttypes.CompactNotice); ok && notice.Status == "complete" {
			complete = true
		}
	}
	if !complete {
		t.Fatalf("没有看到 completed 的压缩事件：%#v", seen)
	}
}

// 对照：普通回合中途自动压缩过、随后进程死掉，仍然必须报「流意外结束」。
// 判据不能只看「见过压缩」—— 否则真正的崩溃会被当成成功回合收尾。
func TestSendMessageNormalTurnAfterCompactionStillReportsUnexpectedEnd(t *testing.T) {
	exec := &compactTurnExec{lines: contextCompactionLines()}
	s := newCompactTurnSession(exec)

	var compacted bool
	s.OnUpdate(func(event agenttypes.Event) {
		if event.Type == agenttypes.EventTypeCompact {
			compacted = true
		}
	})

	err := s.SendMessage(context.Background(), "hi")
	if err == nil {
		t.Fatal("普通回合没完成就该报错")
	}
	if !containsUnexpectedStreamEnd(err) {
		t.Fatalf("err = %v, want stream ended unexpectedly", err)
	}
	if got := exec.compactCalls(); got != 0 {
		t.Fatalf("普通回合不该走 RunCompact，calls = %d", got)
	}
	if !compacted {
		t.Fatal("用例前提不成立：这一轮并没有真的出现压缩事件")
	}
}

func containsUnexpectedStreamEnd(err error) bool {
	return err != nil && err.Error() == "codex response stream ended unexpectedly"
}

// 压缩回合只走到「开始压缩」就断了（进程死在压缩中途）：这一轮并没有干完，
// 仍然要报流意外结束。放行判据认的是 completed，不是「见过压缩条目」。
func TestSendMessageCompactTurnWithoutCompletedCompactionStillReportsUnexpectedEnd(t *testing.T) {
	exec := &compactTurnExec{lines: []string{
		`{"type":"thread.started","threadId":"thread-1"}`,
		`{"type":"item.started","item":{"id":"cmp-1","type":"contextCompaction","status":"running"}}`,
	}}
	s := newCompactTurnSession(exec)

	err := s.SendMessage(context.Background(), "/compact")
	if err == nil {
		t.Fatal("压缩没完成就该报错")
	}
	if !containsUnexpectedStreamEnd(err) {
		t.Fatalf("err = %v, want stream ended unexpectedly", err)
	}
}

// 判据必须与 SDK 的 slash 解析同形，也不能漏掉「SDK 里根本没有 compact 这个命令」
// 的漂移 —— 后者会让上面那条放行永远不成立（症状会退回成「每次都弹错误」）。
func TestIsCompactSlashCommandMatchesSDKCommandList(t *testing.T) {
	supported := false
	for _, command := range codexsdk.SupportedSlashCommands() {
		if command.Name != "compact" {
			continue
		}
		supported = true
		if !isCompactSlashCommand("/" + command.Name) {
			t.Fatalf("SDK 的 %q 命令没被识别成压缩回合", command.Name)
		}
	}
	if !supported {
		t.Fatal("SDK 不再提供 compact 斜杠命令，放行判据已失效")
	}

	cases := map[string]bool{
		"/compact":        true,
		"  /compact  ":    true,
		"/compact\nmore":  true,
		"/compact 现在压":    true,
		"/compact\t":      true,
		"/compacted":      false,
		"/Compact":        false,
		"compact":         false,
		"/review":         false,
		"请帮我 /compact 一下": false,
	}
	for input, want := range cases {
		if got := isCompactSlashCommand(input); got != want {
			t.Fatalf("isCompactSlashCommand(%q) = %v, want %v", input, got, want)
		}
	}
}
