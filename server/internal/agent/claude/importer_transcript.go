package claude

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
	ClaudeLastMessageUUID string
	// StartOffset 是这条 item 第一条相关条目在转录文件里的字节偏移。增量提交以它为界：
	// 它之前的都已落库，之后的才算新内容（取代了原先「拿会被 tool_result 持续改写的
	// Timestamp 当判据」的做法）。
	StartOffset int64
}

// readClaudeImportedExchanges 从 startOffset 起读取增量条目。
// startOffset<=0 表示全量读；>0 时只解析该字节位置之后的内容，避免每次同步都全量
// 解析整个转录（实测某会话转录 67MB，全量读+解析 1.5-3.3s，而每次打开会话都会触发同步）。
// committedOffset 是「已提交给 MindFS 的字节位置」，从该处往后解析。
// 返回的第二个值是**本次提交之后**的新位置：只有完整、已结束的轮次才会被提交，
// 仍在进行的尾轮留在它自己的起点上等下一轮同步。
//
// committedOffset<=0 表示该会话从未同步过（库里 229/232 个绑定都是这种，游标为空），
// 此时不知道已读到哪，退回到 bootstrapAfter 时间戳判据：只取比库内最新一条更新的回合。
// 本次同步会把游标建起来，之后一律走字节偏移。
func readClaudeImportedExchanges(path string, committedOffset int64, bootstrapAfter, floor time.Time) ([]agenttypes.ImportedExchange, int64, error) {
	locators, committed, err := readClaudeImportedExchangeLocators(path, committedOffset, bootstrapAfter, floor)
	if err != nil {
		return nil, committedOffset, err
	}
	items := make([]agenttypes.ImportedExchange, 0, len(locators))
	for _, item := range locators {
		items = append(items, item.ImportedExchange)
	}
	return items, committed, nil
}

// claudeTranscriptTailClosed 判定转录尾部这一轮是否已经走完。
// Claude Code 的回合以「不含 tool_use 的助手条目」收尾：最后一条相关条目还带 tool_use
// （在等工具结果）、或尾部停在 tool_result / 用户条目上，都说明这一轮还在进行。
// 只有「助手条目带正文且不带 tool_use」才算收尾——纯 thinking 条目不算。
func claudeTranscriptTailClosed(lastRole string, lastHadToolUse, lastHadText bool) bool {
	return lastRole == "assistant" && !lastHadToolUse && lastHadText
}

func readClaudeImportedExchangeLocators(path string, committedOffset int64, bootstrapAfter, floor time.Time) ([]importedExchangeLocator, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, committedOffset, apperr.Wrap("open", path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, committedOffset, apperr.Wrap("stat", path, err)
	}
	size := info.Size()
	if committedOffset < 0 || committedOffset > size {
		// 转录被截断/轮转：游标作废，退回全量读。
		committedOffset = 0
	}
	if committedOffset > 0 {
		if _, err := file.Seek(committedOffset, io.SeekStart); err != nil {
			return nil, committedOffset, apperr.Wrap("seek", path, err)
		}
	}

	items := make([]importedExchangeLocator, 0)
	toolLocations := make(map[string]importedToolLocation)
	// 按条目 uuid 去重：同一 uuid 的条目在转录里可能整段重复出现——实测本机某会话
	// 6336 条 assistant 条目中有 1712 条 uuid 重复（重复区间相隔上万行，uuid/时间戳/
	// 内容三者完全相同）。这类远距离重复无法被「相邻同角色才合并」
	// （appendMergedClaudeExchangeLocator）吃掉，会各自成为一个条目被反复落库，
	// 表现为同一段助手文本在会话里出现两次以上。此处同一条目只处理一次。
	seenUUIDs := make(map[string]struct{})
	lastRole := ""
	lastHadToolUse := false
	lastHadText := false
	err = forEachJSONLLine(file, func(lineOffset int64, line string) error {
		// forEachJSONLLine 从当前文件位置起算，而我们已 seek 到 committedOffset，
		// 所以这里要补回基准才是转录里的绝对字节偏移。
		lineOffset += committedOffset
		line = strings.TrimSpace(line)
		if line == "" {
			return nil
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return nil
		}
		role := strings.ToLower(strings.TrimSpace(asString(raw["type"])))
		if role != "user" && role != "assistant" {
			return nil
		}
		uuid := strings.TrimSpace(asString(raw["uuid"]))
		if uuid != "" {
			if _, ok := seenUUIDs[uuid]; ok {
				return nil
			}
			seenUUIDs[uuid] = struct{}{}
		}
		message, _ := raw["message"].(map[string]any)
		if message == nil {
			return nil
		}
		ts := parseTimeRFC3339(asString(raw["timestamp"]))
		if role == "user" {
			lastRole, lastHadToolUse, lastHadText = "user", false, false
			applyClaudeToolResults(items, toolLocations, message["content"], raw["toolUseResult"], ts)
			// 转录自带 isMeta 标记，标明「这条不是用户输入」（CLI 注入的 skill 正文、自动
			// 续跑、命令回显等）。实测 139712 条 user/assistant 条目里 1420 条 isMeta=true，
			// 其中约 1030 条 isMeaningfulClaudeUserText 认不出来。漏进来就是用户没发过的
			// 气泡，还会被「相邻同角色合并」并进相邻的真人消息里。
			if isMeta, _ := raw["isMeta"].(bool); isMeta {
				return nil
			}
			text := extractClaudeImportedUserText(message["content"])
			if text != "" && isMeaningfulClaudeUserText(text) {
				before := len(items)
				items, _, _ = appendMergedClaudeExchangeLocator(items, "user", text, ts, uuid, nil)
				if len(items) > before {
					items[before].StartOffset = lineOffset
				}
			}
			return nil
		}
		lastRole = "assistant"
		lastHadToolUse, lastHadText = false, false
		if blocks, ok := message["content"].([]any); ok {
			for _, block := range blocks {
				item, _ := block.(map[string]any)
				if item == nil {
					continue
				}
				switch strings.TrimSpace(asString(item["type"])) {
				case "tool_use":
					lastHadToolUse = true
				case "text":
					if strings.TrimSpace(asString(item["text"])) != "" {
						lastHadText = true
					}
				}
			}
		}
		text := strings.TrimSpace(extractClaudeMessageText(message["content"]))
		if isAutoContinueAck(text) {
			// 自动续跑一问一答的应答侧。提问侧靠 isMeta 挡住，应答侧没有 isMeta
			// （实测 assistant 条目 isMeta 恒为 false），只能按内容判；不挡就会被
			// 「相邻同角色合并」并进紧邻的真助手文本里。
			text = ""
		}
		aux := extractClaudeToolUseAux(message["content"])
		if text == "" && len(aux) == 0 {
			return nil
		}
		before := len(items)
		var exchangeIndex, auxStart int
		items, exchangeIndex, auxStart = appendMergedClaudeExchangeLocator(
			items,
			"agent",
			text,
			ts,
			uuid,
			aux,
		)
		if len(items) > before {
			items[before].StartOffset = lineOffset
		}
		for index := auxStart; index < len(items[exchangeIndex].Aux); index++ {
			toolCall := items[exchangeIndex].Aux[index].ToolCall
			if toolCall == nil || strings.TrimSpace(toolCall.CallID) == "" {
				continue
			}
			toolLocations[strings.TrimSpace(toolCall.CallID)] = importedToolLocation{
				ExchangeIndex: exchangeIndex,
				AuxIndex:      index,
			}
		}
		return nil
	})
	if err != nil {
		return nil, committedOffset, err
	}
	// 已提交位置：只有在转录尾部已收尾时才推进过最后一条 item。
	// 尾轮还在进行（正在跑工具 / 刚拿到结果还没续写）就先不落库 —— 它的内容会继续变，
	// 落了就是半成品，而且下次同步会因为它「又变了」而被当成新内容再落一遍。
	committed := size
	if n := len(items); n > 0 {
		tail := items[n-1]
		if tail.Role == "agent" && !claudeTranscriptTailClosed(lastRole, lastHadToolUse, lastHadText) {
			if tail.StartOffset > committedOffset {
				committed = tail.StartOffset
			} else {
				committed = committedOffset
			}
			items = items[:n-1]
		}
	}
	filtered := make([]importedExchangeLocator, 0, len(items))
	// floor 是给「live-owned 会话的兜底补齐」用的：那种会话的游标冻结已久，只按偏移读会把
	// 早已落库的回合整段重导（实测 2026-09-16 BP 会话重导了 09-14 的内容，与实时路径写的
	// 行并排显示成重复）。有地板时：游标决定从哪开始读，地板决定读到的东西算不算数。
	passesFloor := func(item importedExchangeLocator) bool {
		if floor.IsZero() {
			return true
		}
		return !item.Timestamp.IsZero() && item.Timestamp.After(floor)
	}
	if committedOffset > 0 {
		for _, item := range items {
			if item.StartOffset < committedOffset {
				continue
			}
			if !passesFloor(item) {
				continue
			}
			filtered = append(filtered, item)
		}
		return filtered, committed, nil
	}
	// 引导：该会话还没有游标，用库内最新时间戳兜底（旧行为，只此一次）。
	// 但地板本身就已经是「只接受比库内最新一条更新」的判据，与有没有游标无关
	// （见 types.go 的 TimestampFloor 契约）。Full 同步恰恰是 committedOffset==0 且
	// bootstrapAfter 为零的组合，若这里直接 return items，地板就被静默跳过 ——
	// live-owned 会话的 Full 同步于是永远导不到「比库里最新一条更新」的尾段：
	// 实测某会话 ctx_seq=115 而导入器只有 83 条，usecase 再按 ctx_seq 切片，delta
	// 恒为空，用户点「同步」永远补不回最新几轮。所以只要地板非零就必须过滤。
	if bootstrapAfter.IsZero() && floor.IsZero() {
		return items, committed, nil
	}
	for _, item := range items {
		if item.Timestamp.IsZero() || !item.Timestamp.After(bootstrapAfter) || !passesFloor(item) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered, committed, nil
}

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
