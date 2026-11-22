package feishu

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Store keeps every card MindFS has sent in one JSON file, rewritten whole on
// each change. The file is small (hundreds of cards at most), and a single
// file is what makes "did we already answer this?" survive a restart.
type Store struct {
	mu    sync.Mutex
	path  string
	cards map[string]Card
	now   func() time.Time
}

type storeFile struct {
	Version int    `json:"version"`
	Cards   []Card `json:"cards"`
}

// NewStore returns a store backed by path. Call Load before use.
func NewStore(path string) *Store {
	return &Store{path: path, cards: map[string]Card{}, now: time.Now}
}

// Load reads the file. A missing file is an empty store; unreadable or
// corrupt files are errors — silently starting empty would forget answers
// that already happened.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.cards = map[string]Card{}
		return nil
	}
	if err != nil {
		return fmt.Errorf("feishu card store %s: %w", s.path, err)
	}
	var f storeFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("feishu card store %s: %w", s.path, err)
	}
	next := make(map[string]Card, len(f.Cards))
	for _, c := range f.Cards {
		if c.ID == "" {
			continue
		}
		// 进程死在「已回执、还没交给 MindFS」之间会留下 applying。没人能再
		// 推进它：之后的点击只会反复补一次「正在处理…」。写回路径本身幂等
		// （gate 重复决定是 no-op，提问重复回答是 ErrQuestionAlreadyAnswered），
		// 所以放回 open 让人再点一次，比永久卡住安全。
		if c.State == StateApplying {
			c.State = StateOpen
			c.Version++
			c.UpdatedAt = s.now()
		}
		next[c.ID] = c
	}
	s.cards = next
	s.pruneExpiredLocked(s.now())
	return s.saveLocked()
}

// PruneExpired drops cards whose TTL has passed, so cards.json does not grow
// with every finished task. A card with a click in flight is kept: the click
// is mid-apply and its acknowledgement still has to be written back.
func (s *Store) PruneExpired(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := len(s.cards)
	if n := s.pruneExpiredLocked(now); n == 0 {
		return 0
	}
	if err := s.saveLocked(); err != nil {
		return 0
	}
	return before - len(s.cards)
}

// pruneExpiredLocked deletes expired cards in memory and reports how many it
// removed; the caller decides whether to persist.
func (s *Store) pruneExpiredLocked(now time.Time) int {
	removed := 0
	for id, c := range s.cards {
		if c.State == StateApplying {
			continue
		}
		if c.Expired(now) {
			delete(s.cards, id)
			removed++
		}
	}
	return removed
}

// Put inserts or replaces a card. An existing card keeps its CreatedAt and
// must have been changed by the caller (Version is bumped here).
func (s *Store) Put(c Card) (Card, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if prev, ok := s.cards[c.ID]; ok {
		if c.CreatedAt.IsZero() {
			c.CreatedAt = prev.CreatedAt
		}
		if c.MessageID == "" {
			c.MessageID = prev.MessageID
		}
		if c.ChatID == "" {
			c.ChatID = prev.ChatID
		}
		if c.Version <= prev.Version {
			c.Version = prev.Version + 1
		}
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	prev, had := s.cards[c.ID]
	s.cards[c.ID] = c
	if err := s.saveLocked(); err != nil {
		s.restoreLocked(c.ID, prev, had)
		return Card{}, err
	}
	return c, nil
}

// Update applies fn under the lock and persists only when fn reports a change.
func (s *Store) Update(id string, fn func(*Card) bool) (Card, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cards[id]
	if !ok {
		return Card{}, false, nil
	}
	before := c
	if !fn(&c) {
		return before, false, nil
	}
	c.ID = id
	c.UpdatedAt = s.now()
	c.Version = before.Version + 1
	s.cards[id] = c
	if err := s.saveLocked(); err != nil {
		s.restoreLocked(id, before, true)
		return Card{}, true, err
	}
	return c, true, nil
}

// MarkDelivered records that the given version is what Feishu now shows.
// It does not bump Version: delivery is bookkeeping, not a content change.
func (s *Store) MarkDelivered(id, messageID string, renderedVersion int) (Card, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cards[id]
	if !ok {
		return Card{}, false, nil
	}
	if messageID != "" {
		c.MessageID = messageID
	}
	c.RenderedVersion = renderedVersion
	c.LastError = ""
	c.DeliveryAttempts = 0
	c.NextAttemptAt = time.Time{}
	c.DeliveryStopped = false
	c.UpdatedAt = s.now()
	before := s.cards[id]
	s.cards[id] = c
	if err := s.saveLocked(); err != nil {
		s.restoreLocked(id, before, true)
		return Card{}, false, err
	}
	return c, true, nil
}

// RestoreOpenError re-applies a user-visible answer error after a successful
// patch. MarkDelivered clears LastError because it tracks transport failures;
// an open retry card must still show why the answer did not reach the worker.
// Version and RenderedVersion are left alone so the card is not re-patched.
func (s *Store) RestoreOpenError(id string, version int, message string) (Card, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cards[id]
	if !ok || c.State != StateOpen || c.Version != version || message == "" {
		return c, false, nil
	}
	before := c
	c.LastError = message
	c.UpdatedAt = s.now()
	s.cards[id] = c
	if err := s.saveLocked(); err != nil {
		s.restoreLocked(id, before, true)
		return Card{}, false, err
	}
	return c, true, nil
}

// RecordDeliveryFailure stores why the last send or patch failed, counts the
// attempt and schedules the next one. Delivery is bookkeeping, not a content
// change, so Version is left alone.
func (s *Store) RecordDeliveryFailure(id, message string, attempts int, nextAt time.Time, stopped bool) (Card, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cards[id]
	if !ok {
		return Card{}, false, nil
	}
	c.LastError = message
	c.DeliveryAttempts = attempts
	c.NextAttemptAt = nextAt
	c.DeliveryStopped = stopped
	c.UpdatedAt = s.now()
	before := s.cards[id]
	s.cards[id] = c
	if err := s.saveLocked(); err != nil {
		s.restoreLocked(id, before, true)
		return Card{}, false, err
	}
	return c, true, nil
}

// restoreLocked puts memory back the way it was when a save failed. Keeping a
// mutation the disk rejected makes the process believe a card was delivered
// (so it never retries) while the file still says otherwise.
func (s *Store) restoreLocked(id string, prev Card, had bool) {
	if had {
		s.cards[id] = prev
		return
	}
	delete(s.cards, id)
}

// Get returns a copy of one card.
func (s *Store) Get(id string) (Card, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cards[id]
	return c, ok
}

// FindOpenPickerByChat returns the live project choice card for a chat, if
// any. Finding one is only an observation: superseding it is the caller's
// decision, so this scan never changes card state.
func (s *Store) FindOpenPickerByChat(chatID string) (Card, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var found Card
	ok := false
	for _, c := range s.cards {
		if c.ChatID != chatID || c.Kind != KindPicker || c.State != StateOpen ||
			c.ExpiresAt.IsZero() || !c.ExpiresAt.After(now) {
			continue
		}
		if !ok || c.CreatedAt.After(found.CreatedAt) {
			found, ok = c, true
		}
	}
	return found, ok
}

// List returns cards ordered oldest first.
func (s *Store) List() []Card {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Card, 0, len(s.cards))
	for _, c := range s.cards {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (s *Store) saveLocked() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	cards := make([]Card, 0, len(s.cards))
	for _, c := range s.cards {
		cards = append(cards, c)
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].CreatedAt.Before(cards[j].CreatedAt) })
	f := storeFile{Version: 1, Cards: cards}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".cards-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.path)
}
