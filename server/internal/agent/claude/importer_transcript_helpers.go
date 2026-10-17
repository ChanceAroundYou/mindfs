package claude

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
)

func forEachJSONLLine(file *os.File, fn func(offset int64, line string) error) error {
	reader := bufio.NewReader(file)
	var offset int64
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if callErr := fn(offset, string(line)); callErr != nil {
				return callErr
			}
			offset += int64(len(line))
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

func appendMergedClaudeExchangeLocator(
	items []importedExchangeLocator,
	role, content string,
	ts time.Time,
	uuid string,
	aux []agenttypes.ImportedExchangeAux,
) ([]importedExchangeLocator, int, int) {
	content = strings.TrimSpace(content)
	if content == "" && len(aux) == 0 {
		return items, -1, 0
	}
	if len(items) > 0 && items[len(items)-1].Role == role {
		last := &items[len(items)-1]
		lineOffset := importedAssistantLine(last.Content)
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
		if strings.TrimSpace(uuid) != "" {
			last.ClaudeLastMessageUUID = strings.TrimSpace(uuid)
			// NativeID 跟着合并后的行走，取**最后一个** uuid —— 与 ACP 侧
			// 「取最后一条 agent 原生消息」同一个口径。合并后一行对应多条转录条目，
			// 它的身份只能取其中一条；取谁必须两端一致，否则永远对不上。
			last.NativeID = strings.TrimSpace(uuid)
		}
		auxStart := len(last.Aux)
		for _, item := range aux {
			item.Line += lineOffset
			last.Aux = append(last.Aux, item)
		}
		return items, len(items) - 1, auxStart
	}
	items = append(items, importedExchangeLocator{
		ImportedExchange: agenttypes.ImportedExchange{
			Role:      role,
			Content:   content,
			Timestamp: ts,
			Aux:       aux,
			// NativeID = 这条转录条目的 `uuid`。它是 claude 侧唯一的消息身份：
			// 实时侧现在也能拿到（handleAssistantMessage 补读 AssistantMessage.UUID），
			// 于是实时写入的助手行与导入写入的行都以它为 agent_id、由它派生出同一个
			// mindfs_id —— 判重不必再退回「内容 + ±5s」。
			NativeID: strings.TrimSpace(uuid),
		},
		ClaudeLastMessageUUID: strings.TrimSpace(uuid),
	})
	return items, len(items) - 1, 0
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
