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
}

func (s *Service) PostComment(ctx context.Context, in PostCommentServiceInput) (TaskActivity, ContinueRequest, error) {
	store, err := s.taskStore(in.RootID)
	if err != nil {
		return TaskActivity{}, ContinueRequest{}, err
	}
	return store.PostComment(ctx, PostCommentInput{
		TaskID: in.TaskID, CommentID: in.CommentID, Author: in.Author, Role: in.Role,
		Body: strings.TrimSpace(in.Body), Type: in.Type,
		WorkflowChoice: in.WorkflowChoice, WorkflowVersionID: in.WorkflowVersionID,
	})
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
