package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"mindfs/server/internal/nodeinfo"
)

// 在线边缘把控制面请求转发给主节点。Q3 裁定同步通道一律 https：不提供
// 「跳过校验」的开关，信任根只有系统根 + 可选的 ca_file（自签 / 内网 CA）。
var ErrInsecurePrimaryURL = errors.New("control_sync: primary_url must be https")

const (
	// ControlProxyTimeout 是「等到响应头」的上限。超过它按主节点不可达处理。
	ControlProxyTimeout = 10 * time.Second
	// ControlProxyStreamTimeout 是整条流式响应的时长上限，防止一条挂死的
	// 长连接把转发协程永久占住。
	ControlProxyStreamTimeout = 10 * time.Minute
)

// controlProxyLocalPrefixes 是本机自己的域：即便它们看起来像控制面，也绝不转发。
//   - web-push：订阅与 VAPID 是设备本地的；
//   - app update：清单指向本机发布通道，与主节点无关；
//   - node-info：节点自述，边缘靠它判断对面有没有 UI；
//   - control-sync：同步自身的状态与推送接口，转发给自己等于回环。
var controlProxyLocalPrefixes = []string{
	"/api/web-push",
	"/api/app/update",
	"/api/node-info",
	"/api/control-sync",
}

// ControlProxyOptions 是构造转发器需要的配置。
type ControlProxyOptions struct {
	// PrimaryURL 必须带 https:// scheme；可以带部署前缀（如 https://host:7331/mindfs）。
	PrimaryURL string
	// CAFile 是可选的自签 / 内网 CA 证书 PEM。留空只信任系统根。
	CAFile string
	// ForwardedBy 写进 X-MindFS-Forwarded-By，主节点日志据此知道是谁转的。
	ForwardedBy string
}

// ControlProxy 把控制面请求转发给主节点，其余请求交还给本机 mux。
type ControlProxy struct {
	primary     *url.URL
	client      *http.Client
	forwardedBy string
}

// NewControlProxy 校验并组装转发器。http:// 一律拒绝：复制的是账户口令哈希，
// 明文通道等于把凭据摆在链路上。
func NewControlProxy(opts ControlProxyOptions) (*ControlProxy, error) {
	raw := strings.TrimSpace(opts.PrimaryURL)
	if raw == "" {
		return nil, errors.New("control_sync: primary_url required")
	}
	primary, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("control_sync: parse primary_url: %w", err)
	}
	if !strings.EqualFold(primary.Scheme, "https") {
		return nil, fmt.Errorf("%w（实际 %q）", ErrInsecurePrimaryURL, primary.Scheme)
	}
	if primary.Host == "" {
		return nil, fmt.Errorf("control_sync: primary_url missing host: %q", raw)
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if strings.TrimSpace(opts.CAFile) != "" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		pem, err := os.ReadFile(opts.CAFile)
		if err != nil {
			return nil, fmt.Errorf("control_sync: read ca_file: %w", err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("control_sync: ca_file %q has no valid certificate", opts.CAFile)
		}
		tlsConfig.RootCAs = pool
	}
	return &ControlProxy{
		primary: primary,
		client: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig:   tlsConfig,
				ForceAttemptHTTP2: true,
			},
			// 3xx 原样交回客户端：主节点的路由决策不该被边缘改写。
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		forwardedBy: strings.TrimSpace(opts.ForwardedBy),
	}, nil
}

// Handler 返回转发处理器：命中控制面且不在本机域白名单里时转发，其余交给 next。
func (p *ControlProxy) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p == nil || !p.ShouldForward(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		p.forward(w, r)
	})
}

// ShouldForward 判断一条（已剥掉部署前缀的）请求路径是否要转发给主节点。
func (p *ControlProxy) ShouldForward(path string) bool {
	if p == nil || p.primary == nil {
		return false
	}
	return nodeinfo.IsControlPlane(path) && !isControlProxyLocal(path)
}

func isControlProxyLocal(path string) bool {
	for _, prefix := range controlProxyLocalPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func (p *ControlProxy) forward(w http.ResponseWriter, r *http.Request) {
	target := *p.primary
	target.Path = joinURLPath(p.primary.Path, r.URL.Path)
	target.RawQuery = r.URL.RawQuery
	target.Fragment = ""
	target.RawPath = ""

	// 超时只覆盖「拿到响应头」这一段：流式响应一旦开始，body 按 StreamTimeout 走。
	// 把总时长压在这条 ctx 上会把正常的长响应半路掐断。
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	timer := time.AfterFunc(ControlProxyTimeout, cancel)
	defer timer.Stop()

	req, err := http.NewRequestWithContext(ctx, r.Method, target.String(), r.Body)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "primary_unreachable"})
		return
	}
	copyForwardHeaders(req.Header, r.Header)
	if p.forwardedBy != "" {
		req.Header.Set("X-MindFS-Forwarded-By", p.forwardedBy)
	}
	resp, err := p.client.Do(req)
	timer.Stop()
	if err != nil {
		// 失败**不**回落本地执行：在线时本地写控制面会与主节点分叉，
		// 边缘该进离线模式由模式状态机决定，而不是在这里偷偷写一份。
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "primary_unreachable"})
		return
	}
	defer resp.Body.Close()
	for name, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	p.copyBody(w, resp)
}

// forwardHeaderWhitelist 是唯一允许带过去的客户端头：身份（Cookie / Authorization）
// 与载荷类型。其它本机头不外泄 —— 边缘可能有主节点根本不该看到的本地配置。
var forwardHeaderWhitelist = []string{"Cookie", "Authorization", "Content-Type"}

func copyForwardHeaders(dst, src http.Header) {
	for _, name := range forwardHeaderWhitelist {
		for _, value := range src.Values(name) {
			dst.Add(name, value)
		}
	}
}

func (p *ControlProxy) copyBody(w http.ResponseWriter, resp *http.Response) {
	flusher, _ := w.(http.Flusher)
	deadline := time.Now().Add(ControlProxyStreamTimeout)
	buf := make([]byte, 32*1024)
	for {
		if time.Now().After(deadline) {
			return
		}
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// joinURLPath 拼主节点的部署前缀与请求路径，避免出现双斜杠或丢掉前缀。
func joinURLPath(base, path string) string {
	base = strings.TrimRight(base, "/")
	if base == "" {
		if path == "" {
			return "/"
		}
		return path
	}
	if path == "" {
		return base
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}
