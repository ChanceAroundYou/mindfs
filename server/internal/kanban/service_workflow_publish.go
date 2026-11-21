package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// W3 T2：人批准 / 驳回一张「固化 workflow」卡。
//
// 卡片在 Router 回合里由 applyRouteSaveWorkflow 落下，状态停在 waiting_user；
// 到这里才真正写控制面（批准）或什么都不写（驳回）。批准用的是卡片里那份完整
// 请求 —— 连同提出请求时的草稿 revision，所以「批的就是看过的这一版」。

var (
	ErrWorkflowPublishNotFound = errors.New("workflow publish request not found")
	ErrWorkflowPublishDecided  = errors.New("workflow publish request already decided")
)

// ResolveWorkflowPublish 处理一次人工决定。decision 只认 approve / reject。
func (s *Service) ResolveWorkflowPublish(ctx context.Context, rootID, taskID, activityID, decision, note string) (TaskActivity, error) {
	if s == nil {
		return TaskActivity{}, errors.New("kanban service not configured")
	}
	decision = strings.ToLower(strings.TrimSpace(decision))
	if decision != "approve" && decision != "reject" {
		return TaskActivity{}, fmt.Errorf("decision must be %q or %q", "approve", "reject")
	}
	store, err := s.taskStore(rootID)
	if err != nil {
		return TaskActivity{}, err
	}
	task, err := store.GetTask(ctx, strings.TrimSpace(taskID))
	if err != nil {
		return TaskActivity{}, err
	}
	acts := NewActivityStore(store.db)
	act, err := acts.Get(ctx, strings.TrimSpace(activityID))
	if err != nil {
		return TaskActivity{}, fmt.Errorf("%w: %v", ErrWorkflowPublishNotFound, err)
	}
	if act.TaskID != task.ID || act.Type != ActivityTypeWorkflowPublish {
		return TaskActivity{}, ErrWorkflowPublishNotFound
	}
	if act.Status != ActivityStatusWaitingUser {
		return *act, ErrWorkflowPublishDecided
	}

	var card workflowPublishCard
	if err := json.Unmarshal([]byte(act.PayloadJSON), &card); err != nil {
		return *act, fmt.Errorf("workflow publish card payload: %w", err)
	}
	req, err := card.Request.Normalize()
	if err != nil {
		return *act, err
	}

	if decision == "reject" {
		body := "已驳回：这一版工作流没有写入目录。"
		if n := strings.TrimSpace(note); n != "" {
			body += "\n\n附言：" + n
		}
		if err := acts.UpdateBody(ctx, act.ID, body); err != nil {
			return *act, err
		}
		if err := acts.UpdateStatus(ctx, act.ID, ActivityStatusCancelled, nil); err != nil {
			return *act, err
		}
		return s.reloadWorkflowPublishActivity(ctx, store, act.ID)
	}

	// 批准方是人，publishedBy 记到人身上，便于审计「谁让它进的目录」。
	version, err := commitWorkflowPublish(ctx, s.activityServiceForRoot(store.root.ID), req, task.RootID, "user")
	if err != nil {
		if errors.Is(err, ErrWorkflowPublishStale) {
			stale := "批准时草稿已被改动，这一版没有写入。请让 Router 重新起草一次。"
			_ = acts.UpdateBody(ctx, act.ID, stale)
			_ = acts.UpdateStatus(ctx, act.ID, ActivityStatusFailed, nil)
			reloaded, _ := s.reloadWorkflowPublishActivity(ctx, store, act.ID)
			return reloaded, ErrWorkflowPublishStale
		}
		return *act, err
	}
	body := fmt.Sprintf("已批准：工作流「%s」（`%s`）以%s版本 `%s` 发布。", req.Name, req.WorkflowID, scopeLabel(req.Scope), version.Version)
	if n := strings.TrimSpace(note); n != "" {
		body += "\n\n附言：" + n
	}
	if err := acts.UpdateBody(ctx, act.ID, body); err != nil {
		return *act, err
	}
	if err := acts.UpdateStatus(ctx, act.ID, ActivityStatusCompleted, nil); err != nil {
		return *act, err
	}
	return s.reloadWorkflowPublishActivity(ctx, store, act.ID)
}

// activityServiceForRoot 取该 root 的 ActivityService —— 控制面 store 挂在它上面。
func (s *Service) activityServiceForRoot(rootID string) *WorkflowStore {
	svc, err := s.ActivityServiceForRoot(rootID)
	if err != nil || svc == nil {
		return nil
	}
	return svc.workflows
}

// reloadWorkflowPublishActivity 读回落库后的行并广播，让任务详情上的卡当场变样。
func (s *Service) reloadWorkflowPublishActivity(ctx context.Context, store *TaskStore, id string) (TaskActivity, error) {
	act, err := NewActivityStore(store.db).Get(ctx, id)
	if err != nil {
		return TaskActivity{}, err
	}
	s.broadcastWorkflowActivity(ctx, store, act, false, map[string]any{
		"status":       act.Status,
		"body":         act.Body,
		"payload_json": act.PayloadJSON,
	})
	return *act, nil
}
