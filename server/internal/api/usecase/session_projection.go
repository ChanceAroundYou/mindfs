package usecase

import (
	"context"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"mindfs/server/internal/session"
)

// SetSessionProjectionDisabledEnv 关闭投影的逃生口：MINDFS_SESSION_PROJECTION=off。
// 投影只影响展示，关掉即回到「按原始数据展示」的旧行为。
const SessionProjectionDisabledEnv = "MINDFS_SESSION_PROJECTION"

var (
	sessionProjectionOnce      sync.Once
	sessionProjectionIsEnabled bool
)

// SessionProjectionEnabled 报告是否启用读取投影（默认启用）。
func SessionProjectionEnabled() bool {
	sessionProjectionOnce.Do(func() {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(SessionProjectionDisabledEnv))) {
		case "off", "false", "0", "no":
			sessionProjectionIsEnabled = false
		default:
			sessionProjectionIsEnabled = true
		}
	})
	return sessionProjectionIsEnabled
}

// 会话读取投影：把同一轮被多个写入者各写一份的行折叠回一份。
//
// 为什么不在写入侧解决完事：写入侧的判据只能靠「内容相似 + ±5 秒」猜（两个写入者的
// 时间戳取自不同事件：用户按发送 vs CLI 写转录，差值实测 6.4s / 15.6s / 36.7s 都会漏）。
// 读取侧可以做**非破坏**的折叠：数据一行不删，投影错了改回来即可，而写入侧猜错就永久落盘。
//
// 只折叠「可证明」的重复，其余一律保留：
//
//	规则 A（来源对决）：一侧算「实时路径写的」、另一侧不算，内容在剥掉 CLI 噪声前缀并
//	  抹掉空白后相等 → 保留实时那侧，隐藏另一侧。新数据靠显式 source 标注判定，
//	  老数据靠实时路径独有字段兜底（否则历史会话里的重复永远折叠不掉）。
//	  配对是一对一的（每条实时行只吸收一条），避免把「用户真的重发过两次」也吞掉。
//	规则 B（铁证）：同角色 + 归一化内容相同 + 时间戳**同一时刻** → 保留首条。真实重发
//	  不可能共享同一时间戳。
//
// 返回保留后的序列、隐藏映射（隐藏 seq → 保留 seq），以及其中由「时间戳铁证」判定的那部分
// （ruleB）；hidden 减去 ruleB 就是规则 A 判定的那些。aux 以 seq 为键，调用方用隐藏映射把
// 隐藏行的 aux 重挂到保留行上，再交给前端已有的按 callId 去重。
func ProjectExchanges(exchanges []session.Exchange) ([]session.Exchange, map[int]int, map[int]int) {
	hiddenToKept := map[int]int{}
	provenToKept := map[int]int{}
	if len(exchanges) < 2 {
		return exchanges, hiddenToKept, provenToKept
	}

	hidden := make([]bool, len(exchanges))

	// 规则 A：带「实时标记」的行吸收不带标记的孪生行，一对一。
	markedByKey := map[projectKey][]int{}
	for index, exchange := range exchanges {
		if !isLiveMarkedExchange(exchange) {
			continue
		}
		if key, ok := projectRowKey(exchange); ok {
			markedByKey[key] = append(markedByKey[key], index)
		}
	}
	consumed := map[projectKey]int{}
	for index, exchange := range exchanges {
		if isLiveMarkedExchange(exchange) {
			continue
		}
		key, ok := projectRowKey(exchange)
		if !ok {
			continue
		}
		candidates := markedByKey[key]
		offset := consumed[key]
		if offset >= len(candidates) {
			continue
		}
		consumed[key] = offset + 1
		hidden[index] = true
		hiddenToKept[exchange.Seq] = exchanges[candidates[offset]].Seq
	}

	// 规则 A2：一侧内容整体包含另一侧 —— 实时路径写的是流式快照（写短了），导入器拿到的是
	// 最终全文（写全了），或反之。来源不同 + 时间相近时保留**更长**的那条：它包含被隐藏行的
	// 全部内容，不丢信息。窗口是必要的：同一句话在几十小时后的另一次出现也可能是前缀关系
	// （实测 1787758959 里 17-20 与 166 相隔 32 小时），那不是重复。
	// 按「角色 + 归一化内容前 64 字符」分桶，避免 O(n²)（会话可达 3000+ 行）。
	const prefixWindow = 600 * time.Second
	type prefixKey struct {
		role string
		head string
	}
	buckets := map[prefixKey][]int{}
	for index, exchange := range exchanges {
		if hidden[index] || exchange.Timestamp.IsZero() {
			continue
		}
		content := normalizeExchangeContent(exchange.Content)
		if len(content) < 40 {
			continue
		}
		head := content
		if len(head) > 64 {
			head = head[:64]
		}
		key := prefixKey{role: strings.ToLower(strings.TrimSpace(exchange.Role)), head: head}
		buckets[key] = append(buckets[key], index)
	}
	for _, bucket := range buckets {
		for i := 0; i < len(bucket); i++ {
			for j := i + 1; j < len(bucket); j++ {
				shorter, longer := bucket[i], bucket[j]
				shortContent := normalizeExchangeContent(exchanges[shorter].Content)
				longContent := normalizeExchangeContent(exchanges[longer].Content)
				if len(shortContent) > len(longContent) {
					shorter, longer = longer, shorter
					shortContent, longContent = longContent, shortContent
				}
				if hidden[shorter] || hidden[longer] || shortContent == longContent {
					continue
				}
				if !strings.HasPrefix(longContent, shortContent) {
					continue
				}
				if isLiveMarkedExchange(exchanges[shorter]) == isLiveMarkedExchange(exchanges[longer]) {
					continue
				}
				gap := exchanges[shorter].Timestamp.Sub(exchanges[longer].Timestamp)
				if gap < 0 {
					gap = -gap
				}
				if gap > prefixWindow {
					continue
				}
				hidden[shorter] = true
				hiddenToKept[exchanges[shorter].Seq] = exchanges[longer].Seq
			}
		}
	}

	// 规则 B：同一时刻写下的同一行（老数据唯一的铁证）。
	type stampedKey struct {
		role    string
		content string
		nanos   int64
	}
	firstByStamp := map[stampedKey]int{}
	for index, exchange := range exchanges {
		if hidden[index] || exchange.Timestamp.IsZero() {
			continue
		}
		content := normalizeExchangeContent(exchange.Content)
		if content == "" {
			// 空内容行是占位（如「只调工具、没有正文」的助手轮），不能按内容折叠。
			continue
		}
		key := stampedKey{role: strings.ToLower(strings.TrimSpace(exchange.Role)), content: content, nanos: exchange.Timestamp.UnixNano()}
		if first, ok := firstByStamp[key]; ok {
			hidden[index] = true
			hiddenToKept[exchange.Seq] = exchanges[first].Seq
			provenToKept[exchange.Seq] = exchanges[first].Seq
			continue
		}
		firstByStamp[key] = index
	}

	kept := make([]session.Exchange, 0, len(exchanges))
	for index, exchange := range exchanges {
		if hidden[index] {
			continue
		}
		kept = append(kept, exchange)
	}
	if len(kept) == len(exchanges) {
		return exchanges, hiddenToKept, provenToKept
	}
	// 收尾：把隐藏映射解成「一跳到底」。规则之间会串成链——实测 BP 会话里 A 把 32→30、
	// A2 又把 30→33，调用方拿着 32→30 去重挂 aux 会落空（30 自己也被隐藏了）。
	resolved := make(map[int]int, len(hiddenToKept))
	for hiddenSeq, keptSeq := range hiddenToKept {
		final := keptSeq
		for depth := 0; depth < 16; depth++ {
			next, ok := hiddenToKept[final]
			if !ok || next == final {
				break
			}
			final = next
		}
		resolved[hiddenSeq] = final
	}
	return kept, resolved, provenToKept
}

// projectKey 是「同一句话」的归一化键。
type projectKey struct {
	role    string
	content string
}

// projectRowKey 归一化一行；空内容（只调工具、没有正文的占位轮）不参与折叠。
func projectRowKey(exchange session.Exchange) (projectKey, bool) {
	content := normalizeExchangeContent(exchange.Content)
	if content == "" {
		return projectKey{}, false
	}
	return projectKey{role: strings.ToLower(strings.TrimSpace(exchange.Role)), content: content}, true
}

// isLiveMarkedExchange 报告这一行是否算「实时路径写的」：
// 显式标了 source=live 的按标记算，标了 source=import 的不算；**老数据没有 source 标注**，
// 用实时路径独有的字段兜底（见 session.ExchangeHasLiveSignature）——没有这一条，2026-09
// 之前的历史会话里的双写重复就永远折叠不掉（实测本会话 6.4s 那对、1789390688 的 15.6s 那对）。
func isLiveMarkedExchange(exchange session.Exchange) bool {
	switch exchange.Source {
	case session.ExchangeSourceLive:
		return true
	case session.ExchangeSourceImport:
		return false
	}
	return session.ExchangeHasLiveSignature(exchange)
}

// RemapExchangeAux 把被隐藏行的 aux 重挂到保留行上（同 seq 的 aux 直接追加）。
// hiddenToKept 来自 ProjectExchanges；keptSeqs 是保留行的 seq 集合。
func RemapExchangeAux(aux map[int][]session.ExchangeAux, hiddenToKept map[int]int, keptSeqs map[int]bool) map[int][]session.ExchangeAux {
	if len(hiddenToKept) == 0 || len(aux) == 0 {
		return aux
	}
	out := make(map[int][]session.ExchangeAux, len(aux))
	for seq, items := range aux {
		if keptSeqs[seq] {
			out[seq] = append(out[seq], items...)
			continue
		}
		if kept, ok := hiddenToKept[seq]; ok && keptSeqs[kept] {
			out[kept] = append(out[kept], items...)
			continue
		}
		out[seq] = append(out[seq], items...)
	}
	return out
}

type AuditSessionInput struct {
	RootID string
	Key    string
}

// AuditDuplicate 是一条"同一轮被写了两份"的记录。
type AuditDuplicate struct {
	HiddenSeq int    `json:"hidden_seq"`
	KeptSeq   int    `json:"kept_seq"`
	Role      string `json:"role"`
	Preview   string `json:"preview"`
}

type AuditSessionOutput struct {
	Audit *session.SessionAudit `json:"audit"`
	// LiveOwned：这个会话是否由 MindFS 自己驱动（决定导入器该不该跟进）
	LiveOwned bool `json:"live_owned"`
	// Proven：同角色 + 归一化内容相同 + 时间戳同一时刻 —— 同一行被写两次，是唯一能直接删的
	Proven []AuditDuplicate `json:"proven_duplicates,omitempty"`
	// Review：一侧实时、一侧导入，内容（剥掉 CLI 噪声前缀后）相同 —— 投影已隐藏，数据保留
	Review []AuditDuplicate `json:"review_duplicates,omitempty"`
}

// AuditSession 只读体检：文件层（seq/坏行/aux 悬空）+ 投影层的重复分类。
// 不修任何东西；要清理得先看过这份报告再人工决定。
func (s *Service) AuditSession(ctx context.Context, in AuditSessionInput) (AuditSessionOutput, error) {
	var out AuditSessionOutput
	if err := s.ensureRegistry(); err != nil {
		return out, err
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return out, err
	}
	raw, err := manager.AuditSession(in.Key)
	if err != nil {
		return out, err
	}
	out.Audit = &raw
	current, err := manager.Get(ctx, in.Key, 0)
	if err != nil {
		return out, err
	}
	out.LiveOwned = session.SessionIsLiveOwned(current.Exchanges)
	_, hidden, proven := ProjectExchanges(current.Exchanges)
	contentBySeq := make(map[int]session.Exchange, len(current.Exchanges))
	for _, exchange := range current.Exchanges {
		contentBySeq[exchange.Seq] = exchange
	}
	for hiddenSeq, keptSeq := range hidden {
		entry := contentBySeq[hiddenSeq]
		item := AuditDuplicate{
			HiddenSeq: hiddenSeq,
			KeptSeq:   keptSeq,
			Role:      entry.Role,
			Preview:   auditPreview(entry.Content),
		}
		if _, ok := proven[hiddenSeq]; ok {
			out.Proven = append(out.Proven, item)
			continue
		}
		out.Review = append(out.Review, item)
	}
	sort.Slice(out.Proven, func(i, j int) bool { return out.Proven[i].HiddenSeq < out.Proven[j].HiddenSeq })
	sort.Slice(out.Review, func(i, j int) bool { return out.Review[i].HiddenSeq < out.Review[j].HiddenSeq })
	return out, nil
}

func auditPreview(content string) string {
	flat := strings.Join(strings.Fields(content), " ")
	runes := []rune(flat)
	if len(runes) > 60 {
		return string(runes[:60]) + "…"
	}
	return flat
}
