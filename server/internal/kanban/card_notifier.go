package kanban

import (
	"context"
	"log"
	"strconv"
)

// Card kinds a task can push to an external channel. They mirror the message
// kinds the channel renders; kanban must not depend on any one channel's
// package, so the values are neutral strings here and mapped at the edge.
const (
	CardKindGate   = "gate"
	CardKindAsk    = "ask"
	CardKindNotice = "notice"
)

// CardNotice is one message a task wants a person to see outside MindFS.
// AccountID is left to the adapter: a Service instance already belongs to one
// account.
type CardNotice struct {
	// ID is the card's identity across restarts and retries. Sending the same
	// ID twice must not produce a second message.
	ID   string
	Kind string

	RootID     string
	TaskID     string
	TaskTitle  string
	TaskNumber int

	WorkflowNodeID string
	NodeName       string
	QuestionID     string

	Prompt  string
	Status  string
	Summary string
	Options []string
}

// CardNotifier delivers task cards (Feishu and whatever comes later). A nil
// notifier means the channel is off, which is the default.
type CardNotifier interface {
	NotifyCard(ctx context.Context, notice CardNotice) error
}

// SetCardNotifier installs the outbound channel. It is set once at startup.
func (s *Service) SetCardNotifier(n CardNotifier) {
	s.cardNotifier = n
}

// notifyCard is the single call site rule for task cards: a channel failure
// is logged and never changes task state or blocks the caller.
func (s *Service) notifyCard(ctx context.Context, n CardNotice) {
	if s.cardNotifier == nil {
		return
	}
	if err := s.cardNotifier.NotifyCard(ctx, n); err != nil {
		log.Printf("[kanban/cards] %s %s: %v", n.Kind, n.ID, err)
	}
}

// taskTitle is the name a person recognizes the task by. Tasks created from a
// first message have no Name until later, so fall back to the number.
func taskTitle(t Task) string {
	if t.Name != "" {
		return t.Name
	}
	if t.TaskNumber > 0 {
		return "#" + strconv.Itoa(t.TaskNumber)
	}
	return t.ID
}

// notifyGateCard turns a materialized waiting gate into one card. The id is
// the NodeRun: a gate waits once, and re-materializing the same attempt must
// not page anyone again.
func (s *Service) notifyGateCard(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode, nr NodeRun, prompt string) {
	if s.cardNotifier == nil {
		return
	}
	task, err := store.GetTask(ctx, run.TaskID)
	if err != nil {
		log.Printf("[kanban/cards] gate %s: load task %s: %v", nr.ID, run.TaskID, err)
		return
	}
	name := node.Name
	if name == "" {
		name = node.ID
	}
	s.notifyCard(ctx, CardNotice{
		ID: "gate-" + nr.ID, Kind: CardKindGate,
		RootID: task.RootID, TaskID: task.ID, TaskTitle: taskTitle(task), TaskNumber: task.TaskNumber,
		WorkflowNodeID: node.ID, NodeName: name,
		Prompt: prompt, Options: []string{"批准", "驳回"},
	})
}
