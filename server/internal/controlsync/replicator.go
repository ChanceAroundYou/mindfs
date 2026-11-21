package controlsync

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// 边缘追平的默认节奏与分页。spec §6.2：每 5 秒拉一次，单页默认 500、上限 2000。
const (
	ReplicatorInterval = 5 * time.Second
	ReplicatorPageSize = 500
	replicatorMaxPage  = 2000
	// 单页响应的硬上限：主节点异常时宁可报错，也不把内存吃光。
	replicatorMaxPageBytes = 64 << 20
)

// RemoteChange 是变更流里的一条记录（主节点 → 边缘）。
//
// 它同时是 GET /api/control-sync/changes 的线上形状：边缘的 Replicator 与主节点
// 的 handler 共用这一个类型，避免两边各写一份 JSON 结构后再对不上。
//
// Rev 是主节点分配的全局序号，边缘直接拿它当水位（base_rev），**不**在本地重新分配：
// 水位只表达「主节点上我追到了哪一条」。
type RemoteChange struct {
	Rev       int64           `json:"rev"`
	Domain    string          `json:"domain"`
	Key       string          `json:"key"`
	Hash      string          `json:"hash"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Deleted   bool            `json:"deleted"`
	UpdatedAt time.Time       `json:"updated_at"`
	Origin    string          `json:"origin,omitempty"`
}

// ReplicatorOptions 只收配置值；测试通过 CAFile 指向 httptest 的自签证书。
type ReplicatorOptions struct {
	PrimaryURL string
	CAFile     string
	// Token 是同步通道的 bearer token。主节点的 /changes 一次能吐出整个控制面
	// （含口令 hash），没 token 的服务端根本不会注册这条路由。
	Token    string
	Limit    int
	Interval time.Duration
	OnError  func(error)
}

// Replicator 在转发期间把主节点的变更流追到本地副本（records 表）。
//
// 本阶段只做「副本追平」：读路径仍走转发，副本是主节点不可达时的底子。
// 把远端记录应用到权威存储（并在两侧都改时判冲突）属于合并阶段，不在这里做。
type Replicator struct {
	store    *Store
	base     *url.URL
	client   *http.Client
	token    string
	limit    int
	interval time.Duration
	onError  func(error)
}

// NewReplicator 校验配置并构造追平器。同步通道一律 https（无绕过开关）；
// ca_file 只做追加，系统根证书照常生效。
func NewReplicator(store *Store, opts ReplicatorOptions) (*Replicator, error) {
	if store == nil {
		return nil, errors.New("controlsync: store required")
	}
	raw := strings.TrimSpace(opts.PrimaryURL)
	if raw == "" {
		return nil, errors.New("controlsync: primary_url required")
	}
	base, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("controlsync: parse primary_url: %w", err)
	}
	if base.Scheme != "https" {
		return nil, fmt.Errorf("controlsync: primary_url must be https, got %q", base.Scheme)
	}
	if base.Host == "" {
		return nil, errors.New("controlsync: primary_url missing host")
	}
	client, err := newSyncHTTPClient(opts.CAFile)
	if err != nil {
		return nil, err
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = ReplicatorPageSize
	}
	if limit > replicatorMaxPage {
		limit = replicatorMaxPage
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = ReplicatorInterval
	}
	return &Replicator{
		store:    store,
		base:     base,
		client:   client,
		token:    strings.TrimSpace(opts.Token),
		limit:    limit,
		interval: interval,
		onError:  opts.OnError,
	}, nil
}

// newSyncHTTPClient 构造同步通道的 HTTP 客户端：TLS 1.2 起步，系统根 + 可选 ca_file。
//
// 与 api.ControlProxy 的 TLS 配置刻意一致（那里也拒绝明文、也禁止 insecure）：
// 同步与转发是同一条信任链的两个方向，任何一边松掉都等于两边一起松。
func newSyncHTTPClient(caFile string) (*http.Client, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if strings.TrimSpace(caFile) != "" {
		data, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("controlsync: read ca_file: %w", err)
		}
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("controlsync: ca_file contains no valid certificate: %s", caFile)
		}
	}
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				RootCAs:    pool,
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

// Run 每 interval 拉一次，直到 ctx 结束。失败只上报（OnError），不推进水位：
// 下一拍从同一个 base_rev 续拉，断网期间攒下的变更一条不丢。
func (r *Replicator) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		if _, err := r.SyncOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			if r.onError != nil {
				r.onError(err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// SyncOnce 把当前水位之后的变更一页页追平，返回本次落地的记录条数。
//
// 逐页提交：进程死在两页之间，重启后从已提交的水位继续，不需要重放整段。
// 某一页失败就整体返回错误 —— 该页事务回滚，水位停在上一次成功的位置。
func (r *Replicator) SyncOnce(ctx context.Context) (int, error) {
	applied := 0
	for {
		base, err := r.store.BaseRev(ctx)
		if err != nil {
			return applied, err
		}
		page, err := r.fetchPage(ctx, base+1)
		if err != nil {
			return applied, err
		}
		if len(page.Changes) == 0 {
			return applied, nil
		}
		if err := r.store.ApplyRemoteChanges(ctx, page.Changes); err != nil {
			return applied, err
		}
		applied += len(page.Changes)
	}
}

type changePage struct {
	Changes []RemoteChange `json:"changes"`
	HeadRev int64          `json:"head_rev"`
}

func (r *Replicator) fetchPage(ctx context.Context, since int64) (*changePage, error) {
	u := *r.base
	u.Path = joinSyncPath(r.base.Path, "/api/control-sync/changes")
	q := u.Query()
	q.Set("since", strconv.FormatInt(since, 10))
	q.Set("limit", strconv.Itoa(r.limit))
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("controlsync: build changes request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("controlsync: fetch changes: %w", err)
	}
	defer resp.Body.Close()
	body := io.LimitReader(resp.Body, replicatorMaxPageBytes)
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(body, 512))
		return nil, fmt.Errorf("controlsync: fetch changes: status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	var page changePage
	dec := json.NewDecoder(body)
	if err := dec.Decode(&page); err != nil {
		return nil, fmt.Errorf("controlsync: decode changes: %w", err)
	}
	return &page, nil
}

// joinSyncPath 拼接主节点 base 路径与接口路径，保留部署前缀（如 /mindfs）。
func joinSyncPath(basePath, apiPath string) string {
	basePath = strings.TrimSuffix(basePath, "/")
	if basePath == "" || basePath == "/" {
		return apiPath
	}
	return basePath + apiPath
}
