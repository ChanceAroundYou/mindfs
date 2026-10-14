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
