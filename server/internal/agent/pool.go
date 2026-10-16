package agent

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"mindfs/server/internal/agent/acp"
	"mindfs/server/internal/agent/claude"
	"mindfs/server/internal/agent/codex"
	agenttypes "mindfs/server/internal/agent/types"
)

// Pool routes agent session creation to protocol-specific runtimes.
type Pool struct {
	cfg             Config
	processCtx      context.Context
	cancel          context.CancelFunc
	mu              sync.Mutex
	sessions        map[string]*sessionEntry
	runtimeEnv      map[string]map[string]string
	closed          bool
	acp             *acp.Runtime
	claude          *claude.Runtime
	codex           *codex.Runtime
	runtimeLocksMu  sync.Mutex
	runtimeLocks    map[runtimeGroup]*sync.Mutex
	memoryMu        sync.RWMutex
	memoryCollectMu sync.Mutex
	memorySnapshot  MemorySnapshot
}

type sessionEntry struct {
	agentName  string
	sessionKey string
	protocol   Protocol
	session    agenttypes.Session
	lastUsedAt time.Time
	activeUses int
	closing    bool
	closeDone  chan struct{}
}

// NewPool creates a new agent pool.
func NewPool(cfg Config) *Pool {
	processCtx, cancel := context.WithCancel(context.Background())
	return &Pool{
		cfg:          cfg,
		processCtx:   processCtx,
		cancel:       cancel,
		sessions:     make(map[string]*sessionEntry),
		runtimeEnv:   make(map[string]map[string]string),
		acp:          acp.NewRuntime(processCtx),
		claude:       claude.NewRuntime(),
		codex:        codex.NewRuntime(),
		runtimeLocks: make(map[runtimeGroup]*sync.Mutex),
	}
}

// SupportsDeveloperInstructions reports whether the configured transport can
// carry MindFS instructions outside the user-message history.
func (p *Pool) SupportsDeveloperInstructions(agentName string) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	def, ok := p.cfg.GetAgent(strings.TrimSpace(agentName))
	if !ok {
		return false
	}
	protocol := def.Protocol
	if protocol == "" {
		protocol = DefaultProtocol(agentName)
	}
	return protocol == ProtocolCodexSDK || protocol == ProtocolClaudeSDK
}

// GetOrCreate returns an existing session handle or creates a new one.
func (p *Pool) GetOrCreate(ctx context.Context, in agenttypes.OpenSessionInput) (agenttypes.Session, error) {
	if in.SessionKey == "" {
		return nil, errors.New("session key required")
	}

	p.mu.Lock()
retryExisting:
	if p.closed {
		p.mu.Unlock()
		return nil, errors.New("agent pool closed")
	}
	if entry, ok := p.sessions[in.SessionKey]; ok {
		if entry.closing {
			done := entry.closeDone
			p.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
			}
			p.mu.Lock()
			goto retryExisting
		}
		entry.lastUsedAt = time.Now()
		p.mu.Unlock()
		return entry.session, nil
	}
	def, ok := p.cfg.GetAgent(in.AgentName)
	if !ok {
		p.mu.Unlock()
		return nil, errors.New("agent not configured: " + in.AgentName)
	}
	protocol := def.Protocol
	if protocol == "" {
		protocol = DefaultProtocol(in.AgentName)
	}
	p.mu.Unlock()
	unlockRuntime := p.lockRuntimeGroups([]runtimeGroup{{agentName: in.AgentName, protocol: protocol}})
	defer unlockRuntime()

	// A concurrent opener may have created the session while this goroutine
	// waited for the shared runtime lock.
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, errors.New("agent pool closed")
	}
	if entry, ok := p.sessions[in.SessionKey]; ok && entry != nil && entry.session != nil {
		entry.lastUsedAt = time.Now()
		existing := entry.session
		p.mu.Unlock()
		return existing, nil
	}
	p.mu.Unlock()

	// openSession starts subprocesses and can be slow, so keep it outside the pool lock.
	sess, err := p.openSession(ctx, protocol, def, in)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		_ = sess.Close()
		return nil, errors.New("agent pool closed")
	}
	// Another goroutine may have created the same session while the lock was released.
	if entry, ok := p.sessions[in.SessionKey]; ok {
		existing := entry.session
		entry.lastUsedAt = time.Now()
		p.mu.Unlock()
		if protocol == ProtocolClaudeSDK {
			_ = sess.Close()
		}
		return existing, nil
	}
	p.sessions[in.SessionKey] = &sessionEntry{
		agentName:  in.AgentName,
		sessionKey: in.SessionKey,
		protocol:   protocol,
		session:    sess,
		lastUsedAt: time.Now(),
	}
	p.mu.Unlock()
	return sess, nil
}

// BeginSessionUse prevents idle cleanup while a turn is using the session.
// The returned function must be called when that use finishes.
func (p *Pool) BeginSessionUse(sessionKey string) func() {
	p.mu.Lock()
	entry := p.sessions[sessionKey]
	if entry == nil || entry.closing {
		p.mu.Unlock()
		return func() {}
	}
	entry.activeUses++
	entry.lastUsedAt = time.Now()
	p.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			if current := p.sessions[sessionKey]; current == entry {
				if current.activeUses > 0 {
					current.activeUses--
				}
				current.lastUsedAt = time.Now()
			}
			p.mu.Unlock()
		})
	}
}

// Close closes a session (not the underlying runtime pool).
func (p *Pool) Close(sessionKey string) {
	p.mu.Lock()
	groups := make([]runtimeGroup, 0, 1)
	if entry := p.sessions[sessionKey]; entry != nil {
		groups = append(groups, runtimeGroup{agentName: entry.agentName, protocol: entry.protocol})
	}
	p.mu.Unlock()
	unlockRuntimes := p.lockRuntimeGroups(groups)
	defer unlockRuntimes()
	entries := p.takeSessions(func(entry *sessionEntry) bool {
		return entry.sessionKey == sessionKey
	})
	if len(entries) == 0 {
		return
	}
	p.closeSessions(entries)
}

func (p *Pool) takeSessions(match func(*sessionEntry) bool) []*sessionEntry {
	p.mu.Lock()
	defer p.mu.Unlock()

	var entries []*sessionEntry
	for key, entry := range p.sessions {
		if entry == nil || !match(entry) {
			continue
		}
		entries = append(entries, entry)
		delete(p.sessions, key)
	}
	return entries
}

func (p *Pool) closeSessions(entries []*sessionEntry) {
	for _, entry := range entries {
		if entry == nil || entry.session == nil {
			continue
		}
		_ = entry.session.Close()
	}
}

// CloseAll closes all runtime resources.
func (p *Pool) CloseAll() {
	p.mu.Lock()
	p.closed = true
	cancel := p.cancel
	p.cancel = nil
	groups := make([]runtimeGroup, 0, len(p.cfg.Agents))
	for _, def := range p.cfg.Agents {
		protocol := def.Protocol
		if protocol == "" {
			protocol = DefaultProtocol(def.Name)
		}
		groups = append(groups, runtimeGroup{agentName: def.Name, protocol: protocol})
	}
	p.mu.Unlock()

	// Cancel the process lifecycle before waiting for runtime locks. An ACP
	// process may still be initializing while holding one of those locks; its
	// command and initialize RPC both use this context.
	if cancel != nil {
		cancel()
	}

	unlockRuntimes := p.lockRuntimeGroups(groups)
	defer unlockRuntimes()

	p.mu.Lock()
	sessions := p.sessions
	p.sessions = make(map[string]*sessionEntry)
	acpRuntime := p.acp
	claudeRuntime := p.claude
	codexRuntime := p.codex
	p.mu.Unlock()

	// Claude SDK sessions own independent CLI processes rather than a shared
	// runtime, so each resident session must be closed explicitly. Close them
	// concurrently so shutdown is bounded by one SDK grace period.
	var claudeCloseWG sync.WaitGroup
	for _, entry := range sessions {
		if entry == nil || entry.protocol != ProtocolClaudeSDK || entry.session == nil {
			continue
		}
		claudeCloseWG.Add(1)
		go func(entry *sessionEntry) {
			defer claudeCloseWG.Done()
			if err := entry.session.Close(); err != nil {
				log.Printf("[agent/pool] close_all.claude_error session=%s agent=%s err=%v", entry.sessionKey, entry.agentName, err)
			}
		}(entry)
	}
	claudeCloseWG.Wait()

	if acpRuntime != nil {
		acpRuntime.CloseAll()
	}
	if claudeRuntime != nil {
		claudeRuntime.CloseAll()
	}
	if codexRuntime != nil {
		codexRuntime.CloseAll()
	}
}
