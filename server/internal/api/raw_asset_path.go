package api

import (
	"net/http"
	"strings"
)

// RawAssetPathPrefix 是「原始资源」端点的路径前缀。
//
// 端点形态：/api/file/raw/{account}/{root}/{path...}
// 账户段必须编码在**路径**里而不是查询串里 —— 网页里的相对 URL
// （style.css、img/x.png）在浏览器里解析时会丢掉查询串，
// 账户分区又只认 user=（见 workspace.go），所以账户不进路径，
// 网页的相对资源就会掉回主账户去取。
const RawAssetPathPrefix = "/api/file/raw/"

// WithRawAccountPath 把路径里的账户段搬进 user= 查询参数，并从路径里剥掉它。
//
// **顺序要求**：这个中间件必须跑在 ScopedRouter 之前 —— 账户在派发那一刻就要定下来，
// 派发之后 chi 才按 "/api/file/raw/{root}/*" 匹配剩下的文件路径。
func WithRawAccountPath(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if !strings.HasPrefix(path, RawAssetPathPrefix) {
			next.ServeHTTP(w, r)
			return
		}
		rest := strings.TrimPrefix(path, RawAssetPathPrefix)
		sep := strings.Index(rest, "/")
		if sep <= 0 {
			// 没有账户段（或账户段为空）：按原样放行，落到既有语义（主账户 / 404）。
			next.ServeHTTP(w, r)
			return
		}
		account := strings.TrimSpace(rest[:sep])
		if account == "" {
			next.ServeHTTP(w, r)
			return
		}

		// r.URL.Path 已经是解码一次的结果（net/http 解析时就解过了），
		// 所以账户段直接用，**不要**再 PathUnescape —— 那会把用户名里的 "%41" 再解一次。
		cloned := r.Clone(r.Context())
		cloned.URL.Path = RawAssetPathPrefix + rest[sep+1:]
		// RawPath 是转义态的原路径，chi 优先用它匹配：留着它，路由参数会停在转义态
		//（文件名带空格/中文时路径就对不上）。清掉后 chi 用解码后的 Path 匹配，
		// handler 直接拿到可读路径。
		cloned.URL.RawPath = ""

		query := cloned.URL.Query()
		if strings.TrimSpace(query.Get(userQueryParam)) == "" {
			query.Set(userQueryParam, account)
			cloned.URL.RawQuery = query.Encode()
		}
		next.ServeHTTP(w, cloned)
	})
}
