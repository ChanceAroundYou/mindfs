package kanban

import (
	"context"
	"log"
	"strings"
	"time"
)

func (rs *RouterService) prepareSessionKey(ctx context.Context, agent RouterAgent, rootID, taskID string, config RouterConfig, promptHash string) string {
	record, err := rs.store.Session(ctx, RouterScopeTask, taskID)
	if err != nil || record == nil {
		return ""
	}
	if record.PromptHash != "" && record.PromptHash != promptHash {
		// 提示词规则变了：旧进程内的 system prompt 不会自动替换，先关 runtime；
		// 下次 open 会带新的 append-system-prompt（manager binding 仍可 resume）。
		if err := agent.CloseRouterSession(ctx, rootID, record.SessionKey); err != nil {
			log.Printf("[router] session.close_on_prompt_change.error task=%s err=%v", taskID, err)
		}
		return ""
	}
	if record.Status == RouterSessionClosed || record.Status == RouterSessionFailed {
		return ""
	}
	return record.SessionKey
}

func (rs *RouterService) recordSession(ctx context.Context, config RouterConfig, promptHash, taskID, rootID string, result RouterAgentResult, status, errText string) {
	sessionKey := strings.TrimSpace(result.SessionKey)
	if sessionKey == "" {
		return
	}
	if err := rs.store.UpsertSession(ctx, &RouterSessionRecord{
		Scope:      RouterScopeTask,
		OwnerID:    taskID,
		RootID:     rootID,
		SessionKey: sessionKey,
		Agent:      config.Agent,
		Model:      config.Model,
		Effort:     config.Effort,
		ConfigDir:  result.ConfigDir,
		Cwd:        result.Cwd,
		PromptHash: promptHash,
		Status:     status,
		LastError:  errText,
		LastTurnAt: time.Now().UTC(),
	}); err != nil {
		log.Printf("[router] session.upsert.error task=%s err=%v", taskID, err)
	}
}

func (rs *RouterService) closeSession(ctx context.Context, taskID, reason string) {
	if rs.store == nil {
		return
	}
	record, err := rs.store.Session(ctx, RouterScopeTask, taskID)
	if err != nil || record == nil {
		return
	}
	if agent := rs.agent(); agent != nil && record.SessionKey != "" {
		if err := agent.CloseRouterSession(ctx, record.RootID, record.SessionKey); err != nil {
			log.Printf("[router] session.close.error task=%s err=%v", taskID, err)
		}
	}
	if err := rs.store.UpdateSessionStatus(ctx, RouterScopeTask, taskID, RouterSessionClosed, reason); err != nil {
		log.Printf("[router] session.status.error task=%s err=%v", taskID, err)
	}
}

// sweepIdleSessions 关闭长时间没有回合的 Router runtime。只关进程内 runtime，
// 不删 manager 会话行：下次回合仍能 resume，转录也仍可读。
func (rs *RouterService) sweepIdleSessions(idleClose time.Duration) {
	interval := idleClose / 4
	if interval < 50*time.Millisecond {
		interval = 50 * time.Millisecond
	}
	if interval > time.Minute {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-rs.closed:
			return
		case <-ticker.C:
			rs.sweepIdleOnce(idleClose)
		}
	}
}

func (rs *RouterService) sweepIdleOnce(idleClose time.Duration) {
	if rs == nil || rs.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	records, err := rs.store.ListSessions(ctx, RouterScopeTask, rs.rootID)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, record := range records {
		if record == nil || record.Status != RouterSessionActive || record.LastTurnAt.IsZero() {
			continue
		}
		if now.Sub(record.LastTurnAt) < idleClose {
			continue
		}
		rs.closeSession(ctx, record.OwnerID, "idle timeout")
	}
}
