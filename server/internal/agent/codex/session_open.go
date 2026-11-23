package codex

import (
	"context"
	"errors"
	"strings"
	"sync"

	"mindfs/server/internal/agent/logs"
	types "mindfs/server/internal/agent/types"

	codexsdk "github.com/fanwenlin/codex-go-sdk/codex"
)

type OpenOptions struct {
	AgentName             string
	SessionKey            string
	Model                 string
	Effort                string
	FastService           string
	PlanMode              bool
	Probe                 bool
	RootPath              string
	Command               string
	Args                  []string
	Env                   map[string]string
	DeveloperInstructions string
	ResumeSessionID       string
	ForkSessionID         string
	CodexUserOrdinal      *int
	// OnCompact is called with the thread id whenever Codex reports that it
	// compacted its own context. The pool uses it to tell the proxy that the
	// next request's prefix cache is cold.
	OnCompact func(threadID string)
	// OnThreadStarted reports the thread id once the app server assigns it.
	// A brand-new thread has no id at open time (StartThread returns before
	// `thread/start` runs), so this is how the pool learns which MindFS
	// session the proxy traffic belongs to.
	OnThreadStarted func(threadID string)
	// OnThreadRethreaded reports a thread that a running session forked onto
	// the current provider after a -32600 provider mismatch. The pool must do
	// both what OnThreadStarted does (register) and mark the thread as having
	// crossed providers, because the fork carries reasoning encrypted by the
	// old provider.
	OnThreadRethreaded func(threadID string)
}

type Runtime struct {
	mu      sync.Mutex
	clients map[string]*codexsdk.Codex
}

func NewRuntime() *Runtime {
	return &Runtime{clients: make(map[string]*codexsdk.Codex)}
}

func (r *Runtime) OpenSession(ctx context.Context, opts OpenOptions) (types.Session, error) {
	if opts.SessionKey == "" {
		return nil, errors.New("session key required")
	}
	client := r.getOrCreateClient(opts)
	var sess *session
	threadOptions := codexsdk.ThreadOptions{
		Model:                 strings.TrimSpace(opts.Model),
		ModelReasoningEffort:  codexsdk.ModelReasoningEffort(strings.TrimSpace(opts.Effort)),
		FastService:           strings.TrimSpace(opts.FastService),
		SandboxMode:           codexsdk.SandboxModeFullAccess,
		WorkingDirectory:      opts.RootPath,
		DeveloperInstructions: strings.TrimSpace(opts.DeveloperInstructions),
		ApprovalPolicy:        codexsdk.ApprovalModeNever,
		ApprovalHandler: func(_ codexsdk.ApprovalRequest) (codexsdk.ApprovalDecision, error) {
			return codexsdk.ApprovalDecisionApproved, nil
		},
		AskUserHandler: func(req codexsdk.AskUserRequest) (codexsdk.AskUserResponse, error) {
			if sess == nil {
				return codexsdk.AskUserResponse{}, errors.New("codex session not initialized")
			}
			return sess.handleAskUserRequest(req)
		},
	}
	if opts.PlanMode {
		threadOptions.CollaborationMode = codexCollaborationMode(true)
	}

	var thread *codexsdk.Thread
	if strings.TrimSpace(opts.ForkSessionID) != "" {
		// fork 的参数里不带 modelProvider 会继承源线程的旧供应商，新会话就会继续
		// 走切换前的 provider；这里显式钉到当前配置。
		forkOptions := threadOptions
		if provider := resolveCodexModelProvider(ctx, client, opts.RootPath); provider != "" {
			forkOptions.ModelProvider = provider
		}
		threadID, err := forkCodexThread(context.Background(), client, forkOptions, strings.TrimSpace(opts.ForkSessionID), opts.CodexUserOrdinal)
		if err != nil {
			return nil, err
		}
		opts.ResumeSessionID = threadID
		thread = client.ResumeThread(threadID, threadOptions)
	} else if strings.TrimSpace(opts.ResumeSessionID) != "" {
		resumeID := strings.TrimSpace(opts.ResumeSessionID)
		forkedID, rethreaded, err := rethreadCodexResumeOnProviderMismatch(ctx, client, resumeID, threadOptions, opts.RootPath)
		if err != nil {
			// 线程确实需要换供应商但没换成。绝不能照常 resume：旧供应商已经不在
			// 配置里，那条线程之后的每一轮都会撞 -32600（2026-11-22 pc 上就是这样
			// 连挂十几次）。把错误抛出去，上层 ensureAgentSession 会丢掉这个绑定、
			// 新开一条会话，用户至少还能继续干活。
			return nil, err
		}
		if rethreaded {
			resumeID = forkedID
			opts.ResumeSessionID = forkedID
		}
		thread = client.ResumeThread(resumeID, threadOptions)
	} else {
		thread = client.StartThread(threadOptions)
	}

	threadID := ""
	if strings.TrimSpace(opts.ResumeSessionID) != "" {
		threadID = strings.TrimSpace(opts.ResumeSessionID)
	} else if id := thread.ID(); id != nil && strings.TrimSpace(*id) != "" {
		threadID = strings.TrimSpace(*id)
	}
	sess = &session{
		client:        client,
		thread:        thread,
		threadOpts:    threadOptions,
		threadID:      threadID,
		sessionKey:    opts.SessionKey,
		planMode:      opts.PlanMode,
		onCompact:     opts.OnCompact,
		onThreadStart: opts.OnThreadStarted,

		onThreadRethreaded: opts.OnThreadRethreaded,
		questionWaits:      make(map[string]chan codexAskUserAnswerResult),
		agentDebugLog:      logs.NewAgentLogger(opts.RootPath, opts.SessionKey, opts.AgentName),
	}
	return sess, nil
}

func forkCodexThread(ctx context.Context, client *codexsdk.Codex, opts codexsdk.ThreadOptions, sourceThreadID string, userOrdinal *int) (string, error) {
	sourceThreadID = strings.TrimSpace(sourceThreadID)
	if sourceThreadID == "" {
		return "", errors.New("codex source thread id required")
	}

	thread, err := client.ForkThread(ctx, sourceThreadID, codexsdk.ThreadForkOptions{
		ThreadOptions:                opts,
		TruncateBeforeNthUserMessage: userOrdinal,
	})
	if err != nil {
		return "", err
	}
	if thread == nil || thread.ID() == nil || strings.TrimSpace(*thread.ID()) == "" {
		return "", errors.New("codex thread/fork did not return thread id")
	}
	return strings.TrimSpace(*thread.ID()), nil
}

func codexCollaborationMode(enabled bool) *codexsdk.CollaborationMode {
	if enabled {
		return codexsdk.NewCollaborationMode(codexsdk.CollaborationModePlan)
	}
	return codexsdk.NewCollaborationMode(codexsdk.CollaborationModeDefault)
}

func (r *Runtime) CloseAll() {
	r.mu.Lock()
	clients := r.clients
	r.clients = make(map[string]*codexsdk.Codex)
	r.mu.Unlock()

	for _, client := range clients {
		if client != nil {
			_ = client.Close()
		}
	}
}

func (r *Runtime) Close(agentName string) error {
	r.mu.Lock()
	client, ok := r.clients[agentName]
	if ok {
		delete(r.clients, agentName)
	}
	r.mu.Unlock()

	if !ok || client == nil {
		return nil
	}
	return client.Close()
}

func (r *Runtime) getOrCreateClient(opts OpenOptions) *codexsdk.Codex {
	r.mu.Lock()
	defer r.mu.Unlock()
	if client, ok := r.clients[opts.AgentName]; ok {
		return client
	}
	client := newClient(opts)
	r.clients[opts.AgentName] = client
	return client
}

// ProcessIDs returns the app-server PID keyed by configured agent.
func (r *Runtime) ProcessIDs() map[string]int {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.clients))
	for agentName, client := range r.clients {
		if pid := client.ProcessID(); pid > 0 {
			out[agentName] = pid
		}
	}
	return out
}

func newClient(opts OpenOptions) *codexsdk.Codex {
	codexOptions := codexsdk.CodexOptions{
		Transport:             codexsdk.TransportAppServer,
		AppServerPathOverride: opts.Command,
		Env:                   opts.Env,
		Verbose:               true,
	}
	if len(opts.Args) > 0 {
		codexOptions.AppServerArgs = append([]string{}, opts.Args...)
	}
	return codexsdk.NewCodex(codexOptions)
}
