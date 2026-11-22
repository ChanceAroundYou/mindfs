package usecase

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"mindfs/server/internal/session"
)

// recoverProvisionalTurnBlocks 处理「流式文字已经落盘、但回合没有收尾就被 kill」的最后一轮。
// 它不读 agent 转录，也不需要最终回复：把 unsealed 块封口成可见助手行，并把未完成
// 工具调用标成 interrupted。没有 provisional 内容时返回 false，原 importer 恢复照旧。
func (s *Service) recoverProvisionalTurnBlocks(
	ctx context.Context,
	manager *session.Manager,
	current *session.Session,
	agentName, agentSessionID string,
	lastUser *session.Exchange,
) (bool, error) {
	if manager == nil || current == nil || lastUser == nil {
		return false, nil
	}
	turnID := strings.TrimSpace(lastUser.MindFSID)
	if turnID == "" {
		return false, nil
	}
	seq := lastUser.Seq + 1
	provisional, err := manager.ReadProvisionalTurnBlocks(ctx, current.Key, seq, turnID)
	if err != nil || len(provisional) == 0 {
		return false, err
	}
	for _, exchange := range current.Exchanges {
		if exchange.Role == "agent" && exchange.MindFSID == turnID {
			// 助手行已存在（上一次恢复写了行、还没封口就再次崩溃）：只补封口。
			if err := manager.SealProvisionalTurnBlocks(ctx, current.Key, seq, turnID); err != nil {
				return true, err
			}
			return true, nil
		}
	}

	identity := session.ExchangeIdentity{MindFSID: turnID, TurnSeq: lastUser.Seq, Partial: true}
	agentSeq, err := manager.AddExchangeForAgentAtWithIdentityFactory(
		ctx, current, "agent", "", agentName, "", "", "",
		time.Time{}, func(int) session.ExchangeIdentity { return identity },
	)
	if err != nil {
		return true, err
	}
	if agentSeq != seq {
		return true, fmt.Errorf("provisional seq mismatch: block=%d exchange=%d", seq, agentSeq)
	}
	if err := manager.SealProvisionalTurnBlocks(ctx, current.Key, seq, turnID); err != nil {
		return true, err
	}
	latest, err := manager.Get(ctx, current.Key, 0)
	if err != nil {
		return true, err
	}
	if err := manager.UpdateAgentState(ctx, latest, agentName, len(latest.Exchanges), agentSessionID); err != nil {
		return true, err
	}
	log.Printf("[session/recovery] provisional_recover session=%s agent=%s user_seq=%d blocks=%d",
		current.Key, agentName, lastUser.Seq, len(provisional))
	return true, nil
}
