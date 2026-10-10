package session

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	agenttypes "mindfs/server/internal/agent/types"
)

// CUSTOM(G-BH): 块流是会话正文的**唯一**载体（方案 §3.1/§3.2，决策 D1）。
//
// 为什么必须保留：上游的 Exchange.Content 是「一轮正文 = 一段字符串」，于是
// 「只有 reasoning、没有 text 块」的轮次落库成空串 —— 流式时 thought_chunk 实时推看得见，
// 收尾落库后正文消失（实测 os 模型 ~15% 轮次）。块流把 reasoning / text / tool_call /
// todo / plan / compact 都当成一等公民，正文不再依赖「必须有 text 块」。
//
// 双写期（S2a）：块流与旧 Content/aux **并存**，读路径仍走旧格式，可随时整体回滚。
// S2b 起读路径改走块流，S2d 删除 Content。

// BlockType 是块的类型。序列化成小写蛇形，与 aux 的既有命名习惯一致。
type BlockType string

const (
	BlockTypeReasoning BlockType = "reasoning"
	BlockTypeText      BlockType = "text"
	BlockTypeToolCall  BlockType = "tool_call"
	BlockTypeTodo      BlockType = "todo"
	BlockTypePlan      BlockType = "plan"
	BlockTypeCompact   BlockType = "compact"
)

// Valid 报告是否是已知的块类型。未知类型不报错（读侧跳过），保证 agent 加新类型时不炸旧服务。
func (t BlockType) Valid() bool {
	switch t {
	case BlockTypeReasoning, BlockTypeText, BlockTypeToolCall, BlockTypeTodo, BlockTypePlan, BlockTypeCompact:
		return true
	default:
		return false
	}
}

// Block 是块流 `<key>.blocks.jsonl` 里的一行。
//
// 身份三级（方案 §3.3）：turn_id（MindFS 权威，全 agent 通用）+ step（agent 的
// 消息投递单元）+ pos（步内段序）。native_id 是回查键，可空、可后补、fork 后可变更 ——
// 它**不是**权威身份（repoint.go:35 明载 fork 后 claude 产出全新 uuid）。
type Block struct {
	Seq      int       `json:"seq"`
	Pos      int       `json:"pos"`
	Revision int       `json:"revision"`
	Type     BlockType `json:"type"`

	// 正文载荷，按 Type 只用其中一个。
	Text     string                    `json:"text,omitempty"`
	ToolCall *agenttypes.ToolCall      `json:"toolcall,omitempty"`
	Todo     *agenttypes.TodoUpdate    `json:"todo,omitempty"`
	Plan     *agenttypes.PlanUpdate    `json:"plan,omitempty"`
	Compact  *agenttypes.CompactNotice `json:"compact,omitempty"`

	// Step 是步序。块在步收口前落盘时写哨兵 -1，收口时用 (seq,pos)+revision+1 补正。
	Step int `json:"step"`

	// TurnID 是 MindFS 权威身份；NativeID 是 agent 侧回查键（可空）。
	TurnID   string `json:"turn_id,omitempty"`
	NativeID string `json:"native_id,omitempty"`

	// 子代理归属（方案 D16）。
	ParentToolUseID string `json:"parent_tool_use_id,omitempty"`
	TaskID          string `json:"task_id,omitempty"`
	SubagentType    string `json:"subagent_type,omitempty"`
}

// blocksFileTpl 与主日志 / aux 同目录，便于一起备份与一起迁移。
const blocksFileTpl = "sessions/%s.blocks.jsonl"

// blockStepSentinel 表示「本轮步序未定」——块先落盘、步后收口时用（方案 §3.3）。
const blockStepSentinel = -1

func (m *Manager) blocksPath(key string) (string, error) {
	if strings.TrimSpace(m.root.MetaDir()) == "" {
		return "", errors.New("managed dir required")
	}
	if key == "" {
		return "", errors.New("session key required")
	}
	if strings.Contains(key, "..") || strings.ContainsRune(key, filepath.Separator) || strings.Contains(key, "/") {
		return "", fmt.Errorf("invalid session key: %s", key)
	}
	return filepath.ToSlash(fmt.Sprintf(blocksFileTpl, key)), nil
}

// AppendBlocks 追加块到块流。**调用方负责 blocks 非空** —— 空切片是 no-op。
//
// 不持 m.mu 由调用方保证吗？不：这里加锁，与 appendExchange / appendExchangeAux 一致。
// 块流没有内存缓存（不像 exchanges），所以读侧不需要游标推进。
func (m *Manager) AppendBlocks(ctx context.Context, key string, blocks []Block) error {
	if len(blocks) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	path, err := m.blocksPath(key)
	if err != nil {
		return err
	}
	file, err := m.root.OpenMetaFileAppend(path)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	for _, block := range blocks {
		payload, err := json.Marshal(block)
		if err != nil {
			return err
		}
		if _, err := writer.Write(append(payload, '\n')); err != nil {
			return err
		}
	}
	return writer.Flush()
}

// ReadBlocks 全量读块流（按写入序）。块流只在窗口读时用，S2b 会加按需读。
func (m *Manager) ReadBlocks(ctx context.Context, key string) ([]Block, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.readBlocksAll(key)
}

func (m *Manager) readBlocksAll(key string) ([]Block, error) {
	path, err := m.blocksPath(key)
	if err != nil {
		return nil, err
	}
	payload, err := m.root.ReadMetaFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Block{}, nil
		}
		return nil, err
	}
	return parseBlocks(payload), nil
}

// GetBlocksWindow 按 seq 集合过滤块流（窗口读用）。
func (m *Manager) GetBlocksWindow(ctx context.Context, key string, seqSet map[int]bool) ([]Block, error) {
	if len(seqSet) == 0 {
		return []Block{}, nil
	}
	all, err := m.ReadBlocks(ctx, key)
	if err != nil {
		return nil, err
	}
	out := make([]Block, 0, len(all))
	for _, block := range all {
		if seqSet[block.Seq] {
			out = append(out, block)
		}
	}
	return out, nil
}

// parseBlocks 逐行解析。**坏行跳过不报错** —— 块流是 append-only 的，一行写坏了
// 不能让整个会话的正文读不出来（与主日志 parseExchangeLine 的既有取舍一致）。
func parseBlocks(payload []byte) []Block {
	blocks := make([]Block, 0, 16)
	if len(payload) == 0 {
		return blocks
	}
	for len(payload) > 0 {
		idx := bytes.IndexByte(payload, '\n')
		var line []byte
		if idx < 0 {
			line = payload
			payload = nil
		} else {
			line = payload[:idx]
			payload = payload[idx+1:]
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var block Block
		if err := json.Unmarshal(line, &block); err != nil {
			continue
		}
		if block.Seq <= 0 {
			continue
		}
		blocks = append(blocks, block)
	}
	return blocks
}

// BuildTurnBlocks 把「一轮的正文 + aux」编译成块序列。
//
// 顺序必须与前端 buildAssistantTimeline 的既有渲染顺序逐条一致：aux 按
// (line, 到达序) 稳定排序后插进正文段之间，空正文段跳过。S2b 起块序就是渲染序，
// 若这里与渲染器不一致，切换读路径的瞬间正文顺序会变（用户可见的回归）。
//
// step 是步序。S2a 双写期块在轮末一次落完，步已收口，直接写真实步序；
// 流式期（块先落盘、步后收口）写 blockStepSentinel，收口时用 (seq,pos)+revision+1 补正。
func BuildTurnBlocks(seq, step int, turnID, content string, aux []ExchangeAux) []Block {
	blocks := make([]Block, 0, len(aux)+1)
	if len(aux) == 0 {
		if strings.TrimSpace(content) == "" {
			return blocks
		}
		blocks = append(blocks, newBlock(seq, 0, BlockTypeText, step, turnID, content))
		return blocks
	}

	var lines []string
	totalLines := 0
	if content != "" {
		lines = strings.Split(content, "\n")
		totalLines = len(lines)
	}

	type indexed struct {
		aux  ExchangeAux
		line int // 钳到 [0, totalLines]，与渲染器一致
		idx  int
	}
	normalized := make([]indexed, 0, len(aux))
	for i, item := range aux {
		line := item.Line
		if line < 0 {
			line = 0
		}
		if line > totalLines {
			line = totalLines
		}
		normalized = append(normalized, indexed{aux: item, line: line, idx: i})
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		if normalized[i].line != normalized[j].line {
			return normalized[i].line < normalized[j].line
		}
		return normalized[i].idx < normalized[j].idx
	})

	pos := 0
	emitted := 0
	emitText := func(text string) {
		if strings.TrimSpace(text) == "" {
			return
		}
		blocks = append(blocks, newBlock(seq, pos, BlockTypeText, step, turnID, text))
		pos++
	}
	for _, item := range normalized {
		if item.line > emitted {
			emitText(strings.Join(lines[emitted:item.line], "\n"))
			emitted = item.line
		}
		if block, ok := auxBlock(seq, step, turnID, item.aux); ok {
			block.Pos = pos
			blocks = append(blocks, block)
			pos++
		}
	}
	if emitted < totalLines {
		emitText(strings.Join(lines[emitted:], "\n"))
	}
	return blocks
}

func newBlock(seq, pos int, blockType BlockType, step int, turnID, text string) Block {
	return Block{Seq: seq, Pos: pos, Type: blockType, Text: text, Step: step, TurnID: turnID}
}

// auxBlock 把一条 aux 编译成块。判据顺序与渲染器一致：thought → plan → todo → compact → toolcall。
func auxBlock(seq, step int, turnID string, aux ExchangeAux) (Block, bool) {
	block := Block{Seq: seq, Step: step, TurnID: turnID}
	switch {
	case aux.Thought != "":
		block.Type = BlockTypeReasoning
		block.Text = aux.Thought
	case aux.Plan != nil:
		block.Type = BlockTypePlan
		block.Plan = aux.Plan
	case aux.Todo != nil:
		block.Type = BlockTypeTodo
		block.Todo = aux.Todo
	case aux.Compact != nil:
		block.Type = BlockTypeCompact
		block.Compact = aux.Compact
	case aux.ToolCall != nil:
		block.Type = BlockTypeToolCall
		block.ToolCall = aux.ToolCall
	default:
		return Block{}, false
	}
	return block, true
}

// ClearNativeIdentity 清空该会话所有 exchange 行与块上的 native id（方案 §14 C1）。
//
// **为什么必须有**：`repoint.go:35` 明载「fork 之后 claude 产出全新 uuid」。本方案
// 开始持久化 native id 之后，旧 native id 会与 fork 后的新转录对不上 —— 判重失效、
// 回查错行。所以在 repoint 换 id 的同时必须把 native id 一并清掉。
// 这是本方案**直接引入**的新步骤：不持久化 native id 时它不存在。
//
// 只清 native id：turn_id（MindFS 权威身份）与正文都不动 —— turn_id 落盘即定、永不改。
//
// 零副作用约定：**文件里本来没有 native id 时一个字节都不写**。无谓重写会改 mtime、
// 白白让内存缓存失效；而落地前的全部历史数据都没有 native id，这条是常态而非边角。
func (m *Manager) ClearNativeIdentity(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.clearNativeFieldsUnsafe(key, exchangePathForKey, "native_turn", "native_prompt"); err != nil {
		return err
	}
	return m.clearNativeFieldsUnsafe(key, blocksPathForKey, "native_id")
}

// clearNativeFieldsUnsafe 按路径模板清掉若干 JSON 字段。changed 为空表示没动过文件。
func (m *Manager) clearNativeFieldsUnsafe(key string, pathFor func(string) (string, error), fields ...string) error {
	path, err := pathFor(key)
	if err != nil {
		return err
	}
	payload, err := m.root.ReadMetaFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	rewritten, changed := stripJSONFields(payload, fields...)
	if !changed {
		return nil
	}
	if err := m.root.WriteMetaFile(path, rewritten); err != nil {
		return err
	}
	// 主日志被重写后，内存缓存与读盘游标都不再可信。游标必须删掉而不是留着：
	// 新文件更短时旧 offset 会越过 EOF，增量读拿不到任何东西，缓存里那份带旧
	// native id 的行就会一直留着 —— 表现为「清了但重启前一直还看得到」。
	delete(m.sessions, key)
	delete(m.exchangeCursors, path)
	return nil
}

func exchangePathForKey(key string) (string, error) {
	return filepath.ToSlash(fmt.Sprintf(exchangeFileTpl, key)), nil
}

func blocksPathForKey(key string) (string, error) {
	return filepath.ToSlash(fmt.Sprintf(blocksFileTpl, key)), nil
}

// stripJSONFields 逐行删掉给定字段。**行级手术而不是结构体往返**：主日志里可能有
// 别的写入者 / 未来版本加的字段，反序列化成 Exchange 再序列化会把它们丢掉。
// 只有真的删掉了东西才返回 changed=true。
func stripJSONFields(payload []byte, fields ...string) ([]byte, bool) {
	if len(payload) == 0 {
		return payload, false
	}
	out := make([]byte, 0, len(payload))
	changed := false
	rest := payload
	for len(rest) > 0 {
		idx := bytes.IndexByte(rest, '\n')
		var line []byte
		if idx < 0 {
			line = rest
			rest = nil
		} else {
			line = rest[:idx+1]
			rest = rest[idx+1:]
		}
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			out = append(out, line...)
			continue
		}
		var decoded map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &decoded); err != nil {
			out = append(out, line...)
			continue
		}
		hit := false
		for _, field := range fields {
			raw, exists := decoded[field]
			if !exists {
				continue
			}
			var value string
			// 空值不算「有 native id」：落地前的历史行本来就没有这个字段，
			// 而某些写入者可能写成空串 —— 两者都不需要重写文件。
			if json.Unmarshal(raw, &value) == nil && value == "" {
				continue
			}
			delete(decoded, field)
			hit = true
		}
		if !hit {
			out = append(out, line...)
			continue
		}
		encoded, err := json.Marshal(decoded)
		if err != nil {
			out = append(out, line...)
			continue
		}
		out = append(out, encoded...)
		out = append(out, '\n')
		changed = true
	}
	if !changed {
		return payload, false
	}
	return out, true
}
