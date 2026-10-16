package codex

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"mindfs/server/internal/apperr"

	agenttypes "mindfs/server/internal/agent/types"
)

type importedExchangeLocator struct {
	agenttypes.ImportedExchange
	CodexUserCountAfter int
}

type importedTurn struct {
	Users []importedExchangeLocator
	Agent importedExchangeLocator
}

func readCodexImportedExchangeLocators(path string, after time.Time) ([]importedExchangeLocator, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, apperr.Wrap("open", path, err)
	}
	defer file.Close()

	items := make([]agenttypes.ImportedExchange, 0)
	toolLocations := make(map[string]importedToolLocation)
	toolOrdinal := 0
	sessionShell := ""
	err = forEachJSONLLine(file, func(line string) error {
		line = strings.TrimSpace(line)
		if line == "" {
			return nil
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return nil
		}
		timestamp := parseTimeRFC3339(asString(raw["timestamp"]))
		switch raw["type"] {
		case "response_item":
			payload, _ := raw["payload"].(map[string]any)
			if payload == nil {
				return nil
			}
			switch strings.TrimSpace(asString(payload["type"])) {
			case "message":
				role := strings.ToLower(strings.TrimSpace(asString(payload["role"])))
				switch role {
				case "user":
					text := extractCodexImportedUserText(payload["content"])
					if text == "" {
						return nil
					}
					items, _, _ = appendMergedCodexExchange(items, "user", text, timestamp, nil)
				case "assistant":
					text := strings.TrimSpace(extractCodexMessageText(payload["content"]))
					text, planAux := extractImportedCodexProposedPlan(text)
					if text == "" && len(planAux) == 0 {
						return nil
					}
					items, _, _ = appendMergedCodexExchange(items, "agent", text, timestamp, planAux)
				}
			case "function_call", "custom_tool_call":
				toolOrdinal++
				toolCall, ok := parseImportedCodexToolCall(payload, toolOrdinal, sessionShell)
				if !ok {
					return nil
				}
				aux := []agenttypes.ImportedExchangeAux{{
					Line:     0,
					ToolCall: &toolCall,
				}}
				var exchangeIndex, auxStart int
				items, exchangeIndex, auxStart = appendMergedCodexExchange(
					items,
					"agent",
					"",
					timestamp,
					aux,
				)
				if exchangeIndex < 0 {
					return nil
				}
				toolLocations[toolCall.CallID] = importedToolLocation{
					ExchangeIndex: exchangeIndex,
					AuxIndex:      auxStart,
				}
			case "function_call_output", "custom_tool_call_output":
				applyImportedCodexToolOutput(items, toolLocations, payload, timestamp)
			}
		case "event_msg":
			if numTurns := codexRollbackTurns(raw); numTurns > 0 {
				items = dropLastCodexUserTurns(items, numTurns)
				toolLocations = rebuildImportedCodexToolLocations(items)
			}
		case "world_state":
			if shell := importedCodexWorldStateShell(raw); shell != "" {
				sessionShell = shell
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return codexExchangeLocatorsAfter(items, after), nil
}

func forEachJSONLLine(file *os.File, fn func(string) error) error {
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if callErr := fn(string(line)); callErr != nil {
				return callErr
			}
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
}

func appendMergedCodexExchange(
	items []agenttypes.ImportedExchange,
	role, content string,
	ts time.Time,
	aux []agenttypes.ImportedExchangeAux,
) ([]agenttypes.ImportedExchange, int, int) {
	content = strings.TrimSpace(content)
	if content == "" && len(aux) == 0 {
		return items, -1, 0
	}
	if len(items) > 0 && items[len(items)-1].Role == role {
		last := &items[len(items)-1]
		lineOffset := importedCodexAssistantLine(last.Content)
		if content != "" {
			if last.Content == "" {
				last.Content = content
			} else {
				last.Content = strings.TrimSpace(last.Content + "\n\n" + content)
			}
		}
		if !ts.IsZero() {
			last.Timestamp = ts
		}
		auxStart := len(last.Aux)
		for _, item := range aux {
			item.Line += lineOffset
			last.Aux = append(last.Aux, item)
		}
		return items, len(items) - 1, auxStart
	}
	items = append(items, agenttypes.ImportedExchange{
		Role:      role,
		Content:   content,
		Timestamp: ts,
		Aux:       aux,
	})
	return items, len(items) - 1, 0
}

func appendMergedExchangeLocator(
	items []importedExchangeLocator,
	role, content string,
	ts time.Time,
	userCount int,
	aux []agenttypes.ImportedExchangeAux,
) []importedExchangeLocator {
	content = strings.TrimSpace(content)
	if content == "" && len(aux) == 0 {
		return items
	}
	if len(items) > 0 && items[len(items)-1].Role == role {
		last := &items[len(items)-1]
		lineOffset := importedCodexAssistantLine(last.Content)
		if content != "" {
			if last.Content == "" {
				last.Content = content
			} else {
				last.Content = strings.TrimSpace(last.Content + "\n\n" + content)
			}
		}
		if !ts.IsZero() {
			last.Timestamp = ts
		}
		for _, item := range aux {
			item.Line += lineOffset
			last.Aux = append(last.Aux, item)
		}
		last.CodexUserCountAfter = userCount
		return items
	}
	items = append(items, importedExchangeLocator{
		ImportedExchange: agenttypes.ImportedExchange{
			Role:      role,
			Content:   content,
			Timestamp: ts,
			Aux:       aux,
		},
		CodexUserCountAfter: userCount,
	})
	return items
}

func codexRollbackTurns(raw map[string]any) int {
	payload, _ := raw["payload"].(map[string]any)
	if payload == nil || strings.TrimSpace(asString(payload["type"])) != "thread_rolled_back" {
		return 0
	}
	switch value := payload["num_turns"].(type) {
	case float64:
		if value > 0 {
			return int(value)
		}
	case int:
		if value > 0 {
			return value
		}
	}
	return 0
}

func dropLastCodexUserTurns(items []agenttypes.ImportedExchange, numTurns int) []agenttypes.ImportedExchange {
	if numTurns <= 0 || len(items) == 0 {
		return items
	}
	out := items
	for ; numTurns > 0 && len(out) > 0; numTurns-- {
		userIndex := -1
		for i := len(out) - 1; i >= 0; i-- {
			if out[i].Role == "user" {
				userIndex = i
				break
			}
		}
		if userIndex < 0 {
			return nil
		}
		out = out[:userIndex]
	}
	return out
}

func codexExchangeLocatorsAfter(items []agenttypes.ImportedExchange, after time.Time) []importedExchangeLocator {
	out := make([]importedExchangeLocator, 0, len(items))
	userCount := 0
	for _, item := range items {
		if item.Role == "user" {
			userCount++
		}
		if !after.IsZero() && (item.Timestamp.IsZero() || !item.Timestamp.After(after)) {
			continue
		}
		out = appendMergedExchangeLocator(
			out,
			item.Role,
			item.Content,
			item.Timestamp,
			userCount,
			item.Aux,
		)
	}
	return out
}

func buildImportedTurns(items []importedExchangeLocator) []importedTurn {
	turns := make([]importedTurn, 0)
	users := make([]importedExchangeLocator, 0)
	for _, item := range items {
		switch item.Role {
		case "user":
			users = append(users, item)
		case "agent":
			turns = append(turns, importedTurn{
				Users: append([]importedExchangeLocator(nil), users...),
				Agent: item,
			})
			users = nil
		}
	}
	return turns
}
