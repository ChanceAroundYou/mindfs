package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"
)

// compactBoundaryPostTokens 从 CLI 转录补读压缩后的上下文大小。
//
// SDK 的 CompactBoundaryMessage 只暴露 trigger / pre_tokens，但 CLI 写在 JSONL
// 里的 compact_boundary 带有 postTokens。少这个数的直接后果是 /compact 回合
// 没有 message_delta，LastContextWindow 永远停在压缩前的大数值，会话反复被
// 判成「又久又大」并再压一次。读不到时返回 0，调用方用 stale 闸兜底。
func (s *session) compactBoundaryPostTokens(msg claudeagent.CompactBoundaryMessage) int {
	id := strings.TrimSpace(msg.UUID)
	sessionID := firstNonEmpty(msg.SessionID, s.SessionID(), s.sessionID)
	if id == "" || sessionID == "" || strings.TrimSpace(s.rootPath) == "" {
		return 0
	}

	projectsDir := s.claudeProjectsDir()
	path := filepath.Join(projectsDir, claudeProjectDirName(s.rootPath), sessionID+".jsonl")
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var line compactBoundaryTranscriptLine
	for scanner.Scan() {
		raw := bytes.TrimSpace(scanner.Bytes())
		if !bytes.Contains(raw, []byte("compact_boundary")) || !bytes.Contains(raw, []byte(id)) {
			continue
		}
		line = compactBoundaryTranscriptLine{}
		if json.Unmarshal(raw, &line) != nil {
			continue
		}
		if line.Subtype != "compact_boundary" || line.UUID != id {
			continue
		}
		if line.CompactMetadata.PostTokens > 0 {
			return line.CompactMetadata.PostTokens
		}
		if line.CompactMetadataSnake.PostTokens > 0 {
			return line.CompactMetadataSnake.PostTokens
		}
	}
	return 0
}

type compactBoundaryTranscriptLine struct {
	Subtype              string                  `json:"subtype"`
	UUID                 string                  `json:"uuid"`
	CompactMetadata      compactBoundaryMetadata `json:"compactMetadata"`
	CompactMetadataSnake compactBoundaryMetadata `json:"compact_metadata"`
}

type compactBoundaryMetadata struct {
	PostTokens int `json:"postTokens"`
}

// claudeProjectsDir 与 SDK / importer 同口径；本方法额外认会话自己的 baseEnv，
// 因为 MindFS 可以给单个会话注入不同的 CLAUDE_CONFIG_DIR。
func (s *session) claudeProjectsDir() string {
	if configDir := strings.TrimSpace(s.baseEnv["CLAUDE_CONFIG_DIR"]); configDir != "" {
		return filepath.Join(configDir, "projects")
	}
	if configDir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); configDir != "" {
		return filepath.Join(configDir, "projects")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}
