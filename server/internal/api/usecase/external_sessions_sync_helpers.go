package usecase

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
)

func externalSessionDeltaAfterCtxSeq(exchanges []agenttypes.ImportedExchange, agentCtxSeq int) []agenttypes.ImportedExchange {
	if agentCtxSeq <= 0 {
		return exchanges
	}
	if agentCtxSeq >= len(exchanges) {
		return nil
	}
	return exchanges[agentCtxSeq:]
}

func appendImportedExchange(
	ctx context.Context,
	manager *session.Manager,
	target *session.Session,
	agentName string,
	exchange agenttypes.ImportedExchange,
) (bool, error) {
	role := strings.TrimSpace(exchange.Role)
	if role != "user" && role != "agent" {
		return false, nil
	}
	if role == "user" && strings.TrimSpace(exchange.Content) == "" {
		return false, nil
	}
	// 幂等护栏：Full 同步按 ctx_seq 切片，而 ctx_seq 在「live 写入 + ImportedCount=0
	// 的增量（ts 相同被 after 过滤）」期间不会推进，点「同步」时会把库内已有的
	// 历史尾段整块重放（2026-09-12 WSL 实测：count=13 重放、old 时间戳、字段缺
	// model_display_name/effort）。
	// 判据与发送侧共用（exchangeAlreadyRecorded）：同一轮的用户条目本来就由这个导入器
	// 和回合结束的 SendMessage 两个人写，护栏必须对称，否则后写的那个照样落重复。
	//
	// 判据顺序（S2c）：**先身份、后内容**。身份是铁证 —— 导入器会把相邻同角色条目
	// 合并，合并后的 Content 与实时路径写的行逐字不同，内容比对必然漏判（实测
	// 2026-10-07 某会话 9 对 live/import 重复）。身份对不上才退回内容 + ±5s，
	// 并且打一条日志：那是「还在猜」的可见信号。
	if exchangeAlreadyRecorded(ctx, manager, target, role, exchange.NativeID, exchange.Content, exchange.Timestamp) {
		return false, nil
	}
	// 把 agent 侧身份写进这一行（方案 §3.1）。没有它，下一轮同步就没有可对齐的键，
	// 只能继续靠内容猜 —— 而内容恰恰是导入器会改写的东西。
	//
	// **这一步同时补上导入路径欠了 S2a 至今的 MindFS 铸造**：此前导入行只写 agent 侧 id、
	// 从不铸 MindFSID。UUIDv5 让 MindFSID 由
	// AgentID 派生 —— 同一行重导得到同一个 id，判重不再需要内容兜底。
	agentID := strings.TrimSpace(exchange.NativeID)
	fallbackSeed := fmt.Sprintf("%s|%s|%d", target.Key, role, session.MaxExchangeSeq(target.Exchanges)+1)
	identity := session.ExchangeIdentity{
		MindFSID: session.NewMindFSID(agentID, fallbackSeed),
		TurnSeq:  session.MaxExchangeSeq(target.Exchanges) + 1,
		AgentID:  agentID,
	}
	if err := manager.AddExchangeForAgentAt(
		session.WithExchangeIdentity(session.WithExchangeSource(ctx, session.ExchangeSourceImport), identity),
		target,
		role,
		// S2d-2：主日志不再存正文。**不能传 `exchange.Content`** —— 传了就是把正文
		// 写回两份真相。块流在下面按 aux 一并编译。
		"",
		agentName,
		"",
		"",
		"",
		exchange.Timestamp,
	); err != nil {
		return false, err
	}
	// aux 以 seq 为键：必须取刚落下那行的真实 seq（max+1 分配下 len ≠ seq）
	seq := session.MaxExchangeSeq(target.Exchanges)
	// S2d-2：正文走块流。aux 先攒起来一次性编译 —— persistTurnBlocks 要按 (line, 到达序)
	// 把 aux 插进正文段之间，逐条追加做不到（那会是每条 aux 一块独立的块，顺序全丢）。
	collected := make([]session.ExchangeAux, 0, len(exchange.Aux))
	for _, importedAux := range exchange.Aux {
		// reasoning 走 Thought 字段，与实时路径的 thought_chunk 同一形状 ——
		// 前端把 `reasoning` 块渲染成灰显的思考文本，而 think 工具卡是另一种外观。
		if strings.TrimSpace(importedAux.Thought) != "" {
			collected = append(collected, session.ExchangeAux{
				Seq:     seq,
				Line:    importedAux.Line,
				Thought: importedAux.Thought,
			})
			continue
		}
		if importedAux.Plan != nil {
			plan := *importedAux.Plan
			collected = append(collected, session.ExchangeAux{
				Seq:  seq,
				Line: importedAux.Line,
				Plan: &plan,
			})
			continue
		}
		if importedAux.ToolCall == nil {
			continue
		}
		toolCall := *importedAux.ToolCall
		if toolCall.Kind != agenttypes.ToolKindExecute &&
			toolCall.Kind != agenttypes.ToolKindEdit &&
			toolCall.Kind != agenttypes.ToolKindThink &&
			toolCall.Kind != agenttypes.ToolKindAskUser &&
			toolCall.Kind != agenttypes.ToolKindWebSearch &&
			toolCall.Kind != agenttypes.ToolKindOther {
			continue
		}
		collected = append(collected, session.ExchangeAux{
			Seq:      seq,
			Line:     importedAux.Line,
			ToolCall: &toolCall,
		})
	}
	// 块在行之后写：块要带 mindfs_id，而 mindfs_id 在 AddExchangeForAgentAt 里才定下来。
	// 主日志已经落了盘，块流失败不该让这一行消失（否则用户少看到一整轮）。
	if err := persistTurnBlocks(ctx, manager, target.Key, seq,
		session.TurnIDForSeq(target.Exchanges, seq), exchange.Content, collected); err != nil {
		log.Printf("[session] persist.import.blocks.error session=%s seq=%d err=%v", target.Key, seq, err)
	}
	// aux 仍要逐条落盘：它还是 `GET /api/sessions/{key}` 回包里 `exchange_aux` 的来源
	// （前端按 seq 渲染工具卡 / reasoning，正文块则单独放在 blocks 字段）。
	// 删掉它会让所有存量读路径立刻看不到工具卡 —— 那不是 S2d-2 的范围。
	for _, aux := range collected {
		if err := manager.AddExchangeAux(ctx, target.Key, aux); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (s *Service) resolveExternalSessionImporter(agentName string) (agenttypes.ExternalSessionImporter, error) {
	importer, err := s.Registry.GetExternalSessionImporter(strings.TrimSpace(agentName))
	if err != nil {
		return nil, err
	}
	return importer, nil
}

func externalSyncLockKey(rootID, key string) string {
	return strings.TrimSpace(rootID) + ":" + strings.TrimSpace(key)
}

func externalSessionSyncLock(rootID, key string) *sync.Mutex {
	lock, _ := externalSessionSyncLocks.LoadOrStore(externalSyncLockKey(rootID, key), &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func lastExternalSyncTimestamp(exchanges []session.Exchange) time.Time {
	for i := len(exchanges) - 1; i >= 0; i-- {
		if !exchanges[i].Timestamp.IsZero() {
			return exchanges[i].Timestamp.UTC()
		}
	}
	return time.Time{}
}
