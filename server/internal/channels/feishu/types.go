// Package feishu turns MindFS decisions into Feishu cards and carries the
// owner's click back. It talks to Feishu only through lark-cli, which keeps
// the app credentials and the long connection; MindFS holds neither.
package feishu

import "time"

// Kind is what a card asks of its reader.
type Kind string

const (
	// KindGate is a workflow human gate: approve or reject.
	KindGate Kind = "gate"
	// KindAsk is a Router question: pick an option or write an answer.
	KindAsk Kind = "ask"
	// KindNotice reports an outcome and asks for nothing.
	KindNotice Kind = "notice"
)

// State is where a card stands in its own life.
type State string

const (
	StateOpen       State = "open"
	StateApplying   State = "applying"
	StateDone       State = "done"
	StateFailed     State = "failed"
	StateExpired    State = "expired"
	StateSuperseded State = "superseded"
)

// Choice is one answer a card offers. Value is what the click means to the
// caller ("approve"/"reject" for gates, "option-<i>" for questions).
type Choice struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Card is one message MindFS sent, and everything needed to act on a click
// that comes back later — possibly after a restart.
type Card struct {
	ID         string `json:"id"`
	Kind       Kind   `json:"kind"`
	State      State  `json:"state"`
	AccountID  string `json:"account_id,omitempty"`
	RootID     string `json:"root_id,omitempty"`
	NodeID     string `json:"node_id,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
	TaskTitle  string `json:"task_title,omitempty"`
	TaskNumber int    `json:"task_number,omitempty"`

	WorkflowNodeID string `json:"workflow_node_id,omitempty"`
	NodeName       string `json:"node_name,omitempty"`
	QuestionID     string `json:"question_id,omitempty"`

	Prompt   string   `json:"prompt,omitempty"`
	Status   string   `json:"status,omitempty"`
	Summary  string   `json:"summary,omitempty"`
	Text     string   `json:"text,omitempty"`
	Note     string   `json:"note,omitempty"`
	Choices  []Choice `json:"choices,omitempty"`
	Selected int      `json:"selected"`

	MessageID string `json:"message_id,omitempty"`
	ChatID    string `json:"chat_id,omitempty"`
	LastError string `json:"last_error,omitempty"`

	// DeliveryAttempts counts consecutive failed sends/patches since the last
	// success; NextAttemptAt holds the card back until its backoff ends;
	// DeliveryStopped means an unsent card burned its attempts and was given up.
	DeliveryAttempts int       `json:"delivery_attempts,omitempty"`
	NextAttemptAt    time.Time `json:"next_attempt_at,omitempty"`
	DeliveryStopped  bool      `json:"delivery_stopped,omitempty"`

	Version         int `json:"version"`
	RenderedVersion int `json:"rendered_version"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Expired reports whether the card may no longer act on a click.
func (c Card) Expired(now time.Time) bool {
	return !c.ExpiresAt.IsZero() && now.After(c.ExpiresAt)
}

// Chosen returns the label the card settled on, if any.
func (c Card) Chosen() string {
	if c.Selected >= 0 && c.Selected < len(c.Choices) {
		return c.Choices[c.Selected].Label
	}
	return ""
}
