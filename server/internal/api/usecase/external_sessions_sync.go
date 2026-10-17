package usecase

import (
	"context"
	"errors"
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
