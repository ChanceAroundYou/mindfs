package usecase

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"mindfs/server/internal/agent/claude"
	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
)

type RepointSessionInput struct {
	RootID string
	Key    string
	// AgentSessionID 用于按 claude 的 session id 反查 mindfs 会话。给脚本用：
	// wt-finish 手里只有 CLAUDE_CODE_SESSION_ID 和 worktree 路径，没有 mindfs 的 key。
	// Key 与 AgentSessionID 二选一，Key 优先。
	AgentSessionID string
	// TargetPath 是搬过去之后会话干活的那个目录（绝对路径）。留空 = 本项目主 checkout，
	// 也就是改造前唯一的那个目标。给了就搬过去并把会话的工作目录钉在那儿（WorkDir）。
	TargetPath string
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
// 写入侧判重折叠不掉 —— 正是 2026-09-16「ask 下面又渲染了一轮出现过的文字」。
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
	// 显式给出的 agent_session_id 指定了要搬的 binding。一个 MindFS 会话可以先后
	// 使用多个 agent；InferAgentFromSession 返回最近写入 exchange 的 agent，不一定是
	// 这份 Claude 转录对应的 agent。不要因此误选另一个 binding。
	selectedAgent := ""
	if agentSessionID != "" {
		selectedAgent = "claude"
		binding, err := manager.FindAgentBindingByAgentSession(ctx, selectedAgent, agentSessionID)
		if err != nil {
			return out, err
		}
		if binding == nil {
			return out, fmt.Errorf("no mindfs session bound to agent session id %q", agentSessionID)
		}
		if key != "" && key != binding.SessionKey {
			return out, fmt.Errorf("agent session id %q is bound to session %q, not %q", agentSessionID, binding.SessionKey, key)
		}
		key = binding.SessionKey
	}
	current, err := manager.Get(ctx, key, 0)
	if err != nil {
		return out, err
	}
	agentName := selectedAgent
	if agentName == "" {
		agentName = session.InferAgentFromSession(current)
	}
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

	// 正在回复的会话不能换目录：下面第一件事就是 pool.Close 掐掉 agent 进程，跑着的
	// 回合会被腰斩在半句上、转录停在中间 —— 搬过去的就是一份截断的历史。这是纯内存
	// 判断，放在所有文件校验之前：它比「目标目录填错了」更根本。
	// 前端那一项此时是灰的，这里兜住直接打 API 的调用方（脚本、并发标签页）。
	if pool := s.Registry.GetAgentPool(); pool != nil && pool.HasActiveUse(agentPoolSessionKey(key, agentName)) {
		return out, errors.New("会话正在回复，等这一轮结束再换目录")
	}

	// 目标目录：显式给了就用它（校验过再动手），没给就是主 checkout —— 与改造前同义。
	oldDir := sessionRuntimeRootPathFor(current)
	currentDir := oldDir
	if currentDir == "" {
		currentDir = rootAbs
	}
	targetAbs := rootAbs
	if strings.TrimSpace(in.TargetPath) != "" {
		targetAbs, err = validateRepointTarget(in.TargetPath)
		if err != nil {
			return out, err
		}
	}
	if targetAbs == "" {
		return out, errors.New("project root path unavailable")
	}
	// 已经在目标目录里就什么都不做。继续往下走会 fork 出一份新转录、换掉 agent session id、
	// 清掉工作树归属 —— 全是副作用，而用户看到的目录一个字都没变。宁可直说。
	if filepath.Clean(targetAbs) == filepath.Clean(currentDir) {
		return out, fmt.Errorf("会话已经在这个目录里：%s", filepath.Clean(currentDir))
	}

	// 掐断活进程之前先确认转录还在：RepointTranscript 最主要的失败原因就是「找不到
	// 转录」，而它排在 pool.Close 之后 —— 不先验就掐，用户拿到的是「会话没了、状态
	// 却一点没改」，唯一那种不可逆又什么都没换来的失败。校验放在不可逆动作之前。
	if !claude.HasTranscript(binding.AgentSessionID) {
		return out, fmt.Errorf("找不到会话 %s 的转录（可能在 ~/.claude/projects 下被移动或删除），先确认再收尾",
			binding.AgentSessionID)
	}

	// 1. 掐断活进程。此后 mindfs 不再往旧转录写。
	if pool := s.Registry.GetAgentPool(); pool != nil {
		pool.Close(agentPoolSessionKey(key, agentName))
	}

	// 2+3. fork 出新 id 并把转录搬进目标目录的 slug 目录。
	moved, err := claude.RepointTranscript(targetAbs, binding.AgentSessionID)
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

	// 5. 解除 worktree 归属：此后 sessionRuntimeRootPath 不再因工作树而指向旧目录。
	cleared, err := manager.ClearRelatedWorktree(ctx, key)
	if err != nil {
		return out, err
	}

	// 5b. 旧工作树里的 related 条目清掉：那些路径已经不在会话干活的地方了（目录多半已被删），
	// 留着会让「相关文件」面板指着一堆不存在的路径。只在旧目录确实是工作树时才清 ——
	// 从主 checkout 搬走时那批文件还在原地，是有效条目。
	if oldDir != "" {
		if removed, err := manager.RemoveRelatedFilesUnder(ctx, key, oldDir); err != nil {
			log.Printf("[session/repoint] clear.related.error root=%s session=%s dir=%s err=%v", strings.TrimSpace(in.RootID), key, oldDir, err)
		} else if removed > 0 {
			log.Printf("[session/repoint] clear.related root=%s session=%s dir=%s removed=%d", strings.TrimSpace(in.RootID), key, oldDir, removed)
		}
	}

	// 5c. 把会话的工作目录钉在目标上。目标是主 checkout 时写空串 —— 空就是「回主 checkout」，
	// 别在库里留一条与项目目录同义的冗余路径（项目改名/搬盘时它会变成假的）。
	workDir := targetAbs
	if filepath.Clean(targetAbs) == filepath.Clean(rootAbs) {
		workDir = ""
	}
	if _, err := manager.SetWorkDir(ctx, key, workDir); err != nil {
		log.Printf("[session/repoint] set.work_dir.error root=%s session=%s dir=%s err=%v", strings.TrimSpace(in.RootID), key, workDir, err)
	}

	// 6. 清掉全部 native id（方案 §14 C1）。
	//
	// repoint 的本质是「fork 出新 id 并换一份转录」，而 claude 在 fork 后产出**全新 uuid**
	//（repoint.go:35 的既有注释就写着这条）。本方案开始持久化 native id 之后，旧 native id
	// 与 fork 后的新转录对不上 —— 判重失效、回查错行。所以换 id 的同时必须一并清掉。
	//
	// 顺序放在最后：前几步任一失败都会 return，此时 native id 原样保留，仍与旧转录一致
	//（旧转录刻意不删，就是回滚保险）。只有整条 repoint 真的走完了才作废它们。
	// 清不掉不算 repoint 失败：id 与转录都已换好、会话可用；残留的 native id 只会让回查
	// 退回「按 MindFS mindfs_id 找」，而那是权威路径。
	if err := manager.ClearNativeIdentity(ctx, key); err != nil {
		log.Printf("[session/repoint] clear.native.error root=%s session=%s agent=%s err=%v", strings.TrimSpace(in.RootID), key, agentName, err)
	}

	// 刻意**不**动任务侧的 worktree 归属。
	//
	// 曾经在这里调 ClearTaskWorktree，理由是「会话侧清了、任务侧没清的话，任务还钉着一个
	// 已删目录」。实测那是把两件事焊死了：repoint 的语义是「这个**会话**搬回主 checkout」，
	// 而 ClearTaskWorktree 的语义是「这个**任务**的活收工了、worktree 该拆」。
	// 2026-09-29 实测的代价：一次纯搬会话的 repoint 把任务 #23 的 worktree_path 清成了空，
	// 而那个任务的目录其实还在（手工建、还在用）。于是卡片命中「路径为空 + missing=false」
	// 这组形状，被渲染成「已收尾」—— 一个从未收过尾的任务声称收工了。
	//
	// 任务侧该清的时候由收尾流程清：FinishTaskWorktree 第 4 步本来就会 ClearWorktreeRefs
	//（worktree_finish.go），语义正确，也不需要谁来代劳。

	log.Printf("[session/repoint] done root=%s session=%s agent=%s old=%s new=%s transcript=%s bytes=%d from=%s to=%s",
		strings.TrimSpace(in.RootID), key, agentName, binding.AgentSessionID, moved.AgentSessionID,
		moved.TranscriptPath, moved.TranscriptBytes, currentDir, targetAbs)

	return RepointSessionOutput{
		SessionKey:             key,
		Agent:                  agentName,
		PreviousAgentSessionID: moved.PreviousAgentSessionID,
		AgentSessionID:         moved.AgentSessionID,
		TranscriptPath:         moved.TranscriptPath,
		WorktreeCleared:        cleared,
	}, nil
}

// validateRepointTarget 校验用户手填的目标目录，返回 Clean 过的绝对路径。
//
// 只做「能不能当工作目录用」这一件事：绝对路径、存在、是目录。刻意不校验「是不是 git 仓库」
// 或「是不是本项目下的目录」—— 换目录的用处之一正是把会话搬到项目之外（比如另一个 checkout），
// 而 claude 的转录 slug 只跟 cwd 字符串有关，跟仓库与否无关。
func validateRepointTarget(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("目标目录不能为空")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("目标目录必须是绝对路径：%s", path)
	}
	clean := filepath.Clean(path)
	info, err := os.Stat(clean)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("目标目录不存在：%s", clean)
		}
		return "", fmt.Errorf("目标目录不可用：%s（%v）", clean, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("目标不是目录：%s", clean)
	}
	return clean, nil
}

// sessionRuntimeRootPathFor 与 api 包里的 sessionRuntimeRootPath 同口径：空 = 主 checkout。
// 这里复制而不是跨包引用，是因为 ws.go 那个在 api 包、且带 websocket 相关上下文。
func sessionRuntimeRootPathFor(current *session.Session) string {
	if current == nil {
		return ""
	}
	if workDir := strings.TrimSpace(current.WorkDir); workDir != "" {
		return workDir
	}
	if current.RelatedWorktree == nil {
		return ""
	}
	if strings.TrimSpace(current.TaskID) == "" && strings.TrimSpace(current.Source) != "worktree" {
		return ""
	}
	return strings.TrimSpace(current.RelatedWorktree.Path)
}
