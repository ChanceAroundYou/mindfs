package kanban

import (
	"context"
	"errors"
	"testing"

	rootfs "mindfs/server/internal/fs"
)

func newCommentTestStore(t *testing.T) *TaskStore {
	t.Helper()
	store, err := NewTaskStore(rootfs.NewRootInfo("test", "test", t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func seedTask(t *testing.T, s *TaskStore, id, status string) {
	t.Helper()
	now := s.now().UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	task := Task{ID: id, RootID: "test", Status: status, CreatedAt: s.now(), UpdatedAt: s.now()}
	_ = now
	if _, err := s.CreateTask(context.Background(), task, StageRun{}, TaskEvent{}); err != nil {
		t.Fatal(err)
	}
}

func TestPostCommentCreatesActivityAndContinueRequest(t *testing.T) {
	s := newCommentTestStore(t)
	seedTask(t, s, "task-1", StatusRunning)
	act, req, err := s.PostComment(context.Background(), PostCommentInput{
		TaskID: "task-1", CommentID: "c-1", Author: "alice", Role: RoleUser, Body: "hello",
		WorkflowChoice: CommentWorkflowChoiceEmpty,
	})
	if err != nil {
		t.Fatal(err)
	}
	if act.ID != "c-1" || req.CommentID != "c-1" || req.Status != ContinueRequestQueued {
		t.Fatalf("unexpected act=%+v req=%+v", act, req)
	}
	list, err := s.ListActivities(context.Background(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 activity got %d", len(list))
	}
}

func TestPostCommentIdempotentSamePayload(t *testing.T) {
	s := newCommentTestStore(t)
	seedTask(t, s, "task-1", StatusRunning)
	in := PostCommentInput{TaskID: "task-1", CommentID: "c-1", Author: "alice", Role: RoleUser, Body: "hi"}
	a1, r1, err := s.PostComment(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	a2, r2, err := s.PostComment(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if a1.ID != a2.ID || r1.RequestID != r2.RequestID {
		t.Fatalf("idempotent mismatch")
	}
	list, _ := s.ListActivities(context.Background(), "task-1")
	if len(list) != 1 {
		t.Fatalf("want 1 got %d", len(list))
	}
}

func TestPostCommentConflictDifferentBody(t *testing.T) {
	s := newCommentTestStore(t)
	seedTask(t, s, "task-1", StatusRunning)
	_, _, err := s.PostComment(context.Background(), PostCommentInput{TaskID: "task-1", CommentID: "c-1", Author: "alice", Body: "one"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.PostComment(context.Background(), PostCommentInput{TaskID: "task-1", CommentID: "c-1", Author: "alice", Body: "two"})
	if !errors.Is(err, ErrCommentConflict) {
		t.Fatalf("want conflict got %v", err)
	}
}

func TestPostCommentTerminalTaskBlocked(t *testing.T) {
	s := newCommentTestStore(t)
	seedTask(t, s, "task-1", StatusSuccess)
	_, req, err := s.PostComment(context.Background(), PostCommentInput{TaskID: "task-1", CommentID: "c-1", Author: "alice", Body: "late"})
	if err != nil {
		t.Fatal(err)
	}
	if req.Status != ContinueRequestBlocked {
		t.Fatalf("want blocked got %s", req.Status)
	}
}

func TestPostCommentWorkflowChoiceValidation(t *testing.T) {
	s := newCommentTestStore(t)
	seedTask(t, s, "task-1", StatusRunning)
	for _, choice := range []string{CommentWorkflowChoiceSpecified, CommentWorkflowChoiceDisabled, CommentWorkflowChoiceEmpty} {
		_, _, err := s.PostComment(context.Background(), PostCommentInput{TaskID: "task-1", CommentID: "c-" + choice, Author: "a", Body: "b", WorkflowChoice: choice, WorkflowVersionID: "wf-v1"})
		if err != nil {
			t.Fatalf("choice %s: %v", choice, err)
		}
	}
	_, _, err := s.PostComment(context.Background(), PostCommentInput{TaskID: "task-1", CommentID: "c-bad", Author: "a", Body: "b", WorkflowChoice: "nonsense"})
	if err == nil {
		t.Fatal("want validation error")
	}
	// specified requires version id
	_, _, err = s.PostComment(context.Background(), PostCommentInput{TaskID: "task-1", CommentID: "c-bad2", Author: "a", Body: "b", WorkflowChoice: CommentWorkflowChoiceSpecified})
	if err == nil {
		t.Fatal("want error for specified without version")
	}
}
