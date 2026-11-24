package webpush

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	webpushgo "github.com/SherClockHolmes/webpush-go"

	"mindfs/server/internal/notify"
)

type Service struct {
	config Config
	store  *Store

	mu     sync.Mutex
	recent map[string]time.Time
	// presence 记录「还有没有页面开着」；懒初始化，零值 Service 也能用。
	presence *PresenceTracker
	// httpClient 供测试注入；生产为 nil 时 webpush-go 用默认客户端。
	httpClient webpushgo.HTTPClient
	// relayURL 非空时，本节点的通知转给控制节点代发（见 relay.go）。
	relayURL string
}

func NewService(config Config, store *Store) *Service {
	return &Service{
		config:   config,
		store:    store,
		recent:   make(map[string]time.Time),
		presence: NewPresenceTracker(),
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
		"relay_enabled":      s.RelayEnabled(),
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
	s.presenceTracker().Leave(endpoint, "")
	return s.store.Delete(endpoint)
}

// ReportPresence 上报「这个页面还开着」（leave=false）或「这个页面关了」（leave=true）。
// clientID 为空且 leave=true 表示整条订阅下已经没有页面（pagehide beacon / SW 自愈）。
func (s *Service) ReportPresence(endpoint, clientID string, leave bool) error {
	if !s.Enabled() {
		return errors.New("web push is not configured")
	}
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return errors.New("web push subscription endpoint is required")
	}
	clientID = strings.TrimSpace(clientID)
	if leave {
		s.presenceTracker().Leave(endpoint, clientID)
	} else {
		s.presenceTracker().Touch(endpoint, clientID)
	}
	return nil
}

// PresenceActive 供状态接口/测试观察：这条订阅当前是否还有页面开着。
func (s *Service) PresenceActive(endpoint string) bool {
	return s.presenceTracker().Live(strings.TrimSpace(endpoint))
}

func (s *Service) presenceTracker() *PresenceTracker {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.presence == nil {
		s.presence = NewPresenceTracker()
	}
	return s.presence
}

func (s *Service) NotifyScheduled(ctx context.Context, in ScheduledNotification) {
	if s == nil || (!s.Enabled() && !s.RelayEnabled()) {
		return
	}
	payload := BuildScheduledPayload(in)
	s.sendAsync(ctx, firstNonEmpty(in.EventID, payload.Tag), payload)
}

func (s *Service) NotifyPayload(ctx context.Context, eventID string, payload Payload) {
	if s == nil || (!s.Enabled() && !s.RelayEnabled()) {
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
	return s.sendToItems(ctx, testPayload(), []Subscription{item}, false)
}
