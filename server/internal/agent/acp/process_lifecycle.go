package acp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	types "mindfs/server/internal/agent/types"

	acp "github.com/coder/acp-go-sdk"
)

// Start spawns an agent process with ACP mode.
func Start(ctx context.Context, agentName, command string, args []string, cwd string, env map[string]string) (*Process, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	configureProcessCommand(cmd, env)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	tree, err := startProcessCommand(cmd)
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		// Platform setup can fail before cmd.Start gets a chance to close the
		// child ends of the pipes it owns.
		for _, stream := range []any{cmd.Stdin, cmd.Stdout, cmd.Stderr} {
			if closer, ok := stream.(io.Closer); ok {
				_ = closer.Close()
			}
		}
		return nil, err
	}

	proc := &Process{
		agentName:              agentName,
		cmd:                    cmd,
		sessions:               make(map[string]*sessionState),
		sessionsByID:           make(map[string]*sessionState),
		waitCh:                 make(chan error, 1),
		tree:                   tree,
		pendingAskUserByCallID: make(map[string]*pendingAskUser),
	}
	proc.client = &mindfsClient{proc: proc}
	go streamProcessStderr(proc, stderr)
	waitCh := proc.waitCh
	go func() {
		err := cmd.Wait()
		// A wrapper may exit before its descendants. Release the job even when
		// nobody explicitly closes the ACP connection after that exit.
		if closeErr := tree.Close(); closeErr != nil {
			log.Printf("[agent/acp] process.cleanup_error agent=%s err=%v", agentName, closeErr)
		}
		waitCh <- err
		close(waitCh)
	}()

	proc.conn = acp.NewClientSideConnection(proc.client, stdin, stdout)

	return proc, nil
}

// acpClientCapabilities 决定向 agent 广告哪些客户端能力。
//
// CUSTOM(G-AS): 只有 dsh 广告 elicitation.form。openma 适配器据此决定
// ask_user_question 走不走 elicitation（bridge.js 的 clientElicitationForm）；
// 不广告，dsh 就直接抛 "the ACP client does not support form elicitation"。
// 其它 ACP agent 不广告，行为与上游逐字节一致 —— 广告只会让它们开始发
// elicitation，而 mindfs 无法按 session 精确路由，只能 decline。
func acpClientCapabilities(agentName string) acp.ClientCapabilities {
	caps := acp.ClientCapabilities{
		Terminal: false,
	}
	if agentName == "dsh" {
		caps.Elicitation = &acp.ElicitationCapabilities{
			Form: &acp.ElicitationFormCapabilities{},
		}
	}
	return caps
}

// Initialize performs ACP handshake.
func (p *Process) Initialize(ctx context.Context) error {
	// Send initialize request
	resp, err := p.conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersionNumber,
		ClientCapabilities: acpClientCapabilities(p.agentName),
		ClientInfo: &acp.Implementation{
			Name:    "mindfs",
			Version: "1.0.0",
		},
	})

	if err != nil {
		return err
	}
	if raw, err := json.Marshal(resp); err == nil {
		log.Printf("[agent/acp] initialize.resp.raw agent=%s resp=%s", p.agentLabel(), string(raw))
	}
	p.capability = CapabilitySnapshot{
		PromptSupportsAudio:   resp.AgentCapabilities.PromptCapabilities.Audio,
		PromptSupportsImage:   resp.AgentCapabilities.PromptCapabilities.Image,
		PromptSupportsContext: resp.AgentCapabilities.PromptCapabilities.EmbeddedContext,
		SupportsSessionClose:  resp.AgentCapabilities.SessionCapabilities.Close != nil,
		SupportsSessionResume: resp.AgentCapabilities.LoadSession,
	}
	return nil
}

// NewSession creates a new ACP session for the given MindFS session key.
func (p *Process) NewSession(ctx context.Context, sessionKey, cwd string) error {
	p.mu.Lock()
	if _, ok := p.sessions[sessionKey]; ok {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	resp, err := p.conn.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		return err
	}
	if raw, err := json.Marshal(resp); err == nil {
		log.Printf("[agent/acp] new_session.resp.raw agent=%s session_key=%s resp=%s", p.agentLabel(), sessionKey, string(raw))
	}
	sess := &sessionState{
		ID:            resp.SessionId,
		models:        resp.Models,
		modes:         resp.Modes,
		configOptions: cloneConfigOptions(resp.ConfigOptions),
	}
	p.mu.Lock()
	if _, ok := p.sessions[sessionKey]; ok {
		p.mu.Unlock()
		return nil
	}
	if resp.Models != nil {
		p.models = resp.Models
	}
	if resp.Modes != nil {
		p.modes = resp.Modes
	}
	p.configOptions = cloneConfigOptions(resp.ConfigOptions)
	p.sessions[sessionKey] = sess
	p.sessionsByID[string(resp.SessionId)] = sess
	p.mu.Unlock()
	return nil
}

func (p *Process) ResumeSession(ctx context.Context, sessionKey, sessionID, cwd string) error {
	p.mu.Lock()
	if _, ok := p.sessions[sessionKey]; ok {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	resp, err := p.conn.ResumeSession(ctx, acp.ResumeSessionRequest{
		Cwd:        cwd,
		McpServers: []acp.McpServer{},
		SessionId:  acp.SessionId(strings.TrimSpace(sessionID)),
	})
	if err != nil {
		return err
	}
	sess := &sessionState{
		ID:            acp.SessionId(strings.TrimSpace(sessionID)),
		models:        resp.Models,
		configOptions: cloneConfigOptions(resp.ConfigOptions),
	}
	if resp.Modes != nil {
		sess.modes = resp.Modes
	}
	p.mu.Lock()
	if _, ok := p.sessions[sessionKey]; ok {
		p.mu.Unlock()
		return nil
	}
	if sess.models != nil {
		p.models = sess.models
	}
	if sess.modes != nil {
		p.modes = sess.modes
	}
	p.configOptions = cloneConfigOptions(resp.ConfigOptions)
	p.sessions[sessionKey] = sess
	p.sessionsByID[string(sess.ID)] = sess
	p.mu.Unlock()
	return nil
}

// SetOnUpdate registers a callback for a specific session.
func (p *Process) SetOnUpdate(sessionKey string, onUpdate func(SessionUpdate)) {
	sess := p.getSessionByKey(sessionKey)
	if sess != nil {
		sess.setOnUpdate(onUpdate)
	}
}

// SendMessage sends a prompt to a specific session.
func (p *Process) SendMessage(ctx context.Context, sessionKey, content string) error {
	start := time.Now()
	sess := p.getSessionByKey(sessionKey)

	if sess == nil {
		return nil
	}
	log.Printf("[agent/acp] send.begin agent=%s session_key=%s content=%q", p.agentLabel(), sessionKey, content)

	promptCtx, promptCancel := context.WithCancel(ctx)
	promptID := time.Now().UnixNano()
	p.setActivePrompt(promptID, promptCancel)
	defer func() {
		p.clearActivePrompt(promptID)
		promptCancel()
	}()

	req := acp.PromptRequest{
		SessionId: sess.ID,
		Prompt: []acp.ContentBlock{
			acp.TextBlock(content),
		},
	}
	// 把 MindFS 本轮的 mindfs_id 作为 prompt 的 messageId 递出去（方案 D3）。
	//
	// 为什么要这么接：实时路径写的 user 行与导入侧写的 user 行都从 agent 的
	// messageId 派生出同一个 mindfs_id，回显这一层让「递出去的」与「写进 agent_id
	// 的」始终是同一条 id —— 未来靠 agent_id 判重时它才可用。
	//
	// ACP 标了这个字段为 **UNSTABLE**，agent 未必回显。所以：
	//   · 没有 mindfs_id（不该发生，但协议允许）→ 照旧不传；
	//   · 必须是 UUID 形态 —— 导入侧只认 UUID，递个别的等于递了个对不上的身份；
	//   · 绝不能为了让 agent 回显而自己编一个 —— 编出来的 id 与库里那一行不同，
	//     反而制造「有身份却对不上」的假行。
	if turnID := types.PromptTurnIDFromContext(promptCtx); isUUIDShape(turnID) {
		messageID := turnID
		req.MessageId = &messageID
	}
	resp, err := p.conn.Prompt(promptCtx, req)
	if err != nil {
		return p.wrapPromptError(sessionKey, string(sess.ID), err)
	}
	// 回显核对（方案 D10：对不上就**记日志，绝不猜**）。
	//
	// 我们递了 mindfs_id，agent 应当在 `PromptResponse.userMessageId` 里原样回显。
	// 回显不一致意味着「我以为这条转录对应库里那一行」的依据不成立 —— 但这**不是**
	// 错误：ACP 把 `messageId` 标为 UNSTABLE，agent 可以不认。库里那一行照写，
	// 只是它将来只能靠内容兜底判重。绝不能在这里拿回显值去改写 agent_id：
	// 换成 agent 的值就等于承认「库里的行」是 agent 那条，两边对不上才是真的错。
	if requested := messageIDOf(req.MessageId); requested != "" {
		if echoed := messageIDOf(resp.UserMessageId); echoed == "" {
			log.Printf("[agent/acp] prompt.message_id.no_echo agent=%s session_key=%s"+
				"（该 agent 未回显 messageId，本轮回退内容判重）", p.agentLabel(), sessionKey)
		} else if echoed != requested {
			log.Printf("[agent/acp] prompt.message_id.mismatch agent=%s session_key=%s requested=%s echoed=%s"+
				"（不采信回显值：agent_id 保持 MindFS mindfs_id，判重本轮退回内容比对）",
				p.agentLabel(), sessionKey, requested, echoed)
		}
	}
	var tokenUsage *types.TokenUsage
	if resp.Usage != nil {
		// Some older ACP agents expose no usage_update notification. Keep a
		// compatibility fallback for those agents, but never let prompt-level
		// accounting overwrite an authoritative session context update.
		if !sess.hasContextUsageUpdate() {
			current := sess.getContextWindow()
			current.TotalTokens = max(0, resp.Usage.TotalTokens)
			sess.setContextWindow(current)
		}
		tokenUsage = sess.tokenUsageForPrompt(p.agentName, resp.Usage)
	}

	// Signal completion
	if onUpdate := sess.getOnUpdate(); onUpdate != nil {
		onUpdate(SessionUpdate{
			Type:       UpdateTypeMessageDone,
			SessionID:  string(sess.ID),
			TokenUsage: tokenUsage,
		})
	}
	log.Printf("[agent/acp] send.done agent=%s session_key=%s duration_ms=%d", p.agentLabel(), sessionKey, time.Since(start).Milliseconds())

	return nil
}

func (p *Process) CancelCurrentTurn(sessionKey string) error {
	p.cancelPendingAskUser(sessionKey)
	sess := p.getSessionByKey(sessionKey)
	if sess == nil {
		return nil
	}
	// CUSTOM(G-G): Cancel 是跨进程 RPC，对端卡住时会永久挂起（与 claude 后端的
	// Interrupt 同形态，见 AGENTS.md「Interrupt 可能永久阻塞」）。上游此处是裸
	// context.Background()，合上游时会被整体覆盖回去 —— 必须保留这个超时包装，
	// 否则「停止按钮」在 ACP 后端上会挂死（守卫见 cancel_rpc_timeout_guard_test.go）。
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return p.conn.Cancel(ctx, acp.CancelNotification{
		SessionId: sess.ID,
	})
}

// ForgetSession removes only MindFS' local bookkeeping for a session.
func (p *Process) ForgetSession(sessionKey string) {
	p.mu.Lock()
	if sess, ok := p.sessions[sessionKey]; ok {
		delete(p.sessionsByID, string(sess.ID))
		delete(p.sessions, sessionKey)
	}
	p.mu.Unlock()
}

// CloseSession asks an ACP agent to cancel outstanding work and release the
// session resources it owns. Local bookkeeping is removed only after the agent
// confirms the close.
func (p *Process) CloseSession(ctx context.Context, sessionKey string) error {
	if !p.capability.SupportsSessionClose {
		return errors.New("ACP agent does not support session/close")
	}
	if !p.capability.SupportsSessionResume {
		return errors.New("ACP agent cannot safely release a resumable session")
	}
	sess := p.getSessionByKey(sessionKey)
	if sess == nil {
		return nil
	}
	if _, err := p.conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: sess.ID}); err != nil {
		return err
	}
	p.ForgetSession(sessionKey)
	p.cancelPendingAskUser(sessionKey)
	return nil
}

// Close terminates the process.
func (p *Process) Close() error {
	p.closeMu.Lock()
	defer p.closeMu.Unlock()
	p.mu.RLock()
	cmd := p.cmd
	waitCh := p.waitCh
	tree := p.tree
	p.mu.RUnlock()

	if cmd == nil || cmd.Process == nil {
		return nil
	}

	// 先把挂起的提问全部按「用户取消」回掉，否则 elicitation handler 会一直阻塞到
	// 连接关闭，白等一个永远不会来的答案。
	p.dropAllPendingAskUser()

	pid := cmd.Process.Pid
	log.Printf("[agent/acp] process.close.begin agent=%s pid=%d", p.agentLabel(), pid)
	if err := tree.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		log.Printf("[agent/acp] process.close.kill_error agent=%s pid=%d err=%v", p.agentLabel(), pid, err)
		return err
	}

	select {
	case err := <-waitCh:
		if closeErr := tree.Close(); closeErr != nil {
			return closeErr
		}
		p.mu.Lock()
		p.cmd = nil
		p.waitCh = nil
		p.tree = nil
		p.mu.Unlock()
		// Forced termination is expected on both Unix and Windows.
		var exitErr *exec.ExitError
		if err != nil && !errors.As(err, &exitErr) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			log.Printf("[agent/acp] process.close.wait_error agent=%s pid=%d err=%v", p.agentLabel(), pid, err)
			return err
		}
		log.Printf("[agent/acp] process.close.done agent=%s pid=%d", p.agentLabel(), pid)
		return nil
	case <-time.After(10 * time.Second):
		log.Printf("[agent/acp] process.close.timeout agent=%s pid=%d %s", p.agentLabel(), pid, processDiagnostic(pid))
		return errors.New("ACP process did not exit after kill")
	}
}
