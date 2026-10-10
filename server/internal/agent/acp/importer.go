package acp

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	agenttypes "mindfs/server/internal/agent/types"
)

type ImporterOptions struct {
	AgentName  string
	Command    string
	Args       []string
	Env        map[string]string
	ResolveCwd func(rootPath string) string
}

type Importer struct {
	agentName  string
	command    string
	args       []string
	env        map[string]string
	resolveCwd func(rootPath string) string
}

type importCollector struct {
	mu        sync.Mutex
	exchanges []agenttypes.ImportedExchange
}

func NewImporter(opts ImporterOptions) *Importer {
	return &Importer{
		agentName:  strings.TrimSpace(opts.AgentName),
		command:    strings.TrimSpace(opts.Command),
		args:       append([]string{}, opts.Args...),
		env:        cloneEnv(opts.Env),
		resolveCwd: opts.ResolveCwd,
	}
}

func (i *Importer) AgentName() string {
	return i.agentName
}

// UsesPromptMessageID 恒为 true：ACP 协议的 `PromptRequest.MessageId` 就是这层身份
// （方案 D3）。MindFS 把自己的 turn_id 递进去、agent 原样回显成 `userMessageId`，
// 于是实时路径与导入路径能用同一个 id 认领同一轮，判重不再靠内容猜。
func (i *Importer) UsesPromptMessageID() bool {
	return true
}

// HasTranscriptTimestamps 恒为 false：ACP 协议的消息更新不带任何时间戳
// （`SessionNotification` 只有 sessionId + update）。所以 ACP 导入行的 Timestamp 恒为
// 合成值（见 ImportedExchange.SynthesizedAt），`TimestampFloor` 对它无效。
func (i *Importer) HasTranscriptTimestamps() bool {
	return false
}

func (i *Importer) ListExternalSessions(ctx context.Context, in agenttypes.ListExternalSessionsInput) (agenttypes.ListExternalSessionsResult, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	items := make([]agenttypes.ExternalSessionSummary, 0, limit)
	err := i.ScanExternalSessions(ctx, in, func(item agenttypes.ExternalSessionSummary) (bool, error) {
		items = append(items, item)
		return len(items) < limit, nil
	})
	if err != nil {
		return agenttypes.ListExternalSessionsResult{}, err
	}
	return agenttypes.ListExternalSessionsResult{Items: items}, nil
}

func (i *Importer) ScanExternalSessions(ctx context.Context, in agenttypes.ListExternalSessionsInput, visit agenttypes.ExternalSessionVisitFunc) error {
	proc, cwd, err := i.openProcess(ctx, in.RootPath)
	if err != nil {
		return err
	}
	defer proc.Close()

	var cursor *string
	for {
		resp, err := proc.conn.ListSessions(ctx, acpsdk.ListSessionsRequest{
			Cursor: cursor,
			Cwd:    &cwd,
		})
		if err != nil {
			if isUnsupportedACPListSessions(err) {
				return nil
			}
			return err
		}
		for _, item := range resp.Sessions {
			if normalizeComparablePath(item.Cwd) != normalizeComparablePath(cwd) {
				continue
			}
			updatedAt := parseTimeStringPtr(item.UpdatedAt)
			if !in.BeforeTime.IsZero() && !updatedAt.Before(in.BeforeTime) {
				continue
			}
			if !in.AfterTime.IsZero() && !updatedAt.After(in.AfterTime) {
				continue
			}
			firstUserText := ""
			if item.Title != nil {
				firstUserText = strings.TrimSpace(*item.Title)
			}
			shouldContinue, err := visit(agenttypes.ExternalSessionSummary{
				Agent:          i.agentName,
				AgentSessionID: string(item.SessionId),
				Cwd:            item.Cwd,
				FirstUserText:  firstUserText,
				UpdatedAt:      updatedAt,
			})
			if err != nil {
				return err
			}
			if !shouldContinue {
				return nil
			}
		}
		if resp.NextCursor == nil || strings.TrimSpace(*resp.NextCursor) == "" {
			break
		}
		next := strings.TrimSpace(*resp.NextCursor)
		cursor = &next
	}
	return nil
}

func isUnsupportedACPListSessions(err error) bool {
	var reqErr *acpsdk.RequestError
	if !errors.As(err, &reqErr) {
		return false
	}
	return reqErr.Code == -32601
}

func (i *Importer) ImportExternalSession(ctx context.Context, in agenttypes.ImportExternalSessionInput) (agenttypes.ImportedExternalSession, error) {
	sessionID := strings.TrimSpace(in.AgentSessionID)
	if sessionID == "" {
		return agenttypes.ImportedExternalSession{}, errors.New("agent session id required")
	}
	// 注意：ACP 的 `SessionUpdate` **不带任何时间戳**（SDK 里只有 NES 的
	// `timestampMs`，与消息无关）。所以这里的时间只能是「本次导入运行的观测时刻」，
	// 它**不是**那一轮真实发生的时间，因此：
	//   ① 打上 SynthesizedAt，让下游能区分「真时间」与「猜的时间」；
	//   ② 不能拿它当过滤判据 —— 重放出来的行每次都会拿到一个更新的时间，
	//      `AfterTimestamp` / `TimestampFloor` 永远挡不住（实测 2026-10-07：某
	//      live-owned 会话 Full 同步把 9 对已经写过的轮次又写了一遍，投影只能靠
	//      内容猜着折叠）。ACP 的防重放只能靠 native id 身份判重（S2c）。

	proc, cwd, err := i.openProcess(ctx, in.RootPath)
	if err != nil {
		return agenttypes.ImportedExternalSession{}, err
	}
	defer proc.Close()

	collector := &importCollector{}
	if err := proc.attachImportCollector(sessionID, collector); err != nil {
		return agenttypes.ImportedExternalSession{}, err
	}
	defer proc.detachImportCollector(sessionID)

	_, err = proc.conn.LoadSession(ctx, acpsdk.LoadSessionRequest{
		Cwd:        cwd,
		McpServers: []acpsdk.McpServer{},
		SessionId:  acpsdk.SessionId(sessionID),
	})
	if err != nil {
		return agenttypes.ImportedExternalSession{}, err
	}
	return agenttypes.ImportedExternalSession{
		Agent:          i.agentName,
		AgentSessionID: sessionID,
		Cwd:            cwd,
		Exchanges:      collector.snapshot(),
	}, nil
}

func (i *Importer) openProcess(ctx context.Context, rootPath string) (*Process, string, error) {
	if strings.TrimSpace(i.command) == "" {
		return nil, "", errors.New("agent command required")
	}
	cwd := strings.TrimSpace(rootPath)
	if i.resolveCwd != nil {
		cwd = strings.TrimSpace(i.resolveCwd(rootPath))
	}
	if cwd == "" {
		return nil, "", errors.New("root path required")
	}
	proc, err := Start(ctx, i.agentName, i.command, i.args, cwd, i.env)
	if err != nil {
		return nil, "", err
	}
	if err := proc.Initialize(ctx); err != nil {
		proc.Close()
		return nil, "", err
	}
	return proc, cwd, nil
}

func (p *Process) attachImportCollector(sessionID string, collector *importCollector) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("session id required")
	}
	if collector == nil {
		return errors.New("collector required")
	}
	state := &sessionState{
		ID: acpsdk.SessionId(sessionID),
	}
	state.setOnUpdate(func(update SessionUpdate) {
		collector.handle(update)
	})
	p.mu.Lock()
	p.sessionsByID[sessionID] = state
	p.mu.Unlock()
	return nil
}

func (p *Process) detachImportCollector(sessionID string) {
	p.mu.Lock()
	delete(p.sessionsByID, strings.TrimSpace(sessionID))
	p.mu.Unlock()
}

func (c *importCollector) handle(update SessionUpdate) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// SessionUpdate 的各个字段是**互斥**的（SDK 的 marshaler 只会序列化其中一个），
	// 所以 thought 必须是独立的一支，不能挂在 message_chunk 上顺带取。
	switch {
	case update.Raw.UserMessageChunk != nil && update.Raw.UserMessageChunk.Content.Text != nil:
		c.append("user", update.Raw.UserMessageChunk.Content.Text.Text,
			messageIDOf(update.Raw.UserMessageChunk.MessageId), "")
	case update.Raw.AgentMessageChunk != nil && update.Raw.AgentMessageChunk.Content.Text != nil:
		c.append("agent", update.Raw.AgentMessageChunk.Content.Text.Text,
			messageIDOf(update.Raw.AgentMessageChunk.MessageId), "")
	case update.Raw.AgentThoughtChunk != nil && update.Raw.AgentThoughtChunk.Content.Text != nil:
		// reasoning 也要导入（方案 §8 遗留缺陷）。丢它的后果与线上那个 bug 同源：
		// 只调工具、没有文本的轮次，导入后正文是空的。
		c.appendThought(update.Raw.AgentThoughtChunk.Content.Text.Text,
			messageIDOf(update.Raw.AgentThoughtChunk.MessageId))
	}
}

// messageIDOf 只接受 UUID 形态的 id。ACP 的 `messageId` 是 **UNSTABLE** 字段，
// agent 应当回显 MindFS 递过去的 UUID；但实现不保证 —— 收不到 uuid 就返回空串，
// 让判重退回内容比对，而不是拿一个谁也对不上的字符串当身份。
//
// 为什么严到只收 UUID：MindFS 递过去的就是自己 mint 的 turn_id（方案 D3），
// 形如 `550e8400-e29b-41d4-a716-446655440000`。只认这一种形态，就保证了
// 「写进 native_prompt 的」和「读回来对上的」是同一个 id 的同一层含义。
func messageIDOf(raw *string) string {
	if raw == nil {
		return ""
	}
	normalized := strings.ToLower(strings.TrimSpace(strings.Trim(*raw, "\"")))
	if !isUUIDShape(normalized) {
		return ""
	}
	return normalized
}

// isUUIDShape 校验 ACP 协议要求的 UUID 形态（8-4-4-4-12，十六进制）。
//
// ACP 规范对 `messageId` 的原文要求是「客户端与 agent **必须**使用 UUID 格式」。
// 只认这一种形态，是为了保证「递出去的」「写进 native_prompt 的」「从转录读回来
// 对上的」三者指的是同一个 id 的同一层含义；别的字符串一律当成「没有身份」。
func isUUIDShape(raw string) bool {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if len(normalized) != 36 {
		return false
	}
	for i, r := range normalized {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if r != '-' {
				return false
			}
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

// append 把一段文本 / reasoning 归到当前消息上。
//
// 行边界 = **消息边界**（ACP 规范原文：「messageId 变化表示一条新消息开始」）。
// 这是判重能成立的前提：行级身份取「该行最后一条 agent 原生消息 id」，两端
// （实时路径 / 导入路径）都按同一个规则切行、取 id，才会取到同一个值。切行规则
// 一旦不一致，同一轮就对不上 —— 那又退回猜内容了。
//
// 没有 messageId（ACP 标该字段 UNSTABLE，未必每个 agent 都发）时退回旧行为：
// 相邻同角色文本合并成一行。这会牺牲一点精度（一条行可能承载多条消息），但
// 碎成多行会让库里多出一堆「同一句话」的行，更糟。
//
// 空内容也要开行：只调工具、只有 reasoning 的轮次没有文本块，没有这一行整轮
// 就在库里消失了（这正是线上那个「空回复」bug 的形态）。
func (c *importCollector) append(role, content, nativeID, thought string) {
	content = strings.TrimSpace(content)
	thought = strings.TrimSpace(thought)
	if content == "" && thought == "" {
		return
	}
	if last := c.tailFor(role, nativeID); last != nil {
		if content != "" {
			if nativeID == "" {
				last.Content = strings.TrimSpace(last.Content + content)
			} else {
				last.Content = strings.TrimSpace(last.Content + "\n\n" + content)
			}
		}
		if thought != "" {
			last.Aux = append(last.Aux, acpThoughtAux(last.Content, thought))
		}
		return
	}
	item := agenttypes.ImportedExchange{
		Role:      role,
		Content:   content,
		NativeID:  nativeID,
		Timestamp: time.Now().UTC(),
	}
	// ACP 的 SessionUpdate **不带任何时间戳**，所以这个 time.Now() 不是「那一轮真实
	// 发生的时间」，只是「我们读到它的时间」。标出来，免得下游把它当真时间用
	// （时间地板 / 排序 / 展示）。
	item.SynthesizedAt = item.Timestamp
	if thought != "" {
		item.Aux = append(item.Aux, acpThoughtAux(item.Content, thought))
	}
	c.exchanges = append(c.exchanges, item)
}

// tailFor 返回「这一段应该并进去的那一行」，不该并入时返回 nil。
func (c *importCollector) tailFor(role, nativeID string) *agenttypes.ImportedExchange {
	if len(c.exchanges) == 0 {
		return nil
	}
	last := &c.exchanges[len(c.exchanges)-1]
	if last.Role != role {
		return nil
	}
	// 有身份时只认同一身份 —— 身份变了就是另一条消息。
	if nativeID != "" {
		if last.NativeID != nativeID {
			return nil
		}
		return last
	}
	// 无身份时只合并 agent 侧的文本：user 侧每一句都是一条独立的用户输入，
	// 把两句「继续」粘成一行会让用户自己发过几次都看不出来（实测同一会话里
	// 「请继续」出现过 7 次）。
	if role != "agent" {
		return nil
	}
	return last
}

// appendThought 把 reasoning 归到当前消息上。
//
// 它和文本走同一条 append 路径而不是另开一套：一轮里「先思考 → 再出文本」是
// 同一 messageId 的两个 chunk，分成两行会让一轮在时间线上变成两轮。
func (c *importCollector) appendThought(thought, nativeID string) {
	thought = strings.TrimSpace(thought)
	if thought == "" {
		return
	}
	c.append("agent", "", nativeID, thought)
}

// acpThoughtAux 把一段 reasoning 归到 aux 的 Thought 字段。
//
// 必须是 Thought 而不是「kind=think 的工具卡」：实时路径的 thought_chunk 写的就是
// Thought，前端把 `reasoning` 块渲染成灰显可折叠的思考文本，而工具卡是另一种外观
// （带💭 图标的折叠卡）。用工具卡的话，同一轮的 reasoning 在「实时看」与「同步后看」
// 两种外观 —— 这正是本方案要消灭的那类「两套真相」。
func acpThoughtAux(content string, thought string) agenttypes.ImportedExchangeAux {
	return agenttypes.ImportedExchangeAux{
		// Line 是「在正文第几行」，与实时路径的 currentAssistantLine 同一口径。
		Line:    currentACPImportLine(content),
		Thought: thought,
	}
}

func currentACPImportLine(content string) int {
	if strings.TrimSpace(content) == "" {
		return 1
	}
	return strings.Count(content, "\n") + 1
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}

func (c *importCollector) snapshot() []agenttypes.ImportedExchange {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]agenttypes.ImportedExchange, len(c.exchanges))
	copy(out, c.exchanges)
	return out
}

func parseTimeStringPtr(raw *string) time.Time {
	if raw == nil {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(*raw))
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func cloneEnv(env map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for key, value := range env {
		out[key] = value
	}
	return out
}

func normalizeComparablePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	clean := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil && strings.TrimSpace(resolved) != "" {
		clean = resolved
	}
	if abs, err := filepath.Abs(clean); err == nil {
		clean = abs
	}
	return filepath.Clean(clean)
}
