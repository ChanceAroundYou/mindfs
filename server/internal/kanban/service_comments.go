package kanban

import (
	"context"
	"strings"
)

type PostCommentServiceInput struct {
	RootID            string
	TaskID            string
	CommentID         string
	Author            string
	Role              string
	Body              string
	Type              string
	WorkflowChoice    string
	WorkflowVersionID string
	ExpectedNodeRunID string
}

func (s *Service) PostComment(ctx context.Context, in PostCommentServiceInput) (TaskActivity, ContinueRequest, error) {
	store, err := s.taskStore(in.RootID)
	if err != nil {
		return TaskActivity{}, ContinueRequest{}, err
	}
	act, req, err := store.PostComment(ctx, PostCommentInput{
		TaskID: in.TaskID, CommentID: in.CommentID, Author: in.Author, Role: in.Role,
		Body: strings.TrimSpace(in.Body), Type: in.Type,
		WorkflowChoice: in.WorkflowChoice, WorkflowVersionID: in.WorkflowVersionID,
		ExpectedNodeRunID: in.ExpectedNodeRunID,
	})
	if err != nil {
		return act, req, err
	}
	// D3/D1: a comment submitted while the task has no active Agent NodeRun is
	// the start of a new execution phase. The comment itself stays pending_phase
	// (never retroactively bound to the node the driver is about to start).
	if req.Status == ContinueRequestPendingPhase {
		_ = s.WakeWorkflowRun(ctx, in.RootID, in.TaskID, "comment")
	}
	return act, req, nil
}

func (s *Service) ListTaskComments(ctx context.Context, rootID, taskID string) ([]TaskActivity, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return nil, err
	}
	if _, err := store.GetTask(ctx, taskID); err != nil {
		return nil, err
	}
	return store.ListActivities(ctx, taskID)
}
