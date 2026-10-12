package session

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
// 身份三级（方案 §3.3）：mindfs_id（MindFS 权威，全 agent 通用）+ step（agent 的
// 消息投递单元）+ pos（步内段序）。agent_id 是桥梁键，可空、可后补、fork 后可变更 ——
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

	// MindFSID 是 MindFS 权威身份；AgentID 是 agent 侧桥梁键（可空）。
	MindFSID string `json:"mindfs_id,omitempty"`
	AgentID  string `json:"agent_id,omitempty"`

	// 子代理归属（方案 D16）。
	ParentToolUseID string `json:"parent_tool_use_id,omitempty"`
	TaskID          string `json:"task_id,omitempty"`
	SubagentType    string `json:"subagent_type,omitempty"`
}

// UnmarshalJSON 兼容块流里的旧键名：turn_id → MindFSID、native_id → AgentID。
//
// 没有这层，重命名之后读存量 39517 个块会全丢身份 —— 块与轮次挂不上，
// 「按轮次折叠 / 重放」之类全失效。写入侧只写新键名，旧键名只读不写。
func (b *Block) UnmarshalJSON(data []byte) error {
	type alias Block
	var raw struct {
		alias
		LegacyMindFSID string `json:"turn_id"`
		LegacyAgentID  string `json:"native_id"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*b = Block(raw.alias)
	if b.MindFSID == "" {
		b.MindFSID = raw.LegacyMindFSID
	}
	if b.AgentID == "" {
		b.AgentID = raw.LegacyAgentID
	}
	return nil
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

// ReadBlocks 全量读块流（按写入序，含全部 revision）。
//
// 用于复制（fork）与体检：那些场景要的是「盘上有什么」，所以不解析 revision。
// 渲染用的窗口读走 GetBlocksWindow。
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
	m.fullBlockReads++
	payload, err := m.root.ReadMetaFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Block{}, nil
		}
		return nil, err
	}
	return parseBlocks(payload), nil
}

// GetBlocksWindow 返回窗口内 seq 的块，已解析 revision 并按 (seq,pos) 升序。
//
// 这是渲染读路径，两条硬约束：
//  1. **O(窗口)**。块流是窗口回包的载荷来源，而窗口读持 m.mu（每 root 一把），
//     全量读会卡住整个 root。实测单会话一轮可达 83 步 / 974 条消息，块流是 MB 级。
//     做法与 readAuxWindowTail 同构：从尾部反向分块读，直到看见一条 seq < minSeq
//     的完整行（或读到文件头）。
//  2. **解析 revision 与哨兵**。同 (seq,pos) 取最高 revision（D8）；step<0 的版本是
//     「步序未定」的中间态，读取侧忽略（§3.3）。反向读天然先遇到最高 revision。
func (m *Manager) GetBlocksWindow(ctx context.Context, key string, seqSet map[int]bool) ([]Block, error) {
	if len(seqSet) == 0 {
		return []Block{}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	minSeq := 0
	for seq := range seqSet {
		if seq <= 0 {
			continue
		}
		if minSeq == 0 || seq < minSeq {
			minSeq = seq
		}
	}
	if minSeq == 0 {
		return []Block{}, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	path, err := m.blocksPath(key)
	if err != nil {
		return nil, err
	}
	raw, ok := m.readBlocksWindowTail(path, minSeq)
	if !ok {
		// 反向读无法可靠完成（不可 seek / 读异常）时回退全量读。宁可慢也不能漏正文。
		all, err := m.readBlocksAll(key)
		if err != nil {
			return nil, err
		}
		raw = all
	}
	return resolveBlocks(raw, seqSet), nil
}

// readBlocksWindowTail 读回 seq >= minSeq 的原始块行。ok=false 表示无法可靠完成，
// 由调用方回退全量读。
//
// 两阶段，刻意不把「反向分块读」的结果直接当内容：
//
//	阶段一（定位）：从尾部反向分块，只解析每块**内部**的完整行（跳过跨块的首尾残片），
//	  找到第一个出现 seq < minSeq 的块起点。
//	阶段二（取内容）：把该起点**对齐到行边界**，然后一次正向读到文件尾。
//
// 为什么不直接收集反向读到的行：反向分块读的经典坑是「跨块的长行被静默丢掉」。
// 每块的首行是半行（前半段在更低偏移处），丢掉它需要「下一块会把完整行读全」这个前提，
// 而单行大于 chunk 时该前提不成立 —— 实测主日志单行最大 308KB，块流里超长 reasoning
// 常见（os 实测 119714 / 103467 字符）。先对齐再正向读就不存在残片拼接问题：
// 从行边界读到文件尾，每一行都是完整的。
//
// 代价是同一段字节被读两遍（定位一遍、取内容一遍），换来的是「不可能丢正文」。
// 定位粒度是 chunk，所以最小读量是 512KB —— 与 readAuxWindowTail 同量级。
func (m *Manager) readBlocksWindowTail(path string, minSeq int) ([]Block, bool) {
	if minSeq <= 0 {
		return nil, false
	}
	file, err := m.root.OpenMetaFile(path)
	if err != nil {
		// 文件缺失等价于「没有块」，不是失败 —— 落地前的历史会话都是这样。
		if errors.Is(err, os.ErrNotExist) {
			return []Block{}, true
		}
		return nil, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, false
	}
	size := info.Size()
	if size == 0 {
		return []Block{}, true
	}

	const chunk = int64(512 << 10)
	stop := int64(-1)
	end := size
	for end > 0 {
		start := end - chunk
		if start < 0 {
			start = 0
		}
		buf := make([]byte, int(end-start))
		if _, err := file.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return nil, false
		}
		if chunkHasSeqBelow(buf, start > 0, end < size, minSeq) {
			stop = start
			break
		}
		if start == 0 {
			stop = 0
			break
		}
		end = start
	}
	if stop < 0 {
		stop = 0
	}

	alignedStart, ok := alignLineStart(file, stop, size)
	if !ok {
		return nil, false
	}
	payload := make([]byte, int(size-alignedStart))
	if _, err := file.ReadAt(payload, alignedStart); err != nil && !errors.Is(err, io.EOF) {
		return nil, false
	}
	return parseBlocks(payload), true
}

// chunkHasSeqBelow 只看 buf 里**内部**的完整行（skipFirst/skipLast 表示首尾是跨块残片），
// 判断是否存在 seq < minSeq 的行。坏行不算「低于」：它可能本来属于窗口内的 seq，
// 过早停止会让窗口内的正文少一块。
func chunkHasSeqBelow(buf []byte, skipFirst, skipLast bool, minSeq int) bool {
	parts := bytes.Split(buf, []byte{'\n'})
	if skipFirst && len(parts) > 0 {
		parts = parts[1:]
	}
	if skipLast && len(parts) > 0 {
		parts = parts[:len(parts)-1]
	}
	for _, rawLine := range parts {
		rawLine = bytes.TrimSpace(rawLine)
		if len(rawLine) == 0 {
			continue
		}
		var probe struct {
			Seq int `json:"seq"`
		}
		if json.Unmarshal(rawLine, &probe) == nil && probe.Seq > 0 && probe.Seq < minSeq {
			return true
		}
	}
	return false
}

// alignLineStart 返回 offset 处或其之前最近的行边界（即「某个 '\n' 之后」的偏移）。
// offset <= 0 时返回 0。找不到 '\n'（整个前缀是一行）时返回 0 —— 那也是行首。
// 用几何步长向前回溯，避免为超长行退化成逐字节扫描。
func alignLineStart(file io.ReaderAt, offset, size int64) (int64, bool) {
	if offset <= 0 {
		return 0, true
	}
	if offset > size {
		offset = size
	}
	const step = int64(64 << 10)
	pos := offset
	for pos > 0 {
		start := pos - step
		if start < 0 {
			start = 0
		}
		buf := make([]byte, int(pos-start))
		if _, err := file.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return 0, false
		}
		if idx := bytes.LastIndexByte(buf, '\n'); idx >= 0 {
			return start + int64(idx) + 1, true
		}
		pos = start
	}
	return 0, true
}

// resolveBlocks 按 (seq,pos) 取最高 revision、丢掉未收口的版本，并按 (seq,pos) 升序返回。
//
// seqSet 为 nil 表示不过滤 seq。revision 相同时取**后写入**的那条（文件序靠后者）：
// 块流是 append-only，后写的才是更新的。
func resolveBlocks(blocks []Block, seqSet map[int]bool) []Block {
	if len(blocks) == 0 {
		return []Block{}
	}
	type key struct{ seq, pos int }
	best := make(map[key]Block, len(blocks))
	for _, block := range blocks {
		if block.Seq <= 0 {
			continue
		}
		if seqSet != nil && !seqSet[block.Seq] {
			continue
		}
		// step<0 = 步序未定，读取侧忽略（§3.3）。
		if block.Step < 0 {
			continue
		}
		k := key{block.Seq, block.Pos}
		if prev, exists := best[k]; exists && prev.Revision > block.Revision {
			continue
		}
		best[k] = block
	}
	out := make([]Block, 0, len(best))
	for _, block := range best {
		out = append(out, block)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Seq != out[j].Seq {
			return out[i].Seq < out[j].Seq
		}
		return out[i].Pos < out[j].Pos
	})
	return out
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
	return Block{Seq: seq, Pos: pos, Type: blockType, Text: text, Step: step, MindFSID: turnID}
}

// auxBlock 把一条 aux 编译成块。判据顺序与渲染器一致：thought → plan → todo → compact → toolcall。
func auxBlock(seq, step int, turnID string, aux ExchangeAux) (Block, bool) {
	block := Block{Seq: seq, Step: step, MindFSID: turnID}
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
// 回查错行。所以在 repoint 换 id 的同时必须把 agent id 一并清掉。
// 这是本方案**直接引入**的新步骤：不持久化 agent id 时它不存在。
//
// 只清 agent id：mindfs_id（MindFS 权威身份）与正文都不动 —— mindfs_id 落盘即定、永不改。
//
// 零副作用约定：**文件里本来没有 agent id 时一个字节都不写**。无谓重写会改 mtime、
// 白白让内存缓存失效；而落地前的全部历史数据都没有 agent id，这条是常态而非边角。
func (m *Manager) ClearNativeIdentity(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	// 老文件写的是 native_turn/native_prompt，新文件写 agent_id —— 三套键名都要清。
	if err := m.clearNativeFieldsUnsafe(key, exchangePathForKey, "agent_id", "native_turn", "native_prompt"); err != nil {
		return err
	}
	return m.clearNativeFieldsUnsafe(key, blocksPathForKey, "agent_id", "native_id")
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

// CompactBlockLight 是窗口专用的块压缩（复用 G-AT 的 CompactExchangeAuxLight 策略）。
//
// 只压 tool_call 的详情：edit/read/execute 的 content 是窗口载荷里最大的一块
// （实测一个会话 65 个 edit 的 content 占 101 KB），而卡片展开时本就走
// GET /api/sessions/{key}/toolcalls/{callID} 懒加载，窗口只需能渲染折叠卡片的最小字段。
//
// **正文（text / reasoning）一律不动**。块流是正文的**唯一**载体（D1），窗口又是渲染读路径；
// 在这里截断等于正文丢失 —— 而那正是本方案要修的 bug。超大块（os 实测 119714 字符）
// 由前端默认折叠 + 标注处理，不靠服务端丢数据。
//
// 返回 ok=false 表示这块不值得下发（未知类型）。读侧跳过未知类型，保证 agent 加新类型时
// 不炸旧服务。
func CompactBlockLight(block Block) (Block, bool) {
	if !block.Type.Valid() {
		return Block{}, false
	}
	if block.ToolCall != nil {
		toolCall := CompactToolCall(*block.ToolCall)
		lightCompactToolCall(&toolCall)
		block.ToolCall = &toolCall
	}
	// 正文与身份字段原样保留。
	return block, true
}

// CompactBlocksLight 对一组块做轻压缩，跳过未知类型。
func CompactBlocksLight(blocks []Block) []Block {
	out := make([]Block, 0, len(blocks))
	for _, block := range blocks {
		compacted, ok := CompactBlockLight(block)
		if !ok {
			continue
		}
		out = append(out, compacted)
	}
	return out
}

// GroupBlocksBySeq 把块按 seq 分组，seq 内保持 (pos) 升序。
//
// 回包形状与 exchange_aux 一致（`{"<seq>": [...]}`），前端查表即可，
// 也让「块与 aux 同 seq 对齐」这件事在结构上显而易见。
func GroupBlocksBySeq(blocks []Block) map[int][]Block {
	out := make(map[int][]Block, len(blocks))
	for _, block := range blocks {
		if block.Seq <= 0 {
			continue
		}
		out[block.Seq] = append(out[block.Seq], block)
	}
	for seq, items := range out {
		sort.SliceStable(items, func(i, j int) bool { return items[i].Pos < items[j].Pos })
		out[seq] = items
	}
	return out
}
