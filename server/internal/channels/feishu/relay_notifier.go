package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"mindfs/server/internal/kanban"
)

const (
	relayMaxAttempts   = 3
	relayMaxBodyBytes  = 64 << 10
	relayClientTimeout = 5 * time.Second
)

var relayBackoffs = [...]time.Duration{10 * time.Second, 30 * time.Second}

// RelayNotifier sends a worker's card to the control node, which owns the
// lark-cli connection. The destination is configuration, never card content:
// a task cannot redirect its own notification. It satisfies kanban.CardNotifier.
type RelayNotifier struct {
	URL    string
	Token  string
	NodeID string
	Client *http.Client
	Sleep  func(time.Duration)
}

var _ kanban.CardNotifier = (*RelayNotifier)(nil)

// NotifyCard posts exactly one configured envelope to the control node. Only
// network failures and 5xx are retried (three attempts, 10s/30s backoff); a
// 4xx means the request itself was rejected and must not be repeated.
func (r *RelayNotifier) NotifyCard(ctx context.Context, notice kanban.CardNotice) error {
	url := strings.TrimSpace(r.URL)
	if url == "" {
		return errors.New("feishu relay: empty relay URL")
	}
	payload, err := json.Marshal(RelayRequest{
		NodeID: r.NodeID,
		Notice: noticeFromCard(r.NodeID, notice),
	})
	if err != nil {
		return fmt.Errorf("feishu relay: encode card: %w", err)
	}
	if len(payload) > relayMaxBodyBytes {
		return fmt.Errorf("feishu relay: card body %d bytes exceeds %d", len(payload), relayMaxBodyBytes)
	}

	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: relayClientTimeout}
	}
	sleep := r.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}

	var lastErr error
	for attempt := 1; attempt <= relayMaxAttempts; attempt++ {
		if attempt > 1 {
			sleep(relayBackoffs[attempt-2])
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("feishu relay: build request: %w", err)
		}
		req.Header.Set(RelayTokenHeader, r.Token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("feishu relay: attempt %d/%d network error: %w", attempt, relayMaxAttempts, err)
			log.Printf("[feishu/relay] attempt=%d/%d network_error card=%s", attempt, relayMaxAttempts, notice.ID)
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		lastErr = fmt.Errorf("feishu relay: attempt %d/%d status %d", attempt, relayMaxAttempts, resp.StatusCode)
		log.Printf("[feishu/relay] attempt=%d/%d status=%d card=%s", attempt, relayMaxAttempts, resp.StatusCode, notice.ID)
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return lastErr
		}
	}
	return lastErr
}

// noticeFromCard maps the neutral task card onto the wire type. NodeID is
// always the local configured node so the control node can check the envelope
// against its node table; the card itself never chooses the sender.
func noticeFromCard(nodeID string, card kanban.CardNotice) Notice {
	return Notice{
		CardID:         card.ID,
		Kind:           Kind(card.Kind),
		RootID:         card.RootID,
		TaskID:         card.TaskID,
		TaskTitle:      card.TaskTitle,
		TaskNumber:     card.TaskNumber,
		NodeID:         nodeID,
		WorkflowNodeID: card.WorkflowNodeID,
		NodeName:       card.NodeName,
		QuestionID:     card.QuestionID,
		Prompt:         card.Prompt,
		Status:         card.Status,
		Summary:        card.Summary,
		Options:        card.Options,
	}
}
