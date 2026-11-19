package usecase

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"mindfs/server/internal/session"
)

type ClearSessionWorktreeOwnershipInput struct {
	RootID string
	Key    string
	// AgentSessionID 与 RepointSession 同口径：脚本手里只有 claude 的 id。
	// Key 与 AgentSessionID 二选一，Key 优先。
	AgentSessionID string
	// Force 允许在 worktree 目录**仍然存在**时也清掉归属。默认拒绝：那种情况下
	// 清掉就是「把还在用的会话从它的工作树里踢出去」，只有人明确要求才做。
	Force bool
}

type ClearSessionWorktreeOwnershipOutput struct {
	SessionKey string `json:"session_key"`
	// RootID/Path 是清掉的那份归属（回显给人核对，清错了也能看出是哪条）。
	RootID  string `json:"worktree_root_id,omitempty"`
	Path    string `json:"worktree_path,omitempty"`
	Branch  string `json:"worktree_branch,omitempty"`
	Cleared bool   `json:"cleared"`
	// WorktreeGone 是判定依据：归属目录当时是否已不存在。
	WorktreeGone bool `json:"worktree_gone"`
	// WorkDir 是清完之后生效的运行时目录（空 = 主 checkout）。
	WorkDir string `json:"work_dir,omitempty"`
	// RuntimeDir 是清完之后会话真正会用的目录（主 checkout 时是项目根）。
	RuntimeDir string `json:"runtime_dir,omitempty"`
	// RelatedFilesRemoved 是被一并清掉的「相关文件」条目数。
	RelatedFilesRemoved int `json:"related_files_removed"`
	// Session 是清完之后的那条会话，只供 handler 拼广播载荷（与 ArchiveSession 同形）。
	Session *session.Session `json:"-"`
}

// ClearSessionWorktreeOwnership 只做一件事：把 related_worktree 摘掉。
//
// 为什么单独需要它：RepointSession 的语义是「fork 新转录 + 换 id + 搬文件 + 清归属」，
// 而在**归属指向的目录已经没了、但运行时 cwd 早已不在那儿**的会话上，那套动作全是
// 副作用（新 id、新转录），用户看到的目录一个字都不变 —— 所以它在
// 「会话已经在这个目录里」那一步直接 400 返回，且**发生在任何副作用之前**。
// 于是那批会话卡在一个两头不通的位置：repoint 拒绝做，清归属又没有入口，
// 界面上只能一直挂着「目录不存在」徽标。这个用例就是补那个入口。
//
// 它刻意**不碰**转录、binding、游标：那些是 repoint 的活，而这类会话的转录本来
// 就还在主 slug 目录里（或压根不存在），搬它只会制造一份多余副本。
//
// 与 RepointSession 末尾「刻意不动任务侧归属」同一条边界：这里只清**会话**这一侧。
func (s *Service) ClearSessionWorktreeOwnership(ctx context.Context, in ClearSessionWorktreeOwnershipInput) (ClearSessionWorktreeOwnershipOutput, error) {
	var out ClearSessionWorktreeOwnershipOutput
	if err := s.ensureRegistry(); err != nil {
		return out, err
	}
	key := strings.TrimSpace(in.Key)
	agentSessionID := strings.TrimSpace(in.AgentSessionID)
	if key == "" && agentSessionID == "" {
		return out, errors.New("session key or agent session id required")
	}
	root, err := s.Registry.GetRoot(in.RootID)
	if err != nil {
		return out, err
	}
	rootAbs, _ := root.RootDir()
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return out, err
	}
	if key == "" {
		// 与 repoint 同一条反查：脚本手里只有 CLAUDE_CODE_SESSION_ID。
		binding, err := manager.FindAgentBindingByAgentSession(ctx, "claude", agentSessionID)
		if err != nil {
			return out, err
		}
		if binding == nil {
			return out, fmt.Errorf("no mindfs session bound to agent session id %q", agentSessionID)
		}
		key = binding.SessionKey
	}
	out.SessionKey = key

	current, err := manager.Get(ctx, key, 0)
	if err != nil {
		return out, err
	}
	wt := current.RelatedWorktree
	if wt == nil || strings.TrimSpace(wt.Path) == "" {
		// 幂等：本来就没有归属。回显当前运行时目录，让调用方知道「无需再动」。
		out.RuntimeDir = sessionRuntimeRootPathFor(current)
		if out.RuntimeDir == "" {
			out.RuntimeDir = rootAbs
		}
		out.WorkDir = strings.TrimSpace(current.WorkDir)
		out.Session = current
		return out, nil
	}
	out.RootID = wt.RootID
	out.Path = wt.Path
	out.Branch = wt.Branch

	// 目录还在就默认拒绝：那是一个**活着的**工作树，清归属等于把会话从它里面踢出去。
	// 它不是这个端点要治的病（那是 repoint / 收尾流程的活）。
	if info, statErr := os.Stat(wt.Path); statErr == nil && info.IsDir() && !in.Force {
		return out, fmt.Errorf("worktree 目录仍然存在：%s（确需解除归属请带 force）", filepath.Clean(wt.Path))
	}
	out.WorktreeGone = wt.WorktreeMissing()

	if pool := s.Registry.GetAgentPool(); pool != nil {
		// 与 repoint 同一句判据：正在回复的会话不换目录（这里是摘目录，同样是中途改道）。
		// agent 名按会话自己的绑定解析 —— 既是 claude 也可能是 codex/dsh。
		agentName := session.InferAgentFromSession(current)
		if pool.HasActiveUse(agentPoolSessionKey(key, agentName)) {
			return out, errors.New("会话正在回复，等这一轮结束再解除工作树归属")
		}
		pool.Close(agentPoolSessionKey(key, agentName))
	}

	cleared, err := manager.ClearRelatedWorktree(ctx, key)
	if err != nil {
		return out, err
	}
	out.Cleared = cleared

	// 旧工作树里的 related 条目清掉 —— 理由同 repoint：那批路径已经不在会话干活的
	// 地方了，留着会让「相关文件」面板指着一堆不存在的路径。
	if removed, removeErr := manager.RemoveRelatedFilesUnder(ctx, key, wt.Path); removeErr != nil {
		log.Printf("[session/worktree] clear.related.error root=%s session=%s dir=%s err=%v",
			strings.TrimSpace(in.RootID), key, wt.Path, removeErr)
	} else {
		out.RelatedFilesRemoved = removed
	}

	// 再把工作目录钉正：work_dir 若恰好就是那个已删目录，摘掉归属后运行时 cwd 仍会
	// 指回它（work_dir 优先于 related_worktree）。这时清成空串 = 回主 checkout。
	if workDir := strings.TrimSpace(current.WorkDir); workDir != "" {
		if _, statErr := os.Stat(workDir); statErr != nil {
			if _, err := manager.SetWorkDir(ctx, key, ""); err != nil {
				log.Printf("[session/worktree] set.work_dir.error root=%s session=%s dir=%s err=%v",
					strings.TrimSpace(in.RootID), key, workDir, err)
			}
		}
	}
	updated, err := manager.Get(ctx, key, 0)
	if err == nil && updated != nil {
		out.Session = updated
		out.WorkDir = strings.TrimSpace(updated.WorkDir)
		out.RuntimeDir = sessionRuntimeRootPathFor(updated)
	}
	if out.RuntimeDir == "" {
		out.RuntimeDir = rootAbs
	}

	log.Printf("[session/worktree] clear.done root=%s session=%s path=%s branch=%s gone=%t cleared=%t related_removed=%d runtime=%s",
		strings.TrimSpace(in.RootID), key, wt.Path, wt.Branch, out.WorktreeGone, cleared, out.RelatedFilesRemoved, out.RuntimeDir)
	return out, nil
}
