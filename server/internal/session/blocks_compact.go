package session

import (
	"context"
	"errors"
	"os"
)

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
