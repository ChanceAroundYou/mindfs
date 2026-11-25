package usecase

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"mindfs/server/internal/session"
)

// 回合收尾之后的「续跑」增量落盘。
//
// 背景（2026-11-25 取证，会话 1795570207-89e3aba017b9）：ACP 的 `session/prompt`
// 在**人类发起的那一轮**结束时返回，Hermes 之后的自发续跑（后台任务结果回注）仍会
// 通过同一个 OnUpdate 送进来。旧实现只在 Prompt 返回处 persistTurnBlocks 一次，
// 迟到的工具卡与正文只活在 provisional 流水（step=-1，读取侧丢弃）里 ——
// 用户看到的就是「工具调用堆在正文后面、后半段正文消失」。实测 seq 39 落盘 71 块、
// provisional 里多出 26 个工具卡与两段正文；seq 35/37/41/45 同类。
//
// 修法不是「等更久再落盘」（续跑可能持续几十分钟），而是把落盘改成**可追加**的：
// 首轮落盘之后装一个 writer，后续每次更新把「游标之后」的增量编译成块，位置顺延。

const (
	// turnTailFlushInterval 是增量落盘的节流窗口：文本连续到达时不逐块写盘，
	// 攒够一个窗口或出现结构性事件（工具/待办/计划/压缩）再落。
	turnTailFlushInterval = 1500 * time.Millisecond
	// turnTailWriteTimeout 只覆盖一次本地追加写，与回合 ctx 无关。
	turnTailWriteTimeout = 10 * time.Second
)

// turnStreamState 是「本轮正文 + aux」的共享缓冲。
//
// 落盘之后写入方（OnUpdate 回调线程）仍在更新它，而尾刷定时器与落盘线程要读，
// 所以比原来的裸变量多一把锁。所有方法各自加锁，不跨调用持有。
type turnStreamState struct {
	mu             sync.Mutex
	content        string
	lastUpdateType string
	aux            []session.ExchangeAux
}

func (s *turnStreamState) appendChunk(chunk string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.content = AppendResponseChunk(s.content, s.lastUpdateType, chunk)
	s.lastUpdateType = "message_chunk"
}

func (s *turnStreamState) noteUpdateType(kind string) {
	s.mu.Lock()
	s.lastUpdateType = kind
	s.mu.Unlock()
}

func (s *turnStreamState) assistantLine() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return currentAssistantLine(s.content)
}

func (s *turnStreamState) addAux(aux session.ExchangeAux) {
	s.mu.Lock()
	s.aux = append(s.aux, aux)
	s.mu.Unlock()
}

// snapshot 返回当前正文与 aux 的拷贝。aux 拷贝是浅拷贝：切片追加只发生在
// addAux（持锁），调用方只读，不需要深拷贝。
func (s *turnStreamState) snapshot() (string, []session.ExchangeAux) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.content, append([]session.ExchangeAux(nil), s.aux...)
}

// turnTailWriter 把首轮落盘之后到达的更新追加成同一轮的块。
//
// 位置（pos）只增不减：首轮占 0..len(initial)-1，之后顺延。已经落过盘的工具卡
// 不新开卡 —— 状态从 running 变 completed 时在原 pos 上写 revision+1，这正是
// revision 的语义（读取侧按 (seq,pos) 取最高 revision）。
type turnTailWriter struct {
	manager *session.Manager
	key     string
	seq     int
	turnID  string
	state   *turnStreamState

	mu        sync.Mutex
	contentAt int // 已落盘的正文字节数
	auxAt     int // 已落盘的 aux 条数（对方是 state.aux 的原始前缀）
	nextPos   int
	toolBlock map[string]session.Block // callId → 已落盘的块
	toolRev   map[string]int           // callId → 当前 revision
	todoPos   int                      // 首轮落盘的待办块 pos，-1 表示没有
	lastFlush time.Time
	timer     *time.Timer
}

// newTurnTailWriter 在首轮 persistTurnBlocks 之后调用。
//
// persistedContentLen 是首轮落盘时 responseText 的字节数，persistedAuxLen 是**原始**
// auxBuffer 的条数（不是去重后的），initial 是首轮落下的块。
func newTurnTailWriter(
	manager *session.Manager,
	key string,
	seq int,
	turnID string,
	state *turnStreamState,
	persistedContentLen int,
	persistedAuxLen int,
	initial []session.Block,
) *turnTailWriter {
	w := &turnTailWriter{
		manager:   manager,
		key:       key,
		seq:       seq,
		turnID:    turnID,
		state:     state,
		contentAt: persistedContentLen,
		auxAt:     persistedAuxLen,
		nextPos:   len(initial),
		toolBlock: make(map[string]session.Block),
		toolRev:   make(map[string]int),
		todoPos:   -1,
		lastFlush: time.Now(),
	}
	for _, block := range initial {
		switch block.Type {
		case session.BlockTypeToolCall:
			if block.ToolCall == nil {
				continue
			}
			callID := strings.TrimSpace(block.ToolCall.CallID)
			if callID == "" {
				continue
			}
			w.toolBlock[callID] = block
			w.toolRev[callID] = block.Revision
		case session.BlockTypeTodo:
			w.todoPos = block.Pos
		}
	}
	return w
}

// note 在有更新到达时调用（OnUpdate 回调线程）。
//
// 节奏：距上次落盘已过一个窗口就立刻落；否则挂一个尾刷定时器，静默后把剩下的
// 一次性落掉 —— 没有尾刷，最后一段正文会一直悬在内存里。
func (w *turnTailWriter) note() {
	w.mu.Lock()
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	due := turnTailFlushInterval - time.Since(w.lastFlush)
	if due > 0 {
		w.timer = time.AfterFunc(due, func() {
			w.mu.Lock()
			w.timer = nil
			w.mu.Unlock()
			w.flush(true)
		})
	}
	w.mu.Unlock()
	if due <= 0 {
		w.flush(false)
	}
}

// flush 追加落盘一次。all=false 时只落到最后一个换行（避免把半行正文拆成两个
// 段落块）；有结构性 aux 待落时总是全落，否则正文会跑到卡片后面去。
func (w *turnTailWriter) flush(all bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	content, rawAux := w.state.snapshot()
	if w.contentAt > len(content) || w.auxAt > len(rawAux) {
		// 缓冲只增不减；回退说明 writer 拿到了别的回合的状态，宁可丢掉也不写错轮。
		log.Printf("[session] turn.tail.state.reset session=%s seq=%d at=%d len=%d aux=%d/%d",
			w.key, w.seq, w.contentAt, len(content), w.auxAt, len(rawAux))
		w.contentAt = len(content)
		w.auxAt = len(rawAux)
		return
	}
	delta := content[w.contentAt:]
	pending := rawAux[w.auxAt:]
	cut := len(delta)
	if len(pending) == 0 && !all {
		if idx := strings.LastIndexByte(delta, '\n'); idx >= 0 {
			cut = idx + 1
		} else {
			cut = 0
		}
	}
	if cut == 0 && len(pending) == 0 {
		return
	}
	prefixLine := turnTailPrefixLines(content[:w.contentAt])
	if err := w.appendBlocksLocked(delta[:cut], pending, prefixLine); err != nil {
		// 游标不推进，下一次重试；块流是追加的，重试不会产生重复位置。
		log.Printf("[session] turn.tail.append.error session=%s seq=%d err=%v", w.key, w.seq, err)
		return
	}
	w.contentAt += cut
	w.auxAt += len(pending)
	w.lastFlush = time.Now()
}

func (w *turnTailWriter) appendBlocksLocked(delta string, pending []session.ExchangeAux, prefixLine int) error {
	shifted := make([]session.ExchangeAux, 0, len(pending))
	revisions := make([]session.Block, 0, 2)
	for _, aux := range pending {
		if aux.ToolCall != nil {
			callID := strings.TrimSpace(aux.ToolCall.CallID)
			if callID != "" {
				if block, ok := w.toolBlock[callID]; ok {
					updated := block
					updated.Revision = w.toolRev[callID] + 1
					toolCall := *aux.ToolCall
					updated.ToolCall = &toolCall
					revisions = append(revisions, updated)
					w.toolRev[callID] = updated.Revision
					continue
				}
			}
		}
		if aux.Todo != nil && w.todoPos >= 0 {
			updated := session.Block{
				Seq: w.seq, Pos: w.todoPos, Revision: 1,
				Type: session.BlockTypeTodo, Step: turnBlockStep, MindFSID: w.turnID,
			}
			todo := *aux.Todo
			updated.Todo = &todo
			revisions = append(revisions, updated)
			continue
		}
		aux.Line -= prefixLine
		if aux.Line < 0 {
			aux.Line = 0
		}
		shifted = append(shifted, aux)
	}
	blocks := session.BuildTurnBlocks(w.seq, turnBlockStep, w.turnID, delta, dedupeExchangeAuxBuffer(shifted))
	for i := range blocks {
		blocks[i].Pos = w.nextPos
		w.nextPos++
		switch blocks[i].Type {
		case session.BlockTypeToolCall:
			if blocks[i].ToolCall == nil {
				continue
			}
			if callID := strings.TrimSpace(blocks[i].ToolCall.CallID); callID != "" {
				w.toolBlock[callID] = blocks[i]
				w.toolRev[callID] = blocks[i].Revision
			}
		case session.BlockTypeTodo:
			w.todoPos = blocks[i].Pos
		}
	}
	all := append(revisions, blocks...)
	if len(all) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), turnTailWriteTimeout)
	defer cancel()
	return w.manager.AppendBlocks(ctx, w.key, all)
}

// turnTailPrefixLines 返回「已落盘前缀占满的行数」，用来把 aux 的绝对行号
// （currentAssistantLine 口径，末尾换行被忽略）平移成增量里的相对行号。
//
// 前缀最后一行没有换行结尾时它还没写完 —— 它的剩余部分属于增量，不占行号，
// 否则这条 aux 会顶到上一行后面去。
func turnTailPrefixLines(prefix string) int {
	if prefix == "" {
		return 0
	}
	lines := currentAssistantLine(prefix)
	if !strings.HasSuffix(prefix, "\n") {
		lines--
	}
	if lines < 0 {
		return 0
	}
	return lines
}
