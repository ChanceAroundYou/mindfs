package claude

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mindfs/server/internal/apperr"

	agenttypes "mindfs/server/internal/agent/types"
)

type subagentFileStamp struct {
	Size      int64
	ModTimeNs int64
}

type claudeSubagentRelation struct {
	AgentID          string
	ParentAgentID    string
	ParentToolCallID string
	Title            string
	Model            string
}

// readClaudeImportedSubagents 发现子代理会话及其与父会话 Task 调用的关系。
// parentStartOffset>0 时父转录只读游标之后的新行：关系映射一旦产生即不变，配合
// i.subagentRelations 缓存即可复用，避免每次同步都全量重读父转录（实测 72MB 父转录
// 的关系扫描 1.33s/次，占本函数总耗时的 67%）。
func (i *Importer) readClaudeImportedSubagents(parentPath string, parentStartOffset int64) ([]agenttypes.ImportedSubagentSession, error) {
	dir := filepath.Join(strings.TrimSuffix(parentPath, filepath.Ext(parentPath)), "subagents")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, apperr.Wrap("read_dir", dir, err)
	}
	pathsByAgentID := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		agentID, err := inspectClaudeSubagentID(path)
		if err != nil {
			return nil, err
		}
		if agentID != "" {
			pathsByAgentID[agentID] = path
		}
	}
	if len(pathsByAgentID) == 0 {
		return nil, nil
	}
	i.mu.Lock()
	if i.subagentRelations == nil {
		i.subagentRelations = make(map[string]claudeSubagentRelation)
	}
	relations := i.subagentRelations
	i.mu.Unlock()
	if err := collectClaudeSubagentRelations(parentPath, "", relations, parentStartOffset); err != nil {
		return nil, err
	}
	for agentID, path := range pathsByAgentID {
		// 子代理转录未变则跳过重读：其关系已在上次扫描时并入 relations 缓存。
		stamp := subagentFileStamp{}
		if info, statErr := os.Stat(path); statErr == nil {
			stamp = subagentFileStamp{Size: info.Size(), ModTimeNs: info.ModTime().UnixNano()}
			i.mu.RLock()
			prev, seen := i.subagentFileCursors[path]
			i.mu.RUnlock()
			if seen && prev == stamp {
				continue
			}
		}
		if err := collectClaudeSubagentRelations(path, agentID, relations, 0); err != nil {
			return nil, err
		}
		i.mu.Lock()
		i.subagentFileCursors[path] = stamp
		i.mu.Unlock()
	}
	items := make([]agenttypes.ImportedSubagentSession, 0, len(relations))
	remaining := make(map[string]claudeSubagentRelation, len(relations))
	for agentID, relation := range relations {
		if _, ok := pathsByAgentID[agentID]; ok {
			remaining[agentID] = relation
		}
	}
	added := make(map[string]bool)
	for len(remaining) > 0 {
		progressed := false
		for agentID, relation := range remaining {
			if relation.ParentAgentID != "" && !added[relation.ParentAgentID] {
				continue
			}
			exchanges, _, err := readClaudeImportedExchanges(pathsByAgentID[agentID], 0, time.Time{}, time.Time{})
			if err != nil {
				return nil, err
			}
			parentID := ""
			if relation.ParentAgentID != "" {
				parentID = "claude-subagent:" + relation.ParentAgentID
			}
			items = append(items, agenttypes.ImportedSubagentSession{
				AgentSessionID:       "claude-subagent:" + agentID,
				ParentAgentSessionID: parentID,
				ParentToolCallID:     relation.ParentToolCallID,
				Title:                relation.Title,
				Model:                relation.Model,
				Exchanges:            exchanges,
			})
			added[agentID] = true
			delete(remaining, agentID)
			progressed = true
		}
		if !progressed {
			break
		}
	}
	return items, nil
}

func inspectClaudeSubagentID(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", apperr.Wrap("open", path, err)
	}
	defer file.Close()
	var agentID string
	err = forEachJSONLLine(file, func(_ int64, line string) error {
		var raw map[string]any
		if json.Unmarshal([]byte(line), &raw) == nil {
			agentID = strings.TrimSpace(asString(raw["agentId"]))
		}
		if agentID != "" {
			return errStopJSONL
		}
		return nil
	})
	if errors.Is(err, errStopJSONL) {
		err = nil
	}
	return agentID, err
}

// 避免每次同步都全量重读父转录。
func collectClaudeSubagentRelations(path, parentAgentID string, relations map[string]claudeSubagentRelation, startOffset int64) error {
	file, err := os.Open(path)
	if err != nil {
		return apperr.Wrap("open", path, err)
	}
	defer file.Close()
	if startOffset > 0 {
		pos := startOffset - subagentRelationSeekBack
		if pos < 0 {
			pos = 0
		}
		if _, err := file.Seek(pos, io.SeekStart); err != nil {
			return apperr.Wrap("seek", path, err)
		}
		if pos > 0 {
			if _, err := bufio.NewReader(file).ReadBytes('\n'); err != nil && !errors.Is(err, io.EOF) {
				return apperr.Wrap("seek_align", path, err)
			}
		}
	}
	callDetails := make(map[string]claudeSubagentRelation)
	return forEachJSONLLine(file, func(_ int64, line string) error {
		var raw map[string]any
		if json.Unmarshal([]byte(line), &raw) != nil {
			return nil
		}
		message, _ := raw["message"].(map[string]any)
		blocks, _ := message["content"].([]any)
		for _, value := range blocks {
			block, _ := value.(map[string]any)
			if block == nil {
				continue
			}
			toolName := strings.ToLower(strings.TrimSpace(asString(block["name"])))
			if strings.EqualFold(asString(block["type"]), "tool_use") && (toolName == "agent" || toolName == "task") {
				input, _ := block["input"].(map[string]any)
				callID := strings.TrimSpace(asString(block["id"]))
				callDetails[callID] = claudeSubagentRelation{
					ParentAgentID:    parentAgentID,
					ParentToolCallID: callID,
					Title:            firstNonEmpty(asString(input["description"]), asString(input["subagent_type"]), "Subagent"),
					Model:            strings.TrimSpace(asString(input["model"])),
				}
			}
		}
		result, _ := raw["toolUseResult"].(map[string]any)
		agentID := strings.TrimSpace(asString(result["agentId"]))
		if agentID == "" {
			return nil
		}
		callID := ""
		for _, value := range blocks {
			block, _ := value.(map[string]any)
			if block != nil && strings.EqualFold(asString(block["type"]), "tool_result") {
				callID = strings.TrimSpace(asString(block["tool_use_id"]))
				break
			}
		}
		relation := callDetails[callID]
		relation.AgentID = agentID
		relation.ParentAgentID = parentAgentID
		relation.ParentToolCallID = callID
		if relation.Title == "" {
			relation.Title = firstNonEmpty(asString(result["agentType"]), "Subagent")
		}
		if relation.Model == "" {
			relation.Model = strings.TrimSpace(asString(result["resolvedModel"]))
		}
		relations[agentID] = relation
		return nil
	})
}
