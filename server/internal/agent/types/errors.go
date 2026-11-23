package types

import (
	"errors"
	"strings"
)

// TurnTerminalError 把 harness 上报的 terminal_reason 带出适配层。
//
// 适配层原先只把 result 里的错误文本变成一个普通 error，到了 usecase 层就只剩
// 字符串可判断。Claude CLI 在上下文用满时给出的是
// `result="Prompt is too long"` + `terminal_reason="blocking_limit"`：前一句是
// 给用户看的文案，后一句才是「这一轮已终止、重试同一个请求不会变」的机器判据。
// 用类型把判据带出去，比在错误文本里找关键字稳。
type TurnTerminalError struct {
	Reason  string
	Message string
}

func (e *TurnTerminalError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// TurnTerminalReason 返回错误链里携带的 terminal reason（小写、去首尾空白）。
// 没有终端原因时返回空串。
func TurnTerminalReason(err error) string {
	if err == nil {
		return ""
	}
	var target *TurnTerminalError
	if !errors.As(err, &target) || target == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(target.Reason))
}
