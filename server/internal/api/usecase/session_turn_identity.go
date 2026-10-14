package usecase

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"
	"unicode"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
)

func exchangeAgentKey(ex session.Exchange) string {
	return strings.TrimSpace(ex.AgentID)
}

// exchangeIdentityRecorded 判定 target 里是否已有**同一条转录消息**。
//
// 判据以可证明的身份为先（方案 S2c / D10）：
//
//	① agent_id 相等 → 铁证，命中。内容可以不同：导入器会把**相邻同角色**条目合并
//	   （claude importer:1275 / acp importer:238），所以同一条 agent 消息在库里可能是
//	   分两行、在转录里是一行。纯内容判重对这种形态永远漏。
//	② ACP user 原生 id 等于已存行的 mindfs_id → 命中。ACP 的 prompt 携带了这个 turn id，
//	   若转录原样回显，它就是同一用户行的桥接键；不需要把它伪装成 agent_id。
//	③ mindfs_id 相等 → 命中。导入侧由真实 agent_id 确定性派生 mindfs_id，重导能对齐。
//
// 两者都为空时返回 false（没有可证明的东西）。
func exchangeIdentityRecorded(target *session.Session, role, agentID, mindfsID string) bool {
	agentID = strings.TrimSpace(agentID)
	mindfsID = strings.TrimSpace(mindfsID)
	if target == nil || (agentID == "" && mindfsID == "") {
		return false
	}
	for _, ex := range target.Exchanges {
		if !strings.EqualFold(strings.TrimSpace(ex.Role), strings.TrimSpace(role)) {
			continue
		}
		if agentID != "" && exchangeAgentKey(ex) == agentID {
			return true
		}
		// ACP user prompts carry MindFS's turn id as PromptRequest.MessageId.
		// When the transcript echoes that exact value, it bridges the native user
		// message to the already persisted user row without copying it into agent_id.
		if strings.EqualFold(strings.TrimSpace(role), "user") && agentID != "" && ex.MindFSID == agentID {
			return true
		}
		if mindfsID != "" && ex.MindFSID != "" && ex.MindFSID == mindfsID {
			return true
		}
	}
	return false
}

// recordedExchangeBody 取一行「用来比对的那段正文」。
//
// S2d-2 起正文住在块流里，主日志的 `LegacyContent` 对新写入的行恒空 —— 只比主日志的
// 话，同一句话会被反复当成新行写下去（claude 的 user 行拿不到 native uuid，判重
// 只能靠正文）。所以取的是**块流里该 seq 自己的 text 块**；块流还没有这一行时
// （老会话、还没回填）退回主日志。
//
// 注意比对用的**不是**块流全文：同一轮可能有多个写入者各落一行（见 `mergeExchangeRecord`），
// 拿全文去比会认不出「这一行自己的正文」。seq 是行的身份，块按 seq 归组才对应得上。
func recordedExchangeBody(ctx context.Context, manager *session.Manager, key string, ex session.Exchange) string {
	if manager == nil || key == "" || ex.Seq <= 0 {
		return ex.LegacyContent
	}
	blocks, err := manager.GetBlocksWindow(ctx, key, map[int]bool{ex.Seq: true})
	if err != nil || len(blocks) == 0 {
		return ex.LegacyContent
	}
	var parts []string
	for _, block := range blocks {
		if block.Type == session.BlockTypeText && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, block.Text)
		}
	}
	if len(parts) == 0 {
		// 这一轮只有工具调用 / 只有 reasoning —— 它没有「正文」可比。
		// 调用方已经把「空正文」排除在外了，这里给空串让它自己走完原来的逻辑。
		return ""
	}
	return strings.Join(parts, "")
}

// exchangeAlreadyRecorded 判定 target 里是否已有这一行。导入侧与发送侧共用，
// 保证两个写入者判据对称（「两把尺」的教训：判据只能有一处实现）。
//
// 判据先找可证明的身份（方案 S2c / D10）：
//
//	① nativeID 相等 → 铁证，命中。内容可以不同：导入器会把**相邻同角色**条目合并
//	   （claude importer:1275 / acp importer:238），所以同一条 agent 消息在库里可能是
//	   分两行、在转录里是一行。纯内容判重对这种形态永远漏。
//	② ACP user 的 nativeID 若等于 live user 行 MindFSID，也命中：该值正是随 prompt
//	   传入并由 ACP 回显的 turn id。
//	③ nativeID 为空（历史数据 / 尚未实证的 agent）→ 退回「内容 + ±5s」，并且
//	   **每次会话记一次日志**，让「这里还在猜」这件事可观测（S2d 之后应当归零）。
//
// nativeID 非空但没匹配上 → **不是重复**，放行。这就是 D10 的「对不上跳过 + 日志，
// 永不猜」在写入侧的对偶：不许拿内容去认领一条身份不同的行。
//
// 为什么必须换掉纯内容判重（实测）：本机会话 `1791339665-fb5134041392` 有 9 对
// 「live 写一遍 + import 又写一遍」的 dsh 重复行。ACP 的更新时间戳是**合成**的
// （协议里 `SessionNotification` 没有时间字段），所以「内容 + ±5s」和 `TimestampFloor`
// 双双失效；同一个会话里还有 import + import 的重复（两次导入相隔 20s）同样漏掉。
func exchangeAlreadyRecorded(ctx context.Context, manager *session.Manager, target *session.Session, role, agentID, content string, ts time.Time) bool {
	if target == nil {
		return false
	}
	if exchangeIdentityRecorded(target, role, agentID, "") {
		return true
	}
	if strings.TrimSpace(agentID) != "" {
		// 有 agent 身份就不许猜 —— 否则「同一条消息分两行落库、其中一行 agent id
		// 被清掉（repoint）」这类行会被内容误判吞掉。
		return false
	}
	if ts.IsZero() {
		return false
	}
	noteIdentitylessGuess(target.Key)
	// 空正文不参与内容判重：那是「这一轮只有工具调用、没有文本」的占位行，
	// 不是某一句具体的话，拿它去认领另一条空行等于把无主的行按无主吞掉。
	// 有 agent id 的空行照样按身份判重 —— 那才是它唯一的真身份。
	if strings.TrimSpace(content) == "" {
		return false
	}
	for _, ex := range target.Exchanges {
		if ex.Role != role || ex.Timestamp.IsZero() {
			continue
		}
		if strings.TrimSpace(recordedExchangeBody(ctx, manager, target.Key, ex)) == "" {
			continue
		}
		if !sameRecordedExchangeContent(role, recordedExchangeBody(ctx, manager, target.Key, ex), content) {
			continue
		}
		if diff := ex.Timestamp.Sub(ts); diff > -userExchangeRepeatTolerance && diff < userExchangeRepeatTolerance {
			return true
		}
	}
	return false
}

// identitylessGuessSessions 只为让「判重还在靠内容猜」每个会话最多打一次日志：
// 判重在每个导入行上都会调用，不能每行一条。
var identitylessGuessSessions sync.Map

func noteIdentitylessGuess(sessionKey string) {
	if strings.TrimSpace(sessionKey) == "" {
		return
	}
	if _, seen := identitylessGuessSessions.LoadOrStore(sessionKey, struct{}{}); !seen {
		log.Printf("[session/identity] session=%s 判重退回内容猜测（这一行没有 native id）——"+
			"历史数据或该 agent 尚未接线；S2d 之后应当归零", sessionKey)
	}
}

// persistUserTurnExchange 在**回合开始前**把本轮用户消息落盘，返回它真实的 seq 与 mindfs_id。
//
// 为什么必须早落：广播给客户端的 seq 若是「预测」而落盘发生在回合结束，就会出现
// 「客户端已认定该行持久化、而窗口 maxSeq 与读接口都还看不到它」的窗口期。前端
// overlay 的「seq<=latestSeq 即让位给窗口」规则（SessionViewer.tailOverlay）会在
// 任何一次重锚定/翻页时把这行丢掉，表现为 agent 执行中自己的消息从列表消失、
// 点同步也刷不回来（2026-09-28 实测）。早落盘让 seq 从预测变成事实，两条判据不再打架。
//
// 判重与导入侧共用 exchangeAlreadyRecorded：转录导入器是另一个写入者，判据必须对称。
// 命中判重（导入器已抢先写）时返回已存在那条的 seq。
//
// 返回的 mindfs_id 是本轮的 MindFS 权威身份（方案 §3.1）：**没有客户端入口可带**
// （Stream.Send 只收 string），所以这里服务端按真实 seq mint UUIDv5（C2）。助手行与块流
// 必须复用同一个 mindfs_id，否则读侧挂不回轮次。
func persistUserTurnExchange(ctx context.Context, manager *session.Manager, current *session.Session, content, agent, mode, effort, fastService string, ts time.Time) (int, string, error) {
	exchangeCtx := session.WithExchangeSource(ctx, session.ExchangeSourceLive)
	if current == nil {
		return 0, "", errors.New("session required")
	}
	// Duplicate detection must inspect the manager's canonical view too. The
	// caller may still hold a pointer from before an external append/reload.
	latest, err := manager.Get(ctx, current.Key, 0)
	if err != nil {
		return 0, "", err
	}
	if latest == nil {
		return 0, "", errors.New("session not found")
	}
	*current = *latest
	// mint mindfs_id 在判重**之后**：判重命中时这一轮已经有了自己的 id（要从已有行
	// 取回来），mint 一个新的只是浪费。判重不看身份而看内容，是因为实时侧此刻
	// 还不知道 agent 侧的 id —— 行还没发出去。
	if exchangeAlreadyRecorded(ctx, manager, current, "user", "", content, ts) {
		return recordedUserExchangeSeq(ctx, manager, current, content, ts), recordedUserExchangeTurnID(ctx, manager, current, content, ts), nil
	}
	// mindfs_id 恒为确定性 UUIDv5。user 行在发送那一刻拿不到 agent 侧 id
	// （claude SDK 的 UserMessage 不带 uuid、codex 尚无实证），所以 seed 用
	// sessionKey|user|seq —— 它在同一会话内唯一且稳定，重导同一行算出同一个 id。
	// seq 必须由 manager 在锁内分配后再参与铸 id，不能从 current.Exchanges 预测。
	//
	// 不把 mindfs_id 复制进 agent_id：它是 MindFS 的 turn 身份，不是独立的 agent 原生 id。
	// ACP 若原样回显作为 PromptRequest.MessageId 传入的 turn id，导入判重会直接对比
	// user 行 mindfs_id；其他 agent 的 user 原生 id 仍只在真实拿到时记录，可为空。
	// S2d-2：主日志不要再带正文（块流是唯一载体，见下面 persistTurnBlocks）。
	var mindfsID string
	userSeq, err := manager.AddExchangeForAgentAtWithIdentityFactory(
		exchangeCtx, current, "user", "", agent, mode, effort, fastService, ts,
		func(seq int) session.ExchangeIdentity {
			mindfsID = session.NewUserTurnMindFSID(current.Key, seq)
			return session.ExchangeIdentity{MindFSID: mindfsID, TurnSeq: seq}
		},
	)
	if err != nil {
		return 0, "", err
	}
	if userSeq <= 0 || mindfsID == "" {
		return 0, "", errors.New("user exchange was not appended")
	}
	// 双写块流（方案 S2a）：用户消息也要有块 —— 块流是唯一正文载体（D1），
	// 少写这一条，S2d 删掉 Content 之后用户自己的话就没了。
	// 失败不拖垮本轮：主日志已经落过盘，块流可在 S2b 前重放补齐。
	if err := persistTurnBlocks(ctx, manager, current.Key, userSeq, mindfsID, content, nil); err != nil {
		log.Printf("[session] persist.user.blocks.error session=%s seq=%d err=%v", current.Key, userSeq, err)
	}
	return userSeq, mindfsID, nil
}

// recordedUserExchangeTurnID 返回判重命中的那条 user 行的 mindfs_id；没有则空串。
// 空串是合法的：历史行本来就没有 mindfs_id，不能因此报错（方案 §3.1「可空、可后补」）。
func recordedUserExchangeTurnID(ctx context.Context, manager *session.Manager, target *session.Session, content string, ts time.Time) string {
	if target == nil {
		return ""
	}
	for i := len(target.Exchanges) - 1; i >= 0; i-- {
		ex := target.Exchanges[i]
		if ex.Role != "user" || ex.Timestamp.IsZero() {
			continue
		}
		if !sameRecordedExchangeContent("user", recordedExchangeBody(ctx, manager, target.Key, ex), content) {
			continue
		}
		if diff := ex.Timestamp.Sub(ts); diff > -userExchangeRepeatTolerance && diff < userExchangeRepeatTolerance {
			return ex.MindFSID
		}
	}
	return ""
}

// newMindFSID mint 一个确定性 mindfs_id（转发 session.NewMindFSID）。
//
// 三处服务端发起的轮次（定时任务 / 看板阶段 / 子会话）都经 SendMessage 走到
// persistUserTurnExchange，那里按 sessionKey|user|seq 铸 id。这里是给
// attachBackgroundSessionUpdates 的子会话行用的同一入口 —— mint 点只有一处
// （session.NewMindFSID），两处调用不等于两套规则。
func newMindFSID(agentID, fallbackSeed string) string {
	return session.NewMindFSID(agentID, fallbackSeed)
}

// turnBlockStep 是 S2a 双写期写在块上的步序。
//
// **这是占位值，不是最终语义。** 方案 §3.3 的 `step` 是「按 agent 的消息投递单元递增」
// （claude = 一条 AssistantMessage，dsh/ACP = 一次 messageId 变更）。S2a 的块在**轮末
// 一次性**落盘，此刻投递边界已经过去、没有被记录下来，所以整轮只能算作一个投递单元。
//
// 为什么不按 §3.3 写哨兵 -1：读取侧的规则是「忽略 step<0 的版本」，写了 -1 就等于
// 把 S2a 期间落下的块**全部作废**，S2b 切读路径的那一刻正文会凭空消失。
// 真按投递单元切分是 S2c 的工作（与 ACP messageId 接线一起做），届时用
// `(seq,pos)` + `revision+1` 覆盖成真值，不需要迁移历史。
const turnBlockStep = 1

// persistTurnBlocks 把一轮的正文 + aux 编译成块流追加进去（方案 S2a 双写）。
//
// 为什么放在 usecase 而不是 Manager.appendExchange 里：块序必须与前端渲染序一致
// （见 session.BuildTurnBlocks），而那需要「正文 + aux 的交错上下文」——
// appendExchange 只看到一段字符串，appendExchangeAux 只看到一条 aux，都拼不出完整顺序。
func persistTurnBlocks(ctx context.Context, manager *session.Manager, key string, seq int, turnID, content string, aux []session.ExchangeAux) error {
	if manager == nil || seq <= 0 {
		return nil
	}
	blocks := session.BuildTurnBlocks(seq, turnBlockStep, turnID, content, aux)
	if len(blocks) == 0 {
		return nil
	}
	return manager.AppendBlocks(ctx, key, blocks)
}

// recordedUserExchangeSeq 返回判重命中的那条已有 user 行的 seq；找不到时返回 0。
func recordedUserExchangeSeq(ctx context.Context, manager *session.Manager, target *session.Session, content string, ts time.Time) int {
	if target == nil {
		return 0
	}
	for i := len(target.Exchanges) - 1; i >= 0; i-- {
		ex := target.Exchanges[i]
		if ex.Role != "user" || ex.Timestamp.IsZero() {
			continue
		}
		if !sameRecordedExchangeContent("user", recordedExchangeBody(ctx, manager, target.Key, ex), content) {
			continue
		}
		if diff := ex.Timestamp.Sub(ts); diff > -userExchangeRepeatTolerance && diff < userExchangeRepeatTolerance {
			return ex.Seq
		}
	}
	return 0
}

// sameRecordedExchangeContent 判断两条同角色内容是否「同一条」。
// 默认要求完全相等；另容忍两类双写差异（同一轮由实时路径与转录导入各写一次）：
//
//  1. 空白归一化后相等 —— 两个写入者对段落空行的处理不同。实测 2026-09-14 16:18 的
//     一轮：实时版 1765 字、导入版 1761 字，逐字符 diff 只有 2 处 "\n\n" 之差
//     （相似度 0.9989），只比字面量必然漏判。
//  2. 助手侧的前缀关系 —— 两边抓到的快照长度可能差很多（实测实时 21 字 seq=128、
//     转录导入 556 字 seq=129＝前者 + "\n\nAPI Error: 502 …"）。
//
// 用户侧不吃前缀容忍：「继续」是「继续吧」的前缀，但那是两句不同的话。前缀关系还要求
// 较短一侧够长（≥16 字），避免「好」/「好的，我这就去…」这种短应答误判。
// 两条判据都只在 ±5s 容忍窗内生效（见 exchangeAlreadyRecorded）。
func sameRecordedExchangeContent(role, a, b string) bool {
	if a == b {
		return true
	}
	if a == "" || b == "" {
		return false
	}
	na, nb := normalizeExchangeContent(a), normalizeExchangeContent(b)
	if na == nb {
		return true
	}
	if role != "agent" {
		return false
	}
	shorter := na
	if len([]rune(nb)) < len([]rune(shorter)) {
		shorter = nb
	}
	if len([]rune(shorter)) < 16 {
		return false
	}
	return strings.HasPrefix(na, nb) || strings.HasPrefix(nb, na)
}

// normalizeExchangeContent 用于「同一条消息的两种渲染」比较：先剥掉 CLI 自注入的标记
// （名单见 agenttypes.TranscriptNoisePrefixes，与导入侧共用），再抹掉所有空白。
//
// 抹掉而不是折叠：折叠成单空格是错的 —— 实时路径会把 \n\n 插进 Markdown 标记内部
// （实测 `**支具\n\n+康复总市场**` vs 导入版 `**支具+康复总市场**`），折叠后前者多一个
// 空格，仍判不相等。代价是 "a b" 与 "ab" 视为相同，但这只发生在 ±5s 容忍窗内，
// 同一轮的两个写入者本来就该是同一条。
func normalizeExchangeContent(s string) string {
	stripped := agenttypes.StripTranscriptNoisePrefixes(s)
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, stripped)
}
