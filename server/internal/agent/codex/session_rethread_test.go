package codex

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	codexsdk "github.com/fanwenlin/codex-go-sdk/codex"
)

// rethreadFakeExec 同时扮演 app-server 的 RPC 端与回合流端。
//
// 2026-11-22 pc 节点上真实发生过的事故需要它来复现：config/read 与 thread/read
// 都正常回答（线程记录 llmux、当前配置 mindfs-chatgpt），thread/fork 却因为
// app-server 正在初始化 MCP manager 而跑了几十秒 —— 旧实现把 fork 与前面的读取
// 塞进同一个 20 秒预算，超时后返回 ("", false)，与「不需要 fork」完全同形，
// 于是打开会话的人照样 resume 了那条旧供应商线程，之后的每一轮都撞 -32600。
type rethreadFakeExec struct {
	mu sync.Mutex

	provider  string
	persisted string
	forkID    string
	forkDelay time.Duration

	runs  int
	lines []string
	// turns 按回合分组：一个回合的若干行必须在同一次 Run 里流完。lines 是它的
	// 简写形式（一回合一行），两者设置其一。
	turns    [][]string
	rpcCalls []string
}

func (e *rethreadFakeExec) Run(codexsdk.CodexExecArgs) <-chan codexsdk.ExecResult {
	e.mu.Lock()
	n := e.runs
	e.runs++
	turn := []string(nil)
	if n < len(e.turns) {
		turn = e.turns[n]
	} else if len(e.turns) > 0 {
		turn = e.turns[len(e.turns)-1]
	} else if n < len(e.lines) {
		turn = e.lines[n : n+1]
	} else if len(e.lines) > 0 {
		turn = e.lines[len(e.lines)-1:]
	}
	e.mu.Unlock()

	ch := make(chan codexsdk.ExecResult, len(turn))
	for _, line := range turn {
		ch <- codexsdk.ExecResult{Line: line}
	}
	close(ch)
	return ch
}

func (e *rethreadFakeExec) RPCCall(ctx context.Context, method string, _ interface{}) (json.RawMessage, error) {
	e.mu.Lock()
	e.rpcCalls = append(e.rpcCalls, method)
	provider, persisted, forkID, forkDelay := e.provider, e.persisted, e.forkID, e.forkDelay
	e.mu.Unlock()

	switch method {
	case "config/read":
		return json.RawMessage(`{"config":{"model_provider":"` + provider + `"}}`), nil
	case "thread/read":
		return json.RawMessage(`{"thread":{"modelProvider":"` + persisted + `"}}`), nil
	case "thread/fork":
		if forkDelay > 0 {
			select {
			case <-time.After(forkDelay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return json.RawMessage(`{"thread":{"id":"` + forkID + `"}}`), nil
	default:
		return nil, errString("unexpected RPC: " + method)
	}
}

func (e *rethreadFakeExec) methods() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string{}, e.rpcCalls...)
}

func (e *rethreadFakeExec) runCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.runs
}

func newRethreadFakeExec() *rethreadFakeExec {
	return &rethreadFakeExec{
		provider:  "mindfs-chatgpt",
		persisted: "llmux",
		forkID:    "01a203d2-f784-76b2-9856-dbfd674a366a",
	}
}

// 需要 fork（元数据不一致）而 fork 本身失败时，绝不能返回「不需要 fork」——
// 那会让调用方照常 resume 旧供应商线程，此后每一轮都撞 -32600。
func TestRethreadCodexResumeReportsForkFailure(t *testing.T) {
	oldFork := codexProviderForkTimeout
	codexProviderForkTimeout = 20 * time.Millisecond
	defer func() { codexProviderForkTimeout = oldFork }()

	exec := newRethreadFakeExec()
	exec.forkDelay = 300 * time.Millisecond
	client := codexsdk.NewCodexWithExec(exec, codexsdk.CodexOptions{})

	forked, ok, err := rethreadCodexResumeOnProviderMismatch(context.Background(), client, "01a20360", codexsdk.ThreadOptions{WorkingDirectory: "/tmp"}, "/tmp")
	if err == nil {
		t.Fatalf("fork failure must be reported, got (%q, %v, nil)", forked, ok)
	}
	if ok || forked != "" {
		t.Fatalf("failed rethread returned (%q, %v), want empty/false", forked, ok)
	}
	if !isCodexProviderRethreadError(err) {
		t.Fatalf("error %v must be recognizable as a provider rethread failure", err)
	}
	// 已经发起过 thread/fork，说明确实走到了这一步，不是被前面的读取挡下。
	if got := exec.methods(); !containsMethod(got, "thread/fork") {
		t.Fatalf("rpc methods = %v, want thread/fork attempted", got)
	}
}

// fork 必须有自己的预算：pc 上真实的 fork 跑了 30.8 秒，而 config/read 与
// thread/read 之前已经把同一份 20 秒预算吃掉大半。这里把读取预算压到
// 200ms、fork 预算留在 5s，让 fork 跑 400ms —— 旧实现（共用一个预算）会超时
// 并静默放弃，新实现必须成功返回 fork 出来的线程。
func TestRethreadCodexResumeForkHasOwnBudget(t *testing.T) {
	oldProbe, oldFork := codexResumeProbeTimeout, codexProviderForkTimeout
	codexResumeProbeTimeout = 200 * time.Millisecond
	codexProviderForkTimeout = 5 * time.Second
	defer func() { codexResumeProbeTimeout, codexProviderForkTimeout = oldProbe, oldFork }()

	exec := newRethreadFakeExec()
	exec.forkDelay = 400 * time.Millisecond
	client := codexsdk.NewCodexWithExec(exec, codexsdk.CodexOptions{})

	forked, ok, err := rethreadCodexResumeOnProviderMismatch(context.Background(), client, "01a20360", codexsdk.ThreadOptions{WorkingDirectory: "/tmp"}, "/tmp")
	if err != nil {
		t.Fatalf("fork within its own budget must succeed, got err=%v", err)
	}
	if !ok || forked != exec.forkID {
		t.Fatalf("rethread = (%q, %v), want (%q, true)", forked, ok, exec.forkID)
	}
}

// 已经中毒的会话（打开时静默 resume 了旧供应商线程、池子里缓存着它）不能再
// 每轮都失败：SendMessage 撞到供应商不一致时要当场 fork 到当前供应商并重试。
func TestSendMessageRethreadsThreadOnProviderMismatch(t *testing.T) {
	exec := newRethreadFakeExec()
	exec.lines = []string{
		`{"type":"turn.failed","error":{"message":"app server error (-32600): This chat uses model provider \"llmux\", which is no longer configured. Restore this provider's configuration to continue."}}`,
		codexTurnCompletedLine,
	}
	client := codexsdk.NewCodexWithExec(exec, codexsdk.CodexOptions{})
	s := &session{
		client:     client,
		thread:     client.ResumeThread("01a20360", codexsdk.ThreadOptions{WorkingDirectory: "/tmp"}),
		threadOpts: codexsdk.ThreadOptions{WorkingDirectory: "/tmp"},
		threadID:   "01a20360",
		sessionKey: "test-session",
	}

	if err := s.SendMessage(context.Background(), "hi"); err != nil {
		t.Fatalf("SendMessage must recover from the provider mismatch, got %v", err)
	}
	if got := s.SessionID(); got != exec.forkID {
		t.Fatalf("thread id = %q, want the forked %q", got, exec.forkID)
	}
	if got := exec.runCount(); got != 2 {
		t.Fatalf("turns = %d, want 2 (failed + retried)", got)
	}
}

// 不是供应商问题时不许走 fork：否则任何一次普通回合失败都会白白 fork 一份历史。
func TestSendMessageDoesNotRethreadOnOtherFailures(t *testing.T) {
	exec := newRethreadFakeExec()
	exec.lines = []string{`{"type":"turn.failed","error":{"message":"model overloaded"}}`}
	client := codexsdk.NewCodexWithExec(exec, codexsdk.CodexOptions{})
	s := &session{
		client:     client,
		thread:     client.ResumeThread("01a20360", codexsdk.ThreadOptions{WorkingDirectory: "/tmp"}),
		threadOpts: codexsdk.ThreadOptions{WorkingDirectory: "/tmp"},
		threadID:   "01a20360",
		sessionKey: "test-session",
	}

	err := s.SendMessage(context.Background(), "hi")
	if err == nil {
		t.Fatal("SendMessage must surface a non-provider failure")
	}
	if got := exec.methods(); len(got) != 0 {
		t.Fatalf("unexpected rpc calls %v, want none", got)
	}
	if !strings.Contains(err.Error(), "model overloaded") {
		t.Fatalf("error = %v, want the original cause", err)
	}
}

// 打开会话时换绑失败必须中断打开，不能接着 resume 旧供应商线程。
//
// 这是 2026-11-22 pc 上的真实顺序：fork 超时被静默吞掉 → 照样 resume 了 llmux
// 线程 → 11 秒后这一轮撞 -32600。打开就失败虽然不好受，但上层会丢掉绑定、新开
// 一条会话；把用户绑死在一条永远发不出去的线程上才是真正的故障。
func TestOpenSessionDoesNotResumeStaleProviderThread(t *testing.T) {
	oldFork := codexProviderForkTimeout
	codexProviderForkTimeout = 20 * time.Millisecond
	defer func() { codexProviderForkTimeout = oldFork }()

	exec := newRethreadFakeExec()
	exec.forkDelay = 300 * time.Millisecond
	runtime := NewRuntime()
	runtime.clients["codex"] = codexsdk.NewCodexWithExec(exec, codexsdk.CodexOptions{})

	sess, err := runtime.OpenSession(context.Background(), OpenOptions{
		AgentName:       "codex",
		SessionKey:      "test-session",
		ResumeSessionID: "01a20360",
		RootPath:        "/tmp",
	})
	if err == nil {
		t.Fatalf("OpenSession must fail rather than resume the stale thread, got session %v", sess)
	}
	if !isCodexProviderRethreadError(err) {
		t.Fatalf("error %v must be recognizable as a provider rethread failure", err)
	}
	// 拒绝 resume 的证据：一旦 resume 了，后面每一轮才会报 -32600；这里要求
	// 连一次 thread/resume 都不该发生（换绑判定完全走 thread/read）。
	if got := exec.methods(); containsMethod(got, "thread/resume") {
		t.Fatalf("rpc methods = %v, must not resume after a failed rethread", got)
	}
}

// 换绑之后重试仍然失败时，报出来的必须是真实原因，不能是「另一个 Codex 客户端
// 正占用该会话」—— 那句会把用户引到完全无关的方向去排查。
func TestSendMessageRethreadFailureKeepsRealCause(t *testing.T) {
	exec := newRethreadFakeExec()
	exec.lines = []string{
		`{"type":"turn.failed","error":{"message":"app server error (-32600): This chat uses model provider \"llmux\", which is no longer configured."}}`,
		`{"type":"turn.failed","error":{"message":"model overloaded"}}`,
	}
	oldFork := codexProviderForkTimeout
	codexProviderForkTimeout = 20 * time.Millisecond
	defer func() { codexProviderForkTimeout = oldFork }()
	exec.forkDelay = 0
	client := codexsdk.NewCodexWithExec(exec, codexsdk.CodexOptions{})
	s := &session{
		client:     client,
		thread:     client.ResumeThread("01a20360", codexsdk.ThreadOptions{WorkingDirectory: "/tmp"}),
		threadOpts: codexsdk.ThreadOptions{WorkingDirectory: "/tmp"},
		threadID:   "01a20360",
		sessionKey: "test-session",
	}

	err := s.SendMessage(context.Background(), "hi")
	if err == nil {
		t.Fatal("SendMessage must report the retry failure")
	}
	if strings.Contains(err.Error(), "另一个 Codex 客户端") {
		t.Fatalf("error must not blame a writer conflict, got %v", err)
	}
	if !strings.Contains(err.Error(), "model overloaded") {
		t.Fatalf("error = %v, want the real cause", err)
	}
}

// 已经流出去一半内容的回合不许因为撞到供应商错误而重发：那一轮真正的失败原因
// 可能只是「线程被另一个客户端抢了」，重发会把用户消息写两遍。这条守卫与
// writer 冲突守卫同一个理由（见 session.emitted 字段注释）。
func TestSendMessageDoesNotRethreadAfterStreamingStarted(t *testing.T) {
	exec := newRethreadFakeExec()
	// 同一个回合里先流出正文、再报供应商错误 —— 这正是打开时静默 resume 了旧
	// 供应商线程之后的样子：模型已经开始答，答到一半 app-server 才发现配置不对。
	exec.turns = [][]string{{
		`{"type":"item.completed","item":{"id":"msg-1","type":"agentMessage","text":"partial answer"}}`,
		`{"type":"turn.failed","error":{"message":"app server error (-32600): This chat uses model provider \"llmux\", which is no longer configured."}}`,
	}}
	client := codexsdk.NewCodexWithExec(exec, codexsdk.CodexOptions{})
	s := &session{
		client:     client,
		thread:     client.ResumeThread("01a20360", codexsdk.ThreadOptions{WorkingDirectory: "/tmp"}),
		threadOpts: codexsdk.ThreadOptions{WorkingDirectory: "/tmp"},
		threadID:   "01a20360",
		sessionKey: "test-session",
	}

	err := s.SendMessage(context.Background(), "hi")
	if err == nil {
		t.Fatal("SendMessage must surface the failure after content was streamed")
	}
	if got := exec.methods(); len(got) != 0 {
		t.Fatalf("rpc calls = %v, must not rethread after streaming started", got)
	}
	if got := exec.runCount(); got != 1 {
		t.Fatalf("turns = %d, want 1 (no resend after partial output)", got)
	}
}

// 现场换绑必须走「换绑」回调，而不是复用「新线程」回调。
//
// 两者在池子里的动作不一样：新线程只需登记（RegisterThread），换绑还要标记
// 这条线程跨了供应商（MarkProviderSwitch），否则 fork 出来的历史里旧供应商的
// encrypted_content 会让下一轮整条请求被拒（G-PK）。如果这里退回 onThreadStart，
// 会话会被修好一次、然后以另一种方式立刻再死一次。
func TestRethreadThreadNotifiesProviderSwitch(t *testing.T) {
	exec := newRethreadFakeExec()
	exec.lines = []string{
		`{"type":"turn.failed","error":{"message":"app server error (-32600): This chat uses model provider \"llmux\", which is no longer configured."}}`,
		codexTurnCompletedLine,
	}
	client := codexsdk.NewCodexWithExec(exec, codexsdk.CodexOptions{})

	var started, rethreaded []string
	s := &session{
		client:     client,
		thread:     client.ResumeThread("01a20360", codexsdk.ThreadOptions{WorkingDirectory: "/tmp"}),
		threadOpts: codexsdk.ThreadOptions{WorkingDirectory: "/tmp"},
		threadID:   "01a20360",
		sessionKey: "test-session",
		onThreadStart: func(threadID string) {
			started = append(started, threadID)
		},
		onThreadRethreaded: func(threadID string) {
			rethreaded = append(rethreaded, threadID)
		},
	}

	if err := s.SendMessage(context.Background(), "hi"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if len(rethreaded) != 1 || rethreaded[0] != exec.forkID {
		t.Fatalf("rethread callbacks = %v, want [%s]", rethreaded, exec.forkID)
	}
	// 走换绑回调时就不要再走新线程回调：池子侧两个回调各自都会登记一次，
	// 双发只会让同一件事做两遍（且新线程回调漏掉标记，见上）。
	if len(started) != 0 {
		t.Fatalf("onThreadStart must not fire for a rethread, got %v", started)
	}
}

// 改模型 / 改 plan 模式会重新 ResumeThread 一条线程。如果那条线程还记着已经不再
// 配置的供应商，这条路径以前会把它原样留下 —— 用户接着发消息，撞的还是 -32600，
// 而且打开路径的修复救不到它（池子里缓存着会话，根本不会重新打开）。
// 两条设置路径都必须先把线程换绑到当前供应商。
func TestSetModelRethreadsStaleProviderThread(t *testing.T) {
	exec := newRethreadFakeExec()
	client := codexsdk.NewCodexWithExec(exec, codexsdk.CodexOptions{})
	s := &session{
		client:     client,
		thread:     client.ResumeThread("01a20360", codexsdk.ThreadOptions{WorkingDirectory: "/tmp"}),
		threadOpts: codexsdk.ThreadOptions{WorkingDirectory: "/tmp"},
		threadID:   "01a20360",
		sessionKey: "test-session",
	}

	if err := s.SetModel(context.Background(), "gpt-6-sol"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	if got := s.SessionID(); got != exec.forkID {
		t.Fatalf("thread id = %q, want the forked %q", got, exec.forkID)
	}
}

func containsMethod(methods []string, want string) bool {
	for _, method := range methods {
		if method == want {
			return true
		}
	}
	return false
}
