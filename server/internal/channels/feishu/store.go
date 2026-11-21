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
		next[c.ID] = c
	}
	s.cards = next
	return nil
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
	s.cards[c.ID] = c
	if err := s.saveLocked(); err != nil {
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
	c.UpdatedAt = s.now()
	s.cards[id] = c
	if err := s.saveLocked(); err != nil {
		return Card{}, false, err
	}
	return c, true, nil
}

// RecordDeliveryError stores why the last send or patch failed, without
// pretending the card's content changed.
func (s *Store) RecordDeliveryError(id, message string) (Card, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cards[id]
	if !ok {
		return Card{}, false, nil
	}
	c.LastError = message
	c.UpdatedAt = s.now()
	s.cards[id] = c
	if err := s.saveLocked(); err != nil {
		return Card{}, false, err
	}
	return c, true, nil
}

// Get returns a copy of one card.
func (s *Store) Get(id string) (Card, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cards[id]
	return c, ok
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
