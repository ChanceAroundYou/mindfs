package api

import (
	"errors"
	"net/http"
	"strings"
	"sync"
)

// userQueryParam 是客户端声明身份的查询参数。API 层不做鉴权（见 docs/multi-user-prd.md §1.1），
// 服务端只按这个值分区，不校验——这是刻意的，不要在这里加授权。
const userQueryParam = "user"

// ErrUnknownUser 客户端声明了一个不存在的账户（多半是账户被删后前端还存着旧 id）。
var ErrUnknownUser = errors.New("unknown_user")

// WorkspaceProvider 按账户 id 提供该账户的 AppContext。
type WorkspaceProvider interface {
	// Workspace 返回账户的工作区；userID 为空表示主账户。
	Workspace(userID string) (*AppContext, error)
	// PrimaryUserID 是存量数据的归属账户（请求没带 user= 时用它）。
	PrimaryUserID() string
}

// scopedEntry 保证每个账户的工作区/handler 只构建一次，且并发请求会等待第一次构建完成。
// 用 per-account 的 once 而不是一把全局锁：构建要开 SQLite、起探针 goroutine，
// 全局锁会让一个账户的慢启动拖住所有账户。
type scopedEntry struct {
	once  sync.Once
	value http.Handler
	err   error
}

// ScopedRouter 按请求上的 user= 把请求派发给对应账户的 handler。
//
// 每个账户拿到一份独立的 *HTTPHandler / *WSHandler，于是几百处 h.AppContext 引用
// 一处都不用改，隔离天然成立——每个 handler 的 AppContext 本来就是那个账户的全部状态。
type ScopedRouter struct {
	provider WorkspaceProvider
	build    func(*AppContext) http.Handler

	mu      sync.Mutex
	entries map[string]*scopedEntry
}

func NewScopedRouter(provider WorkspaceProvider, build func(*AppContext) http.Handler) *ScopedRouter {
	return &ScopedRouter{provider: provider, build: build, entries: map[string]*scopedEntry{}}
}

// RequestUserID 解析请求声明的账户 id；为空则回落到主账户。
func RequestUserID(r *http.Request, provider WorkspaceProvider) string {
	uid := strings.TrimSpace(r.URL.Query().Get(userQueryParam))
	if uid != "" {
		return uid
	}
	if provider == nil {
		return ""
	}
	return provider.PrimaryUserID()
}

func (s *ScopedRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	uid := RequestUserID(r, s.provider)
	if uid == "" {
		respondError(w, http.StatusServiceUnavailable, errors.New("no account configured"))
		return
	}
	handler, err := s.handlerFor(uid)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrUnknownUser) {
			status = http.StatusNotFound
		}
		respondError(w, status, err)
		return
	}
	handler.ServeHTTP(w, r)
}

func (s *ScopedRouter) handlerFor(uid string) (http.Handler, error) {
	s.mu.Lock()
	entry, ok := s.entries[uid]
	if !ok {
		entry = &scopedEntry{}
		s.entries[uid] = entry
	}
	s.mu.Unlock()

	entry.once.Do(func() {
		ctx, err := s.provider.Workspace(uid)
		if err != nil {
			entry.err = err
			return
		}
		entry.value = s.build(ctx)
	})
	return entry.value, entry.err
}
