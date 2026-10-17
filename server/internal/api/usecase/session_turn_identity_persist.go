package usecase

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"
	"unicode"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
)


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
