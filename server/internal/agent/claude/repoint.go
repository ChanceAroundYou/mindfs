package claude

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"
)

// RepointTranscriptResult 描述一次转录搬移的结果。
type RepointTranscriptResult struct {
	// PreviousAgentSessionID 是搬移前的 claude 会话 id（回滚保险，不删它的文件）。
	PreviousAgentSessionID string `json:"previous_agent_session_id"`
	// AgentSessionID 是搬移后的新 id。
	AgentSessionID string `json:"agent_session_id"`
	// TranscriptPath 是新转录在主 checkout slug 目录下的绝对路径。
	TranscriptPath string `json:"transcript_path"`
	// PreviousTranscriptDir 是旧 slug 目录（fork 的来源）。
	PreviousTranscriptDir string `json:"previous_transcript_dir"`
	// TranscriptBytes 是新转录的字节数——游标要推进到它，见 RepointSession 的说明。
	TranscriptBytes int64 `json:"transcript_bytes"`
	// TranscriptModTimeUnixNano 是新转录的 mtime。游标的第三个字段要和 Offset 同源，
	// 分两次 stat 会在文件被写时算出对不上的一对值，触发一次无谓的全量重读。
	TranscriptModTimeUnixNano int64 `json:"transcript_mod_time_unix_nano"`
}

// RepointTranscript 把一个跑在 worktree cwd 上的 claude 会话转录，完整搬进
// rootPath（主 checkout）对应的 slug 目录，并换上新的 agent_session_id。
//
// 为什么必须换 id：同一个 id 在两个 slug 目录下各有一份时，`claude -r <id>` 和 SDK
// 的 findSessionFile 都只认找到的那一份，mindfs 侧和用户侧会各写各的，历史就此分叉。
// ForkSession 产出的新 id 是干净的，而 mindfs 并不持久化 claude 的 uuid
// （导入器只把 uuid 当瞬时去重键），所以 uuid 重映射对 mindfs 不可见、无副作用。
//
// 为什么搬文件而不是靠 Dir 选项：SDK 的 projectKey 把非字母数字全替成 '-' 且不产生
// 前导 '-'，与 Claude Code 真实编码不符，ForkSessionOptions.Dir 算出来的目录是错的。
// 所以这里 fork 之后自己搬。
func RepointTranscript(rootPath, previousAgentSessionID string) (RepointTranscriptResult, error) {
	rootPath = normalizeComparablePath(rootPath)
	if rootPath == "" {
		return RepointTranscriptResult{}, errors.New("root path required")
	}
	previousAgentSessionID = strings.TrimSpace(previousAgentSessionID)
	if previousAgentSessionID == "" {
		return RepointTranscriptResult{}, errors.New("agent session id required")
	}

	forked, err := claudeagent.ForkSession(previousAgentSessionID, nil)
	if err != nil {
		return RepointTranscriptResult{}, fmt.Errorf("fork transcript: %w", err)
	}
	newID := ""
	if forked != nil {
		newID = strings.TrimSpace(forked.SessionID)
	}
	if newID == "" {
		return RepointTranscriptResult{}, errors.New("fork transcript did not return session id")
	}

	projectsDir, err := claudeProjectsDir()
	if err != nil {
		return RepointTranscriptResult{}, err
	}
	dstDir := filepath.Join(projectsDir, claudeProjectDirName(rootPath))
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return RepointTranscriptResult{}, err
	}
	// fork 就地落在**源转录所在目录**（SDK 的 target = dir(file.path)/<新id>.jsonl）。
	// 这里不猜它在哪，而是按 id 找——同一 id 可能有多份副本。
	//
	// 关键的一格：源**已经在 dstDir** 时（会话早先搬过一次，或 worktree 目录被手工
	// 清掉之后才来收尾），fork 直接落在 dstDir——没有「dstDir 之外的副本」可搬，
	// 原地就是终点。旧写法在这种输入下硬报 "not found outside %s"，而 RepointSession
	// 在调它之前已经 pool.Close 掐掉了 agent 进程，用户拿到的是「会话没了、状态却
	// 一点没改」——最坏的那种失败。2026-10-02 实测踩到。
	srcDir := ""
	sawInPlace := false
	for _, candidate := range transcriptDirsForSession(projectsDir, newID) {
		if filepath.Clean(candidate) == filepath.Clean(dstDir) {
			sawInPlace = true
			continue
		}
		if srcDir == "" {
			srcDir = candidate
		}
	}
	if srcDir == "" {
		if !sawInPlace {
			return RepointTranscriptResult{}, fmt.Errorf("forked transcript %s not found under %s", newID, projectsDir)
		}
		srcDir = dstDir
	}

	dstFile := filepath.Join(dstDir, newID+".jsonl")
	if filepath.Clean(srcDir) != filepath.Clean(dstDir) {
		srcFile := filepath.Join(srcDir, newID+".jsonl")
		// 目标已存在就停手：覆盖会毁掉一份可能正被读写的转录。
		if _, statErr := os.Stat(dstFile); statErr == nil {
			return RepointTranscriptResult{}, fmt.Errorf("transcript target already exists: %s", dstFile)
		} else if !os.IsNotExist(statErr) {
			return RepointTranscriptResult{}, statErr
		}
		if err := moveClaudeTranscriptEntry(srcFile, dstFile); err != nil {
			return RepointTranscriptResult{}, err
		}
	}
	// 附属目录（tool-results / subagents）按**旧 id** 命名，fork 不会复制它们（SDK 只重写
	// 并写出 .jsonl），所以这里搬旧 id 那份、落成新 id 的名字。缺失是正常的，不是错误。
	if err := moveClaudeTranscriptDir(
		filepath.Join(srcDir, previousAgentSessionID),
		filepath.Join(dstDir, newID),
	); err != nil {
		return RepointTranscriptResult{}, err
	}

	info, err := os.Stat(dstFile)
	if err != nil {
		return RepointTranscriptResult{}, err
	}
	return RepointTranscriptResult{
		PreviousAgentSessionID:    previousAgentSessionID,
		AgentSessionID:            newID,
		TranscriptPath:            dstFile,
		PreviousTranscriptDir:     srcDir,
		TranscriptBytes:           info.Size(),
		TranscriptModTimeUnixNano: info.ModTime().UnixNano(),
	}, nil
}

// HasTranscript 报告某个 agent session id 的转录是否还能找到。
//
// 给调用方在**不可逆动作之前**做前置校验用：RepointSession 第 1 步是掐断活着的 agent
// 进程，而紧接着的 RepointTranscript 最常见、也最要命的失败就是「找不到转录」。
// 不先验就掐，代价是一次「会话没了、状态却一点没改」——所以这个判据要能单独拿出来问。
func HasTranscript(agentSessionID string) bool {
	id := strings.TrimSpace(agentSessionID)
	if id == "" {
		return false
	}
	projectsDir, err := claudeProjectsDir()
	if err != nil {
		return false
	}
	return len(transcriptDirsForSession(projectsDir, id)) > 0
}

// claudeProjectsDir 与 SDK 的 sessionsProjectsDir 同口径：CLAUDE_CONFIG_DIR 优先，
// 否则 ~/.claude/projects。搬错目录等于把转录复制到一个没人读的地方。
func claudeProjectsDir() (string, error) {
	if env := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); env != "" {
		return filepath.Join(env, "projects"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// transcriptDirsForSession 返回所有含 <id>.jsonl 的 slug 目录。find 的返回顺序不保证，
// 唯一性由调用方的「排除目标目录」判断承担。
func transcriptDirsForSession(projectsDir, id string) []string {
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil
	}
	dirs := make([]string, 0, 2)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(projectsDir, entry.Name())
		if _, err := os.Stat(filepath.Join(dir, id+".jsonl")); err == nil {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

func moveClaudeTranscriptEntry(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	// 跨设备（~/.claude 在别的盘）时 rename 报 EXDEV，退化成复制。
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return err
	}
	return os.Remove(src)
}

func moveClaudeTranscriptDir(src, dst string) error {
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := moveClaudeTranscriptEntry(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())); err != nil {
			return err
		}
	}
	return os.Remove(src)
}
