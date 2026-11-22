package app

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

	"mindfs/server/internal/channels/feishu"
	"mindfs/server/internal/nodes"
)

// answerRelay 是卡片点击的落点：本地直接写任务库，远端由主控 POST 到 worker。
type answerRelay func(ctx context.Context, answer feishu.Answer) error

const (
	feishuAnswerTimeout  = 5 * time.Second
	feishuAnswerMaxBody  = 64 << 10
	feishuAnswerMaxTries = 4 // 首次 + 三次重试
	feishuAnswerDrainMax = 4 << 10
	feishuAnswerPath     = "/api/channels/feishu/answer"
	feishuAnswerBackoff1 = 10 * time.Second
	feishuAnswerBackoff2 = 30 * time.Second
	feishuAnswerBackoff3 = 2 * time.Minute
)

var feishuAnswerBackoffs = [...]time.Duration{feishuAnswerBackoff1, feishuAnswerBackoff2, feishuAnswerBackoff3}

// feishuAnswerRelay 只按 answer.NodeID 路由。远端地址只能来自配置好的节点表；
// 载荷与请求体里没有任何 URL，卡片内容永远不能挑选目的地。
type feishuAnswerRelay struct {
	Nodes       *nodes.Store
	LocalNodeID string
	Token       string
	Apply       answerRelay
	Client      *http.Client
	Sleep       func(context.Context, time.Duration) error
}

// retryableAnswerDeliveryError marks a temporary remote-delivery failure. It
// implements feishu.RetryableAnswerError so a click handler knows the card
// should stay open; deterministic local errors stay plain errors.
type retryableAnswerDeliveryError struct{ err error }

func (e *retryableAnswerDeliveryError) Error() string         { return e.err.Error() }
func (e *retryableAnswerDeliveryError) Unwrap() error         { return e.err }
func (e *retryableAnswerDeliveryError) RetryableAnswer() bool { return true }

func retryableAnswerError(err error) error {
	return &retryableAnswerDeliveryError{err: err}
}

func sleepWithAnswerContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// newFeishuAnswerRelay 把进程级中继身份与节点表收成一个点击处理器。
func newFeishuAnswerRelay(workspaces *workspaceManager) answerRelay {
	if workspaces == nil {
		return func(context.Context, feishu.Answer) error {
			return errors.New("feishu answer: workspace manager is nil")
		}
	}
	relay := &feishuAnswerRelay{
		Nodes:       workspaces.shared.nodes,
		LocalNodeID: strings.TrimSpace(workspaces.shared.feishuRelay.NodeID),
		Token:       workspaces.shared.feishuRelay.Token,
		Apply:       workspaces.ApplyFeishuAnswer,
		Client:      feishu.NewRelayClient(feishuAnswerTimeout),
		Sleep:       sleepWithAnswerContext,
	}
	return relay.Relay
}

func (r *feishuAnswerRelay) Relay(ctx context.Context, answer feishu.Answer) error {
	nodeID := strings.TrimSpace(answer.NodeID)
	if nodeID == "" || nodeID == r.LocalNodeID {
		if r.Apply == nil {
			return errors.New("feishu answer: no local answer handler")
		}
		return r.Apply(ctx, answer)
	}

	target, err := r.remoteURL(nodeID)
	if err != nil {
		return err
	}
	body, err := json.Marshal(answer)
	if err != nil {
		return fmt.Errorf("feishu answer: encode: %w", err)
	}
	if len(body) > feishuAnswerMaxBody {
		return fmt.Errorf("feishu answer: body too large (%d bytes)", len(body))
	}
	token := strings.TrimSpace(r.Token)
	if err := feishu.ValidateRelayToken(token); err != nil {
		return err
	}

	client := r.Client
	if client == nil {
		client = feishu.NewRelayClient(feishuAnswerTimeout)
	}
	sleep := r.Sleep
	if sleep == nil {
		sleep = sleepWithAnswerContext
	}

	var lastErr error
	for attempt := 0; attempt < feishuAnswerMaxTries; attempt++ {
		if err := ctx.Err(); err != nil {
			return retryableAnswerError(fmt.Errorf("feishu answer: 回传节点 %s 已中断：%w", nodeID, err))
		}
		if attempt > 0 {
			wait := feishuAnswerBackoffs[attempt-1]
			if err := sleep(ctx, wait); err != nil {
				return retryableAnswerError(fmt.Errorf("feishu answer: 回传节点 %s 退避中断：%w", nodeID, err))
			}
			if err := ctx.Err(); err != nil {
				return retryableAnswerError(fmt.Errorf("feishu answer: 回传节点 %s 已中断：%w", nodeID, err))
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return retryableAnswerError(fmt.Errorf("feishu answer: 回传节点 %s 已中断：%w", nodeID, ctxErr))
			}
			return fmt.Errorf("feishu answer: build request for node %s: %w", nodeID, err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(feishu.RelayTokenHeader, token)

		resp, err := client.Do(req)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return retryableAnswerError(fmt.Errorf("feishu answer: 回传节点 %s 已中断：%w", nodeID, ctxErr))
			}
			lastErr = retryableAnswerError(fmt.Errorf("feishu answer: node %s attempt %d/%d network error: %w", nodeID, attempt+1, feishuAnswerMaxTries, err))
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, feishuAnswerDrainMax))
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return fmt.Errorf("feishu answer: node %s rejected answer with HTTP %d", nodeID, resp.StatusCode)
		}
		lastErr = retryableAnswerError(fmt.Errorf("feishu answer: node %s attempt %d/%d HTTP %d", nodeID, attempt+1, feishuAnswerMaxTries, resp.StatusCode))
	}
	return lastErr
}

// remoteURL 是远端目的地的唯一来源：节点表里按 id 查到的 URL，绝不读载荷。
func (r *feishuAnswerRelay) remoteURL(nodeID string) (string, error) {
	if r.Nodes == nil {
		return "", errors.New("feishu answer: node table unavailable")
	}
	for _, node := range r.Nodes.List() {
		if strings.TrimSpace(node.ID) != nodeID {
			continue
		}
		base := strings.TrimRight(strings.TrimSpace(node.URL), "/")
		parsed, err := url.Parse(base)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return "", fmt.Errorf("feishu answer: node %s has an invalid URL", nodeID)
		}
		return base + feishuAnswerPath, nil
	}
	return "", fmt.Errorf("feishu answer: 未知节点 %q，无法回传", nodeID)
}
