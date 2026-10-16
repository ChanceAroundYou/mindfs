package codex

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	"mindfs/server/internal/apperr"

	agenttypes "mindfs/server/internal/agent/types"
)

type codexSpawnRelation struct {
	ParentToolCallID string
	Title            string
}

func (i *Importer) readImportedSubagents(root codexSessionFile) ([]agenttypes.ImportedSubagentSession, error) {
	candidates, err := sortedSessionJSONLFiles(i.baseDir)
	if err != nil {
		return nil, err
	}
	files := make(map[string]codexSessionFile)
	for _, candidate := range candidates {
		item, ok, err := inspectCodexSessionFile(candidate.Path)
		if err != nil {
			return nil, err
		}
		if ok && normalizeComparablePath(item.Cwd) == normalizeComparablePath(root.Cwd) {
			files[item.AgentSessionID] = item
		}
	}
	relationsByParent := make(map[string]map[string]codexSpawnRelation)
	for id, file := range files {
		relations, err := readCodexSpawnRelations(file.Path)
		if err != nil {
			return nil, err
		}
		relationsByParent[id] = relations
	}
	items := make([]agenttypes.ImportedSubagentSession, 0)
	added := map[string]bool{root.AgentSessionID: true}
	for {
		progressed := false
		for id, file := range files {
			if id == root.AgentSessionID || added[id] || !added[file.ParentAgentSessionID] {
				continue
			}
			exchanges, err := readCodexImportedExchanges(file.Path, time.Time{})
			if err != nil {
				return nil, err
			}
			relation := relationsByParent[file.ParentAgentSessionID][id]
			parentID := file.ParentAgentSessionID
			if parentID == root.AgentSessionID {
				parentID = ""
			}
			title := firstNonEmpty(file.AgentNickname, relation.Title, file.AgentRole, file.FirstUserText, "Subagent")
			items = append(items, agenttypes.ImportedSubagentSession{
				AgentSessionID:       id,
				ParentAgentSessionID: parentID,
				ParentToolCallID:     relation.ParentToolCallID,
				Title:                title,
				Exchanges:            exchanges,
			})
			added[id] = true
			progressed = true
		}
		if !progressed {
			break
		}
	}
	return items, nil
}

func readCodexSpawnRelations(path string) (map[string]codexSpawnRelation, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, apperr.Wrap("open", path, err)
	}
	defer file.Close()
	callDetails := make(map[string]codexSpawnRelation)
	relations := make(map[string]codexSpawnRelation)
	err = forEachJSONLLine(file, func(line string) error {
		var raw map[string]any
		if json.Unmarshal([]byte(line), &raw) != nil || raw["type"] != "response_item" {
			return nil
		}
		payload, _ := raw["payload"].(map[string]any)
		switch asString(payload["type"]) {
		case "function_call":
			if asString(payload["name"]) != "spawn_agent" {
				return nil
			}
			callID := strings.TrimSpace(asString(payload["call_id"]))
			var args map[string]any
			_ = json.Unmarshal([]byte(asString(payload["arguments"])), &args)
			callDetails[callID] = codexSpawnRelation{
				ParentToolCallID: callID,
				Title:            firstNonEmpty(asString(args["agent_type"]), asString(args["message"]), "Subagent"),
			}
		case "function_call_output":
			callID := strings.TrimSpace(asString(payload["call_id"]))
			relation, ok := callDetails[callID]
			if !ok {
				return nil
			}
			var result map[string]any
			if json.Unmarshal([]byte(asString(payload["output"])), &result) != nil {
				return nil
			}
			agentID := strings.TrimSpace(asString(result["agent_id"]))
			if agentID != "" {
				if nickname := strings.TrimSpace(asString(result["nickname"])); nickname != "" {
					relation.Title = nickname
				}
				relations[agentID] = relation
			}
		}
		return nil
	})
	return relations, err
}
