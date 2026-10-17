package acp

import (
	"path/filepath"
	"strings"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	// 别名 sessionpkg：本包有一个同名类型 session（acp session），撞名。
	sessionpkg "mindfs/server/internal/session"
)


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
// 为什么严到只收 UUID：MindFS 递过去的就是自己 mint 的 mindfs_id（方案 D3），
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
//
// 判据本体在 session.IsTurnIDShape（S2e 起 pending 灯的回合身份也是 mindfs_id，
// 形状判据只能有一份）；这里留一层薄转发，让本包的读者继续读本包的名字。
func isUUIDShape(raw string) bool {
	return sessionpkg.IsMindFSIDShape(raw)
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
