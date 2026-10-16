package usecase

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
)

var externalSessionSyncLocks sync.Map

// externalSessionSyncTimes 记录每个 (rootID,key) 最近一次 best-effort 同步时间，用于节流。
// externalSyncThrottle 内重复的非 Full 同步直接跳过，避免 handleSessionGet 高频轮询重复磁盘扫描。
// ponytail: 无清理（外部会话绑定数量有限），若会话海量需换成带 TTL 的 map。
var externalSessionSyncTimes sync.Map

const externalSyncThrottle = 2 * time.Second

func (s *Service) SyncExternalSessionDelta(ctx context.Context, in SyncExternalSessionDeltaInput) (SyncExternalSessionDeltaOutput, error) {
	var out SyncExternalSessionDeltaOutput
	if err := s.ensureRegistry(); err != nil {
		return out, err
	}
	lock := externalSessionSyncLock(in.RootID, in.Key)
	lock.Lock()
	defer lock.Unlock()

	// 节流：非 Full 的 best-effort 同步（每次 handleSessionGet 都会触发）在时间窗内直接跳过，
	// 避免高频轮询重复跑磁盘扫描 + SQLite 查询。Full 同步（用户主动 sync）不受限。
	if !in.Full {
		lockKey := externalSyncLockKey(in.RootID, in.Key)
		if last, ok := externalSessionSyncTimes.Load(lockKey); ok {
			if elapsed := time.Since(last.(time.Time)); elapsed < externalSyncThrottle {
				return out, nil
			}
		}
		externalSessionSyncTimes.Store(lockKey, time.Now().UTC())
	}

	root, err := s.Registry.GetRoot(in.RootID)
	if err != nil {
		return out, err
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return out, err
	}
	current, err := manager.Get(ctx, in.Key, 0)
	if err != nil {
		return out, err
	}
	agentName := session.InferAgentFromSession(current)
	if agentName == "" {
		return out, nil
	}
	binding, err := manager.FindAgentBinding(ctx, current.Key, agentName)
	if err != nil {
		return out, err
	}
	if binding == nil || strings.TrimSpace(binding.AgentSessionID) == "" {
		return out, nil
	}
	lastTimestamp := lastExternalSyncTimestamp(current.Exchanges)
	if lastTimestamp.IsZero() {
		return out, nil
	}
	out.LastTimestamp = lastTimestamp

	// 所有权守卫：MindFS 自己在驱动的会话由实时路径独占持久化，导入器**一律不做自动增量
	// 同步**。双写（同一轮两个写入者各落一行）是 2026-04-29 自动同步上线时的副作用，判重
	// 只能靠「内容相似 + ±5 秒」猜，实测 6.4s / 15.6s / 36.7s 的差值全会漏。
	//
	// 这里曾经留过「进程启动后每会话一次兜底补齐」的例外，2026-09-16 被真实事故证伪并撤掉：
	// live-owned 会话的字节游标是**冻结**的（导入器平时不跑），拿它当起点会把早已落库的旧
	// 回合整段重导；而导入器会把相邻同角色条目**合并**，重导出来的行与实时路径写的行并不
	// 逐字相同 —— 写入侧判重（±5s）也折叠不掉，于是
	// 用户看到「ask 下面又渲染了一轮出现过的文字」（AIS/docs 的 BP 会话，重导了 09-14 的内容）。
	//
	// 需要补历史时走用户手点的「同步」（Full）：那是显式意图，且带时间地板（见下），
	// 只会补「比库里最新一条更新」的部分。
	if !in.Full && session.SessionIsLiveOwned(current.Exchanges) {
		return out, nil
	}

	importer, err := s.resolveExternalSessionImporter(agentName)
	if err != nil {
		return out, err
	}
	importInput := agenttypes.ImportExternalSessionInput{
		RootPath:       root.RootPath,
		Agent:          agentName,
		AgentSessionID: binding.AgentSessionID,
	}
	// live-owned 会话的游标是**冻结**的（导入器平时不跑），只按偏移读会把早已落库的旧回合
	// 整段重导一遍——实测 2026-09-16：BP 会话被重导了 09-14 的内容，且因为导入器会把相邻
	// 同角色条目合并，那些重导行的内容与实时路径写的行并不逐字相同，写入侧判重
	// 也折叠不掉，用户看到「ask 下面又渲染了一轮出现过的文字」。所以给这种会话一道
	// 时间地板：只接受比库里最新一条更新的条目，兜底补齐真正要的只是「进程重启时被中断的
	// 那一轮」。
	//
	// **只给有真时间戳的导入器设地板**（HasTranscriptTimestamps）。ACP 的 `SessionUpdate`
	// 不带时间戳（SDK 里 SessionNotification 只有 sessionId + update），它给每行盖的
	// 时间是**读到它的那一刻**，恒晚于地板 —— 地板形同虚设，而代码看着像「有地板就安全」。
	// 实测 2026-10-07：live-owned 的 dsh 会话 Full 同步多出 9 对重复行，全部带伪造时间戳。
	// 现在这一层的职责已经交给 native id 身份判重（appendImportedExchange →
	// exchangeAlreadyRecorded），身份对不上的行一律落库 —— 宁可多留一条，也不丢内容。
	if session.SessionIsLiveOwned(current.Exchanges) && importer.HasTranscriptTimestamps() {
		importInput.TimestampFloor = lastTimestamp
	}
	// Child-agent JSONL files may keep growing without changing the root file.
	// Keep the root cursor fast path only when there are no imported children.
	children, err := manager.List(ctx, session.ListOptions{ParentSessionKey: current.Key, Limit: 1})
	if err != nil {
		return out, err
	}
	if !in.Full {
		importInput.AfterTimestamp = lastTimestamp
		// 始终传字节游标：root 转录据此从上次结束位置增量读，而非每次全量解析
		// （实测某会话转录 72MB，全量解析 3.6s，而实际只落后 76KB）。
		importInput.Cursor = binding.ExternalCursor()
		// 有子会话时额外强制重读：子代理转录可能在 root 文件不变的情况下增长，
		// 「未变即跳过」会漏掉它们。这与游标是两件事——原先二者被同一个
		// `len(children)==0` 条件捆在一起，导致有子会话的会话连增量读也一并失效。
		importInput.ForceRead = len(children) > 0
	}
	imported, err := importer.ImportExternalSession(ctx, importInput)
	if err != nil {
		return out, err
	}

	delta := imported.Exchanges
	// import-owned 会话的库与转录按序号对齐，ctx_seq 切片是正确的「已导入」边界。
	// live-owned 会话则不行：实时路径会写导入器不产出的行（tool-only 空占位等），库长度
	// 系统性大于导入器条数（实测 119 vs 83），ctx_seq 被刷成库长度后切片 `imported[ctx_seq:]`
	// 恒为空 —— Full 同步于是永远导不到「比库里最新一条更新」的尾段，用户点「同步」补不回
	// 最新几轮。这种会话的增量由导入器的**时间地板**圈定（live-owned 必带 TimestampFloor），
	// 不能再叠一层序号切片，否则地板白设。
	if in.Full && !session.SessionIsLiveOwned(current.Exchanges) {
		delta = externalSessionDeltaAfterCtxSeq(imported.Exchanges, binding.AgentCtxSeq)
	}
	importedCount := 0
	for _, exchange := range delta {
		added, err := appendImportedExchange(ctx, manager, current, agentName, exchange)
		if err != nil {
			return out, err
		}
		if added {
			importedCount++
		}
	}
	// 即使本轮 ImportedCount=0 也要刷新 agent_ctx_seq：live 写入与 count=0 的增量
	// 会让 ctx_seq 落后于库长度，下次 Full 同步按陈旧游标切片就会重放尾段（实测
	// 「点同步整段重放 count=13」根因之一）。幂等护栏只挡重放，刷游标消根因。
	latest, err := manager.Get(ctx, current.Key, 0)
	if err != nil {
		return out, err
	}
	agentSessionID := strings.TrimSpace(imported.AgentSessionID)
	if agentSessionID == "" {
		agentSessionID = binding.AgentSessionID
	}
	if err := manager.UpdateAgentState(ctx, latest, agentName, len(latest.Exchanges), agentSessionID); err != nil {
		return out, err
	}
	subagentCount, err := syncImportedSubagentSessions(ctx, manager, latest, agentName, imported.Subagents)
	if err != nil {
		return out, err
	}
	if imported.Cursor.SourcePath != "" {
		if err := manager.UpdateExternalSessionCursor(ctx, current.Key, agentName, imported.Cursor); err != nil {
			return out, err
		}
	}
	out.ImportedCount = importedCount + subagentCount
	out.LastTimestamp = lastExternalSyncTimestamp(latest.Exchanges)
	if out.ImportedCount > 0 {
		log.Printf("[session/sync] external delta imported root=%s session=%s agent=%s agent_session_id=%s count=%d", strings.TrimSpace(in.RootID), strings.TrimSpace(in.Key), agentName, binding.AgentSessionID, out.ImportedCount)
	}
	return out, nil
}

func syncImportedSubagentSessions(
	ctx context.Context,
	manager *session.Manager,
	rootSession *session.Session,
	agentName string,
	items []agenttypes.ImportedSubagentSession,
) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	sessionsByAgentID := make(map[string]*session.Session, len(items))
	pending := append([]agenttypes.ImportedSubagentSession(nil), items...)
	importedCount := 0
	for len(pending) > 0 {
		progressed := false
		next := make([]agenttypes.ImportedSubagentSession, 0, len(pending))
		for _, item := range pending {
			agentSessionID := strings.TrimSpace(item.AgentSessionID)
			if agentSessionID == "" {
				continue
			}
			parent := rootSession
			if parentAgentSessionID := strings.TrimSpace(item.ParentAgentSessionID); parentAgentSessionID != "" {
				parent = sessionsByAgentID[parentAgentSessionID]
				if parent == nil {
					next = append(next, item)
					continue
				}
			}
			binding, err := manager.FindAgentBindingByAgentSession(ctx, agentName, agentSessionID)
			if err != nil {
				return importedCount, err
			}
			var child *session.Session
			start := 0
			if binding == nil {
				name := strings.TrimSpace(item.Title)
				if name == "" {
					name = "Subagent"
				}
				if alias, ok := manager.LookupAliasForAgent(agentName, agentSessionID); ok {
					name = alias
				}
				child, err = manager.Create(ctx, session.CreateInput{
					Type:             session.TypeChat,
					ParentSessionKey: parent.Key,
					ParentToolCallID: strings.TrimSpace(item.ParentToolCallID),
					Agent:            agentName,
					Model:            strings.TrimSpace(item.Model),
					Name:             name,
				})
			} else {
				child, err = manager.Get(ctx, binding.SessionKey, 0)
				start = binding.AgentCtxSeq
			}
			if err != nil {
				return importedCount, err
			}
			for _, exchange := range externalSessionDeltaAfterCtxSeq(item.Exchanges, start) {
				added, err := appendImportedExchange(ctx, manager, child, agentName, exchange)
				if err != nil {
					return importedCount, err
				}
				if added {
					importedCount++
				}
			}
			latest, err := manager.Get(ctx, child.Key, 0)
			if err != nil {
				return importedCount, err
			}
			if err := manager.UpdateAgentState(ctx, latest, agentName, len(latest.Exchanges), agentSessionID); err != nil {
				return importedCount, err
			}
			sessionsByAgentID[agentSessionID] = latest
			progressed = true
		}
		if !progressed {
			return importedCount, errors.New("imported subagent parent session not found")
		}
		pending = next
	}
	return importedCount, nil
}

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
