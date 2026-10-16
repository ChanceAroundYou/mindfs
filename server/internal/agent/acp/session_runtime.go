package acp

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"mindfs/server/internal/agent/logs"
	types "mindfs/server/internal/agent/types"
)

type OpenOptions struct {
	AgentName       string
	SessionKey      string
	Model           string
	Mode            string
	Effort          string
	RootPath        string
	Command         string
	Args            []string
	Env             map[string]string
	Cwd             string
	ResumeSessionID string
}

type Runtime struct {
	processCtx context.Context
	mu         sync.Mutex
	processes  map[string]*Process
	closeHints map[string]string
	closed     bool
}

func NewRuntime(processCtx context.Context) *Runtime {
	return &Runtime{
		processCtx: processCtx,
		processes:  make(map[string]*Process),
		closeHints: make(map[string]string),
	}
}

func (r *Runtime) OpenSession(ctx context.Context, opts OpenOptions) (types.Session, error) {
	if opts.SessionKey == "" {
		return nil, errors.New("session key required")
	}
	proc, err := r.getOrCreateProcess(opts)
	if err != nil {
		return nil, err
	}

	sess, err := openOnProcess(ctx, proc, opts)
	if err != nil {
		// 建立失败必须回收进程：进程按 agent 缓存在 Runtime 里，只有
		// KillAgentProcess/CloseAll 摘得掉。失败的会话不会进 sessions，于是一个
		// 「零会话」的 agent 进程会一直常驻（实测 100+MB），后续请求还会复用它
		// 继续失败。先把本次的半成品记账清掉，再按剩余会话数决定进程去留。
		proc.ForgetSession(opts.SessionKey)
		r.releaseUnusedProcess(opts.AgentName)
		return nil, err
	}
	return sess, nil
}

// openOnProcess 在已握手的进程上建立 ACP 会话并应用运行参数。
func openOnProcess(ctx context.Context, proc *Process, opts OpenOptions) (types.Session, error) {
	if strings.TrimSpace(opts.ResumeSessionID) != "" {
		if err := proc.ResumeSession(ctx, opts.SessionKey, opts.ResumeSessionID, opts.Cwd); err != nil {
			return nil, err
		}
	} else if err := proc.NewSession(ctx, opts.SessionKey, opts.RootPath); err != nil {
		return nil, err
	}
	if strings.TrimSpace(opts.Model) != "" {
		if err := proc.SetModel(ctx, opts.SessionKey, opts.Model); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(opts.Mode) != "" {
		if err := proc.SetMode(ctx, opts.SessionKey, opts.Mode); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(opts.Effort) != "" {
		if err := proc.SetThoughtLevel(ctx, opts.SessionKey, opts.Effort); err != nil {
			return nil, err
		}
	}
	return &session{
		proc:          proc,
		sessionKey:    opts.SessionKey,
		agentDebugLog: logs.NewAgentLogger(opts.RootPath, opts.SessionKey, opts.AgentName),
	}, nil
}

// releaseUnusedProcess 关掉并摘掉一个「一个会话都不剩」的进程。
//
// 还有别的会话挂在这个进程上时不能动它 —— 同 agent 的会话共用一个进程。
// 并发前提：同一 agent 的 OpenSession 由调用方串行化（Pool.GetOrCreate 全程持有
// per-agent 运行时锁），所以不存在「刚判定为零、同一刻又挂上新会话」的窗口。
func (r *Runtime) releaseUnusedProcess(agentName string) {
	if strings.TrimSpace(agentName) == "" {
		return
	}
	r.mu.Lock()
	proc, ok := r.processes[agentName]
	if !ok || proc == nil || proc.LiveSessionCount() > 0 {
		r.mu.Unlock()
		return
	}
	delete(r.processes, agentName)
	delete(r.closeHints, agentName)
	r.mu.Unlock()
	if err := proc.Close(); err != nil {
		log.Printf("[agent/acp] process.release_unused.error agent=%s err=%v", agentName, err)
	}
}

func (r *Runtime) CloseSession(sessionKey string) {
	for _, proc := range r.listProcesses() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = proc.CloseSession(ctx, sessionKey)
		cancel()
	}
}

func (r *Runtime) Close(agentName string) error {
	if strings.TrimSpace(agentName) == "" {
		return nil
	}
	r.mu.Lock()
	proc := r.processes[agentName]
	delete(r.processes, agentName)
	delete(r.closeHints, agentName)
	r.mu.Unlock()
	if proc != nil {
		closeErr := proc.Close()
		if hint, ok := waitForRecentStderrHint(proc, 750*time.Millisecond); ok {
			r.mu.Lock()
			r.closeHints[agentName] = hint
			r.mu.Unlock()
		}
		return closeErr
	}
	return nil
}

func (r *Runtime) RecentCloseHint(agentName string) (string, bool) {
	if strings.TrimSpace(agentName) == "" {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	hint, ok := r.closeHints[agentName]
	if !ok || strings.TrimSpace(hint) == "" {
		return "", false
	}
	delete(r.closeHints, agentName)
	return hint, true
}

func waitForRecentStderrHint(proc *Process, wait time.Duration) (string, bool) {
	if proc == nil {
		return "", false
	}
	deadline := time.Now().Add(wait)
	for {
		if hint, ok := proc.RecentStderrHint(); ok {
			return hint, true
		}
		if time.Now().After(deadline) {
			return "", false
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (r *Runtime) CloseAll() {
	closeProcessesConcurrently(r.listProcessesAndReset(), (*Process).Close)
}

func closeProcessesConcurrently(procs []*Process, closeProcess func(*Process) error) {
	var closeWG sync.WaitGroup
	for _, proc := range procs {
		if proc == nil {
			continue
		}
		closeWG.Add(1)
		go func(proc *Process) {
			defer closeWG.Done()
			if err := closeProcess(proc); err != nil {
				log.Printf("[agent/acp] process.close_all.error agent=%s err=%v", proc.agentLabel(), err)
			}
		}(proc)
	}
	closeWG.Wait()
}

func (r *Runtime) listProcesses() []*Process {
	r.mu.Lock()
	defer r.mu.Unlock()
	procs := make([]*Process, 0, len(r.processes))
	for _, proc := range r.processes {
		procs = append(procs, proc)
	}
	return procs
}

// ProcessIDs returns the live shared process PID keyed by configured agent.
func (r *Runtime) ProcessIDs() map[string]int {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.processes))
	for agentName, proc := range r.processes {
		if pid := proc.ProcessID(); pid > 0 {
			out[agentName] = pid
		}
	}
	return out
}

func (r *Runtime) listProcessesAndReset() []*Process {
	r.mu.Lock()
	defer r.mu.Unlock()
	procs := make([]*Process, 0, len(r.processes))
	for _, proc := range r.processes {
		procs = append(procs, proc)
	}
	r.closed = true
	r.processes = make(map[string]*Process)
	return procs
}

func (r *Runtime) getOrCreateProcess(opts OpenOptions) (*Process, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, errors.New("agent runtime closed")
	}
	if proc, ok := r.processes[opts.AgentName]; ok {
		r.mu.Unlock()
		return proc, nil
	}
	r.mu.Unlock()

	proc, err := Start(r.processCtx, opts.AgentName, opts.Command, opts.Args, opts.Cwd, opts.Env)
	if err != nil {
		return nil, err
	}

	initCtx, cancelInit := context.WithTimeout(r.processCtx, initializeTimeout)
	initErr := proc.Initialize(initCtx)
	cancelInit()
	if initErr != nil {
		proc.Close()
		return nil, initErr
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		proc.Close()
		return nil, errors.New("agent runtime closed")
	}
	if existing, ok := r.processes[opts.AgentName]; ok {
		r.mu.Unlock()
		proc.Close()
		return existing, nil
	}
	r.processes[opts.AgentName] = proc
	r.mu.Unlock()
	return proc, nil
}
