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
}

type Runtime struct {
	mu      sync.Mutex
	clients map[string]*codexsdk.Codex
}

func NewRuntime() *Runtime {
	return &Runtime{clients: make(map[string]*codexsdk.Codex)}
}

func (r *Runtime) OpenSession(_ context.Context, opts OpenOptions) (types.Session, error) {
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
		threadID, err := forkCodexThread(context.Background(), client, threadOptions, strings.TrimSpace(opts.ForkSessionID), opts.CodexUserOrdinal)
		if err != nil {
			return nil, err
		}
		opts.ResumeSessionID = threadID
		thread = client.ResumeThread(threadID, threadOptions)
	} else if strings.TrimSpace(opts.ResumeSessionID) != "" {
		thread = client.ResumeThread(strings.TrimSpace(opts.ResumeSessionID), threadOptions)
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
		questionWaits: make(map[string]chan codexAskUserAnswerResult),
		agentDebugLog: logs.NewAgentLogger(opts.RootPath, opts.SessionKey, opts.AgentName),
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
