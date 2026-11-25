package usecase

import (
	"errors"
	"fmt"

	"mindfs/server/internal/agent/claude"
	"mindfs/server/internal/session"
)

// Repoint 的 harness 差异只有「转录要不要跟着 cwd 搬」这一件事。
//
// claude 的转录放在 ~/.claude/projects/<slug(cwd)> 下，换 cwd 必须 fork 到新 slug；
// codex 的 rollout 全局放在 <CODEX_HOME>/sessions 下，cwd 是每轮 resume 的入参，
// 所以只需清归属、把 WorkDir 指回主 checkout —— 连 agent_session_id 都不用换。
// 其余 harness 明确报不支持，不猜一个「大概能行」的路径去改 binding。
type repointAdapter interface {
	supported() bool
	preflight(agentSessionID string) error
	move(rootAbs string, binding *session.AgentBinding) (repointMove, error)
}

type repointMove struct {
	previousID                string
	agentID                   string
	transcriptPath            string
	transcriptBytes           int64
	transcriptModTimeUnixNano int64
	// bindingChanged 为真才需要换 binding 并推进游标（claude fork 出新 id）。
	bindingChanged bool
	// clearNativeIdentity 为真才清 native id（codex 的 native id 就是 thread id，不能清）。
	clearNativeIdentity bool
}

type claudeRepointAdapter struct{}

func (claudeRepointAdapter) supported() bool { return true }
func (claudeRepointAdapter) preflight(agentSessionID string) error {
	if !claude.HasTranscript(agentSessionID) {
		return fmt.Errorf("找不到会话 %s 的转录（可能在 ~/.claude/projects 下被移动或删除），先确认再收尾",
			agentSessionID)
	}
	return nil
}
func (claudeRepointAdapter) move(rootAbs string, binding *session.AgentBinding) (repointMove, error) {
	moved, err := claude.RepointTranscript(rootAbs, binding.AgentSessionID)
	if err != nil {
		return repointMove{}, err
	}
	return repointMove{
		previousID:                moved.PreviousAgentSessionID,
		agentID:                   moved.AgentSessionID,
		transcriptPath:            moved.TranscriptPath,
		transcriptBytes:           moved.TranscriptBytes,
		transcriptModTimeUnixNano: moved.TranscriptModTimeUnixNano,
		bindingChanged:            true,
		clearNativeIdentity:       true,
	}, nil
}

type codexRepointAdapter struct{}

func (codexRepointAdapter) supported() bool        { return true }
func (codexRepointAdapter) preflight(string) error { return nil }
func (codexRepointAdapter) move(_ string, b *session.AgentBinding) (repointMove, error) {
	if b == nil || b.AgentSessionID == "" {
		return repointMove{}, errors.New("codex repoint requires an agent session id")
	}
	return repointMove{
		previousID: b.AgentSessionID,
		agentID:    b.AgentSessionID,
	}, nil
}

func repointAdapterFor(agent string) repointAdapter {
	switch agent {
	case "claude":
		return claudeRepointAdapter{}
	case "codex":
		return codexRepointAdapter{}
	default:
		return nil
	}
}

// RepointSupported 供 UI / 巡检查询：只有登记过的 harness 才给入口。
func RepointSupported(agent string) bool {
	a := repointAdapterFor(agent)
	return a != nil && a.supported()
}
