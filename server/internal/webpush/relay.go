package webpush

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// relayTimeout 是单次中继 POST 的上限。它跑在通知的异步分支里，超时只影响
// 这一条通知的可得性，不能拖住任何执行路径。
const relayTimeout = 5 * time.Second

// relayHTTPClient 与 send.go 一样不挂全局 client：通知是旁路，连接池由
// 默认 Transport 共享即可。
var relayHTTPClient = &http.Client{Timeout: relayTimeout}

// RelayRequest 是 worker → 控制节点的中继载荷。
//
// 为什么需要中继：推送订阅只能由控制节点创建（/api/web-push/* 在 worker 上 403），
// 而浏览器对某个源只保留一条订阅。worker 自己既没有订阅表，也没有浏览器信任的
// VAPID 私钥，所以它把「该发什么」交给控制节点，由控制节点用自己的密钥与订阅表发送。
type RelayRequest struct {
	EventID string  `json:"event_id"`
	Payload Payload `json:"payload"`
}

// SetRelayURL 设置中继地址（控制节点的 /api/web-push/relay 完整 URL）。
// 空值 = 不中继；非 http/https 或缺少 host 直接报错，避免配置写错后静默失效。
func (s *Service) SetRelayURL(raw string) error {
	if s == nil {
		return nil
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed != "" {
		parsed, err := url.Parse(trimmed)
		if err != nil {
			return fmt.Errorf("invalid web push relay url: %w", err)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return fmt.Errorf("invalid web push relay url scheme %q", parsed.Scheme)
		}
		if parsed.Host == "" {
			return errors.New("invalid web push relay url: missing host")
		}
	}
	s.mu.Lock()
	s.relayURL = trimmed
	s.mu.Unlock()
	return nil
}

// RelayEnabled 表示本节点是否把通知转给控制节点代发。
func (s *Service) RelayEnabled() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.relayURL != ""
}

// Relay 把一条通知交给控制节点发送。返回错误只用于日志。
func (s *Service) Relay(payload Payload, eventID string) error {
	s.mu.Lock()
	target := s.relayURL
	s.mu.Unlock()
	if target == "" {
		return nil
	}
	body, err := json.Marshal(RelayRequest{EventID: strings.TrimSpace(eventID), Payload: payload})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), relayTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := relayHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("relay responded %s", resp.Status)
	}
	return nil
}

// ValidateRelayedPayload 是接收端的输入校验。
//
// API 层不鉴权（用户决策），所以这里只挡「不可能由本系统产生」的载荷：类型必须属于
// 已知通知类型，URL 必须是相对路径。后者是真正有意义的一条 —— 通知点击会
// openWindow(url)，放行绝对 URL 等于让能访问控制节点的人推送任意钓鱼链接。
func ValidateRelayedPayload(payload Payload) error {
	switch strings.TrimSpace(payload.Type) {
	case "session.done", "session.ask_user", "scheduled.done", "scheduled.failed", "test":
	default:
		return fmt.Errorf("unsupported notification type %q", payload.Type)
	}
	if payload.URL != "" && !strings.HasPrefix(payload.URL, "./") && !strings.HasPrefix(payload.URL, "/") {
		return errors.New("relayed notification url must be relative")
	}
	return nil
}
