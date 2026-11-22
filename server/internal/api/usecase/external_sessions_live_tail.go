package usecase

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

// 进程重启时被掐断的那一轮：从 agent 转录把助手侧内容补回 MindFS。
//
// 为什么需要它：实时路径把整轮正文攒在内存里，只在回合收尾时写块流（见
// session_send.go 的 persistTurnBlocks）。进程在回合中途被杀（`systemctl restart`、
// 崩溃、OOM）时，用户屏幕上已经流式看过的那段内容一个字都没落盘；而 live-owned 会话的
// 常规增量同步是**故意**关掉的（external_sessions_sync.go 的所有权守卫，2026-09-16
// 重导事故之后），于是表现为「聊天记录消失，刷新/重开都不回来」——2026-11-17 实测：
// codex 会话 1794875218-883d38d3f760 在长任务回合中被重启，库里只剩用户行和一条空
// 助手行，转录里那条回复还在。
//
// 与「每会话启动兜底补齐」的区别（那是被事故证伪并撤掉的方案）：这里**只补最后一轮**，
// 且必须先证明这一轮没收尾 —— 尾部没有任何块/旧格式正文。已收尾的历史回合一个字都不碰，
// 所以导入器「相邻同角色条目合并」导致的内容差异不再有机会制造重复。
//
// 补写形态对齐「正常收尾」：
//   - 尾部已有空助手行（收尾路径写下的占位行）→ 正文/工具卡直接挂到它上面，仍是
//     「这条提问 → 这条回复」；
//   - 尾部只有用户行（进程没活到收尾）→ 按用户行身份新建助手行（同一 mindfs_id/
//     turn_seq），与实时路径收尾时写出来的行同形。
//
// 时间地板取**用户行**的时间戳（回合开始），不是最后一行的时间戳：被中断的助手行是
// 回合收尾时才写的，它的时间比转录里的回复还晚，拿它当起点会把要补的那条永远挡在外面。

var interruptedLiveTailAttempts sync.Map

func interruptedLiveTailAttemptKey(rootID, sessionKey string, userSeq int) string {
	return fmt.Sprintf("%s::%s::%d", strings.TrimSpace(rootID), strings.TrimSpace(sessionKey), userSeq)
}

// orphanAdoptAttempts 记录本进程已经尝试过「认领孤立转录」的会话，避免每次轮询都重扫磁盘。
var orphanAdoptAttempts sync.Map

// adoptOrphanedLiveTailBinding 为「首轮中途被重启、绑定里没有转录 id」的 live-owned 会话
// 找回它的转录，并补写绑定。
//
// 成因：claude 的 CLI session id 要等第一条流消息才可见，若进程在第一轮中途被重启，绑定里
// 一直是空的（persistLearnedAgentSessionID 没来得及写）。重启后既 resume 不了，同步也因
// 「没有 id」直接返回 —— 那一轮内容永久留在转录里、界面看不到，正是用户报的「丢记录、
// 同步不回来」。
//
// 判据必须保守：只认领**唯一**匹配的转录 —— 首条用户正文（无损空白后是转录首条用户消息的
// 子串）且 cwd 与根目录一致；0 个或多个匹配一律放弃（绝不猜），避免把别处会话的内容串进来。
// 只在「尾部未收尾」时尝试：已收尾的回合本就没有要补的东西，没必要扫盘。
func (s *Service) adoptOrphanedLiveTailBinding(
	ctx context.Context,
	root fs.RootInfo,
	rootID string,
	manager *session.Manager,
	current *session.Session,
	agentName string,
	binding *session.AgentBinding,
) (*session.AgentBinding, error) {
	if manager == nil || current == nil || !session.SessionIsLiveOwned(current.Exchanges) {
		return nil, nil
	}
	attemptKey := fmt.Sprintf("%s::%s", strings.TrimSpace(rootID), strings.TrimSpace(current.Key))
	if _, seen := orphanAdoptAttempts.LoadOrStore(attemptKey, struct{}{}); seen {
		return nil, nil
	}
	lastUser, tailFinished, err := liveTailIndex(ctx, manager, current)
	if err != nil || lastUser == nil || tailFinished {
		return nil, err
	}
	firstUserText := firstUserExchangeText(ctx, manager, current)
	if firstUserText == "" {
		return nil, nil
	}
	importer, err := s.resolveExternalSessionImporter(agentName)
	if err != nil {
		return nil, err
	}
	listed, err := importer.ListExternalSessions(ctx, agenttypes.ListExternalSessionsInput{
		RootPath: root.RootPath,
		Agent:    agentName,
		Limit:    200,
	})
	if err != nil {
		return nil, err
	}
	var match *agenttypes.ExternalSessionSummary
	ambiguous := false
	for index := range listed.Items {
		item := listed.Items[index]
		if !strings.EqualFold(strings.TrimSpace(item.Agent), agentName) {
			continue
		}
		if trimPath(item.Cwd) != trimPath(root.RootPath) {
			continue
		}
		if !firstUserTextMatches(firstUserText, item.FirstUserText) {
			continue
		}
		if match != nil {
			ambiguous = true
			break
		}
		copied := item
		match = &copied
	}
	if ambiguous || match == nil || strings.TrimSpace(match.AgentSessionID) == "" {
		return nil, nil
	}
	ctxSeq := 0
	if binding != nil {
		ctxSeq = binding.AgentCtxSeq
	}
	if err := manager.UpdateAgentState(ctx, current, agentName, ctxSeq, match.AgentSessionID); err != nil {
		return nil, err
	}
	log.Printf("[session/recovery] live_tail.adopt root=%s session=%s agent=%s agent_session_id=%s",
		strings.TrimSpace(rootID), current.Key, agentName, match.AgentSessionID)
	return manager.FindAgentBinding(ctx, current.Key, agentName)
}

// firstUserExchangeText 返回会话第一条用户行的正文（块流优先，老会话退回主日志）。
func firstUserExchangeText(ctx context.Context, manager *session.Manager, current *session.Session) string {
	if current == nil {
		return ""
	}
	for _, exchange := range current.Exchanges {
		if exchange.Role != "user" {
			continue
		}
		if body := strings.TrimSpace(recordedExchangeBody(ctx, manager, current.Key, exchange)); body != "" {
			return body
		}
	}
	return ""
}

// firstUserTextMatches 判定「会话里存的用户正文」是不是「转录首条用户消息」的可信前缀/子串。
// 转录首条 = 用户正文 + MindFS 注入的上下文/提示（后缀），所以正文通常是它的前缀；插件模式下
// 正文被包在 [USER_PROMPT] 里，用子串兜住。要求正文足够长（≥16 字）以免「继续」「好」这类
// 短应答误配。
func firstUserTextMatches(sessionText, transcriptText string) bool {
	needle := normalizeExchangeContent(sessionText)
	haystack := normalizeExchangeContent(transcriptText)
	if len([]rune(needle)) < 16 || haystack == "" {
		return false
	}
	return strings.Contains(haystack, needle)
}

func trimPath(path string) string {
	return strings.TrimRight(strings.TrimSpace(path), "/")
}

// recoverInterruptedLiveTurn 在 best-effort 同步的「开始处」被调用（增量与手点「同步」都走它）。
// 每个 (会话, 最后一轮用户行) 每进程只尝试一次：转录里确实没有回复的回合（比如
// provider 直接 403）不该让每次轮询都重读一遍转录。force=true（用户手点「同步」，Full）
// 跳过这条去重 —— 首拍转录可能还没 flush，用户有权重试一次。
func (s *Service) recoverInterruptedLiveTurn(
	ctx context.Context,
	root fs.RootInfo,
	rootID string,
	manager *session.Manager,
	current *session.Session,
	agentName string,
	binding *session.AgentBinding,
	force bool,
) error {
	if manager == nil || current == nil || binding == nil {
		return nil
	}
	if strings.TrimSpace(binding.AgentSessionID) == "" {
		// 没有转录 id 就无从导入。正常情况下这里不该出现：会话进行中会由
		// persistLearnedAgentSessionID 尽早写回；真正的空绑定由调用方在进入本函数前
		// 尝试「按首条用户消息认领孤立转录」。
		return nil
	}
	// 本进程里这一轮正在跑：屏幕上的流式内容属于**进行中**的回合，不能被当成被中断的尾巴。
	if SessionTurnActive(rootID, current.Key) {
		return nil
	}
	lastUser, tailFinished, err := liveTailIndex(ctx, manager, current)
	if err != nil || lastUser == nil || tailFinished {
		return err
	}
	attemptKey := interruptedLiveTailAttemptKey(rootID, current.Key, lastUser.Seq)
	if !force {
		if _, seen := interruptedLiveTailAttempts.LoadOrStore(attemptKey, struct{}{}); seen {
			return nil
		}
	}
	importer, err := s.resolveExternalSessionImporter(agentName)
	if err != nil {
		interruptedLiveTailAttempts.Delete(attemptKey)
		return err
	}
	imported, err := importer.ImportExternalSession(ctx, agenttypes.ImportExternalSessionInput{
		RootPath:       root.RootPath,
		Agent:          agentName,
		AgentSessionID: binding.AgentSessionID,
		AfterTimestamp: lastUser.Timestamp,
		// 游标在这里不可信（live-owned 会话的字节游标是冻结的）：强制重读再按时间地板过滤。
		ForceRead: true,
	})
	if err != nil {
		interruptedLiveTailAttempts.Delete(attemptKey)
		return err
	}
	items := recoveryAgentExchanges(imported.Exchanges)
	if len(items) == 0 {
		return nil
	}
	log.Printf("[session/recovery] live_tail.recover root=%s session=%s agent=%s agent_session_id=%s user_seq=%d items=%d",
		strings.TrimSpace(rootID), current.Key, agentName, binding.AgentSessionID, lastUser.Seq, len(items))
	return applyRecoveredLiveTail(ctx, manager, current, agentName, binding.AgentSessionID, lastUser, items)
}

// liveTailIndex 找到最后一轮的 user 行，并判断这一轮是否已经收尾（尾部有块或旧格式正文）。
func liveTailIndex(ctx context.Context, manager *session.Manager, current *session.Session) (*session.Exchange, bool, error) {
	var lastUser *session.Exchange
	for i := len(current.Exchanges) - 1; i >= 0; i-- {
		if current.Exchanges[i].Role == "user" {
			lastUser = &current.Exchanges[i]
			break
		}
	}
	if lastUser == nil {
		return nil, false, nil
	}
	tailSeqs := make(map[int]bool)
	for _, exchange := range current.Exchanges {
		if exchange.Seq > lastUser.Seq {
			tailSeqs[exchange.Seq] = true
		}
	}
	if len(tailSeqs) == 0 {
		return lastUser, false, nil
	}
	blocks, err := manager.GetBlocksWindow(ctx, current.Key, tailSeqs)
	if err != nil {
		return nil, false, err
	}
	withBlocks := make(map[int]bool, len(blocks))
	for _, block := range blocks {
		withBlocks[block.Seq] = true
	}
	for _, exchange := range current.Exchanges {
		if exchange.Seq <= lastUser.Seq || exchange.Role != "agent" {
			continue
		}
		if withBlocks[exchange.Seq] || strings.TrimSpace(exchange.LegacyContent) != "" {
			return lastUser, true, nil
		}
	}
	return lastUser, false, nil
}

// recoverLiveContinuationTail 补录「回合边界之后 CLI 自己续跑」的那一段输出。
//
// 与上面 recoverInterruptedLiveTurn 的区别：那条补的是**没走完的回合**（尾部是空占位或
// 根本没有助手行）；这条补的是**已经收尾的回合之后**又冒出来的内容 —— Claude CLI 在
// result 之后收到后台任务回注（task-notification）会自己再跑一整段循环，事件实时可见
// （走的还是上一轮那个 OnUpdate 回调），但实时路径的落盘只在 SendMessage 返回时做了一次，
// 之后的续写没有任何写入者；live-owned 会话的自动增量同步又是关着的，于是「刷新就没了」
// （2026-11-22 实测 llm-deploy 会话：seq=196 之后的思考卡与工具行全在转录里、库里没有）。
//
// 三个闸门，缺一不可：
//   - 回合不在跑：跑着的那一轮由实时路径负责，导入器插进来就是双写；
//   - 补录标记：标记只在「回合结束之后还有事件进来」时置位（session_send.go），
//     否则每次同步都要读一次转录（live-owned 的字节游标是冻结的，读一次就是从游标到文件尾）；
//   - 最后一行是助手行 + 有真实时间：地板取**最后一行**的时间戳。续写发生在整轮落盘之后，
//     所以「比最后一行还新」正好把已落库的内容挡在外面；尾部是用户行说明这一轮没走完，
//     归 recoverInterruptedLiveTurn 管。
//
// 写入只取助手侧条目（recoveryAgentExchanges）：用户行由实时路径在发消息时就写好了。
// 判重靠导入器的 native id（appendImportedExchange），所以同一条转录被补两次也只会落一行。
func (s *Service) recoverLiveContinuationTail(
	ctx context.Context,
	root fs.RootInfo,
	rootID string,
	manager *session.Manager,
	current *session.Session,
	agentName string,
	binding *session.AgentBinding,
) error {
	if current == nil || binding == nil || strings.TrimSpace(binding.AgentSessionID) == "" {
		return nil
	}
	if SessionTurnActive(rootID, current.Key) {
		return nil
	}
	if !takeSessionContinuationTail(rootID, current.Key) {
		return nil
	}
	// 出错要把标记放回去，否则这一段续写要等到下一轮结束才有人再看它一眼。
	restore := true
	defer func() {
		if restore {
			markSessionContinuationTail(rootID, current.Key)
		}
	}()
	importer, err := s.resolveExternalSessionImporter(agentName)
	if err != nil || importer == nil || !importer.HasTranscriptTimestamps() {
		// 没有真实时间戳的 agent 找不到可信地板，交给身份判重兜不住重复，宁可不补。
		restore = false
		return nil
	}
	if n := len(current.Exchanges); n == 0 || current.Exchanges[n-1].Role != "agent" {
		restore = false
		return nil
	}
	floor := lastExternalSyncTimestamp(current.Exchanges)
	if floor.IsZero() {
		restore = false
		return nil
	}
	imported, err := importer.ImportExternalSession(ctx, agenttypes.ImportExternalSessionInput{
		RootPath:       root.RootPath,
		Agent:          agentName,
		AgentSessionID: binding.AgentSessionID,
		TimestampFloor: floor,
		// 从冻结的字节游标往后读：这条路径只在真有续写时才进来，游标能让重复补录
		// 退化成一次 stat（导入器的「文件未变即跳过」快路径）。
		Cursor: binding.ExternalCursor(),
	})
	if err != nil {
		return err
	}
	restore = false
	items := recoveryAgentExchanges(imported.Exchanges)
	added := 0
	for _, item := range items {
		ok, err := appendImportedExchange(ctx, manager, current, agentName, item)
		if err != nil {
			return err
		}
		if ok {
			added++
		}
	}
	// 游标必须推进：它只服务于「这条路径下次从哪儿接着读」，Full 同步与中断回合补录
	// 都不吃它（那两条路径故意从 0 重读再按时间地板过滤）。
	if strings.TrimSpace(imported.Cursor.SourcePath) != "" {
		if err := manager.UpdateExternalSessionCursor(ctx, current.Key, agentName, imported.Cursor); err != nil {
			log.Printf("[session/recovery] live_continuation.cursor.error root=%s session=%s err=%v",
				strings.TrimSpace(rootID), current.Key, err)
		}
	}
	// 尾部还没收尾（续写仍在跑 / 正在跑工具）：这一条被导入器扣下了，标记放回去，
	// 下一次同步再试。收尾之后 committed 会追上 offset。
	if imported.Cursor.CommittedOffset < imported.Cursor.Offset {
		restore = true
	}
	if added > 0 {
		latest, err := manager.Get(ctx, current.Key, 0)
		if err != nil {
			return err
		}
		if err := manager.UpdateAgentState(ctx, latest, agentName, len(latest.Exchanges), binding.AgentSessionID); err != nil {
			return err
		}
		log.Printf("[session/recovery] live_continuation.recover root=%s session=%s agent=%s agent_session_id=%s rows=%d",
			strings.TrimSpace(rootID), current.Key, agentName, binding.AgentSessionID, added)
	}
	return nil
}

// recoveryAgentExchanges 只取助手侧条目：用户行在实时路径上**发消息前**就已落盘，
// 这里再导一次只会多出一个「相邻用户条目合并」后的巨型气泡（实测 2026-11-17 手动同步
// 就在 ask 下面渲染了四条提问拼起来的重复用户消息）。
func recoveryAgentExchanges(exchanges []agenttypes.ImportedExchange) []agenttypes.ImportedExchange {
	out := make([]agenttypes.ImportedExchange, 0, len(exchanges))
	for _, exchange := range exchanges {
		if strings.TrimSpace(exchange.Role) != "agent" {
			continue
		}
		if strings.TrimSpace(exchange.Content) == "" && len(exchange.Aux) == 0 {
			continue
		}
		out = append(out, exchange)
	}
	return out
}

func applyRecoveredLiveTail(
	ctx context.Context,
	manager *session.Manager,
	current *session.Session,
	agentName string,
	agentSessionID string,
	lastUser *session.Exchange,
	items []agenttypes.ImportedExchange,
) error {
	targetSeq := 0
	turnID := strings.TrimSpace(lastUser.MindFSID)
	for i := len(current.Exchanges) - 1; i >= 0; i-- {
		exchange := current.Exchanges[i]
		if exchange.Seq <= lastUser.Seq {
			break
		}
		if exchange.Role == "agent" {
			// 收尾路径写下的空占位行：正文直接挂上去，用户看到的仍是同一条回复。
			targetSeq = exchange.Seq
			if turnID = strings.TrimSpace(exchange.MindFSID); turnID == "" {
				turnID = strings.TrimSpace(lastUser.MindFSID)
			}
			break
		}
	}
	if targetSeq <= 0 {
		seq, err := manager.AddExchangeForAgentAtWithIdentityFactory(
			session.WithExchangeSource(ctx, session.ExchangeSourceImport),
			current, "agent", "", agentName, "", "", "", items[0].Timestamp,
			func(seq int) session.ExchangeIdentity {
				return session.ExchangeIdentity{
					MindFSID: strings.TrimSpace(lastUser.MindFSID),
					TurnSeq:  lastUser.Seq,
					AgentID:  strings.TrimSpace(items[0].NativeID),
				}
			},
		)
		if err != nil {
			return err
		}
		targetSeq = seq
	}
	if targetSeq <= 0 {
		return nil
	}
	if turnID == "" {
		turnID = session.TurnIDForSeq(current.Exchanges, targetSeq)
	}
	if err := persistImportedExchangeContent(ctx, manager, current.Key, targetSeq, turnID, items[0]); err != nil {
		return err
	}
	for _, item := range items[1:] {
		if _, err := appendImportedExchange(ctx, manager, current, agentName, item); err != nil {
			return err
		}
	}
	latest, err := manager.Get(ctx, current.Key, 0)
	if err != nil {
		return err
	}
	// AgentCtxSeq 记的是「agent 已经看到多少行」：补回的这行是 agent 自己产出的，
	// 与正常收尾后的口径一致（= 已落盘总行数）。
	return manager.UpdateAgentState(ctx, latest, agentName, len(latest.Exchanges), agentSessionID)
}
