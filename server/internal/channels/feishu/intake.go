package feishu

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// intakeMaxInputBytes is the hard cap on a message forwarded into a task.
// Beyond this a single Feishu message would dominate the model's context.
const intakeMaxInputBytes = 32 * 1024

// TaskCreator turns a resolved (node, project, text) triple into a task. The
// app layer owns the actual task store and cross-node HTTP call; the channel
// layer only needs the resulting task id for the owner's receipt.
type TaskCreator interface {
	Create(ctx context.Context, nodeID, rootID, input string) (taskID string, err error)
}

// RemoteTaskBody is the JSON body for POST /api/tasks used by the Feishu
// inbound creator. workflow_choice=empty routes the first user message through
// the router instead of pinning a workflow; start_immediately=true makes the
// task start in the same request. Keeping the shape here lets the channel tests
// pin the wire contract without starting an HTTP server.
func RemoteTaskBody(rootID, input, name string) ([]byte, error) {
	return json.Marshal(struct {
		RootID           string `json:"root_id"`
		Input            string `json:"input"`
		Name             string `json:"name"`
		WorkflowChoice   string `json:"workflow_choice"`
		StartImmediately bool   `json:"start_immediately"`
	}{
		RootID:           rootID,
		Input:            input,
		Name:             name,
		WorkflowChoice:   "empty",
		StartImmediately: true,
	})
}

// IntakeService handles owner p2p text: dedup, rate limit, deterministic
// project resolution, task creation (or a bounded project picker) and a
// visible receipt for every outcome. Non-owner, group and non-text events are
// ignored before any of that work.
type IntakeService struct {
	cfg      Config
	catalog  ProjectCatalog
	store    *IntakeStore
	creator  TaskCreator
	notify   func(context.Context, Notice) error
	clock    func() time.Time
	limiter  func(string) bool
	fallback *inboundLimiter

	mu           sync.Mutex
	pickerFinder func(string, time.Time) (Card, bool)
	targetFinder func(string, time.Time) (Card, bool)
	appender     TaskAppender
	tasks        TaskCatalog
}

// NewIntakeService builds the inbound service. A nil limiter means the
// default 30 messages/minute with a burst of 10 per chat. A nil clock means
// time.Now.
func NewIntakeService(cfg Config, catalog ProjectCatalog, store *IntakeStore, creator TaskCreator,
	notify func(context.Context, Notice) error, clock func() time.Time, limiter func(string) bool) *IntakeService {
	if clock == nil {
		clock = time.Now
	}
	s := &IntakeService{
		cfg: cfg, catalog: catalog, store: store, creator: creator,
		notify: notify, clock: clock, limiter: limiter,
	}
	if limiter == nil {
		s.fallback = newInboundLimiter(30, 10, clock)
	}
	return s
}

// SetPickerFinder installs the one-open-picker-per-chat check, normally backed
// by the card Store. It is optional so unit tests and future transports can
// build the service without a card store.
func (s *IntakeService) SetPickerFinder(fn func(string, time.Time) (Card, bool)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pickerFinder = fn
}

func (s *IntakeService) openPicker(chatID string) (Card, bool) {
	s.mu.Lock()
	fn := s.pickerFinder
	s.mu.Unlock()
	if fn == nil {
		return Card{}, false
	}
	return fn(chatID, s.clock())
}

// Handle processes one parsed inbound message. A returned error is only for
// the supervisor's log: the owner has already been told what happened, and
// redelivering the same message will be swallowed by the dedup store.
func (s *IntakeService) Handle(ctx context.Context, msg InboundMessage) error {
	if msg.ChatType != "p2p" || msg.SenderOpenID != s.cfg.OwnerOpenID || strings.TrimSpace(msg.Text) == "" {
		return nil
	}
	id := inboundID(msg)
	if id == "" {
		return nil
	}
	// Claim before the first side effect: two redeliveries must not both pass
	// Seen() and create two tasks. Every terminal path below advances the
	// reservation to a final status, so a replay is swallowed by Claim.
	claimed, err := s.store.Claim(id)
	if err != nil {
		n := Notice{Kind: KindNotice, Text: "暂时无法记录消息，请稍后再试。", SourceMessageID: id, ChatID: msg.ChatID, NodeID: s.cfg.NodeID}
		return errors.Join(err, s.notifyWithCardID(ctx, id, n))
	}
	if !claimed {
		return nil
	}
	if !s.allowed(msg.ChatID) {
		return s.reject(ctx, msg, "", "rate_limited", "消息太多，请稍后再试。")
	}

	text, truncated := clampInput(msg.Text)
	res, err := ResolveProject(text, msg.ChatID, s.cfg.Bindings, s.catalog, s.cfg.DefaultRootID, s.cfg.PickerMax)
	if err != nil {
		return s.fail(ctx, msg, "", "catalog_error", "暂时查不到项目，请稍后再试。", err)
	}

	notice := Notice{SourceMessageID: id, ChatID: msg.ChatID, NodeID: s.cfg.NodeID, IntakeText: text}
	switch res.Kind {
	case ResolutionProject:
		// A uniquely resolved project is not yet permission to write: append to
		// the chat's confirmed target, or ask the owner to confirm on a card.
		return s.resolveUniquelyNamedTarget(ctx, msg, id, res.Project, text, truncated)
	case ResolutionPicker:
		if _, ok := s.openPicker(msg.ChatID); ok {
			return s.reject(ctx, msg, "", "picker_pending", "你还有一张未回复的项目选择卡，请先完成选择。")
		}
		projects := make([]PickerProject, 0, len(res.Candidates))
		for _, ref := range res.Candidates {
			nodeID := ref.NodeID
			if nodeID == "" {
				nodeID = s.cfg.NodeID
			}
			projects = append(projects, PickerProject{NodeID: nodeID, RootID: ref.RootID, Label: pickerLabel(ref, s.cfg.NodeID)})
		}
		notice.Kind = KindPicker
		notice.Text = text
		notice.Projects = projects
		notice.PickerTTL = s.pickerTTL()
		if err := s.notifyWithCardID(ctx, id, notice); err != nil {
			// 卡片没发出去，owner 什么都没看到：放开占坑，让同一条消息的
			// 重投还能再试一次，而不是被去重记录静默吞掉。
			if relErr := s.store.Release(id); relErr != nil {
				return fmt.Errorf("%w（且未能释放去重占坑：%v）", err, relErr)
			}
			return err
		}
		return s.store.SetStatus(id, "", "picker")
	case ResolutionAnswer:
		return s.reject(ctx, msg, "", "answer", "没有找到对应项目。请在消息里写项目名（必要时带节点前缀，如 #fn/docs）；候选超过 5 个时请写得更具体。")
	default:
		return s.fail(ctx, msg, "", "failed", "项目解析结果未知，请稍后重试。", fmt.Errorf("feishu intake: unknown resolution %q", res.Kind))
	}
}

func (s *IntakeService) allowed(chatID string) bool {
	if s.limiter != nil {
		return s.limiter(chatID)
	}
	return s.fallback.Allow(chatID)
}

func (s *IntakeService) pickerTTL() time.Duration {
	if s.cfg.PickerTTL > 0 {
		return s.cfg.PickerTTL
	}
	return PickerTTL
}

func (s *IntakeService) notifyWithCardID(ctx context.Context, messageID string, n Notice) error {
	if n.CardID == "" {
		sum := sha256.Sum256([]byte(messageID + "\x00" + string(n.Kind)))
		n.CardID = "intake-" + hex.EncodeToString(sum[:])[:16]
	}
	return s.notify(ctx, n)
}

// reject is a handled outcome: the owner sees why, the message is deduped,
// and the supervisor does not log an error.
func (s *IntakeService) reject(ctx context.Context, msg InboundMessage, taskID, status, text string) error {
	id := inboundID(msg)
	if err := s.store.SetStatus(id, taskID, status); err != nil {
		return err
	}
	n := Notice{Kind: KindNotice, Text: text, SourceMessageID: id, ChatID: msg.ChatID, NodeID: s.cfg.NodeID}
	return s.notifyWithCardID(ctx, id, n)
}

// fail is an unexpected error: the owner sees a receipt, the supervisor gets
// the error, and the message is deduped so a replay cannot double-notify.
func (s *IntakeService) fail(ctx context.Context, msg InboundMessage, taskID, status, text string, cause error) error {
	id := inboundID(msg)
	if err := s.store.SetStatus(id, taskID, status); err != nil {
		return errors.Join(cause, err)
	}
	n := Notice{Kind: KindNotice, Text: text, SourceMessageID: id, ChatID: msg.ChatID, NodeID: s.cfg.NodeID}
	if err := s.notifyWithCardID(ctx, id, n); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func inboundID(msg InboundMessage) string {
	if id := strings.TrimSpace(msg.MessageID); id != "" {
		return id
	}
	return strings.TrimSpace(msg.EventID)
}

func clampInput(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if len(text) <= intakeMaxInputBytes {
		return text, false
	}
	cut := intakeMaxInputBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "\n\n（消息超过 32 KiB，后续内容已截断）", true
}

func pickerLabel(ref ProjectRef, localNode string) string {
	if strings.Contains(ref.Name, " · ") {
		return ref.Name
	}
	node := ref.NodeID
	if node == "" {
		node = localNode
	}
	if node == "" {
		return ref.Name
	}
	return ref.Name + " · " + node
}

// NewInboundLimiter returns a per-chat rate check with the given defaults.
// Zero/negative values fall back to 30 per minute with a burst of 10.
func NewInboundLimiter(perMinute, burst int, clock func() time.Time) func(string) bool {
	if perMinute <= 0 {
		perMinute = 30
	}
	if burst <= 0 {
		burst = 10
	}
	return newInboundLimiter(perMinute, burst, clock).Allow
}

// inboundLimiter is a small per-chat token bucket used when the caller does
// not inject one (i.e. in production). It is intentionally not persisted: a
// restart refills the bucket, while the dedup ledger still stops replays.
type inboundLimiter struct {
	mu      sync.Mutex
	buckets map[string]inboundBucket
	rate    float64
	burst   float64
	clock   func() time.Time
}

type inboundBucket struct {
	tokens float64
	at     time.Time
}

func newInboundLimiter(perMinute, burst int, clock func() time.Time) *inboundLimiter {
	return &inboundLimiter{
		buckets: map[string]inboundBucket{}, rate: float64(perMinute) / 60,
		burst: float64(burst), clock: clock,
	}
}

func (l *inboundLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock()
	b := l.buckets[key]
	if b.at.IsZero() {
		b.tokens, b.at = l.burst, now
	}
	b.tokens += now.Sub(b.at).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.at = now
	if b.tokens < 1 {
		l.buckets[key] = b
		return false
	}
	b.tokens--
	l.buckets[key] = b
	return true
}
