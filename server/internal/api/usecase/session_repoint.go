package usecase

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"mindfs/server/internal/agent/claude"
	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/session"
)

type RepointSessionInput struct {
	RootID string
	Key    string
	// AgentSessionID 用于按 claude 的 session id 反查 mindfs 会话。给脚本用：
	// wt-finish 手里只有 CLAUDE_CODE_SESSION_ID 和 worktree 路径，没有 mindfs 的 key。
	// Key 与 AgentSessionID 二选一，Key 优先。
	AgentSessionID string
}

type RepointSessionOutput struct {
	SessionKey             string `json:"session_key"`
	Agent                  string `json:"agent"`
	PreviousAgentSessionID string `json:"previous_agent_session_id"`
	AgentSessionID         string `json:"agent_session_id"`
	TranscriptPath         string `json:"transcript_path"`
	WorktreeCleared        bool   `json:"worktree_cleared"`
}

// RepointSession 把一个绑定了工作树的会话彻底搬回主 checkout：转录复制并换 id、
// 落到主 slug 目录、解除 worktree 归属、游标推进到新转录末尾。
//
// 顺序是有讲究的，不能换：
//   - 先 pool.Close 掐断活进程，之后才动文件——否则 claude 进程可能正按旧路径 append。
//   - 先搬文件，再写游标——游标要记的是新路径的 EOF，中途写会记到旧路径上。
//
// 游标那一步是全流程唯一容易踩坑的地方：必须推进到**末尾**而不是清零。live-owned
// 会话的 exchange 表由实时路径独占持久化、已经是完整的；一旦游标是 0，下次同步会从
// 0 整份重读转录，重导出来的行与实时写的行并不逐字相同（导入器会合并相邻同角色条目），
// 写入侧判重和读取投影都折叠不掉 —— 正是 2026-09-16「ask 下面又渲染了一轮出现过的文字」。
// 见 usecase/external_sessions.go 与 api/http.go 里 worktreeCandidateRoots 那段注释。
//
// 旧 id 的转录文件**不删**：那是回滚保险。repoint 出问题时，把 binding 改回去即可。
func (s *Service) RepointSession(ctx context.Context, in RepointSessionInput) (RepointSessionOutput, error) {
	var out RepointSessionOutput
	if err := s.ensureRegistry(); err != nil {
		return out, err
	}
	key := strings.TrimSpace(in.Key)
	agentSessionID := strings.TrimSpace(in.AgentSessionID)
	if key == "" && agentSessionID == "" {
		return out, errors.New("session key or agent session id required")
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return out, err
	}
	if key == "" {
		// 脚本只知道 claude 的 session id，反查 mindfs 会话。
		binding, err := manager.FindAgentBindingByAgentSession(ctx, "claude", agentSessionID)
		if err != nil {
			return out, err
		}
		if binding == nil {
			return out, fmt.Errorf("no mindfs session bound to agent session id %q", agentSessionID)
		}
		key = binding.SessionKey
	}
	current, err := manager.Get(ctx, key, 0)
	if err != nil {
		return out, err
	}
	agentName := session.InferAgentFromSession(current)
	if agentName == "" {
		return out, errors.New("session has no agent binding")
	}
	if agentName != "claude" {
		return out, fmt.Errorf("repoint is only supported for claude sessions, got %q", agentName)
	}
	binding, err := manager.FindAgentBinding(ctx, key, agentName)
	if err != nil {
		return out, err
	}
	if binding == nil || strings.TrimSpace(binding.AgentSessionID) == "" {
		return out, errors.New("session has no agent session id")
	}
	root, err := s.Registry.GetRoot(in.RootID)
	if err != nil {
		return out, err
	}
	rootAbs, _ := root.RootDir()

	worktreePath := sessionRuntimeRootPathFor(current)
	if worktreePath == "" {
		return out, errors.New("session is not bound to a worktree")
	}

	// 1. 掐断活进程。此后 mindfs 不再往旧转录写。
	if pool := s.Registry.GetAgentPool(); pool != nil {
		pool.Close(agentPoolSessionKey(key, agentName))
	}

	// 2+3. fork 出新 id 并把转录搬进主 checkout 的 slug 目录。
	moved, err := claude.RepointTranscript(rootAbs, binding.AgentSessionID)
	if err != nil {
		return out, err
	}

	// 4+6. 换绑定 id 并把游标推进到新转录末尾——**一个事务**。分两次写时，若游标那步
	// 失败，binding 已是新 id 而游标还指着旧路径的冻结偏移；导入器按 SourcePath 判变化，
	// 路径一变就从 committed（live-owned 会话为 0）整份重读，正是 2026-09-16 那个
	// 「ask 下面又渲染了一轮出现过的文字」。合成事务就没有这个中间态。
	if err := manager.RepointAgentBinding(key, agentName, binding.AgentSessionID, moved.AgentSessionID, agenttypes.ExternalSessionCursor{
		SourcePath:      moved.TranscriptPath,
		Offset:          moved.TranscriptBytes,
		ModTimeUnixNano: moved.TranscriptModTimeUnixNano,
		CommittedOffset: moved.TranscriptBytes,
	}); err != nil {
		return out, err
	}

	// 5. 解除 worktree 归属：此后 sessionRuntimeRootPath 返回空，运行时 cwd 回到主 checkout。
	cleared, err := manager.ClearRelatedWorktree(ctx, key)
	if err != nil {
		return out, err
	}

	// 5b. 任务侧同步解绑。会话侧清了、任务侧没清的话，任务还钉着一个已删目录：
	// 前端 relatedWorktree 是任务优先（App.tsx），会拿它当事实用。
	//
	// 刻意不阻断 repoint：任务库出问题时，会话已经搬成功了（转录、绑定、游标都到位），
	// 为此让整个 repoint 报错反而会掩盖「已经搬成了」这个事实。失败只记日志。
	if taskID := strings.TrimSpace(current.TaskID); taskID != "" {
		clearTaskWorktree(ctx, s.Registry, in.RootID, taskID)
	}

	log.Printf("[session/repoint] done root=%s session=%s agent=%s old=%s new=%s transcript=%s bytes=%d worktree=%s",
		strings.TrimSpace(in.RootID), key, agentName, binding.AgentSessionID, moved.AgentSessionID,
		moved.TranscriptPath, moved.TranscriptBytes, worktreePath)

	return RepointSessionOutput{
		SessionKey:             key,
		Agent:                  agentName,
		PreviousAgentSessionID: moved.PreviousAgentSessionID,
		AgentSessionID:         moved.AgentSessionID,
		TranscriptPath:         moved.TranscriptPath,
		WorktreeCleared:        cleared,
	}, nil
}

// sessionRuntimeRootPathFor 与 api 包里的 sessionRuntimeRootPath 同口径：空 = 主 checkout。
// 这里复制而不是跨包引用，是因为 ws.go 那个在 api 包、且带 websocket 相关上下文。
func sessionRuntimeRootPathFor(current *session.Session) string {
	if current == nil || current.RelatedWorktree == nil {
		return ""
	}
	if strings.TrimSpace(current.TaskID) == "" && strings.TrimSpace(current.Source) != "worktree" {
		return ""
	}
	return strings.TrimSpace(current.RelatedWorktree.Path)
}

// kanbanWorktreeClearer 是 AppContext 已实现、但没进 usecase.Registry 接口的那一小块。
// 用可选接口断言拿而不是给 Registry 加方法：Registry 是被 211 处 AppContext 消费
// 的窄接口，为一个收尾期的清理动作扩它不划算（而且 AppContext 之外还有测试 fake）。
type kanbanWorktreeClearer interface {
	GetKanbanService() (*kanban.Service, error)
}

// clearTaskWorktree 尽力清掉任务的 worktree 归属。失败只记日志，不影响 repoint 主流程。
func clearTaskWorktree(ctx context.Context, reg Registry, rootID, taskID string) {
	provider, ok := reg.(kanbanWorktreeClearer)
	if !ok {
		return
	}
	svc, err := provider.GetKanbanService()
	if err != nil {
		log.Printf("[session/repoint] task worktree clear skipped root=%s task=%s err=%v", rootID, taskID, err)
		return
	}
	if err := svc.ClearTaskWorktree(ctx, rootID, taskID); err != nil {
		log.Printf("[session/repoint] task worktree clear failed root=%s task=%s err=%v", rootID, taskID, err)
		return
	}
	log.Printf("[session/repoint] task worktree cleared root=%s task=%s", rootID, taskID)
}
