package usecase

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

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
		// 时间窗必须**第一个**判：它是这一圈里选择性最高的条件，而下面取正文要从块流
		// 里读回来并反序列化整段块窗口。判据顺序换一下，结果一模一样（纯合取），
		// 代价却从「每行历史各解析一次块窗口」降到「只解析时间上真的挨着的那一两行」。
		//
		// 放在最后是 O(N²)：导入 N 条、每条的正文又都不在库里（判重必然走到底），
		// 就是 2N 次块窗口解析 —— 实测 2026-10-19 pc-win 一份 105MB 的 codex 转录，
		// 每条耗时从 5.3s 一路涨到 21s，整份导入 20 分钟起步。取正文也只取一次：
		// 原来同一个 ex 连读两遍。
		if diff := ex.Timestamp.Sub(ts); diff <= -userExchangeRepeatTolerance || diff >= userExchangeRepeatTolerance {
			continue
		}
		body := recordedExchangeBody(ctx, manager, target.Key, ex)
		if strings.TrimSpace(body) == "" {
			continue
		}
		if !sameRecordedExchangeContent(role, body, content) {
			continue
		}
		return true
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
