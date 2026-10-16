package acp

import (
	"sync"
	"time"

	types "mindfs/server/internal/agent/types"

	acp "github.com/coder/acp-go-sdk"
)

type CapabilitySnapshot struct {
	PromptSupportsAudio   bool
	PromptSupportsImage   bool
	PromptSupportsContext bool
	SupportsSessionClose  bool
	SupportsSessionResume bool
}

type stderrHintState struct {
	mu            sync.Mutex
	expectMessage bool
	message       string
	messageAt     time.Time
}

type sessionState struct {
	ID                     acp.SessionId
	models                 *acp.SessionModelState
	modes                  *acp.SessionModeState
	configOptions          []acp.SessionConfigOption
	commands               []acp.AvailableCommand
	contextWindow          types.ContextWindow
	contextUsageUpdateSeen bool
	lastUsage              cumulativeTokenUsage
	onUpdate               func(SessionUpdate)
	mu                     sync.RWMutex
}

type cumulativeTokenUsage struct {
	inputTokens      int
	outputTokens     int
	cacheReadTokens  int
	cacheWriteTokens int
}

type qwenSlashCommandNotification struct {
	SessionID   string `json:"sessionId"`
	Command     string `json:"command"`
	MessageType string `json:"messageType"`
	Message     string `json:"message"`
}

func (s *sessionState) setOnUpdate(onUpdate func(SessionUpdate)) {
	s.mu.Lock()
	s.onUpdate = onUpdate
	s.mu.Unlock()
}

func (s *sessionState) getOnUpdate() func(SessionUpdate) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.onUpdate
}

func (s *sessionState) setModels(models *acp.SessionModelState) {
	s.mu.Lock()
	s.models = models
	s.mu.Unlock()
}

func (s *sessionState) getModels() *acp.SessionModelState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.models
}

func (s *sessionState) setModes(modes *acp.SessionModeState) {
	s.mu.Lock()
	s.modes = modes
	s.mu.Unlock()
}

func (s *sessionState) getModes() *acp.SessionModeState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.modes
}

func (s *sessionState) setConfigOptions(options []acp.SessionConfigOption) {
	s.mu.Lock()
	s.configOptions = cloneConfigOptions(options)
	s.mu.Unlock()
}

func (s *sessionState) getConfigOptions() []acp.SessionConfigOption {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneConfigOptions(s.configOptions)
}

func (s *sessionState) setCommands(commands []acp.AvailableCommand) {
	s.mu.Lock()
	if len(commands) == 0 {
		s.commands = nil
	} else {
		s.commands = append([]acp.AvailableCommand(nil), commands...)
	}
	s.mu.Unlock()
}

func (s *sessionState) getCommands() []acp.AvailableCommand {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.commands) == 0 {
		return nil
	}
	return append([]acp.AvailableCommand(nil), s.commands...)
}

func (s *sessionState) setContextWindow(contextWindow types.ContextWindow) {
	s.mu.Lock()
	s.contextWindow = contextWindow
	s.mu.Unlock()
}

func (s *sessionState) getContextWindow() types.ContextWindow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.contextWindow
}

func (s *sessionState) setUsageUpdate(used, size int) {
	s.mu.Lock()
	if size > 0 {
		s.contextWindow.ModelContextWindow = size
	}
	s.contextWindow.TotalTokens = max(0, used)
	s.contextUsageUpdateSeen = true
	s.mu.Unlock()
}

func (s *sessionState) hasContextUsageUpdate() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.contextUsageUpdateSeen
}

func (s *sessionState) tokenUsageForPrompt(agentName string, usage *acp.Usage) *types.TokenUsage {
	if usage == nil {
		return nil
	}
	current := cumulativeTokenUsage{
		inputTokens:  max(0, usage.InputTokens),
		outputTokens: max(0, usage.OutputTokens),
	}
	if usage.CachedReadTokens != nil {
		current.cacheReadTokens = max(0, *usage.CachedReadTokens)
	}
	if usage.CachedWriteTokens != nil {
		current.cacheWriteTokens = max(0, *usage.CachedWriteTokens)
	}

	// DSH's ACP adapter resets usage for every prompt. Other agents retain
	// the existing cumulative-counter behavior until their semantics are verified.
	var previous cumulativeTokenUsage
	if agentName != "dsh" {
		s.mu.Lock()
		previous = s.lastUsage
		s.lastUsage = current
		s.mu.Unlock()
	}

	inputTokens := cumulativeCounterDelta(current.inputTokens, previous.inputTokens)
	outputTokens := cumulativeCounterDelta(current.outputTokens, previous.outputTokens)
	result := &types.TokenUsage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}
	if usage.CachedReadTokens != nil {
		value := cumulativeCounterDelta(current.cacheReadTokens, previous.cacheReadTokens)
		result.CacheReadTokens = &value
		result.InputTokens = max(result.InputTokens, value)
	}
	if usage.CachedWriteTokens != nil {
		value := cumulativeCounterDelta(current.cacheWriteTokens, previous.cacheWriteTokens)
		result.CacheWriteTokens = &value
		result.InputTokens = max(result.InputTokens, value)
	}
	if result.InputTokens == 0 && result.OutputTokens == 0 && result.CacheReadTokens == nil && result.CacheWriteTokens == nil {
		return nil
	}
	return result
}

func cumulativeCounterDelta(current, previous int) int {
	if current < previous {
		return current
	}
	return current - previous
}
