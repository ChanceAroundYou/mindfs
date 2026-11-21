package controlsync

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

// PushSender 是边缘推送待同步变更的出口。抽成接口是为了让 PendingPusher 能在
// 测试里换掉真实 HTTP 与 TLS，同时线上只认 https。
type PushSender interface {
	Push(ctx context.Context, req PushRequest) (PushBatch, error)
}

// PushClientOptions 是 POST /api/control-sync/push 的连接参数。
type PushClientOptions struct {
	PrimaryURL string
	Token      string
	CAFile     string
	Timeout    time.Duration
}

// PushClient 是同步通道上行的 HTTP 客户端。
//
// 与 Replicator 同一条信任链：一律 https（没有绕过开关）、TLS 1.2 起步、
// 系统根 + 可选 ca_file、Bearer token。非 https 直接构造失败 —— 口令 hash
// 会随 users 域上行，明文通道等于把凭据摊在网上。
type PushClient struct {
	endpoint *url.URL
	token    string
	client   *http.Client
}

// NewPushClient 校验配置并构造客户端。
func NewPushClient(opts PushClientOptions) (*PushClient, error) {
	raw := strings.TrimSpace(opts.PrimaryURL)
	if raw == "" {
		return nil, errors.New("controlsync: primary_url required")
	}
	base, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("controlsync: invalid primary_url: %w", err)
	}
	if base.Scheme != "https" {
		return nil, fmt.Errorf("controlsync: primary_url must be https, got %q", base.Scheme)
	}
	if base.Host == "" {
		return nil, errors.New("controlsync: primary_url has no host")
	}
	if strings.TrimSpace(opts.Token) == "" {
		return nil, errors.New("controlsync: sync token required")
	}
	client, err := newSyncHTTPClient(opts.CAFile)
	if err != nil {
		return nil, err
	}
	if opts.Timeout > 0 {
		client.Timeout = opts.Timeout
	}
	endpoint := *base
	endpoint.Path = joinSyncPath(base.Path, "/api/control-sync/push")
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	return &PushClient{endpoint: &endpoint, token: opts.Token, client: client}, nil
}

// Push 发一整批并解析逐条结论。非 2xx 一律当失败：同步通道上「部分成功」
// 无法从状态码推断，逐条结论只在 200 响应体里。
func (c *PushClient) Push(ctx context.Context, req PushRequest) (PushBatch, error) {
	var batch PushBatch
	body, err := json.Marshal(req)
	if err != nil {
		return batch, fmt.Errorf("controlsync: encode push request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return batch, fmt.Errorf("controlsync: build push request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return batch, fmt.Errorf("controlsync: push: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return batch, fmt.Errorf("controlsync: read push response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return batch, fmt.Errorf("controlsync: push: primary returned %d: %s",
			resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if err := json.Unmarshal(data, &batch); err != nil {
		return batch, fmt.Errorf("controlsync: decode push response: %w", err)
	}
	return batch, nil
}
