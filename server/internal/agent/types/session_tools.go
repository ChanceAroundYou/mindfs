package types

import (
	"context"
	"encoding/json"
	"strings"
)

// Router MCP server 在 CLI 里的名字。暴露给模型的工具名是
// mcp__<server>__<tool>；agent 层与 kanban 层必须一致。
const (
	RouterMCPServerName = "mindfs_router"
	RouterToolPrefix    = "mcp__mindfs_router__"
)

// ToolSpec 是一个进程内 MCP 工具。Handler 直接返回文本结果；错误通过
// IsError 结果呈现给模型，而不是杀掉整个回合。
//
// 这个类型刻意不依赖 claude SDK：router 的领域层（kanban）定义工具，agent
// 适配层把它翻译成 SDK 的 ToolDef/AddToolUntyped。
type ToolSpec struct {
	Name        string
	Description string
	InputSchema map[string]any
	Handler     func(ctx context.Context, args json.RawMessage) (string, error)
}

// ToolPolicy 是工具使用的真实边界（allowed/disallowed 只是 CLI 侧提示，
// handleCanUseTool 才是 deny-by-default 的闸门）。
type ToolPolicy struct {
	AllowedTools    []string
	DisallowedTools []string
	DenyByDefault   bool
	// BuiltinTools 是 Claude CLI `--tools` 的值（claude 适配器专用）：
	// nil 表示不传、保留 CLI 默认内置工具集；"" 关闭全部内置工具（Router
	// 回合只留进程内 MCP 工具）；"Read,Grep,Glob" 之类只保留列出的内置工具
	// （只读复核回合）。内置工具定义本身就有 ~190 KB，全部发送等于每次请求
	// 白付约 49k prompt tokens。
	BuiltinTools *string
}

// Allows 判断一个工具名是否放行。规则：
//   - disallowed 精确命中 -> 拒绝（拒绝优先）；
//   - 没有策略 -> 放行（普通会话行为不变）；
//   - allowed 支持末尾 "*" 前缀通配；
//   - DenyByDefault 且不在 allowed 内 -> 拒绝。
func (p *ToolPolicy) Allows(name string) bool {
	if p == nil {
		return true
	}
	name = strings.TrimSpace(name)
	for _, denied := range p.DisallowedTools {
		if strings.TrimSpace(denied) == name {
			return false
		}
	}
	for _, allowed := range p.AllowedTools {
		allowed = strings.TrimSpace(allowed)
		if allowed == "" {
			continue
		}
		if strings.HasSuffix(allowed, "*") {
			if strings.HasPrefix(name, strings.TrimSuffix(allowed, "*")) {
				return true
			}
			continue
		}
		if allowed == name {
			return true
		}
	}
	return !p.DenyByDefault
}
