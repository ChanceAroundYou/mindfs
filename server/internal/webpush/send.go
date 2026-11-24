package webpush

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	webpushgo "github.com/SherClockHolmes/webpush-go"
	"mindfs/server/internal/notify"
)

func (s *Service) sendAsync(ctx context.Context, eventID string, payload Payload) {
	if !s.shouldSend(eventID) {
		return
	}
	go func() {
		// 先中继、再本地发。控制节点没有 relayURL，只有 worker 会走到第一条；
		// 两条都成功也不会给用户多一条通知 —— service worker 按 tag 覆盖。
		if s.RelayEnabled() {
			if err := s.Relay(payload, eventID); err != nil {
				log.Printf("[webpush] relay.error type=%s tag=%s err=%v", payload.Type, payload.Tag, err)
			}
		}
		if !s.Enabled() {
			return
		}
		if err := s.send(ctx, payload); err != nil {
			log.Printf("[webpush] send.error type=%s tag=%s err=%v", payload.Type, payload.Tag, err)
		}
	}()
}

func (s *Service) shouldSend(eventID string) bool {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return true
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, seen := range s.recent {
		if now.Sub(seen) > 30*time.Minute {
			delete(s.recent, key)
		}
	}
	if _, ok := s.recent[eventID]; ok {
		return false
	}
	s.recent[eventID] = now
	return true
}

func (s *Service) send(ctx context.Context, payload Payload) error {
	items, err := s.store.List()
	if err != nil {
		return err
	}
	return s.sendToItems(ctx, payload, items, true)
}

// requirePresence 为真时，声明了 presence 能力、但当前没有任何页面心跳的订阅会被跳过：
// 页面关掉以后不再弹桌面通知。用户当场发起的「发送测试」走 requirePresence=false。
func (s *Service) sendToItems(ctx context.Context, payload Payload, items []Subscription, requirePresence bool) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if len(body) > maxPayloadBytes {
		// 超限时只收紧正文，且仍走同一套纯文本规则（去格式、留尾部）。
		payload.Body = notify.PlainText(payload.Body, notify.FallbackBodyMaxRunes)
		body, err = json.Marshal(payload)
		if err != nil {
			return err
		}
	}
	activeCount := 0
	presenceSkipped := 0
	successCount := 0
	var failures []string
	for _, item := range items {
		if !item.Enabled {
			continue
		}
		if requirePresence && item.PresenceCapable && !s.presenceTracker().Live(item.Endpoint) {
			// 页面已经不在了：不是失败，也不删订阅 —— 用户下一次打开页面会重新心跳。
			presenceSkipped++
			continue
		}
		activeCount++
		resp, err := webpushgo.SendNotificationWithContext(ctx, body, &webpushgo.Subscription{
			Endpoint: item.Endpoint,
			Keys: webpushgo.Keys{
				Auth:   item.Keys.Auth,
				P256dh: item.Keys.P256dh,
			},
		}, &webpushgo.Options{
			HTTPClient:      s.httpClient,
			Subscriber:      s.config.Subject,
			TTL:             defaultTTLSeconds,
			Urgency:         webpushgo.UrgencyNormal,
			VAPIDPublicKey:  s.config.PublicKey,
			VAPIDPrivateKey: s.config.PrivateKey,
		})
		responseStatus := ""
		responseBody := ""
		if resp != nil {
			responseStatus = resp.Status
			if b, readErr := io.ReadAll(io.LimitReader(resp.Body, 1024)); readErr == nil {
				responseBody = strings.TrimSpace(string(b))
			}
			resp.Body.Close()
		}
		if err != nil {
			_ = s.store.MarkError(item.ID, err.Error())
			failures = append(failures, err.Error())
			continue
		}
		if resp != nil && (resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusNotFound) {
			_ = s.store.DeleteByID(item.ID)
			failure := responseFailureMessage(responseStatus, responseBody)
			failures = append(failures, failure)
			continue
		}
		if resp != nil && resp.StatusCode >= 300 {
			failure := responseFailureMessage(responseStatus, responseBody)
			_ = s.store.MarkError(item.ID, failure)
			failures = append(failures, failure)
			continue
		}
		successCount++
	}
	if activeCount == 0 {
		if presenceSkipped > 0 {
			// 所有订阅都因为「页面已关闭」被跳过 —— 这是预期内的静默，不是错误。
			return nil
		}
		return errors.New("no web push subscriptions")
	}
	if successCount == 0 && len(failures) > 0 {
		return fmt.Errorf("web push delivery failed: %s", strings.Join(failures, "; "))
	}
	return nil
}

func responseFailureMessage(status, body string) string {
	status = strings.TrimSpace(status)
	body = strings.TrimSpace(body)
	if status == "" {
		status = "web push request failed"
	}
	if body == "" {
		return status
	}
	return status + ": " + body
}

func subscriptionID(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if len(endpoint) <= 24 {
		return endpoint
	}
	return endpoint[len(endpoint)-24:]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
