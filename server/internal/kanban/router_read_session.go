package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Router 读会话转录（2026-11-19 用户裁定）：
//
//	「给 router 专门设置一个工具，他有全部读取的权力，但是一般只能读取到最后一个
//	 文本，可以通过 offset 读到前面」
//
// 动机是 #86：节点的结果写在会话转录里，而 Run 投影可能是一片空白（重启把 running
// 标成 unknown，投影只在终态写）。Router 唯一的读工具 inspect_run 只能看投影，
// 于是只能反复问用户「请先结算 Run」。
//
// 边界（不因为「全部读取」就放开）：
//   - 只能读本任务的会话，白名单来自 TaskRelatedSessionKeys（主会话 + 节点会话 +
//     Run 共享会话 + 活动会话 + 子会话）；
//   - 只返回文本块，工具调用/推理内容不进 Router 的上下文；
//   - 单块与整次读取都有字节上限，翻页由 offset 承担。

const (
	routerReadSessionBlockMaxBytes = 8000
	routerReadSessionTotalMaxBytes = 16000
	routerReadSessionMaxLimit      = 5
)

// RouterTranscriptBlock 是 Router 能看到的最小会话块：只要类型与正文。
type RouterTranscriptBlock struct {
	Type string
	Text string
}

// RouterTranscriptReader 按 root + session key 读一个会话的全部块。
// 由 api 层用 session.Manager 实现；nil 时 router_read_session 显式报错。
type RouterTranscriptReader interface {
	ReadSessionBlocks(ctx context.Context, rootID, sessionKey string) ([]RouterTranscriptBlock, error)
}

// SetRouterTranscriptReader 挂上 Router 读会话的端口。装配期调一次。
func (s *Service) SetRouterTranscriptReader(reader RouterTranscriptReader) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.routerTranscripts = reader
	s.mu.Unlock()
}

func (s *Service) routerTranscriptReader() RouterTranscriptReader {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.routerTranscripts
}

func (e *routerToolEnv) readSessionTool() RouterToolSpec {
	return RouterToolSpec{
		Name: "router_read_session",
		Description: "只读：读本任务某个会话的文本。默认只给最后一段文本，用 offset 往前翻" +
			"（offset=1 是倒数第二段），limit 最多 5 段。只能读本任务的会话；不返回工具调用与推理内容。",
		Class: RouterToolRead,
		InputSchema: objectSchema(map[string]any{
			"session_key": map[string]any{"type": "string", "description": "可选；缺省用本任务最近一次节点执行的会话"},
			"offset":      map[string]any{"type": "integer", "minimum": 0, "description": "从最新一段往前数几段，默认 0"},
			"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": routerReadSessionMaxLimit},
		}, nil),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				SessionKey string `json:"session_key"`
				Offset     int    `json:"offset"`
				Limit      int    `json:"limit"`
			}
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &args); err != nil {
					return "", fmt.Errorf("invalid arguments: %w", err)
				}
			}
			return e.readSession(ctx, strings.TrimSpace(args.SessionKey), args.Offset, args.Limit)
		},
	}
}

// readSession 读会话最近的文本块。offset 从最新一段往前数，返回按从新到旧排列，
// 每块带上自己的 offset —— 模型据此继续往前翻。
func (e *routerToolEnv) readSession(ctx context.Context, sessionKey string, offset, limit int) (string, error) {
	if e.transcripts == nil {
		return "", errors.New("session reader not configured")
	}
	if e.store == nil {
		return "", errors.New("task store unavailable")
	}
	if e.brief == nil || strings.TrimSpace(e.brief.Task.ID) == "" {
		return "", errors.New("no task for this turn")
	}
	taskID := e.brief.Task.ID
	allowed, err := e.store.TaskRelatedSessionKeys(ctx, taskID)
	if err != nil {
		return "", fmt.Errorf("list task sessions: %w", err)
	}
	if sessionKey == "" {
		if sessionKey, err = e.defaultReadSessionKey(ctx, taskID); err != nil {
			return "", err
		}
	}
	if !containsStringValue(allowed, sessionKey) {
		return "", fmt.Errorf("session %s is not a session of this task", sessionKey)
	}
	blocks, err := e.transcripts.ReadSessionBlocks(ctx, e.brief.Task.RootID, sessionKey)
	if err != nil {
		return "", fmt.Errorf("read session %s: %w", sessionKey, err)
	}
	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != "text" || strings.TrimSpace(block.Text) == "" {
			continue
		}
		texts = append(texts, block.Text)
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 1
	}
	if limit > routerReadSessionMaxLimit {
		limit = routerReadSessionMaxLimit
	}
	payload := make([]map[string]any, 0, limit)
	total := 0
	for back := offset; back < len(texts) && len(payload) < limit; back++ {
		index := len(texts) - 1 - back
		text := truncateRouterText(texts[index], routerReadSessionBlockMaxBytes)
		if len(payload) > 0 && total+len(text) > routerReadSessionTotalMaxBytes {
			break
		}
		payload = append(payload, map[string]any{"offset": back, "text": text})
		total += len(text)
	}
	raw, err := json.Marshal(map[string]any{
		"session_key": sessionKey,
		"text_blocks": len(texts),
		"offset":      offset,
		"blocks":      payload,
	})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// defaultReadSessionKey 缺省读「本任务最近一次节点执行的会话」：节点结果就写在
// 那里，而任务主会话是用户自己看得见的那条。两者都没有时如实报错。
func (e *routerToolEnv) defaultReadSessionKey(ctx context.Context, taskID string) (string, error) {
	task, err := e.store.GetTask(ctx, taskID)
	if err != nil {
		return "", fmt.Errorf("load task: %w", err)
	}
	if run, err := e.store.WorkflowRunForTask(ctx, taskID); err == nil && run != nil {
		if nodeRuns, err := e.store.ListWorkflowNodeRuns(ctx, run.ID); err == nil {
			best, bestAt := "", time.Time{}
			for _, nodeRun := range nodeRuns {
				key := strings.TrimSpace(nodeRun.SessionKey)
				if key == "" {
					continue
				}
				if best == "" || nodeRun.StartedAt.After(bestAt) {
					best, bestAt = key, nodeRun.StartedAt
				}
			}
			if best != "" {
				return best, nil
			}
		}
	}
	if key := strings.TrimSpace(task.MainSessionKey); key != "" {
		return key, nil
	}
	return "", errors.New("task has no session to read")
}

func containsStringValue(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
