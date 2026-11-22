package feishu

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"sync"
	"time"
)

// Delivery retries: a failed card waits out an exponential backoff instead of
// being re-sent on every tick, and an unsent card stops after a bounded number
// of attempts (nothing is behind it that a reader could click).
const (
	deliveryAttemptCap  = 12
	deliveryBackoffBase = 5 * time.Second
	deliveryBackoffCap  = 10 * time.Minute
)

// idempotencyKey dedupes retries of one card upstream. Feishu caps the key at
// 50 characters and terminal/scheduled card ids are longer than that on their
// own, so the key is a digest: deterministic per card, never over the cap.
func idempotencyKey(cardID string) string {
	sum := sha256.Sum256([]byte(cardID))
	return "mindfs-" + hex.EncodeToString(sum[:])[:32]
}

// deliveryBackoff is exponential from deliveryBackoffBase, capped so a long
// outage still retries a few times an hour instead of never.
func deliveryBackoff(attempts int) time.Duration {
	d := deliveryBackoffBase
	for i := 1; i < attempts; i++ {
		if d >= deliveryBackoffCap {
			return deliveryBackoffCap
		}
		d *= 2
	}
	if d > deliveryBackoffCap {
		return deliveryBackoffCap
	}
	return d
}

// Config is what the channel needs from MindFS configuration. Nothing here
// is a credential: lark-cli owns those.
type Config struct {
	// OwnerOpenID is the only person whose clicks count.
	OwnerOpenID string
	// LarkCLIPath defaults to "lark-cli" on PATH.
	LarkCLIPath string
	// NodeID names the machine that sent the card, so a click can be traced.
	// It comes from the root-level feishu_node_id and also labels local
	// projects in the intake catalog.
	NodeID string
	// RelayToken is shared with workers; the relay HTTP layer owns its checks.
	RelayToken string
	// Bindings pin a chat to a project; the first matching binding wins.
	Bindings []Binding
	// DefaultRootID is only a fallback when text names no unique project.
	DefaultRootID string
	// PickerTTL / PickerMax bound the project choice card.
	PickerTTL time.Duration
	PickerMax int
	// CardTTL is how long a card may still act. Zero means 168h.
	CardTTL time.Duration
	// DeliverInterval is how often unsent or stale cards are re-sent.
	DeliverInterval time.Duration

	Now  func() time.Time
	Logf func(string, ...any)
}

// Notice is a request to send (or refresh) a card. The ID is the card's
// identity across restarts: sending the same ID twice is not a second card.
type Notice struct {
	CardID string
	Kind   Kind

	AccountID  string
	RootID     string
	NodeID     string
	TaskID     string
	TaskTitle  string
	TaskNumber int

	WorkflowNodeID string
	NodeName       string
	QuestionID     string

	Prompt  string
	Status  string
	Summary string
	Text    string
	Options []string

	// Intake fields travel with a project choice card so the click can create
	// the task for the original message even after a restart.
	SourceMessageID string
	ChatID          string
	IntakeText      string
	Projects        []PickerProject
	PickerTTL       time.Duration

	// Explicit target confirmation: Target is where the message would go,
	// Candidates are the other tasks it could join, ReviewHits are review
	// categories the project matched, and TargetMode is what 「确认」 will do
	// ("append" to Target.TaskID or "create" a new task).
	Target     *TargetRef
	Candidates []TargetRef
	ReviewHits []string
	TargetMode string
}

// Answer is what a click meant, in MindFS terms rather than Feishu ones.
type Answer struct {
	CardID string
	Kind   Kind

	AccountID      string
	RootID         string
	NodeID         string
	TaskID         string
	WorkflowNodeID string
	QuestionID     string

	// Decision is "approve" or "reject" for a gate.
	Decision string
	// Text is the ask answer (chosen label or the words typed).
	Text string
	// Note is the handling note on a notice card.
	Note string
}

// RetryableAnswerError marks a temporary answer-delivery failure. The relay
// wraps network/5xx/deadline failures in this type so the Service can keep the
// card open for another click; deterministic local errors stay plain errors.
type RetryableAnswerError interface {
	error
	RetryableAnswer() bool
}

// Service owns the cards: it sends them, keeps them current, and turns clicks
// into answers. It never blocks MindFS on Feishu being reachable.
type Service struct {
	cfg   Config
	store *Store
	cli   *CLI
	now   func() time.Time
	logf  func(string, ...any)
	ttl   time.Duration
	tick  time.Duration

	mu             sync.Mutex
	handler        func(context.Context, Answer) error
	messageHandler func(context.Context, []byte) error
	intake         *IntakeService
	started        bool

	// loops counts the goroutines Start launched. Both write to disk (card
	// store, lark-cli), so shutdown must be able to see them finish; Wait is
	// what makes "ctx is done" mean "nothing is still writing".
	loops sync.WaitGroup
}

// New builds a service over an already-loaded store.
func New(cfg Config, store *Store, cli *CLI) *Service {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	logf := cfg.Logf
	if logf == nil {
		logf = log.Printf
	}
	ttl := cfg.CardTTL
	if ttl <= 0 {
		ttl = 168 * time.Hour
	}
	tick := cfg.DeliverInterval
	if tick <= 0 {
		tick = 2 * time.Second
	}
	if cli == nil {
		cli = &CLI{}
	}
	if cli.Path == "" {
		cli.Path = "lark-cli"
	}
	return &Service{cfg: cfg, store: store, cli: cli, now: now, logf: logf, ttl: ttl, tick: tick}
}

// Store exposes the card store for the HTTP layer and tests.
func (s *Service) Store() *Store { return s.store }

// SetAnswerHandler installs what a click does. It may be nil, in which case
// clicks are still acknowledged and recorded but change nothing.
func (s *Service) SetAnswerHandler(fn func(context.Context, Answer) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handler = fn
}

// SetMessageHandler installs what an inbound chat message does. It receives
// the raw event line so intake owns parsing, dedupe and policy. A nil handler
// is a no-op: the listener still runs but drops the messages.
func (s *Service) SetMessageHandler(fn func(context.Context, []byte) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messageHandler = fn
}

// SetIntake installs the owner-p2p inbound service on the message supervisor
// callback. An explicitly installed raw message handler still wins, so an
// embedding app can replace the whole pipeline (tests and future transports).
func (s *Service) SetIntake(service *IntakeService) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.intake = service
}

func (s *Service) handleRawMessage(ctx context.Context, line []byte) error {
	s.mu.Lock()
	fn, intake := s.messageHandler, s.intake
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx, line)
	}
	if intake == nil {
		return nil
	}
	msg, ok := ParseMessage(line)
	if !ok {
		return nil
	}
	return intake.Handle(ctx, msg)
}

func (s *Service) answerHandler() func(context.Context, Answer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.handler
}

// Notify records a card to send. The same CardID is a no-op: a retry of the
// same event must not produce a second message.
func (s *Service) Notify(_ context.Context, n Notice) error {
	if n.CardID == "" {
		return fmt.Errorf("feishu: card id is required")
	}
	if _, ok := s.store.Get(n.CardID); ok {
		return nil
	}
	now := s.now()
	expires := now.Add(s.ttl)
	if (n.Kind == KindPicker || n.Kind == KindTarget) && n.PickerTTL > 0 {
		expires = now.Add(n.PickerTTL)
	}
	c := Card{
		ID:              n.CardID,
		Kind:            n.Kind,
		State:           StateOpen,
		AccountID:       n.AccountID,
		RootID:          n.RootID,
		NodeID:          n.NodeID,
		TaskID:          n.TaskID,
		TaskTitle:       n.TaskTitle,
		TaskNumber:      n.TaskNumber,
		WorkflowNodeID:  n.WorkflowNodeID,
		NodeName:        n.NodeName,
		QuestionID:      n.QuestionID,
		Prompt:          n.Prompt,
		Status:          n.Status,
		Summary:         n.Summary,
		Text:            n.Text,
		Choices:         choicesFor(n),
		Selected:        -1,
		Version:         1,
		CreatedAt:       now,
		UpdatedAt:       now,
		ExpiresAt:       expires,
		SourceMessageID: n.SourceMessageID,
		ChatID:          n.ChatID,
		IntakeText:      n.IntakeText,
		Projects:        n.Projects,
		Target:          n.Target,
		Candidates:      n.Candidates,
		ReviewHits:      n.ReviewHits,
		TargetMode:      n.TargetMode,
	}
	_, err := s.store.Put(c)
	return err
}

func choicesFor(n Notice) []Choice {
	switch n.Kind {
	case KindGate:
		labels := n.Options
		if len(labels) != 2 {
			labels = []string{"批准", "驳回"}
		}
		return []Choice{{Label: labels[0], Value: "approve"}, {Label: labels[1], Value: "reject"}}
	case KindAsk:
		out := make([]Choice, 0, len(n.Options))
		for i, label := range n.Options {
			out = append(out, Choice{Label: label, Value: fmt.Sprintf("option-%d", i)})
		}
		return out
	case KindNotice:
		return []Choice{{Label: "知道了", Value: "ack"}}
	case KindTarget:
		// 「确认」 honours the card's prefilled task (or creates one); the other
		// three force a path so a stale prefill cannot trap the owner.
		return []Choice{
			{Label: "确认", Value: "confirm"},
			{Label: "新建任务", Value: "create"},
			{Label: "切换目标", Value: "switch"},
			{Label: "取消", Value: "cancel"},
		}
	default:
		return nil
	}
}

// Start runs delivery and both event listeners until ctx ends. It returns at
// once; the loops are goroutines so a broken lark-cli never delays startup.
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()

	s.loops.Add(3)
	go func() {
		defer s.loops.Done()
		s.deliverLoop(ctx)
	}()
	sup := &Supervisor{Path: s.cli.Path, Handle: s.HandleAction, Logf: s.logf}
	go func() {
		defer s.loops.Done()
		sup.Run(ctx)
	}()
	// Feishu needs a separate long connection per event type. The two
	// supervisors keep separate stdin pipes and never hand a chat message to
	// the click handler.
	messages := &Supervisor{Path: s.cli.Path, EventType: "im.message.receive_v1",
		HandleRaw: s.handleRawMessage, Logf: s.logf}
	go func() {
		defer s.loops.Done()
		messages.Run(ctx)
	}()
}

// Wait blocks until every loop Start launched has returned. Callers cancel the
// ctx they passed to Start first; Wait then guarantees no delivery, no patch,
// no click handling and no chat handling is still in flight — needed before
// shutdown, and by tests whose temp dirs must not be written behind their back.
func (s *Service) Wait() {
	s.loops.Wait()
}

func (s *Service) deliverLoop(ctx context.Context) {
	ticker := time.NewTicker(s.tick)
	defer ticker.Stop()
	for {
		if err := s.DeliverOnce(ctx); err != nil && ctx.Err() == nil {
			s.logf("飞书卡片投递失败：%v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// DeliverOnce sends every card that has never reached Feishu, and patches
// every card whose state moved since the message was last rendered. A failed
// card is held back until its backoff ends; one that keeps failing to send is
// given up on rather than retried forever.
func (s *Service) DeliverOnce(ctx context.Context) error {
	// 过期卡片在这里顺手清掉：没人清的话状态文件随任务数一直长，而每一拍
	// 都要在锁里重写整个文件。
	s.store.PruneExpired(s.now())
	for _, c := range s.store.List() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if c.DeliveryStopped || s.now().Before(c.NextAttemptAt) {
			continue
		}
		if c.MessageID == "" {
			s.sendCard(ctx, c)
			continue
		}
		if c.RenderedVersion != c.Version {
			s.patchCard(ctx, c.ID)
		}
	}
	return nil
}

func (s *Service) sendCard(ctx context.Context, c Card) {
	content, err := RenderJSON(c)
	if err != nil {
		s.recordError(c.ID, err)
		return
	}
	id, err := s.cli.SendInteractive(ctx, s.cfg.OwnerOpenID, content, idempotencyKey(c.ID))
	if err != nil {
		s.recordError(c.ID, err)
		return
	}
	if _, _, err := s.store.MarkDelivered(c.ID, id, c.Version); err != nil {
		s.logf("飞书记下卡片 %s 的 message_id 失败：%v", c.ID, err)
	}
}

// patchCard re-renders one card in place. Cards never sent are left to
// DeliveryOnce, which sends their current state instead of patching nothing.
func (s *Service) patchCard(ctx context.Context, id string) error {
	c, ok := s.store.Get(id)
	if !ok || c.MessageID == "" {
		return nil
	}
	content, err := RenderJSON(c)
	if err != nil {
		s.recordError(id, err)
		return err
	}
	if err := s.cli.PatchInteractive(ctx, c.MessageID, content); err != nil {
		s.recordError(id, err)
		return err
	}
	if _, _, err := s.store.MarkDelivered(id, c.MessageID, c.Version); err != nil {
		return err
	}
	return nil
}

// recordError books a failed send or patch: it counts the attempt, schedules
// the retry, and gives up on a card that never reached Feishu — there is no
// message behind it, so nothing a reader could have clicked is lost.
func (s *Service) recordError(id string, err error) {
	msg := oneLine(err.Error())
	s.logf("飞书卡片 %s：%s", id, msg)
	c, ok := s.store.Get(id)
	if !ok {
		return
	}
	attempts := c.DeliveryAttempts + 1
	stopped := c.MessageID == "" && attempts >= deliveryAttemptCap
	if stopped {
		s.logf("飞书卡片 %s 连续 %d 次发送失败，停止重试（卡片保留到过期）", id, attempts)
	}
	next := s.now().Add(deliveryBackoff(attempts))
	if _, _, uerr := s.store.RecordDeliveryFailure(id, msg, attempts, next, stopped); uerr != nil {
		s.logf("飞书记下卡片 %s 的错误失败：%v", id, uerr)
	}
}
