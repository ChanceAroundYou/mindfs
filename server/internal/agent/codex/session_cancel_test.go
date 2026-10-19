package codex

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	codexsdk "github.com/fanwenlin/codex-go-sdk/codex"
)

// cancelAwareExec 复刻真 exec 在回合被取消时的行为：CodexExecArgs.Context
// （就是 mindfs 的 turnCtx）一断，app-server 进程被杀、输出通道关闭。
// SDK 的转发协程于是直接 close(events) —— 没有 TurnCompleted，也没有错误事件：
// 「这一轮是被停掉的」这个原因**只存在于 ctx 里**，流里一个字都没有。
type cancelAwareExec struct {
	started chan struct{}
	once    sync.Once
}

func (e *cancelAwareExec) Run(args codexsdk.CodexExecArgs) <-chan codexsdk.ExecResult {
	e.once.Do(func() { close(e.started) })
	ch := make(chan codexsdk.ExecResult)
	go func() {
		<-args.Context.Done()
		close(ch)
	}()
	return ch
}

func (e *cancelAwareExec) RPCCall(_ context.Context, _ string, _ interface{}) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

// stoppedTurnMustNotLookRecoverable 钉住「停止按钮」在 codex 上真的能停。
//
// 2026-10-19 在 pc-win 上实测：按停止 → SDK 收到 turn/interrupt → 事件通道直接关闭
// → mindfs 报 "codex response stream ended unexpectedly" → 恢复循环把它当成可重试的
// 失败，action=continue 重发，agent 又被拉起来跑（用户连按七次都停不下来）。
// 判据是**错误本身**：取消必须报成 context.Canceled，恢复循环才认得出「别重试」。
func TestSendMessageReportsCancellationWhenTurnIsStopped(t *testing.T) {
	exec := &cancelAwareExec{started: make(chan struct{})}
	client := codexsdk.NewCodexWithExec(exec, codexsdk.CodexOptions{})
	s := &session{
		client:   client,
		thread:   client.ResumeThread("thread-1", codexsdk.ThreadOptions{}),
		threadID: "thread-1",
	}

	done := make(chan error, 1)
	go func() { done <- s.SendMessage(context.Background(), "hi") }()

	// 等 exec 真的跑起来：s.turn.Begin 在 RunStreamed 之前，跑到这里说明
	// 取消句柄已经注册，不会出现「按了停止但没人接」的竞态。
	select {
	case <-exec.started:
	case <-time.After(10 * time.Second):
		t.Fatal("exec 未启动")
	}
	if err := s.CancelCurrentTurn(); err != nil {
		t.Fatalf("CancelCurrentTurn: %v", err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if strings.Contains(err.Error(), "stream ended unexpectedly") {
			t.Fatalf("被取消的回合不该报成流意外结束：%v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("取消后 SendMessage 没有返回")
	}
}

// closedRunExec 立刻关闭输出通道：模拟 app-server 真的死了（不是被取消）。
type closedRunExec struct{}

func (e *closedRunExec) Run(codexsdk.CodexExecArgs) <-chan codexsdk.ExecResult {
	ch := make(chan codexsdk.ExecResult)
	close(ch)
	return ch
}

func (e *closedRunExec) RPCCall(_ context.Context, _ string, _ interface{}) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

// 对照组：真·意外结束不能被当成取消 —— 否则进程死了也会被当「用户按了停止」，
// 错误信息会骗人，恢复循环的判据也会走偏。
func TestSendMessageUnexpectedStreamEndIsNotCancellation(t *testing.T) {
	exec := &closedRunExec{}
	client := codexsdk.NewCodexWithExec(exec, codexsdk.CodexOptions{})
	s := &session{
		client:   client,
		thread:   client.ResumeThread("thread-1", codexsdk.ThreadOptions{}),
		threadID: "thread-1",
	}
	err := s.SendMessage(context.Background(), "hi")
	if err == nil {
		t.Fatal("回合没完成就该报错")
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("意外结束被误报成取消：%v", err)
	}
	if !strings.Contains(err.Error(), "stream ended unexpectedly") {
		t.Fatalf("err = %v, want stream ended unexpectedly", err)
	}
}
