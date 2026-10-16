package acp

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	types "mindfs/server/internal/agent/types"

	acp "github.com/coder/acp-go-sdk"
)

// processTree owns platform resources for one ACP process and its descendants.
// Kill and Close must be safe to call concurrently with context cancellation.
type processTree interface {
	Kill() error
	Close() error
}

// SessionUpdate is the internal session update type.
type SessionUpdate struct {
	Type       UpdateType
	SessionID  string
	Raw        acp.SessionUpdate
	TokenUsage *types.TokenUsage
}

// UpdateType defines the type of session update.
type UpdateType string

// mindfsClient implements acp.Client interface
type mindfsClient struct {
	proc *Process
}

func (c *mindfsClient) RequestPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	// Emit a synthetic tool_call update for permission-gated operations so upper
	// layers can track tool execution and associate file paths immediately.
	if session := c.proc.getSessionByID(string(params.SessionId)); session != nil {
		if handler := session.getOnUpdate(); handler != nil {
			toolCall := &acp.SessionUpdateToolCall{
				Content:    params.ToolCall.Content,
				Locations:  params.ToolCall.Locations,
				RawInput:   params.ToolCall.RawInput,
				RawOutput:  params.ToolCall.RawOutput,
				Title:      "",
				ToolCallId: params.ToolCall.ToolCallId,
				Status:     acp.ToolCallStatusPending,
			}
			if params.ToolCall.Title != nil {
				toolCall.Title = *params.ToolCall.Title
			}
			if params.ToolCall.Kind != nil {
				toolCall.Kind = *params.ToolCall.Kind
			} else {
				toolCall.Kind = acp.ToolKindOther
			}
			if params.ToolCall.Status != nil {
				toolCall.Status = *params.ToolCall.Status
			}
			handler(SessionUpdate{
				Type:      UpdateTypeToolCall,
				SessionID: string(params.SessionId),
				Raw: acp.SessionUpdate{
					ToolCall: toolCall,
				},
			})
		}
	}
	// TODO: Forward to frontend for user approval
	// For now, auto-approve with first allow option
	for _, opt := range params.Options {
		if opt.Kind == acp.PermissionOptionKindAllowOnce || opt.Kind == acp.PermissionOptionKindAllowAlways {
			return acp.RequestPermissionResponse{
				Outcome: acp.RequestPermissionOutcome{
					Selected: &acp.RequestPermissionOutcomeSelected{
						OptionId: opt.OptionId,
					},
				},
			}, nil
		}
	}
	// Fallback to first option
	if len(params.Options) > 0 {
		return acp.RequestPermissionResponse{
			Outcome: acp.RequestPermissionOutcome{
				Selected: &acp.RequestPermissionOutcomeSelected{
					OptionId: params.Options[0].OptionId,
				},
			},
		}, nil
	}
	return acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{
			Cancelled: &acp.RequestPermissionOutcomeCancelled{},
		},
	}, nil
}

// UnstableCreateElicitation 处理 dsh 的 ask_user_question 提问请求。
//
// CUSTOM(G-AS): 上游 ACP 客户端没有这个 handler，dsh 的 ask_user_question 会直接
// 报 "the ACP client does not support form elicitation"。
//
// ACP SDK 的 UnstableCreateElicitationForm 不带 sessionId/toolCallId（这个 fork 的
// codegen 丢了），所以这里靠题目 id + 文本关联到已渲染的 ask_user 卡。
func (c *mindfsClient) UnstableCreateElicitation(ctx context.Context, req acp.UnstableCreateElicitationRequest) (acp.UnstableCreateElicitationResponse, error) {
	if req.Form == nil || req.Form.Mode != "form" {
		return acp.UnstableCreateElicitationResponse{Decline: &acp.UnstableCreateElicitationDecline{}}, nil
	}
	questions := dshElicitationQuestions(req.Form.Meta)
	if len(questions) == 0 {
		c.proc.logElicitation("unsupported", "", "", "message", req.Form.Message)
		return acp.UnstableCreateElicitationResponse{Decline: &acp.UnstableCreateElicitationDecline{}}, nil
	}
	entry := c.proc.bindElicitation(questions)
	if entry == nil {
		c.proc.logElicitation("unbound", "", "", "questions", len(questions))
		return acp.UnstableCreateElicitationResponse{Decline: &acp.UnstableCreateElicitationDecline{}}, nil
	}
	c.proc.logElicitation("open", entry.sessionKey, entry.callID, "questions", len(entry.questions))
	select {
	case result := <-entry.waiter:
		if len(result.content) == 0 {
			c.proc.logElicitation("declined", entry.sessionKey, entry.callID)
			return acp.UnstableCreateElicitationResponse{Decline: &acp.UnstableCreateElicitationDecline{}}, nil
		}
		c.proc.logElicitation("answered", entry.sessionKey, entry.callID, "fields", len(result.content))
		return acp.UnstableCreateElicitationResponse{
			Accept: &acp.UnstableCreateElicitationAccept{Content: result.content},
		}, nil
	case <-ctx.Done():
		// 这一条路没有经过 AnswerElicitation，条目还留在表里 —— 不释放的话它会一直
		// 挂着，且因为已 bound，reap 也绕开它；下一次问同一道题（id+文本相同）时
		// 可能被匹配到这条废弃条目上，答案投进没人读的 waiter。
		c.proc.releasePendingAskUser(entry)
		c.proc.logElicitation("cancelled", entry.sessionKey, entry.callID)
		return acp.UnstableCreateElicitationResponse{Cancel: &acp.UnstableCreateElicitationCancel{}}, nil
	}
}

func (c *mindfsClient) ReadTextFile(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	// Agent handles file operations itself
	return acp.ReadTextFileResponse{Content: ""}, nil
}

func (c *mindfsClient) WriteTextFile(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, nil
}

func (c *mindfsClient) CreateTerminal(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, nil
}

func (c *mindfsClient) TerminalOutput(ctx context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, nil
}

func (c *mindfsClient) ReleaseTerminal(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, nil
}

func (c *mindfsClient) WaitForTerminalExit(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, nil
}

func (c *mindfsClient) KillTerminal(ctx context.Context, params acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, nil
}

func (c *mindfsClient) HandleExtensionMethod(_ context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "_qwencode/slash_command":
		return nil, c.handleQwenSlashCommandNotification(params)
	default:
		return nil, acp.NewMethodNotFound(method)
	}
}

func (c *mindfsClient) handleQwenSlashCommandNotification(params json.RawMessage) error {
	var notif qwenSlashCommandNotification
	if err := json.Unmarshal(params, &notif); err != nil {
		return acp.NewInvalidParams(map[string]any{"error": err.Error()})
	}
	if strings.TrimSpace(notif.SessionID) == "" {
		return acp.NewInvalidParams(map[string]any{"error": "sessionId required"})
	}
	handler := c.proc.getSessionUpdateHandler(notif.SessionID)
	if handler == nil {
		return nil
	}
	content := notif.Message
	if content == "" {
		return nil
	}
	log.Printf("[agent/acp] ext.notification agent=%s method=_qwencode/slash_command session_id=%s command=%q message_type=%s", c.proc.agentLabel(), notif.SessionID, notif.Command, notif.MessageType)
	handler(newMessageChunkUpdate(notif.SessionID, content, map[string]any{
		"source":       "_qwencode/slash_command",
		"command":      notif.Command,
		"message_type": notif.MessageType,
	}))
	return nil
}

func newMessageChunkUpdate(sessionID, content string, meta map[string]any) SessionUpdate {
	return SessionUpdate{
		Type:      UpdateTypeMessageChunk,
		SessionID: sessionID,
		Raw: acp.SessionUpdate{
			AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
				Content:       acp.TextBlock(content),
				SessionUpdate: "agent_message_chunk",
				Meta:          meta,
			},
		},
	}
}
