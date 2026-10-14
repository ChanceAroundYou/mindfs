package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

type OpenOptions struct {
	AgentName             string
	SessionKey            string
	Model                 string
	Effort                string
	PlanMode              bool
	RootPath              string
	Command               string
	Args                  []string
	Env                   map[string]string
	DeveloperInstructions string
	ResumeSessionID       string
	ForkSessionID         string
	ResumeMessageID       string
}

type Runtime struct{}

func NewRuntime() *Runtime {
	return &Runtime{}
}

func (r *Runtime) OpenSession(ctx context.Context, opts OpenOptions) (types.Session, error) {
	if opts.SessionKey == "" {
		return nil, errors.New("session key required")
	}

	s := &session{
		sessionKey:      opts.SessionKey,
		model:           canonicalClaudeModel(opts.Model),
		planMode:        opts.PlanMode,
		agentDebugLog:   logs.NewAgentLogger(opts.RootPath, opts.SessionKey, opts.AgentName),
		questionWaits:   make(map[string]chan askUserAnswerResult),
		emittedAskUsers: make(map[string]struct{}),
		rootPath:        opts.RootPath,
		baseEnv:         cloneEnv(opts.Env),
	}

	optionList := []claudeagent.Option{
		claudeagent.WithCwd(opts.RootPath),
		claudeagent.WithEnv(opts.Env),
		claudeagent.WithVerbose(true),
		claudeagent.WithIncludePartialMessages(true),
		claudeagent.WithAgentProgressSummaries(true),
		claudeagent.WithForwardSubagentText(true),
		claudeagent.WithCanUseTool(s.handleCanUseTool),
	}
	optionList = appendClaudeDeveloperInstructions(optionList, opts.DeveloperInstructions)
	if strings.TrimSpace(opts.Command) != "" {
		optionList = append(optionList, claudeagent.WithCLIPath(opts.Command))
	}
	forkSessionID := strings.TrimSpace(opts.ForkSessionID)
	resumeMessageID := strings.TrimSpace(opts.ResumeMessageID)
	explicitSessionID := ""
	if forkSessionID != "" && resumeMessageID != "" {
		forked, err := claudeagent.ForkSession(forkSessionID, &claudeagent.ForkSessionOptions{
			UpToMessageID: resumeMessageID,
		})
		if err != nil {
			return nil, fmt.Errorf("fork claude session at message: %w", err)
		}
		if forked == nil || strings.TrimSpace(forked.SessionID) == "" {
			return nil, errors.New("fork claude session did not return session id")
		}
		explicitSessionID = strings.TrimSpace(forked.SessionID)
		optionList = append(optionList, claudeagent.WithResume(explicitSessionID))
	} else if forkSessionID != "" {
		optionList = append(optionList, claudeagent.WithForkSession(forkSessionID))
	} else if strings.TrimSpace(opts.ResumeSessionID) != "" {
		optionList = append(optionList, claudeagent.WithResume(strings.TrimSpace(opts.ResumeSessionID)))
	}
	if resumeMessageID != "" && forkSessionID == "" {
		optionList = append(optionList, claudeagent.WithResumeSessionAt(resumeMessageID))
	}
	if strings.TrimSpace(opts.Model) != "" {
		optionList = append(optionList, claudeagent.WithModel(s.resolveClaudeModelArg(opts.Model)))
	}
	if strings.TrimSpace(opts.Effort) != "" {
		optionList = append(optionList, claudeagent.WithEffort(claudeagent.EffortLevel(strings.TrimSpace(opts.Effort))))
	}
	if opts.PlanMode {
		optionList = append(optionList, claudeagent.WithPermissionMode(claudeagent.PermissionModePlan))
	}

	client, err := claudeagent.NewClient(optionList...)
	if err != nil {
		return nil, err
	}
	stream, err := client.Stream(ctx)
	if err != nil {
		client.Close()
		return nil, err
	}

	selectedModel := canonicalClaudeModel(opts.Model)
	if selectedModel == "" && opts.ResumeSessionID == "" {
		if candidate, ok := claudeFirstAvailableModel(client); ok {
			selectedModel = canonicalClaudeModel(candidate)
		}
	}
	// WithModel already selects an explicitly requested model at CLI startup.
	// A control-plane SetModel here would repeat the same switch immediately and
	// force an unnecessary provider confirmation. Keep SetModel only for the
	// first-available fallback, where no model was passed to the CLI.
	if selectedModel != "" && strings.TrimSpace(opts.Model) == "" {
		if err := stream.SetModel(ctx, s.resolveClaudeModelArg(selectedModel)); err != nil {
			client.Close()
			return nil, err
		}
	}
	if opts.PlanMode {
		if err := stream.SetPermissionMode(ctx, claudeagent.PermissionModePlan); err != nil {
			client.Close()
			return nil, err
		}
	}

	s.client = client
	s.stream = stream
	s.sessionID = stream.SessionID()
	if strings.TrimSpace(s.sessionID) == "" {
		s.sessionID = explicitSessionID
	}
	s.model = selectedModel
	go s.consumeMessages()
	return s, nil
}

func appendClaudeDeveloperInstructions(options []claudeagent.Option, developerInstructions string) []claudeagent.Option {
	if instructions := strings.TrimSpace(developerInstructions); instructions != "" {
		return append(options, claudeagent.WithExtraArgs(map[string]*string{
			"append-system-prompt": &instructions,
		}))
	}
	return options
}

func (r *Runtime) CloseAll() {}

func claudeFirstAvailableModel(client *claudeagent.Client) (string, bool) {
	if client == nil {
		return "", false
	}
	for _, item := range client.SupportedModelsFromInit() {
		candidate := strings.TrimSpace(item.Value)
		if candidate == "" || strings.EqualFold(candidate, "default") {
			continue
		}
		return candidate, true
	}
	return "", false
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
	emittedAskUsers map[string]struct{}
}

func (s *session) ProcessID() int {
	if s == nil || s.client == nil {
		return 0
	}
	return s.client.ProcessID()
}

// claimAskUserEmit 为一次 AskUserQuestion 的发证：首次调用返回 true（调用方负责发射），
// 重复调用返回 false。SDK 的两条路径（CanUseTool / AssistantMessage）都可能触发，
// 用集合让「先到者发射、后到者跳过」，不依赖两条路径的先后次序。
func (s *session) claimAskUserEmit(callID string) bool {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return true
	}
	s.questionMu.Lock()
	defer s.questionMu.Unlock()
	if s.emittedAskUsers == nil {
		s.emittedAskUsers = make(map[string]struct{})
	}
	if _, exists := s.emittedAskUsers[callID]; exists {
		return false
	}
	s.emittedAskUsers[callID] = struct{}{}
	return true
}

type askUserAnswerResult struct {
	answers claudeagent.Answers
	err     error
}

func (s *session) SendMessage(ctx context.Context, content string) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	if s.stream == nil {
		return errors.New("claude session not initialized")
	}
	turnCtx, turnID := s.turn.Begin(ctx)
	defer s.turn.End(turnID)
	s.sawMessageText = false
	log.Printf("[agent/claude] input session=%s content=%q", s.sessionKey, preview(content))

	waiter := make(chan error, 1)
	s.enqueueTurn(waiter)
	if err := s.stream.Send(turnCtx, content); err != nil {
		s.dequeueTurn(waiter)
		log.Printf("[agent/claude] send.error session=%s err=%v", s.sessionKey, err)
		return err
	}

	select {
	case err := <-waiter:
		if err != nil && !isTurnCanceledError(err) {
			log.Printf("[agent/claude] send.error session=%s err=%v", s.sessionKey, err)
		}
		return err
	case <-turnCtx.Done():
		s.dequeueTurn(waiter)
		log.Printf("[agent/claude] send.error session=%s err=%v", s.sessionKey, turnCtx.Err())
		return turnCtx.Err()
	}
}

func (s *session) AnswerQuestion(ctx context.Context, answer types.AskUserAnswer) error {
	callID := strings.TrimSpace(answer.ToolUseID)
	if callID == "" {
		return errors.New("toolUseId required")
	}
	if len(answer.Answers) == 0 {
		return errors.New("answers required")
	}

	s.questionMu.Lock()
	waiter, ok := s.questionWaits[callID]
	s.questionMu.Unlock()
	if !ok {
		return errors.New("question is not pending: " + callID)
	}

	answers := make(claudeagent.Answers, len(answer.Answers))
	for key, value := range answer.Answers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			answers[key] = value
		}
	}
	if len(answers) == 0 {
		return errors.New("answers required")
	}

	select {
	case waiter <- askUserAnswerResult{answers: answers}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *session) handleCanUseTool(ctx context.Context, req claudeagent.ToolPermissionRequest) claudeagent.PermissionResult {
	if req.ToolName != "AskUserQuestion" {
		return claudeagent.PermissionAllow{}
	}

	var input claudeagent.AskUserQuestionInput
	if err := json.Unmarshal(req.Arguments, &input); err != nil || len(input.Questions) == 0 {
		return claudeagent.PermissionAllow{}
	}

	callID := strings.TrimSpace(req.Context.ToolUseID)
	if callID == "" {
		return claudeagent.PermissionDeny{Reason: "ask user question missing tool use id"}
	}

	answers, err := s.awaitAskUserQuestion(ctx, claudeagent.QuestionSet{
		ToolUseID: callID,
		Questions: input.Questions,
		SessionID: req.Context.SessionID,
	})
	if err != nil {
		return claudeagent.PermissionDeny{Reason: err.Error()}
	}

	updatedInput := make(map[string]interface{})
	if err := json.Unmarshal(req.Arguments, &updatedInput); err != nil {
		updatedInput["questions"] = input.Questions
	}
	updatedInput["answers"] = normalizeAskUserAnswers(input.Questions, answers)

	return claudeagent.PermissionAllow{UpdatedInput: updatedInput}
}

func normalizeAskUserAnswers(questions []claudeagent.QuestionItem, answers claudeagent.Answers) map[string]string {
	normalized := make(map[string]string, len(answers))

	for key, value := range answers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" || askUserQuestionTextForKey(key, questions) != "" {
			continue
		}
		normalized[key] = value
	}
	for key, value := range answers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		questionText := askUserQuestionTextForKey(key, questions)
		if questionText == "" {
			questionText = key
		}
		if _, exists := normalized[questionText]; !exists {
			normalized[questionText] = value
		}
	}

	return normalized
}

func askUserQuestionTextForKey(key string, questions []claudeagent.QuestionItem) string {
	for index, question := range questions {
		if key == fmt.Sprintf("q_%d", index) {
			return strings.TrimSpace(question.Question)
		}
	}
	return ""
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

func (s *session) cancelPendingQuestions(err error) {
	if err == nil {
		err = errors.New("turn canceled")
	}
	s.questionMu.Lock()
	type pendingQuestion struct {
		callID string
		waiter chan askUserAnswerResult
	}
	waiters := make([]pendingQuestion, 0, len(s.questionWaits))
	for callID, waiter := range s.questionWaits {
		waiters = append(waiters, pendingQuestion{callID: callID, waiter: waiter})
		delete(s.questionWaits, callID)
	}
	s.questionMu.Unlock()

	for _, pending := range waiters {
		select {
		case pending.waiter <- askUserAnswerResult{err: err}:
		default:
		}
		if update, ok := s.cancelPendingToolCall(pending.callID, err.Error()); ok {
			s.emit(types.Event{
				Type:      types.EventTypeToolUpdate,
				SessionID: s.SessionID(),
				Data:      update,
			})
		}
	}
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

func (s *session) consumeMessages() {
	if s.stream == nil {
		return
	}

	// Claude SDK multiplexes several logical message types onto one channel:
	// - PartialAssistantMessage: incremental tokens for visible text or thinking
	// - AssistantMessage: a finalized assistant payload, including tool_use blocks
	// - UserMessage: a local echo that primarily carries tool_use_result here
	// - ToolProgressMessage: intermediate progress for a running tool call
	// - ResultMessage: the completion or error boundary for the current turn
	//
	// This function normalizes them into our internal session event stream.
	s.sawDelta = false
	s.pendingText.Reset()
	s.pendingThinking.Reset()
	for msg := range s.stream.Messages() {
		raw, _ := json.Marshal(msg)
		s.updateSessionID(msg)

		switch m := msg.(type) {
		case claudeagent.PartialAssistantMessage:
			// Incremental text / thinking tokens. Buffer and coalesce them into
			// larger readable chunks before emitting UI updates.
			s.handlePartialAssistantMessage(m.Event, subagentMetaFromPartial(m))
		case claudeagent.StreamEvent:
			s.handleStreamEvent(m)
		case claudeagent.AssistantMessage:
			// Finalized assistant message. Flush pending deltas first so finalized
			// blocks do not interleave with previously buffered streaming output.
			s.flushAllDeltas()
			s.handleAssistantMessage(m, s.sawDelta)
		case claudeagent.UserMessage:
			// Claude SDK surfaces tool execution results as a synthetic "user"
			// message. ToolUseResult maps to the earliest pending tool_use.
			s.flushAllDeltas()
			s.logRawToolResult(m)
			s.handleUserMessage(m)
		case claudeagent.TodoUpdateMessage:
			s.flushAllDeltas()
			s.handleTodoUpdateMessage(m)
		case claudeagent.ToolProgressMessage:
			// Lightweight progress heartbeat for a tool call that is still running.
			s.flushAllDeltas()
			s.emitToolUpdate(m.ToolUseID, m.ToolName, subagentMetaFromToolProgress(m))
		case claudeagent.CompactBoundaryMessage:
			s.flushAllDeltas()
			s.handleCompactBoundaryMessage(m)
		case claudeagent.AuthStatusMessage:
			s.flushAllDeltas()
			s.handleAuthStatusMessage(m)
		case claudeagent.SubagentResultMessage:
			s.flushAllDeltas()
			s.handleSubagentResultMessage(m)
		case claudeagent.TaskStartedMessage:
			s.flushAllDeltas()
			s.handleTaskStartedMessage(m)
		case claudeagent.TaskProgressMessage:
			s.flushAllDeltas()
			s.handleTaskProgressMessage(m)
		case claudeagent.TaskUpdatedMessage:
			s.flushAllDeltas()
			s.handleTaskUpdatedMessage(m)
		case claudeagent.TaskNotificationMessage:
			s.flushAllDeltas()
			s.handleTaskNotificationMessage(m)
		case claudeagent.ThinkingTokensMessage:
			// Live estimate for redacted thinking token progress. It is useful
			// telemetry for clients that render token counters, but MindFS does
			// not currently expose it in the session timeline.
		case claudeagent.ResultMessage:
			// End-of-turn boundary. Claude may place the final text here when no
			// incremental tokens were streamed, so emit it as a last fallback.
			s.flushAllDeltas()
			if !s.sawMessageText && strings.TrimSpace(m.Result) != "" {
				s.emitMessageChunk(m.Result)
			}
			s.updateContextWindow(m)
			s.logRawMessage(raw)
			contextWindow, _ := s.ContextWindow(context.Background())
			tokenUsage := claudeTokenUsage(m)
			s.emit(types.Event{
				Type:      types.EventTypeMessageDone,
				SessionID: s.SessionID(),
				Data:      types.MessageDone{ContextWindow: contextWindow, TokenUsage: tokenUsage},
			})
			s.completeTurn(resultErr(m))
			s.sawDelta = false
			s.sawMessageText = false
		default:
			s.logRawMessage(raw)
		}
	}

	s.failPendingTurns(errors.New("response stream ended unexpectedly"))
}

func (s *session) handlePartialAssistantMessage(rawEvent json.RawMessage, meta subagentMeta) {
	if contextTokens := contextTokensFromPartialEvent(rawEvent); contextTokens > 0 {
		s.mu.Lock()
		s.context.TotalTokens = contextTokens
		s.mu.Unlock()
	}
	textDelta, thinkingDelta := extractDeltas(rawEvent)
	if textDelta == "" && thinkingDelta == "" && len(rawEvent) > 0 && !isKnownNonDisplayPartialEvent(rawEvent) {
		log.Printf("[agent/claude] output.unhandled.partial session=%s raw=%s", s.sessionKey, truncateRaw(rawEvent))
	}
	if meta.hasSubagentRef() {
		s.flushAllDeltas()
		if thinkingDelta != "" {
			s.emitThoughtChunkWithMeta(thinkingDelta, meta)
		}
		if textDelta != "" {
			s.emitMessageChunkWithMeta(textDelta, meta)
		}
		return
	}
	// Thinking and visible text are rendered in separate UI lanes, so flush the
	// other lane before appending to the current one.
	if thinkingDelta != "" {
		s.flushDelta(deltaTypeText)
		s.appendDelta(deltaTypeThinking, thinkingDelta)
	}
	if textDelta != "" {
		s.flushDelta(deltaTypeThinking)
		s.appendDelta(deltaTypeText, textDelta)
	}
}

func (s *session) pendingBuilder(kind deltaType) *strings.Builder {
	if kind == deltaTypeThinking {
		return &s.pendingThinking
	}
	return &s.pendingText
}

func (s *session) flushAllDeltas() {
	s.flushDelta(deltaTypeText)
	s.flushDelta(deltaTypeThinking)
}

func (s *session) flushDelta(kind deltaType) {
	pending := s.pendingBuilder(kind)
	if pending.Len() == 0 {
		return
	}
	delta := pending.String()
	pending.Reset()
	if kind == deltaTypeThinking {
		s.emitThoughtChunk(delta)
		return
	}
	s.sawDelta = true
	s.emitMessageChunk(delta)
}

func (s *session) appendDelta(kind deltaType, delta string) {
	if delta == "" {
		return
	}
	pending := s.pendingBuilder(kind)
	pending.WriteString(delta)
	// Coalesce token-level fragments into readable chunks while keeping streaming feel.
	if pending.Len() >= chunkFlushThreshold || strings.ContainsAny(delta, "\n.!?;:") {
		s.flushDelta(kind)
	}
}

func (s *session) emitThoughtChunk(content string) {
	s.emitThoughtChunkWithMeta(content, subagentMeta{})
}

func (s *session) emitThoughtChunkWithMeta(content string, meta subagentMeta) {
	s.emit(types.Event{
		Type:      types.EventTypeThoughtChunk,
		SessionID: s.SessionID(),
		Data: types.ThoughtChunk{
			Content:         content,
			ParentToolUseID: meta.ParentToolUseID,
			TaskID:          meta.TaskID,
			SubagentType:    meta.SubagentType,
			TaskDescription: meta.TaskDescription,
		},
	})
}

func (s *session) handleAssistantMessage(msg claudeagent.AssistantMessage, sawDelta bool) {
	meta := subagentMetaFromAssistant(msg)
	// 补上 agent 侧桥梁键：AssistantMessage.UUID 是这条消息在转录里的 uuid，
	// 导入器重放同一条时读回的就是它。SDK 一直带着这个字段，只是此前从未读取 ——
	// 于是 live 写的助手行没有 agent_id、import 写的那行有，两个写入者永远对不上。
	meta.MessageID = strings.TrimSpace(msg.UUID)
	for _, block := range msg.Message.Content {
		switch block.Type {
		case "text":
			// If text was already streamed via partial deltas, skip the duplicated
			// finalized text block.
			if sawDelta {
				continue
			}
			s.emitMessageChunkWithMeta(block.Text, meta)
		case "thinking":
			s.emitThoughtChunkWithMeta(block.Text, meta)
		case "tool_use":
			// tool_use is the structured tool invocation request. Its completion
			// arrives later as UserMessage + ToolUseResult.
			s.logRawToolCallBlock(block)
			toolCall := newRunningToolCall(block.ID, block.Name, block.Type, block.Input)
			toolCall.Meta = mergeToolCallMeta(toolCall.Meta, meta.toMap())
			if isTaskListToolName(block.Name) {
				toolCall.Meta = mergeToolCallMeta(toolCall.Meta, map[string]any{
					"taskTool":  canonicalTaskListToolName(block.Name),
					"toolUseId": block.ID,
				})
				s.trackPendingToolCall(toolCall)
				continue
			}
			s.trackPendingToolCall(toolCall)
			// AskUserQuestion 已由 CanUseTool 回调（awaitAskUserQuestion）发过带完整
			// meta.questions 的 tool_call；此处是 SDK 的同一次提问的第二条路径，
			// 跳过以免同一张卡出现两份。
			if mapToolKind(block.Name) == types.ToolKindAskUser {
				if !s.claimAskUserEmit(block.ID) {
					continue
				}
			}
			s.emit(types.Event{
				Type:      types.EventTypeToolCall,
				SessionID: s.SessionID(),
				Data:      toolCall,
			})
		}
	}
}

func (s *session) handleUserMessage(msg claudeagent.UserMessage) {
	// Claude tool results do not come back on AssistantMessage. They arrive
	// here, and we map the result onto the earliest pending tool call.
	update, ok := s.toolResultUpdate(msg)
	if !ok {
		return
	}
	s.emit(types.Event{
		Type:      types.EventTypeToolUpdate,
		SessionID: s.SessionID(),
		Data:      update,
	})
}

func (s *session) handleTodoUpdateMessage(msg claudeagent.TodoUpdateMessage) {
	items := make([]types.TodoItem, 0, len(msg.Items))
	for _, item := range msg.Items {
		items = append(items, types.TodoItem{
			Content:    item.Content,
			ActiveForm: item.ActiveForm,
			Status:     string(item.Status),
		})
	}
	s.emit(types.Event{
		Type:      types.EventTypeTodoUpdate,
		SessionID: s.SessionID(),
		Data:      types.TodoUpdate{Items: items},
	})
}

func (s *session) handleStreamEvent(msg claudeagent.StreamEvent) {
	if msg.Delta != "" {
		s.emitMessageChunk(msg.Delta)
		return
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return
	}
	s.handlePartialAssistantMessage(raw, subagentMeta{})
}

func (s *session) handleCompactBoundaryMessage(msg claudeagent.CompactBoundaryMessage) {
	s.emit(types.Event{
		Type:      types.EventTypeCompact,
		SessionID: s.SessionID(),
		Data: types.CompactNotice{
			ID:      firstNonEmpty(msg.UUID, msg.SessionID),
			Status:  strings.TrimSpace(msg.CompactMetadata.Trigger),
			Summary: compactBoundarySummary(msg),
		},
	})
}

func (s *session) handleAuthStatusMessage(msg claudeagent.AuthStatusMessage) {
	status := "success"
	if msg.IsAuthenticating {
		status = "running"
	}
	if strings.TrimSpace(msg.Error) != "" {
		status = "failed"
	}
	lines := append([]string(nil), msg.Output...)
	if errText := strings.TrimSpace(msg.Error); errText != "" {
		lines = append(lines, errText)
	}
	callID := firstNonEmpty(msg.UUID, "auth-"+msg.SessionID, "auth-status")
	s.emit(types.Event{
		Type:      types.EventTypeToolUpdate,
		SessionID: s.SessionID(),
		Data: types.ToolCall{
			CallID:  callID,
			Title:   "authentication",
			Status:  status,
			Kind:    types.ToolKindOther,
			RawType: "auth_status",
			Content: []types.ToolCallContentItem{{Type: "text", Text: strings.Join(lines, "\n")}},
			Meta: map[string]any{
				"rawType":          "auth_status",
				"isAuthenticating": msg.IsAuthenticating,
			},
		},
	})
}

func (s *session) handleSubagentResultMessage(msg claudeagent.SubagentResultMessage) {
	agentName := strings.TrimSpace(msg.AgentName)
	title := firstNonEmpty(agentName, "subagent")
	status := "complete"
	if strings.EqualFold(strings.TrimSpace(msg.Status), "error") {
		status = "failed"
	}
	callID := "subagent-result-" + strings.ToLower(strings.ReplaceAll(title, " ", "-"))
	s.emit(types.Event{
		Type:      types.EventTypeToolUpdate,
		SessionID: s.SessionID(),
		Data: types.ToolCall{
			CallID:  callID,
			Title:   title,
			Status:  status,
			Kind:    types.ToolKindTask,
			RawType: "subagent_result",
			Content: []types.ToolCallContentItem{{Type: "text", Text: strings.TrimSpace(msg.Result)}},
			Meta: map[string]any{
				"rawType":      "subagent_result",
				"subagentType": agentName,
				"status":       strings.TrimSpace(msg.Status),
			},
		},
	})
}

func (s *session) handleTaskStartedMessage(msg claudeagent.TaskStartedMessage) {
	s.trackTaskInfo(msg.TaskID, claudeTaskInfo{
		ToolUseID:       msg.ToolUseID,
		TaskType:        msg.TaskType,
		Description:     msg.Description,
		SubagentType:    msg.SubagentType,
		TaskDescription: msg.Description,
	})
	if isIgnoredClaudeTaskType(msg.TaskType) {
		return
	}
	toolCall := claudeTaskToolCall(
		firstNonEmpty(msg.ToolUseID, msg.TaskID),
		"running",
		msg.Description,
		msg.Prompt,
		subagentMeta{
			ParentToolUseID: msg.ToolUseID,
			TaskID:          msg.TaskID,
			SubagentType:    msg.SubagentType,
			TaskDescription: msg.Description,
		},
		map[string]any{
			"subtype":      msg.Subtype,
			"taskType":     msg.TaskType,
			"workflowName": msg.WorkflowName,
		},
	)
	if strings.TrimSpace(toolCall.CallID) != "" {
		s.trackPendingToolCall(toolCall)
	}
	s.emit(types.Event{Type: types.EventTypeToolCall, SessionID: s.SessionID(), Data: toolCall})
}

func (s *session) handleTaskProgressMessage(msg claudeagent.TaskProgressMessage) {
	if isIgnoredClaudeTaskType(s.taskType(msg.TaskID)) {
		return
	}
	s.trackTaskInfo(msg.TaskID, claudeTaskInfo{
		ToolUseID:       msg.ToolUseID,
		Description:     msg.Description,
		SubagentType:    msg.SubagentType,
		TaskDescription: msg.Description,
	})
	callID := firstNonEmpty(msg.ToolUseID, msg.TaskID)
	toolCall := claudeTaskToolCall(
		callID,
		"running",
		msg.Description,
		msg.Summary,
		subagentMeta{
			ParentToolUseID: msg.ToolUseID,
			TaskID:          msg.TaskID,
			SubagentType:    msg.SubagentType,
			TaskDescription: msg.Description,
		},
		map[string]any{
			"subtype":      msg.Subtype,
			"lastToolName": msg.LastToolName,
			"progress":     firstNonEmpty(msg.Summary, msg.Description),
		},
	)
	if s.hasPendingToolCall(callID) {
		toolCall.Title = ""
	}
	s.emit(types.Event{Type: types.EventTypeToolUpdate, SessionID: s.SessionID(), Data: toolCall})
}

func (s *session) handleTaskUpdatedMessage(msg claudeagent.TaskUpdatedMessage) {
	info := s.taskInfo(msg.TaskID)
	if isIgnoredClaudeTaskType(info.TaskType) {
		return
	}
	description := firstNonEmpty(msg.Patch.Description, info.TaskDescription, info.Description)
	toolCall := claudeTaskToolCall(
		firstNonEmpty(info.ToolUseID, msg.TaskID),
		claudeTaskRunStatus(string(msg.Patch.Status)),
		description,
		msg.Patch.Error,
		subagentMeta{
			ParentToolUseID: info.ToolUseID,
			TaskID:          msg.TaskID,
			SubagentType:    info.SubagentType,
			TaskDescription: description,
		},
		map[string]any{
			"subtype":        msg.Subtype,
			"error":          msg.Patch.Error,
			"isBackgrounded": boolPtrValue(msg.Patch.IsBackgrounded),
		},
	)
	s.emit(types.Event{Type: types.EventTypeToolUpdate, SessionID: s.SessionID(), Data: toolCall})
}

func (s *session) handleTaskNotificationMessage(msg claudeagent.TaskNotificationMessage) {
	info := s.taskInfo(msg.TaskID)
	if isIgnoredClaudeTaskType(info.TaskType) {
		return
	}
	status := "complete"
	switch msg.Status {
	case claudeagent.TaskNotificationStatusFailed:
		status = "failed"
	case claudeagent.TaskNotificationStatusStopped:
		status = "cancelled"
	}
	extra := map[string]any{
		"subtype":    msg.Subtype,
		"outputFile": msg.OutputFile,
	}
	if strings.TrimSpace(msg.Summary) != "" {
		extra["summary"] = msg.Summary
	}
	// 注意：description 参数会成为卡片标题（见 claudeTaskToolCall 的 title := description）。
	// SDK 的 TaskNotificationMessage 没有 Description 字段，只有 Summary —— 而 Summary 是
	// 子代理的报告正文。过去这里直接把 msg.Summary 当 description 传，导致报告全文被提升成
	// 卡片标题，UI 上出现「一段没有思考的裸文本工具卡」（实测 2026-09-12：与同 line 锚点的
	// ask_user 卡相邻渲染，看起来像提问被重复了一遍，实际是子代理报告卡）。
	// 真正的任务描述在 trackTaskInfo 里（task_started/progress 写入），这里取它；
	// 拿不到时退到子代理类型，也不退到报告正文。
	description := firstNonEmpty(info.Description, info.TaskDescription, info.SubagentType, "task")
	toolCall := claudeTaskToolCall(
		firstNonEmpty(msg.ToolUseID, info.ToolUseID, msg.TaskID),
		status,
		description,
		msg.Summary,
		subagentMeta{
			ParentToolUseID: firstNonEmpty(msg.ToolUseID, info.ToolUseID),
			TaskID:          msg.TaskID,
			SubagentType:    info.SubagentType,
			TaskDescription: description,
		},
		extra,
	)
	s.emit(types.Event{Type: types.EventTypeToolUpdate, SessionID: s.SessionID(), Data: toolCall})
}

func (s *session) awaitAskUserQuestion(ctx context.Context, qs claudeagent.QuestionSet) (claudeagent.Answers, error) {
	callID := strings.TrimSpace(qs.ToolUseID)
	if callID == "" {
		return nil, errors.New("ask user question missing tool use id")
	}

	waiter := make(chan askUserAnswerResult, 1)
	s.questionMu.Lock()
	if s.questionWaits == nil {
		s.questionWaits = make(map[string]chan askUserAnswerResult)
	}
	if _, exists := s.questionWaits[callID]; exists {
		s.questionMu.Unlock()
		return nil, errors.New("ask user question already pending: " + callID)
	}
	s.questionWaits[callID] = waiter
	s.questionMu.Unlock()
	defer func() {
		s.questionMu.Lock()
		delete(s.questionWaits, callID)
		s.questionMu.Unlock()
	}()

	toolCall := askUserQuestionToolCall(qs)
	s.trackPendingToolCall(toolCall)
	// 与 handleAssistantMessage 的 tool_use 分支二选一发射：谁先到谁发，避免重复卡片。
	if s.claimAskUserEmit(callID) {
		s.emit(types.Event{
			Type:      types.EventTypeToolCall,
			SessionID: s.SessionID(),
			Data:      toolCall,
		})
	}

	select {
	case result := <-waiter:
		if result.err != nil {
			return nil, result.err
		}
		if len(result.answers) == 0 {
			return nil, errors.New("empty ask user answers")
		}
		return result.answers, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func askUserQuestionToolCall(qs claudeagent.QuestionSet) types.ToolCall {
	questions := make([]types.AskUserQuestionItem, 0, len(qs.Questions))
	for _, question := range qs.Questions {
		options := make([]types.AskUserQuestionOption, 0, len(question.Options))
		for _, option := range question.Options {
			options = append(options, types.AskUserQuestionOption{
				Label:       option.Label,
				Description: option.Description,
			})
		}
		questions = append(questions, types.AskUserQuestionItem{
			Question:    question.Question,
			Header:      question.Header,
			Options:     options,
			MultiSelect: question.MultiSelect,
		})
	}

	payload := claudeagent.AskUserQuestionInput{Questions: qs.Questions}
	raw, _ := json.Marshal(payload)
	toolCall := newRunningToolCall(qs.ToolUseID, "AskUserQuestion", "tool_use", raw)
	if toolCall.Meta == nil {
		toolCall.Meta = map[string]any{}
	}
	toolCall.Meta["toolUseId"] = qs.ToolUseID
	toolCall.Meta["questions"] = questions
	if qs.ParentToolUseID != nil && strings.TrimSpace(*qs.ParentToolUseID) != "" {
		toolCall.Meta["parentToolUseId"] = strings.TrimSpace(*qs.ParentToolUseID)
	}
	return toolCall
}

func newRunningToolCall(callID, name, rawType string, input json.RawMessage) types.ToolCall {
	title, meta, locations, content := summarizeToolCall(name, input)
	return types.ToolCall{
		CallID:    callID,
		Title:     title,
		Status:    "running",
		Kind:      mapToolKind(name),
		Locations: locations,
		Content:   content,
		RawType:   rawType,
		Meta:      meta,
	}
}

func subagentMetaFromPartial(msg claudeagent.PartialAssistantMessage) subagentMeta {
	meta := subagentMeta{}
	if msg.ParentToolUseID != nil {
		meta.ParentToolUseID = strings.TrimSpace(*msg.ParentToolUseID)
	}
	return meta
}

func subagentMetaFromAssistant(msg claudeagent.AssistantMessage) subagentMeta {
	meta := subagentMeta{
		SubagentType:    strings.TrimSpace(msg.SubagentType),
		TaskDescription: strings.TrimSpace(msg.TaskDescription),
	}
	if msg.ParentToolUseID != nil {
		meta.ParentToolUseID = strings.TrimSpace(*msg.ParentToolUseID)
	}
	return meta
}

func subagentMetaFromToolProgress(msg claudeagent.ToolProgressMessage) subagentMeta {
	meta := subagentMeta{}
	if msg.ParentToolUseID != nil {
		meta.ParentToolUseID = strings.TrimSpace(*msg.ParentToolUseID)
	}
	if msg.TaskID != nil {
		meta.TaskID = strings.TrimSpace(*msg.TaskID)
	}
	return meta
}

func (m subagentMeta) hasSubagentRef() bool {
	return strings.TrimSpace(m.ParentToolUseID) != "" || strings.TrimSpace(m.TaskID) != ""
}

func (m subagentMeta) toMap() map[string]any {
	out := map[string]any{}
	if value := strings.TrimSpace(m.ParentToolUseID); value != "" {
		out["parentToolUseId"] = value
	}
	if value := strings.TrimSpace(m.TaskID); value != "" {
		out["taskId"] = value
	}
	if value := strings.TrimSpace(m.SubagentType); value != "" {
		out["subagentType"] = value
	}
	if value := strings.TrimSpace(m.TaskDescription); value != "" {
		out["taskDescription"] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func claudeTaskToolCall(callID, status, description, text string, meta subagentMeta, extra map[string]any) types.ToolCall {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		callID = strings.TrimSpace(meta.TaskID)
	}
	if callID == "" {
		callID = "claude-task"
	}
	title := strings.TrimSpace(description)
	if title == "" {
		title = strings.TrimSpace(text)
	}
	if title == "" {
		title = firstNonEmpty(meta.SubagentType, meta.TaskID, callID, "task")
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = "running"
	}
	content := []types.ToolCallContentItem(nil)
	if body := strings.TrimSpace(text); body != "" && body != title {
		content = []types.ToolCallContentItem{{Type: "text", Text: body}}
	}
	merged := mergeToolCallMeta(meta.toMap(), map[string]any{
		"rawType": "claude_task",
	})
	merged = mergeToolCallMeta(merged, extra)
	return types.ToolCall{
		CallID:  callID,
		Title:   title,
		Status:  status,
		Kind:    types.ToolKindTask,
		RawType: "claude_task",
		Content: content,
		Meta:    merged,
	}
}

func claudeTaskRunStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed":
		return "complete"
	case "failed", "killed":
		return "failed"
	case "paused":
		return "running"
	case "pending", "running":
		return "running"
	default:
		return "running"
	}
}

func compactBoundarySummary(msg claudeagent.CompactBoundaryMessage) string {
	parts := make([]string, 0, 2)
	if trigger := strings.TrimSpace(msg.CompactMetadata.Trigger); trigger != "" {
		parts = append(parts, "trigger: "+trigger)
	}
	if msg.CompactMetadata.PreTokens > 0 {
		parts = append(parts, fmt.Sprintf("tokens before compact: %d", msg.CompactMetadata.PreTokens))
	}
	return strings.Join(parts, "\n")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func boolPtrValue(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}

func isIgnoredClaudeTaskType(taskType string) bool {
	switch strings.ToLower(strings.TrimSpace(taskType)) {
	case "local_bash":
		return true
	default:
		return false
	}
}

func (s *session) trackTaskInfo(taskID string, update claudeTaskInfo) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return
	}
	s.pendingToolMu.Lock()
	defer s.pendingToolMu.Unlock()
	if s.taskInfos == nil {
		s.taskInfos = make(map[string]claudeTaskInfo)
	}
	info := s.taskInfos[taskID]
	if value := strings.TrimSpace(update.ToolUseID); value != "" {
		info.ToolUseID = value
	}
	if value := strings.TrimSpace(update.TaskType); value != "" {
		info.TaskType = value
	}
	if value := strings.TrimSpace(update.Description); value != "" {
		info.Description = value
	}
	if value := strings.TrimSpace(update.SubagentType); value != "" {
		info.SubagentType = value
	}
	if value := strings.TrimSpace(update.TaskDescription); value != "" {
		info.TaskDescription = value
	}
	s.taskInfos[taskID] = info
}

func (s *session) taskInfo(taskID string) claudeTaskInfo {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return claudeTaskInfo{}
	}
	s.pendingToolMu.Lock()
	defer s.pendingToolMu.Unlock()
	if s.taskInfos == nil {
		return claudeTaskInfo{}
	}
	return s.taskInfos[taskID]
}

func (s *session) taskType(taskID string) string {
	return s.taskInfo(taskID).TaskType
}

func (s *session) emitMessageChunk(content string) {
	s.emitMessageChunkWithMeta(content, subagentMeta{})
}

func (s *session) emitMessageChunkWithMeta(content string, meta subagentMeta) {
	if strings.TrimSpace(content) != "" && !meta.hasSubagentRef() {
		s.sawMessageText = true
	}
	s.emit(types.Event{
		Type:      types.EventTypeMessageChunk,
		SessionID: s.SessionID(),
		Data: types.MessageChunk{
			Content:         content,
			ParentToolUseID: meta.ParentToolUseID,
			TaskID:          meta.TaskID,
			SubagentType:    meta.SubagentType,
			TaskDescription: meta.TaskDescription,
			// MessageID 是 claude 转录条目的 `uuid`（SDK 的 AssistantMessage.UUID）。
			// 它是助手行在 agent 侧的桥梁键：导入器重放同一条消息时读回的就是它，
			// 两个写入者（live / import）由此对上 —— 否则判重只能靠内容猜。
			// 此前这层身份从不落盘，live 与 import 的 agent_id 永远对不上。
			MessageID: meta.MessageID,
		},
	})
}

func (s *session) emitToolUpdate(callID, name string, meta subagentMeta) {
	toolCall := types.ToolCall{
		CallID: callID,
		Title:  name,
		Status: "running",
		Kind:   mapToolKind(name),
		Meta:   meta.toMap(),
	}
	s.emit(types.Event{
		Type:      types.EventTypeToolUpdate,
		SessionID: s.SessionID(),
		Data:      toolCall,
	})
}

func (s *session) emit(event types.Event) {
	s.mu.RLock()
	handler := s.onUpdate
	s.mu.RUnlock()
	if handler == nil {
		return
	}
	handler(event)
}

func (s *session) logRawMessage(raw []byte) {
	log.Printf("[agent/claude] output.raw session=%s msg=%s", s.sessionKey, truncateRaw(raw))
}

func (s *session) logRawToolCallBlock(block claudeagent.ContentBlock) {
	raw, err := json.Marshal(block)
	if err != nil {
		return
	}
	s.agentDebugLog.AppendRaw(raw)
}

func (s *session) logRawToolResult(msg claudeagent.UserMessage) {
	if msg.ToolUseResult == nil {
		return
	}
	raw, err := json.Marshal(msg.ToolUseResult)
	if err != nil {
		return
	}
	s.agentDebugLog.AppendRaw(raw)
}

func (s *session) updateSessionID(msg any) {
	switch m := msg.(type) {
	case claudeagent.SystemMessage:
		s.setSessionID(m.SessionID)
	case claudeagent.AssistantMessage:
		s.setSessionID(m.SessionID)
	case claudeagent.ResultMessage:
		s.setSessionID(m.SessionID)
	case claudeagent.ToolProgressMessage:
		s.setSessionID(m.SessionID)
	case claudeagent.PartialAssistantMessage:
		s.setSessionID(m.SessionID)
	case claudeagent.CompactBoundaryMessage:
		s.setSessionID(m.SessionID)
	case claudeagent.AuthStatusMessage:
		s.setSessionID(m.SessionID)
	case claudeagent.TaskStartedMessage:
		s.setSessionID(m.SessionID)
	case claudeagent.TaskProgressMessage:
		s.setSessionID(m.SessionID)
	case claudeagent.TaskUpdatedMessage:
		s.setSessionID(m.SessionID)
	case claudeagent.TaskNotificationMessage:
		s.setSessionID(m.SessionID)
	case claudeagent.ThinkingTokensMessage:
		s.setSessionID(m.SessionID)
	}
}

func (s *session) setSessionID(sessionID string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	s.mu.Lock()
	s.sessionID = sessionID
	s.mu.Unlock()
}

func (s *session) updateContextWindow(msg claudeagent.ResultMessage) {
	selectedModel := s.CurrentModel()
	if selectedModel != "" {
		for model, usage := range msg.ModelUsage {
			if strings.EqualFold(strings.TrimSpace(model), selectedModel) && usage.ContextWindow > 0 {
				log.Printf("[agent/claude] context_window session=%s model=%q window=%d source=current_model", s.sessionKey, model, usage.ContextWindow)
				s.mu.Lock()
				s.context.ModelContextWindow = usage.ContextWindow
				s.mu.Unlock()
				return
			}
		}
	}

	modelContextWindow := 0
	switch len(msg.ModelUsage) {
	case 0:
	case 1:
		for _, usage := range msg.ModelUsage {
			modelContextWindow = usage.ContextWindow
		}
	default:
		maxUsageTokens := -1
		for _, usage := range msg.ModelUsage {
			usageTokens := usage.InputTokens + usage.OutputTokens
			if usageTokens <= maxUsageTokens {
				continue
			}
			maxUsageTokens = usageTokens
			modelContextWindow = usage.ContextWindow
		}
	}
	if modelContextWindow == 0 {
		return
	}

	s.mu.Lock()
	s.context.ModelContextWindow = modelContextWindow
	s.mu.Unlock()
}

func claudeTokenUsage(msg claudeagent.ResultMessage) *types.TokenUsage {
	inputTokens := 0
	outputTokens := 0
	cacheReadTokens := 0
	cacheWriteTokens := 0
	if msg.Usage != nil {
		inputTokens = max(0, msg.Usage.InputTokens)
		outputTokens = max(0, msg.Usage.OutputTokens)
		cacheReadTokens = max(0, msg.Usage.CacheReadInputTokens)
		cacheWriteTokens = max(0, msg.Usage.CacheCreationInputTokens)
	} else {
		for _, usage := range msg.ModelUsage {
			inputTokens += max(0, usage.InputTokens)
			outputTokens += max(0, usage.OutputTokens)
			cacheReadTokens += max(0, usage.CacheReadInputTokens)
			cacheWriteTokens += max(0, usage.CacheCreationInputTokens)
		}
	}
	if inputTokens == 0 && outputTokens == 0 && cacheReadTokens == 0 && cacheWriteTokens == 0 {
		return nil
	}
	logicalInputTokens := inputTokens + cacheReadTokens + cacheWriteTokens
	return &types.TokenUsage{
		InputTokens:      logicalInputTokens,
		OutputTokens:     outputTokens,
		CacheReadTokens:  &cacheReadTokens,
		CacheWriteTokens: &cacheWriteTokens,
	}
}

func (s *session) enqueueTurn(waiter chan error) {
	s.turnMu.Lock()
	s.turns = append(s.turns, waiter)
	s.turnMu.Unlock()
}

func (s *session) markTurnInterrupted() {
	s.turnMu.Lock()
	s.turnInterrupted = true
	s.turnMu.Unlock()
}

func (s *session) consumeTurnInterruptedLocked() bool {
	interrupted := s.turnInterrupted
	s.turnInterrupted = false
	return interrupted
}

func (s *session) dequeueTurn(waiter chan error) {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	for i, ch := range s.turns {
		if ch != waiter {
			continue
		}
		s.turns = append(s.turns[:i], s.turns[i+1:]...)
		if len(s.turns) == 0 {
			s.turnInterrupted = false
		}
		return
	}
}

func (s *session) completeTurn(err error) {
	s.turnMu.Lock()
	interrupted := s.consumeTurnInterruptedLocked()
	if len(s.turns) == 0 {
		s.turnMu.Unlock()
		return
	}
	waiter := s.turns[0]
	s.turns = s.turns[1:]
	s.turnMu.Unlock()

	if interrupted && err != nil {
		err = errors.New("turn canceled")
	}
	waiter <- err
}

func (s *session) failPendingTurns(err error) {
	s.turnMu.Lock()
	pending := s.turns
	s.turns = nil
	s.turnInterrupted = false
	s.turnMu.Unlock()
	for _, ch := range pending {
		ch <- err
	}
}

func resultErr(msg claudeagent.ResultMessage) error {
	status := strings.ToLower(strings.TrimSpace(msg.Status))
	subtype := strings.ToLower(strings.TrimSpace(msg.Subtype))
	if !msg.IsError && (status == "success" || subtype == "success") && !strings.HasPrefix(subtype, "error") {
		return nil
	}
	if len(msg.Errors) > 0 {
		return errors.New(strings.Join(msg.Errors, "; "))
	}
	if strings.TrimSpace(msg.Result) != "" && (msg.IsError || strings.EqualFold(msg.Status, "error")) {
		return errors.New(msg.Result)
	}
	if strings.TrimSpace(msg.Subtype) != "" {
		return errors.New("claude result: " + msg.Subtype)
	}
	return errors.New("claude turn failed")
}

func isTurnCanceledError(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	return value == "turn canceled" || value == "turn cancelled" || strings.Contains(value, "context canceled")
}

func contextTokensFromPartialEvent(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var event struct {
		Type  string `json:"type"`
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &event); err != nil || strings.TrimSpace(event.Type) != "message_delta" {
		return 0
	}
	return event.Usage.InputTokens + event.Usage.CacheReadInputTokens + event.Usage.CacheCreationInputTokens
}

func extractDeltas(raw json.RawMessage) (string, string) {
	if len(raw) == 0 {
		return "", ""
	}
	var event struct {
		Delta struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
		} `json:"delta"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return "", ""
	}
	switch strings.TrimSpace(event.Delta.Type) {
	case "text_delta":
		if event.Delta.Text != "" {
			return event.Delta.Text, ""
		}
	case "thinking_delta":
		if strings.TrimSpace(event.Delta.Thinking) != "" {
			return "", event.Delta.Thinking
		}
	}
	if event.Delta.Text != "" {
		return event.Delta.Text, ""
	}
	if strings.TrimSpace(event.Delta.Thinking) != "" {
		return "", event.Delta.Thinking
	}
	return event.Text, ""
}

func isKnownNonDisplayPartialEvent(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var event struct {
		Type  string `json:"type"`
		Delta struct {
			Type string `json:"type"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return false
	}
	switch strings.TrimSpace(event.Delta.Type) {
	case "input_json_delta", "signature_delta":
		return true
	}
	switch strings.TrimSpace(event.Type) {
	case "content_block_start", "content_block_stop", "message_start", "message_stop":
		return true
	default:
		return false
	}
}

func mapToolKind(name string) types.ToolKind {
	switch normalizeToolName(name) {
	case "read":
		return types.ToolKindRead
	case "edit", "write", "multiedit":
		return types.ToolKindEdit
	case "delete":
		return types.ToolKindDelete
	case "move", "rename":
		return types.ToolKindMove
	case "glob", "grep", "search":
		return types.ToolKindSearch
	case "websearch":
		return types.ToolKindWebSearch
	case "bash", "execute":
		return types.ToolKindExecute
	case "webfetch", "fetch":
		return types.ToolKindFetch
	case "agent", "task", "taskcreate", "taskupdate", "tasklist", "taskget":
		return types.ToolKindTask
	case "askuserquestion":
		return types.ToolKindAskUser
	case "todowrite", "todos":
		return types.ToolKindTodo
	case "think", "updateplan", "enterplanmode", "exitplanmode":
		return types.ToolKindThink
	case "switchmode":
		return types.ToolKindSwitchMode
	default:
		return types.ToolKindOther
	}
}

func normalizeToolName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "_", "")
	name = strings.ReplaceAll(name, "-", "")
	return name
}

func preview(content string) string {
	trimmed := strings.TrimSpace(content)
	if len(trimmed) <= 300 {
		return trimmed
	}
	return trimmed[:300] + "...(truncated)"
}

func toolCallLogValue(toolCall types.ToolCall) string {
	raw, err := json.Marshal(toolCall)
	if err != nil {
		return `{"marshal_error":true}`
	}
	return string(raw)
}

func truncateRaw(raw []byte) string {
	const maxRawLogBytes = 1024
	if len(raw) > maxRawLogBytes {
		raw = append(raw[:maxRawLogBytes], []byte("...(truncated)")...)
	}
	return string(raw)
}
