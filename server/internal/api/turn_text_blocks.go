package api

import (
	"strings"

	"mindfs/server/internal/agent/types"
	"mindfs/server/internal/api/usecase"
)

// appendTurnTextBlock 把一轮 workflow 回合里的助手消息块拼成一段正文。
//
// 同一条回复会被工具调用切成多个消息块（线上 task #99 的 6 块），逐块直接相接
// 会黏成「changes.Now update ...」。所以两段之间夹了工具调用/思考/待办更新时补
// 一个空行，并吃掉下一块自带的前导换行。分隔规则与流式累积
// （usecase.AppendResponseChunk）同源，不在这里另立一套。
func appendTurnTextBlock(text, lastType string, updateType types.EventType, chunk string) (string, string) {
	if updateType == types.EventTypeMessageChunk {
		if strings.TrimSpace(text) != "" && lastType != string(types.EventTypeMessageChunk) {
			chunk = strings.TrimLeft(chunk, "\n")
		}
		return usecase.AppendResponseChunk(text, lastType, chunk), string(updateType)
	}
	switch updateType {
	case types.EventTypeThoughtChunk,
		types.EventTypeToolCall,
		types.EventTypeToolUpdate,
		types.EventTypeTodoUpdate,
		types.EventTypePlanUpdate,
		types.EventTypeCompact:
		return text, string(updateType)
	}
	return text, lastType
}
