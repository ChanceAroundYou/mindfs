// Package acp provides ACP-based agent process implementation.
// All supported agents are accessed through ACP.
package acp

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os/exec"
	"regexp"
	"sync"

	acp "github.com/coder/acp-go-sdk"
)

// Process manages an agent process using ACP.
// This implementation works with any ACP-compatible agent:
// - claude (via claude-code-acp wrapper)
// - gemini (via --experimental-acp flag)
// - codex (via codex-acp wrapper)
type Process struct {
	agentName string
	cmd       *exec.Cmd
	conn      *acp.ClientSideConnection
	client    *mindfsClient
	waitCh    chan error
	tree      processTree
	closeMu   sync.Mutex

	mu            sync.RWMutex
	sessions      map[string]*sessionState // sessionKey -> state
	sessionsByID  map[string]*sessionState // ACP session id -> state
	capability    CapabilitySnapshot
	models        *acp.SessionModelState
	modes         *acp.SessionModeState
	configOptions []acp.SessionConfigOption
	commands      []acp.AvailableCommand
	stderrHint    stderrHintState
	activePrompt  activePromptState

	// elicitationMu 只保护 pendingAskUserByCallID。单独一把锁而不是复用 p.mu：
	// 提问路径要在持锁状态下往 waiter 投递，而 waiter 的对面是可能长时间阻塞的
	// elicitation handler，复用 p.mu 会把会话读写一起拖住。
	elicitationMu          sync.Mutex
	pendingAskUserByCallID map[string]*pendingAskUser
}

var stderrMessagePattern = regexp.MustCompile(`"message"\s*:\s*"([^"]+)"`)

type sessionUpdateHandler func(SessionUpdate)

const (
	UpdateTypeMessageChunk UpdateType = "message_chunk"
	UpdateTypeUserMessage  UpdateType = "user_message_chunk"
	UpdateTypeThoughtChunk UpdateType = "thought_chunk"
	UpdateTypeToolCall     UpdateType = "tool_call"
	UpdateTypeToolUpdate   UpdateType = "tool_update"
	UpdateTypePlan         UpdateType = "plan_update"
	UpdateTypeMessageDone  UpdateType = "message_done"
)

// LiveSessionCount 报告该进程当前仍记账在案的会话数。
// CUSTOM(G-AC): agent 池据此判断进程能否空闲释放，上游没有这个读数
func (p *Process) LiveSessionCount() int {
	if p == nil {
		return 0
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.sessions)
}

func (p *Process) getSessionUpdateHandler(sessionID string) sessionUpdateHandler {
	session := p.getSessionByID(sessionID)
	if session == nil {
		return nil
	}
	return session.getOnUpdate()
}

func (c *mindfsClient) SessionUpdate(ctx context.Context, params acp.SessionNotification) error {
	session := c.proc.getSessionByID(string(params.SessionId))
	if session == nil {
		return nil
	}
	handler := session.getOnUpdate()
	if handler == nil {
		return nil
	}

	internalUpdate := wrapSessionUpdate(string(params.SessionId), params.Update)
	if params.Update.AvailableCommandsUpdate != nil {
		session.setCommands(params.Update.AvailableCommandsUpdate.AvailableCommands)
		c.proc.mu.Lock()
		c.proc.commands = append([]acp.AvailableCommand(nil), params.Update.AvailableCommandsUpdate.AvailableCommands...)
		c.proc.mu.Unlock()
	}
	if params.Update.CurrentModeUpdate != nil {
		current := params.Update.CurrentModeUpdate.CurrentModeId
		if state := session.getModes(); state != nil {
			state.CurrentModeId = current
			session.setModes(state)
			c.proc.mu.Lock()
			c.proc.modes = state
			c.proc.mu.Unlock()
		}
	}
	if params.Update.ConfigOptionUpdate != nil {
		session.setConfigOptions(params.Update.ConfigOptionUpdate.ConfigOptions)
		c.proc.mu.Lock()
		c.proc.configOptions = cloneConfigOptions(params.Update.ConfigOptionUpdate.ConfigOptions)
		c.proc.mu.Unlock()
	}
	if params.Update.UsageUpdate != nil {
		session.setUsageUpdate(
			params.Update.UsageUpdate.Used,
			params.Update.UsageUpdate.Size,
		)
	}

	if internalUpdate.Type != "" {
		handler(internalUpdate)
	} else {
		raw, _ := json.Marshal(params.Update)
		log.Printf("[agent/acp] unhandled raw=%s", string(raw))
	}
	return nil
}

func (p *Process) getSessionByKey(sessionKey string) *sessionState {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.sessions[sessionKey]
}

func (p *Process) getSessionByID(sessionID string) *sessionState {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.sessionsByID[sessionID]
}

// convertSessionUpdate converts acp-go SessionUpdate to internal format
func wrapSessionUpdate(sessionID string, update acp.SessionUpdate) SessionUpdate {
	result := SessionUpdate{
		SessionID: sessionID,
		Raw:       update,
	}
	switch {
	case update.UserMessageChunk != nil:
		result.Type = UpdateTypeUserMessage
	case update.AgentMessageChunk != nil:
		result.Type = UpdateTypeMessageChunk
	case update.AgentThoughtChunk != nil:
		result.Type = UpdateTypeThoughtChunk
	case update.ToolCall != nil:
		result.Type = UpdateTypeToolCall
	case update.ToolCallUpdate != nil:
		result.Type = UpdateTypeToolUpdate
	case update.Plan != nil || update.PlanUpdate != nil:
		result.Type = UpdateTypePlan
	}
	return result
}

func (p *Process) wrapPromptError(sessionKey, sessionID string, err error) error {
	if hint, ok := p.recentStderrHint(); ok {
		log.Printf("[agent/acp] send.error agent=%s session_key=%s err=%v hint=%q", p.agentLabel(), sessionKey, err, hint)
		return errors.New(hint)
	}
	log.Printf("[agent/acp] send.error agent=%s session_key=%s err=%v", p.agentLabel(), sessionKey, err)
	return err
}

func sessionUpdateLogValue(data any) string {
	raw, err := json.Marshal(data)
	if err != nil {
		return `{"marshal_error":true}`
	}
	return string(raw)
}
