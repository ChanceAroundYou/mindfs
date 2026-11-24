package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"mindfs/server/internal/controlsync"
)

// RelayHubService 是 relay 入口需要的最小能力面。
//
// 只要这四件事：按名字找一台边控端、判断它在线、借一条数据通道、读完请求归还。
// 主控侧的 HTTP 层不关心隧道怎么建、心跳怎么发 —— 那是 controlsync.RelayHub 的事。
type RelayHubService interface {
	// EdgeByRelayName 按 /mindfs/relay/<name> 里的名字找边控端。
	EdgeByRelayName(ctx context.Context, name string) (controlsync.EdgeInfo, error)
	// Acquire 借一条只服务一次请求的数据通道。
	Acquire(ctx context.Context, instanceID string) (net.Conn, error)
	// Online 报告这条隧道当前是否还在。
	Online(instanceID string) bool
}

var (
	// errRelayUnknown 是「路径里的名字没有对应任何边控端」。
	//
	// 与 offline 分开：名字拼错了是永远连不上的，回 404 让调用方立刻知道去改地址；
	// 机器关机则回 503，重试是有意义的。
	errRelayUnknown = errors.New("relay: unknown edge")
	// errRelayOffline 是「有这台机器，但它现在没拨进来」。
	errRelayOffline = errors.New("relay: edge offline")
	// errRelayBusy 是「隧道在，但空闲通道用完了」。
	errRelayBusy = errors.New("relay: edge busy")
)

// relayRequestTimeout 是单条数据通道从借到到请求读完的总时限。
//
// 与 nginx 那边 proxy_read_timeout 3600s 同一档：文件面板的大文件、长轮询、
// WS 会话流都要能跑完。这里只是防止一条通道永远不归还。
const relayRequestTimeout = 30 * time.Minute

// NewRelayHandler 构造 /relay/<name>/... 的转发器。
//
// 为什么放在 MindFS 后端而不是 nginx：后端在部署前缀之后接管了整个 origin
// （server.go 的 mux.Handle("/", StripDeployPrefix(...))），nginx 无法在同一个
// origin 下把 /mindfs/relay/* 切给另一个上游而不破坏其余路径。所以 relay 必须是
// 后端的一条真实路由。
//
// 转发方式：标准库 ReverseProxy + 一个「每次请求现借一条隧道连接」的 Transport。
// 这样 WS 升级（101 及之后的全双工字节流）由标准库处理，不需要自己写帧协议。
func NewRelayHandler(hub RelayHubService) http.Handler {
	if hub == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			respondError(w, http.StatusServiceUnavailable, errInvalidRequest("relay not configured"))
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, rest, ok := splitRelayPath(r.URL.Path)
		if !ok {
			respondError(w, http.StatusNotFound, errRelayUnknown)
			return
		}
		ctx := r.Context()
		edge, err := hub.EdgeByRelayName(ctx, name)
		if err != nil {
			respondError(w, http.StatusNotFound, errRelayUnknown)
			return
		}
		if edge.Revoked() {
			// 被踢掉的机器不再对外提供服务。403 而不是 404：地址是对的，
			// 是运维明确关掉的，调用方不该去猜是不是自己写错了。
			respondError(w, http.StatusForbidden, errEdgeRevoked)
			return
		}
		if !hub.Online(edge.InstanceID) {
			respondError(w, http.StatusServiceUnavailable, errRelayOffline)
			return
		}

		proxy := &httputil.ReverseProxy{
			Director: func(req *http.Request) {
				// 边控端看到的是它自己的原始路径：relay 前缀已经被剥掉。
				// 保留查询串 —— /relay/hasee/api/dirs?root=x 里的 root 是业务参数。
				req.URL.Path = rest
				req.URL.RawPath = ""
				// Host 换成边控端自己的名字：它的 handler 里有按 Host 的判断
				// （例如 WS Origin 校验），拿主控域名过去会被当成跨站。
				req.Host = "relay." + name
				if req.Header.Get("X-Forwarded-Proto") == "" {
					req.Header.Set("X-Forwarded-Proto", schemeOf(req))
				}
				req.Header.Set("X-MindFS-Relay-Edge", edge.InstanceID)
			},
			Transport: &relayTransport{hub: hub, instanceID: edge.InstanceID},
			// -1 = 每写一块就往下游刷。SSE 与 WS 靠它不被缓冲。
			FlushInterval: -1,
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				switch {
				case errors.Is(err, errRelayBusy):
					respondError(w, http.StatusServiceUnavailable, errRelayBusy)
				case errors.Is(err, errRelayOffline), errors.Is(err, controlsync.ErrRelayOffline):
					respondError(w, http.StatusServiceUnavailable, errRelayOffline)
				default:
					respondError(w, http.StatusBadGateway, errInvalidRequest("relay upstream failed"))
				}
			},
		}
		proxy.ServeHTTP(w, r)
	})
}

// splitRelayPath 把 /relay/<name>/<rest> 拆开。
//
// 每一段都要过校验：名字是外部输入，路径其余部分会被拼成边控端的请求路径，
// 任何 `..`、空段、编码过的分隔符都必须在这里拒绝（仓库既有规则：路径校验
// 在拼之前做，不做就等于是路径穿越）。返回的 rest 一定以 "/" 开头。
func splitRelayPath(p string) (name, rest string, ok bool) {
	p = strings.TrimPrefix(p, "/")
	if !strings.HasPrefix(p, "relay/") {
		return "", "", false
	}
	p = strings.TrimPrefix(p, "relay/")
	slash := strings.IndexByte(p, '/')
	if slash < 0 {
		return "", "", false
	}
	name = p[:slash]
	rest = p[slash:]
	if !controlsync.ValidRelayName(name) {
		return "", "", false
	}
	if !validRelayRest(rest) {
		return "", "", false
	}
	return name, rest, true
}

// validRelayRest 拒绝会在边控端上变成另一次路径解析的输入。
//
// 空段、`.`、`..`、反斜杠、以及编码后的 %2f / %5c / %2e 都会让下游把它当成
// 别的路径。这里按「解码后」判断，因为 r.URL.Path 已经是解码过的，而 RawPath
// 在 Director 里被清掉了 —— 两道都堵住。
func validRelayRest(p string) bool {
	if !strings.HasPrefix(p, "/") {
		return false
	}
	lower := strings.ToLower(p)
	for _, bad := range []string{"%2f", "%5c", "%2e", "\\", "//"} {
		if strings.Contains(lower, bad) {
			return false
		}
	}
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case ".", "..":
			return false
		}
	}
	return true
}

func schemeOf(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if v := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); v != "" {
		return v
	}
	return "http"
}

// relayTransport 让 ReverseProxy 直接用隧道连接，不经过真实的 TCP 拨号。
//
// DisableKeepAlives 是这里的关键：它让 net/http 在每个请求结束后关闭连接，
// 「一条数据通道 = 一次请求」因此是这个传输层自然语义，反过来边控端那边也就
// 不需要在字节流里切分请求边界。
type relayTransport struct {
	hub        RelayHubService
	instanceID string
}

func (t *relayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	if t == nil || t.hub == nil {
		return nil, errRelayOffline
	}
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > relayRequestTimeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, relayRequestTimeout)
		defer cancel()
	}
	conn, err := t.hub.Acquire(ctx, t.instanceID)
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return conn, nil
		},
		DisableKeepAlives: true,
		// 边控端那边也是 Go 的 http.Server，默认就接受 HTTP/1.1；
		// 明确关掉 h2 是因为隧道是一条裸字节流，不需要 ALPN 协商。
		ForceAttemptHTTP2:   false,
		MaxIdleConns:        -1,
		IdleConnTimeout:     time.Second,
		TLSHandshakeTimeout: time.Second,
		// 大文件上传：不给请求体加额外的缓冲上限。
		ExpectContinueTimeout: 0,
	}
	outReq := req.Clone(ctx)
	outReq.URL.Scheme = "http"
	outReq.URL.Host = "relay"
	return tr.RoundTrip(outReq)
}

// relayURL 只用于日志与诊断：把 relay 名字拼成一个可读地址。
func relayURL(scheme, host, name string) string {
	u := url.URL{Scheme: scheme, Host: host, Path: "/relay/" + name + "/"}
	return u.String()
}
