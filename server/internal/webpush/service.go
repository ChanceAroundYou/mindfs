package webpush

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"mindfs/server/internal/notify"
)

type Service struct {
	config Config
	store  *Store

	mu     sync.Mutex
	recent map[string]time.Time
}

func NewService(config Config, store *Store) *Service {
	return &Service{
		config: config,
		store:  store,
		recent: make(map[string]time.Time),
	}
}

func (s *Service) Enabled() bool {
	return s != nil && s.config.Enabled && s.config.PublicKey != "" && s.config.PrivateKey != "" && s.store != nil
}

func (s *Service) PublicKey() string {
	if s == nil || !s.config.Enabled {
		return ""
	}
	return s.config.PublicKey
}

func (s *Service) Status() map[string]any {
	enabled := s.Enabled()
	count := 0
	platforms := map[string]int{}
	if enabled {
		if items, err := s.store.List(); err == nil {
			for _, item := range items {
				if item.Enabled {
					count++
					platform := strings.TrimSpace(item.Platform)
					if platform == "" {
						platform = "unknown"
					}
					platforms[platform]++
				}
			}
		}
	}
	return map[string]any{
		"enabled":            enabled,
		"vapid_public_key":   s.PublicKey(),
		"subscription_count": count,
		"platform_counts":    platforms,
	}
}

func (s *Service) SaveSubscription(sub Subscription) (Subscription, error) {
	if !s.Enabled() {
		return Subscription{}, errors.New("web push is not configured")
	}
	return s.store.Upsert(sub)
}

func (s *Service) DeleteSubscription(endpoint string) error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.Delete(endpoint)
}

func (s *Service) NotifyScheduled(ctx context.Context, in ScheduledNotification) {
	if s == nil || !s.Enabled() {
		return
	}
	payload := BuildScheduledPayload(in)
	s.sendAsync(ctx, firstNonEmpty(in.EventID, payload.Tag), payload)
}

func (s *Service) NotifyPayload(ctx context.Context, eventID string, payload Payload) {
	if s == nil || !s.Enabled() {
		return
	}
	s.sendAsync(ctx, firstNonEmpty(eventID, notify.EventID(payload), payload.Tag), payload)
}

func (s *Service) SendTest(ctx context.Context) error {
	if !s.Enabled() {
		return errors.New("web push is not configured")
	}
	return s.send(ctx, testPayload())
}

func (s *Service) SendTestToEndpoint(ctx context.Context, endpoint string) error {
	if !s.Enabled() {
		return errors.New("web push is not configured")
	}
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return errors.New("web push subscription endpoint is required")
	}
	item, ok, err := s.store.GetByEndpoint(endpoint)
	if err != nil {
		return err
	}
	if !ok || !item.Enabled {
		return errors.New("web push subscription is not registered on this server")
	}
	return s.sendToItems(ctx, testPayload(), []Subscription{item})
}
