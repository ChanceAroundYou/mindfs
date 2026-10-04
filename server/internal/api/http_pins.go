package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"

	"mindfs/server/internal/pins"
	"mindfs/server/internal/session"
)

// 置顶（pin）的 HTTP 面。
//
// **控制面**：权威只在主节点，worker 上这些端点一律 403（见 nodeinfo 前缀表），
// 前端用 controlPath 打页面服务器而不是跟着选中节点走。
//
// 键的形状：
//   - 项目置顶 = scopeKey（nodeID::rootID），与旧偏好里的格式逐字节相同，
//     所以存量键不用重写。
//   - 会话置顶 = rootID::sessionKey（**不带 node id**）。会话数据面按机器分，
//     但「顶哪些」是跨设备的一致性偏好：键里带上 node id 就等于把置顶按节点
//     分了片，改键等于改语义。前端按选中节点各自读回自己那部分。
//
// 不做跨设备实时推送（用户 2026-10-04 定）：置顶变更只在切换项目等导航时顺带刷新，
// 设备之间最多差一次刷新。

// pinsResponse 是 GET/PUT 的统一回包。
type pinsResponse struct {
	// Projects 是项目置顶（键 = scopeKey，值 = 毫秒时间戳）。
	Projects map[string]int64 `json:"projects"`
	// Sessions 是会话置顶（键 = rootID::sessionKey，值 = RFC3339）。
	Sessions map[string]string `json:"sessions"`
}

func (h *HTTPHandler) requirePins(w http.ResponseWriter) bool {
	if h.AppContext == nil || h.AppContext.GetPins() == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("pins not configured"))
		return false
	}
	return true
}

func (h *HTTPHandler) handlePinsGet(w http.ResponseWriter, _ *http.Request) {
	if !h.requirePins(w) {
		return
	}
	respondJSON(w, http.StatusOK, pinsResponse{
		Projects: h.AppContext.GetPins().ProjectPins(),
		Sessions: h.AppContext.GetPins().SessionPinnedAtAll(),
	})
}

type projectPinRequest struct {
	Key    string `json:"key"`
	Pinned bool   `json:"pinned"`
}

func (h *HTTPHandler) handlePinsProjectPut(w http.ResponseWriter, r *http.Request) {
	if !h.requirePins(w) {
		return
	}
	var req projectPinRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		return
	}
	if strings.TrimSpace(req.Key) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("project scope key required"))
		return
	}
	if _, _, err := h.AppContext.GetPins().SetProjectPin(req.Key, req.Pinned); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		return
	}
	respondJSON(w, http.StatusOK, pinsResponse{
		Projects: h.AppContext.GetPins().ProjectPins(),
		Sessions: h.AppContext.GetPins().SessionPinnedAtAll(),
	})
}

type sessionPinRequest struct {
	RootID string `json:"root_id"`
	Key    string `json:"key"`
	Pinned bool   `json:"pinned"`
}

// 它同时是 /api/pins/session 的请求体与旧路径的内部传参：旧的
// /api/sessions/{key}/pin 用 URL 参数带 root/key，只读 body 的 pinned。
// 用同一个结构体是刻意的 —— 两条路径写的是同一张表，不该有两个形状。

func (h *HTTPHandler) handlePinsSessionPut(w http.ResponseWriter, r *http.Request) {
	var req sessionPinRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		return
	}
	h.pinsSessionPut(w, r, req)
}

// pinsSessionPut 是两个入口共用的实现（/api/pins/session 与旧的
// /api/sessions/{key}/pin）：写置顶表，回整张表。
//
// 会话置顶**不再**广播 WS：广播只到本机连接的浏览器，而置顶的权威在主节点，
// 各 worker 的浏览器是各自直连的，主节点碰不到 —— 发了也只是让本机这一份
// UI 少闪一次，代价是让人以为跨设备实时能work。各设备下次导航时刷新即可。
func (h *HTTPHandler) pinsSessionPut(w http.ResponseWriter, _ *http.Request, req sessionPinRequest) {
	if !h.requirePins(w) {
		return
	}
	key := sessionPinKey(req.RootID, req.Key)
	if key == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root_id and key required"))
		return
	}
	if _, _, err := h.AppContext.GetPins().SetSessionPin(key, req.Pinned); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		return
	}
	respondJSON(w, http.StatusOK, pinsResponse{
		Projects: h.AppContext.GetPins().ProjectPins(),
		Sessions: h.AppContext.GetPins().SessionPinnedAtAll(),
	})
}

// sessionPinKey 拼会话置顶键：rootID::sessionKey（rootID 可空，表示跨项目通用）。
//
// 分隔符与项目置顶的 scopeKey 共用同一个 "::"，靠**段数**区分（前端 scope.ts
// 是同样的规则）：项目键 1 段（nodeID::rootID）或 2 段（rootID 单独），
// 会话键恒 2 段且第二段是会话 key —— 真实会话 key 带 UUID，绝不会等于项目名，
// 但不靠这个运气：调用方各自知道自己是哪种。
func sessionPinKey(rootID, sessionKey string) string {
	rootID = strings.TrimSpace(rootID)
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return ""
	}
	if rootID == "" {
		return sessionKey
	}
	return rootID + "::" + sessionKey
}

// pinnedSessionsForRoot 从置顶表取某个项目的已置顶会话，按置顶时间倒序。
//
// 查不到的键被静默丢弃：置顶表跨节点跨项目，读者只认自己这份会话库里有的。
// 这正是 worker 的行为 —— 它读到的置顶表是空的（控制面 403），返回空切片，
// 前端拿主节点的置顶键做叠加即可。
func (h *HTTPHandler) pinnedSessionsForRoot(ctx context.Context, rootID string, topLevelOnly bool) ([]*session.Session, []string) {
	if h.AppContext == nil || h.AppContext.GetPins() == nil {
		return nil, nil
	}
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return nil, nil
	}
	manager, err := h.AppContext.GetSessionManager(rootID)
	if err != nil || manager == nil {
		return nil, nil
	}
	keys := pinSessionKeysForRoot(h.AppContext.GetPins(), rootID)
	if len(keys) == 0 {
		return nil, nil
	}
	items, err := manager.ListByKeys(ctx, keys)
	if err != nil {
		// 取不到就当没置顶：置顶是装饰性排序数据，不该让整个会话列表 500。
		return nil, nil
	}
	return items, pinnedSessionKeys(items)
}

// pinSessionKeysForRoot 从置顶表里挑出属于某个项目的会话键。
func pinSessionKeysForRoot(store *pins.Store, rootID string) []string {
	prefix := rootID + "::"
	out := make([]string, 0, 8)
	for _, scoped := range store.SessionKeys() {
		if !strings.HasPrefix(scoped, prefix) {
			continue
		}
		key := strings.TrimSpace(strings.TrimPrefix(scoped, prefix))
		if key == "" {
			continue
		}
		out = append(out, key)
	}
	// 按置顶时间倒序，与旧的 pinned_at DESC 排序一致。
	sort.SliceStable(out, func(i, j int) bool {
		a, _ := store.SessionPinnedAt(prefix + out[i])
		b, _ := store.SessionPinnedAt(prefix + out[j])
		return a.After(b)
	})
	return out
}

func pinnedSessionKeys(items []*session.Session) []string {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		if item == nil || strings.TrimSpace(item.Key) == "" {
			continue
		}
		keys = append(keys, item.Key)
	}
	return keys
}
