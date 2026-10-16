package usecase

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/commandexec"
	"mindfs/server/internal/session"
)

type RunTransientSlashCommandInput struct {
	RootID      string
	Key         string
	Agent       string
	Model       string
	Mode        string
	Effort      string
	FastService string
	Command     string
	OnUpdate    func(agenttypes.Event)
}

type codexDeviceCodeLoginSession interface {
	LoginChatGPTDeviceCode(context.Context) error
}

func (s *Service) sendCommandMessage(ctx context.Context, in SendMessageInput, manager *session.Manager, current *session.Session, turnID string, userExchangeSeq int) error {
	if strings.TrimSpace(in.Content) == "" {
		return errors.New("command required")
	}
	root := manager.Root()
	rootAbs, err := root.RootDir()
	if err != nil {
		return err
	}
	callID := "cmd-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	startTool := agenttypes.ToolCall{
		CallID:  callID,
		Title:   in.Content,
		Status:  "running",
		Kind:    agenttypes.ToolKindExecute,
		RawType: "commandExecution",
		Meta: map[string]any{
			"source":       "userShell",
			"phase":        "start",
			"command":      in.Content,
			"cwd":          ".",
			"terminalCols": in.TerminalCols,
		},
	}
	if in.OnUpdate != nil {
		in.OnUpdate(agenttypes.Event{Type: agenttypes.EventTypeToolCall, Data: startTool})
	}

	proc, err := commandexec.StartInSession(ctx, commandexec.Options{
		Command:      in.Content,
		Cwd:          rootAbs,
		Shells:       configuredShells(s.Registry),
		Shell:        in.Shell,
		RootID:       in.RootID,
		Session:      current.Key,
		TerminalCols: in.TerminalCols,
	})
	if err != nil {
		log.Printf("[command] start.error root=%s session=%s command=%q err=%v", in.RootID, current.Key, in.Content, err)
		final := startTool
		final.Status = "failed"
		final.Meta = cloneMeta(final.Meta)
		final.Meta["phase"] = "final"
		final.Meta["exitCode"] = -1
		final.Meta["error"] = err.Error()
		final.Content = []agenttypes.ToolCallContentItem{{Type: "text", Text: err.Error()}}
		if in.OnUpdate != nil {
			in.OnUpdate(agenttypes.Event{Type: agenttypes.EventTypeToolUpdate, Data: final})
			in.OnUpdate(agenttypes.Event{Type: agenttypes.EventTypeMessageDone, Data: agenttypes.MessageDone{}})
		}
		if persistErr := persistCommandTurn(ctx, manager, current, final, turnID, userExchangeSeq); persistErr != nil {
			return persistErr
		}
		return err
	}

	limiter := commandexec.NewOutputLimiter()
	ticker := time.NewTicker(limiter.FlushEvery())
	defer ticker.Stop()
	done := make(chan commandexec.Result, 1)
	go func() {
		done <- proc.Wait()
	}()

	var result commandexec.Result
	var haveResult bool
	cancelStarted := false
	outputCh := proc.Output()
	for !haveResult {
		select {
		case chunk, ok := <-outputCh:
			if !ok {
				outputCh = nil
				continue
			}
			limiter.Write(chunk)
		case <-ticker.C:
			flushCommandOutput(in.OnUpdate, startTool, limiter)
			ticker.Reset(limiter.FlushEvery())
		case result = <-done:
			haveResult = true
		case <-ctx.Done():
			if !cancelStarted {
				cancelStarted = true
				go stopCommandProcess(proc)
			}
		}
	}
	drainCommandOutput(proc.Output(), limiter, 250*time.Millisecond)
	flushCommandOutput(in.OnUpdate, startTool, limiter)

	final := startTool
	final.Status = "success"
	if cancelStarted || ctx.Err() != nil {
		final.Status = "cancelled"
	} else if result.ExitCode != 0 {
		final.Status = "failed"
	}
	tail := limiter.Tail()
	persistedBytes := limiter.TailBytes()
	outputBytes := limiter.TotalBytes()
	text := string(tail)
	if strings.TrimSpace(result.Shell) != "" {
		if err := manager.UpdateShell(context.Background(), current, result.Shell); err != nil {
			log.Printf("[command] shell.update.error root=%s session=%s shell=%q err=%v", in.RootID, current.Key, result.Shell, err)
		}
	}
	if outputBytes > persistedBytes {
		text = fmt.Sprintf("[output truncated: showing last %d bytes of %d bytes]\n%s", persistedBytes, outputBytes, text)
	}
	final.Meta = map[string]any{
		"source":         "userShell",
		"phase":          "final",
		"command":        in.Content,
		"cwd":            ".",
		"shell":          result.Shell,
		"exitCode":       result.ExitCode,
		"durationMs":     result.Duration.Milliseconds(),
		"outputBytes":    outputBytes,
		"persistedBytes": persistedBytes,
		"truncated":      outputBytes > persistedBytes,
		"truncation":     "tail",
		"terminalCols":   in.TerminalCols,
	}
	if cancelStarted || ctx.Err() != nil {
		final.Meta["cancelled"] = true
	}
	final.Content = []agenttypes.ToolCallContentItem{{Type: "text", Text: text}}
	if in.OnUpdate != nil {
		in.OnUpdate(agenttypes.Event{Type: agenttypes.EventTypeToolUpdate, Data: final})
		in.OnUpdate(agenttypes.Event{Type: agenttypes.EventTypeMessageDone, Data: agenttypes.MessageDone{}})
	}
	if err := persistCommandTurn(context.Background(), manager, current, final, turnID, userExchangeSeq); err != nil {
		log.Printf("[command] persist.error root=%s session=%s call=%s err=%v", in.RootID, current.Key, callID, err)
		return err
	}
	if final.Status == "success" || final.Status == "cancelled" {
		if err := UpsertCommandSuggestion(manager, CommandSuggestion{
			Command:        in.Content,
			Cwd:            ".",
			Shell:          result.Shell,
			RootID:         in.RootID,
			LastExitCode:   result.ExitCode,
			LastDurationMs: result.Duration.Milliseconds(),
			LastUsedAt:     result.FinishedAt,
		}); err != nil {
			log.Printf("[command/history] upsert.error root=%s session=%s err=%v", in.RootID, current.Key, err)
		}
	}
	return nil
}

func flushCommandOutput(onUpdate func(agenttypes.Event), base agenttypes.ToolCall, limiter *commandexec.OutputLimiter) {
	if onUpdate == nil || limiter == nil {
		return
	}
	chunk, ok := limiter.Flush()
	if !ok {
		return
	}
	update := base
	update.Status = "running"
	update.Meta = map[string]any{
		"source":       "userShell",
		"phase":        "stream",
		"outputMode":   "ring",
		"skippedBytes": chunk.SkippedBytes,
	}
	update.Content = []agenttypes.ToolCallContentItem{{Type: "text", Text: chunk.Text}}
	onUpdate(agenttypes.Event{Type: agenttypes.EventTypeToolUpdate, Data: update})
}

func drainCommandOutput(output <-chan []byte, limiter *commandexec.OutputLimiter, maxWait time.Duration) {
	if output == nil || limiter == nil {
		return
	}
	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	for {
		select {
		case chunk, ok := <-output:
			if !ok {
				return
			}
			if len(chunk) > 0 {
				limiter.Write(chunk)
			}
		case <-timer.C:
			return
		}
	}
}

func stopCommandProcess(proc commandexec.Process) {
	if proc == nil {
		return
	}
	_ = proc.Interrupt()
	time.Sleep(2 * time.Second)
	_ = proc.Terminate()
	time.Sleep(3 * time.Second)
	_ = proc.KillTree()
}

func configuredShells(registry Registry) []commandexec.ShellSpec {
	if registry == nil {
		return nil
	}
	pool := registry.GetAgentPool()
	if pool == nil {
		return nil
	}
	cfg := pool.Config()
	shells := make([]commandexec.ShellSpec, 0, len(cfg.Shells))
	for _, shell := range cfg.Shells {
		shells = append(shells, commandexec.ShellSpec{
			Command:       shell.Command,
			Args:          append([]string(nil), shell.Args...),
			LongShellArgs: append([]string(nil), shell.LongShellArgs...),
			CommandPrefix: shell.CommandPrefix,
		})
	}
	return shells
}

// persistCommandTurn 只补助手行与其 aux：用户行已在 SendMessage 起手、命令真正执行前
// 落盘（与聊天会话同一条规则），这里再写一次会落重复。
//
// 助手行必须复用本轮 user 行 mint 的 mindfs_id（方案 C2）：命令会话是三条服务端发起的
// 轮次路径之一（另两条是 SendMessage 与子会话订阅），少了它命令轮的正文在 S2b 之后
// 挂不回轮次。助手正文为空，块的正文全部来自 aux（tool_call）。
func persistCommandTurn(ctx context.Context, manager *session.Manager, current *session.Session, final agenttypes.ToolCall, turnID string, userExchangeSeq int) error {
	ctx = session.WithExchangeSource(ctx, session.ExchangeSourceLive)
	ctx = session.WithExchangeIdentity(ctx, session.ExchangeIdentity{MindFSID: turnID, TurnSeq: userExchangeSeq})
	agentSeq, err := manager.AddExchangeForAgentAndGetSeq(ctx, current, "agent", "", "", "", "", "")
	if err != nil {
		return err
	}
	if agentSeq <= 0 {
		return errors.New("command agent exchange was not appended")
	}
	aux := session.ExchangeAux{
		Seq:      agentSeq,
		Line:     0,
		ToolCall: &final,
	}
	if err := manager.AddExchangeAux(ctx, current.Key, aux); err != nil {
		return err
	}
	if err := persistTurnBlocks(ctx, manager, current.Key, agentSeq, turnID, "", []session.ExchangeAux{aux}); err != nil {
		log.Printf("[command] persist.blocks.error session=%s seq=%d err=%v", current.Key, agentSeq, err)
	}
	return nil
}

func cloneMeta(meta map[string]any) map[string]any {
	out := make(map[string]any, len(meta))
	for k, v := range meta {
		out[k] = v
	}
	return out
}

func (s *Service) RunTransientSlashCommand(ctx context.Context, in RunTransientSlashCommandInput) error {
	if err := s.ensureRegistry(); err != nil {
		return err
	}
	command := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(in.Command)), "/")
	agentName := strings.TrimSpace(in.Agent)
	if agentName != "codex" || (command != "status" && command != "login") {
		return errors.New("unsupported transient slash command")
	}
	key := strings.TrimSpace(in.Key)
	if key == "" {
		key = fmt.Sprintf("transient-%s-%d", command, time.Now().UnixNano())
	}
	sendLock := getSessionSendLock(key)
	sendLock.Lock()
	defer sendLock.Unlock()

	turnCtx, turnCancel := context.WithCancel(ctx)
	registerActiveTurn(in.RootID, key, turnCancel)
	defer unregisterActiveTurn(in.RootID, key)

	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return err
	}
	current, err := manager.Get(ctx, key, 0)
	if err != nil {
		current = &session.Session{
			Key:         key,
			Type:        session.TypeChat,
			Model:       strings.TrimSpace(in.Model),
			AgentCtxSeq: map[string]int{},
			Name:        "Transient slash command",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
	} else {
		if current.Type == session.TypeCommand {
			return errors.New("slash commands are not supported for command sessions")
		}
		if currentAgent := strings.TrimSpace(session.InferAgentFromSession(current)); currentAgent != "" && currentAgent != agentName {
			return errors.New("slash command agent does not match session agent")
		}
	}
	if err := s.validateAgentModel(agentName, in.Model); err != nil {
		return err
	}
	agentPool := s.Registry.GetAgentPool()
	if agentPool == nil {
		return errors.New("agent pool not configured")
	}
	root := manager.Root()
	rootAbs, _ := root.RootDir()
	sess, _, err := s.ensureAgentSession(turnCtx, agentPool, manager, current, agentName, in.Model, in.Mode, in.Effort, in.FastService, rootAbs, "")
	if err != nil {
		return err
	}
	setActiveTurnSession(in.RootID, current.Key, sess)

	sess.OnUpdate(func(update agenttypes.Event) {
		update = normalizeAgentUpdatePaths(root, update)
		clientUpdate := compactAgentUpdate(update)
		if in.OnUpdate != nil {
			in.OnUpdate(clientUpdate)
		}
	})

	if command == "login" {
		loginSess, ok := sess.(codexDeviceCodeLoginSession)
		if !ok {
			return errors.New("codex login is not supported by this runtime")
		}
		err = loginSess.LoginChatGPTDeviceCode(turnCtx)
	} else {
		finishUse := agentPool.BeginSessionUse(agentPoolSessionKey(current.Key, agentName))
		defer finishUse()
		err = sess.SendMessage(turnCtx, "/"+command)
	}
	if err != nil && !isCanceledTurnError(err) {
		if prober := s.Registry.GetProber(); prober != nil {
			prober.ReportRuntimeFailure(agentName, err)
		}
		return err
	}
	return err
}
