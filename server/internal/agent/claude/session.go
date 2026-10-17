package claude

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"mindfs/server/internal/agent/logs"
	types "mindfs/server/internal/agent/types"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"
)

const chunkFlushThreshold = 24

type deltaType string

const (
	deltaTypeText     deltaType = "text"
	deltaTypeThinking deltaType = "thinking"
)

type subagentMeta struct {
	ParentToolUseID string
	TaskID          string
	SubagentType    string
	TaskDescription string
	// MessageID 是本条 AssistantMessage 在 claude 转录里的 `uuid`。
	// 它随 chunk 一起上行，最终落进助手的 agent_id（桥梁键）。
	MessageID string
}

type claudeTaskInfo struct {
	ToolUseID       string
	TaskType        string
	Description     string
	SubagentType    string
	TaskDescription string
}

type session struct {
	client *claudeagent.Client
	stream *claudeagent.Stream

	mu         sync.RWMutex
	onUpdate   func(types.Event)
	sessionID  string
	sessionKey string
	model      string
	planMode   bool
	context    types.ContextWindow

	sendMu          sync.Mutex
	turnMu          sync.Mutex
	turns           []chan error
	turnInterrupted bool

	closeOnce sync.Once
	turn      types.TurnCanceler

	agentDebugLog *logs.AgentLogger

	rootPath string
	baseEnv  map[string]string

	sawDelta        bool
	sawMessageText  bool
	pendingText     strings.Builder
	pendingThinking strings.Builder

	pendingToolMu    sync.Mutex
	pendingToolCalls map[string]types.ToolCall
	taskInfos        map[string]claudeTaskInfo

	questionMu    sync.Mutex
	questionWaits map[string]chan askUserAnswerResult
	// emittedAskUsers 记录已经发过 tool_call 的 AskUserQuestion callID。
	// SDK 会对同一次提问走两条独立路径（CanUseTool 回调 → awaitAskUserQuestion，
	// 以及 AssistantMessage 的 tool_use 块 → handleAssistantMessage），两者 CallID
	// 相同但来自不同 goroutine、先后次序不定。谁先到谁发射，后者跳过，避免同一张
	// ask_user 卡被发两次（实测 2026-09-12 症状 1：卡片与推理文本各出现两份）。
	// 条目随会话结束释放，不回收 —— 为什么不能提前回收见 claimAskUserEmit 的注释。
	emittedAskUsers map[string]struct{}
}

func (s *session) ProcessID() int {
	if s == nil || s.client == nil {
		return 0
	}
	return s.client.ProcessID()
}

func (s *session) CurrentModel() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.model)
}

func (s *session) SetModel(ctx context.Context, model string) error {
	if s == nil || s.stream == nil {
		return errors.New("claude session not initialized")
	}
	trimmed := canonicalClaudeModel(model)
	if err := s.stream.SetModel(ctx, s.resolveClaudeModelArg(model)); err != nil {
		return err
	}
	s.mu.Lock()
	s.model = trimmed
	s.mu.Unlock()
	return nil
}

func (s *session) SetPlanMode(ctx context.Context, enabled bool) error {
	if s == nil || s.stream == nil {
		return errors.New("claude session not initialized")
	}
	mode := claudeagent.PermissionModeDefault
	if enabled {
		mode = claudeagent.PermissionModePlan
	}
	if err := s.stream.SetPermissionMode(ctx, mode); err != nil {
		return err
	}
	s.mu.Lock()
	s.planMode = enabled
	s.mu.Unlock()
	return nil
}

func (s *session) SetMode(_ context.Context, _ string) error {
	return nil
}

func (s *session) ListModes(_ context.Context) (types.ModeList, error) {
	return types.ModeList{}, nil
}

func (s *session) ListCommands(ctx context.Context) (types.CommandList, error) {
	_ = ctx
	if s.client == nil {
		return types.CommandList{}, errors.New("claude session not initialized")
	}
	supported := s.client.InitializationInfo().Commands
	commands := make([]types.CommandInfo, 0, len(supported))
	for _, command := range supported {
		name := strings.TrimSpace(command.Name)
		if name == "" || strings.EqualFold(name, "keybindings-help") {
			continue
		}
		commands = append(commands, types.CommandInfo{
			Name:         name,
			Description:  strings.TrimSpace(command.Description),
			ArgumentHint: strings.TrimSpace(command.ArgumentHint),
		})
	}
	log.Printf("[agent/claude] commands.cached session=%s count=%d", s.sessionKey, len(commands))
	return types.CommandList{Commands: commands}, nil
}

func (s *session) OnUpdate(onUpdate func(types.Event)) {
	s.mu.Lock()
	s.onUpdate = onUpdate
	s.mu.Unlock()
}

func (s *session) SessionID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionID
}

func (s *session) ContextWindow(_ context.Context) (types.ContextWindow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.context, nil
}

func (s *session) CancelCurrentTurn() error {
	s.cancelPendingQuestions(errors.New("turn canceled"))
	if s.stream == nil {
		s.turn.Cancel()
		return nil
	}
	// Interrupt 会阻塞等待 CLI 确认（SDK 内部用 Background ctx，无超时）。
	// 若 CLI 正卡在长工具调用里迟迟不 ack，这里会永久挂起，卡死整个会话。
	// 用短超时包住，3s 没确认就退化为取消 turn 上下文（结束本轮）。
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.stream.Interrupt(ctx); err == nil {
		s.markTurnInterrupted()
		return nil
	}
	s.turn.Cancel()
	return nil
}

func (s *session) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		s.cancelPendingQuestions(errors.New("claude session closed"))
		if s.stream != nil {
			closeErr = s.stream.Close()
		}
		if s.client != nil {
			if err := s.client.Close(); err != nil && closeErr == nil {
				closeErr = err
			}
		}
		s.failPendingTurns(errors.New("claude session closed"))
	})
	return closeErr
}
