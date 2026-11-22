package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"mindfs/server/internal/controlsync"
)

// ControlSyncService 是 HTTP 层用到的控制面同步能力。
//
// 只收「人要看/人要按」的三件事；转发与追平由中间件和 Replicator 各自驱动，
// 不从这里穿过去 —— 否则 HTTP 层会变成同步内核的第二张装配图。
type ControlSyncService interface {
	Status(ctx context.Context) (controlsync.Status, error)
	SwitchLocal(ctx context.Context) error
	Retry(ctx context.Context) (controlsync.Mode, error)
}

// ControlSyncPusher 是主节点侧接收边缘推送的入口（spec §6.1）。
// 与 ControlSyncService 分开：边缘没有 Pusher，主节点也不一定有模式控制器。
type ControlSyncPusher interface {
	Push(ctx context.Context, req controlsync.PushRequest) (controlsync.PushBatch, error)
}

// ControlSyncFeed 是主节点侧的只读同步端点：head 与 changes。
// 与 Pusher 同理，边缘没有它。
type ControlSyncFeed interface {
	Head(ctx context.Context) (int64, error)
	Page(ctx context.Context, since int64, limit int) ([]controlsync.RemoteChange, int64, error)
}

// errControlSyncUnauthorized 是同步通道鉴权失败的唯一响应体。
// 不回显「token 错了还是没带」，两者对调用方是同一件事。
var errControlSyncUnauthorized = errors.New("control_sync: unauthorized")

// errSyncBodyTooLarge 只用于区分 413 与读失败（400）。
var errSyncBodyTooLarge = errors.New("controlsync: body too large")

// requireSyncToken 给同步通道端点套上认证：bearer token + 时间戳 + nonce + HMAC。
//
// 这些端点能一次读出（或改写）整个控制面，包括用户口令 hash —— 主节点是公网可达的，
// 不能沿用应用里「API 层不鉴权」的约定。只有配了 token 才注册这些路由；
// token 形状在启动时校验，这里做完整校验。
//
// 校验要读 body，所以中间件必须先收全再还原：签名覆盖原始字节，任何「边读边验」
// 都会让下游拿到被截断的载荷。上限与 push handler 共用同一个常量，8 MiB 以外
// 在认证层就回 413，不把超大体读进内存再交给解码器。
func (h *HTTPHandler) requireSyncToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := readSyncBody(r)
		if err != nil {
			if errors.Is(err, errSyncBodyTooLarge) {
				respondError(w, http.StatusRequestEntityTooLarge, errInvalidRequest("control sync body too large"))
			} else {
				respondError(w, http.StatusBadRequest, errInvalidRequest("control sync body unreadable"))
			}
			return
		}
		if err := controlsync.VerifySyncRequest(r, body, controlsync.SyncVerifyOptions{
			Token:  h.ControlSyncToken,
			Nonces: h.syncNonceCache(),
		}); err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="control-sync"`)
			respondError(w, http.StatusUnauthorized, errControlSyncUnauthorized)
			return
		}
		next(w, r)
	}
}

// syncNonceCache 懒创建：正常构造路径与测试都只设置 ControlSyncToken，
// 不该为了一个重放缓存再要求每个调用方多传一个参数。
func (h *HTTPHandler) syncNonceCache() *controlsync.NonceCache {
	h.controlSyncNoncesOnce.Do(func() {
		h.controlSyncNonces = controlsync.NewNonceCache(controlsync.SyncNonceTTL)
	})
	return h.controlSyncNonces
}

// readSyncBody 把请求体收进内存并放回 r.Body。超上限返回错误而不是截断：
// 截断后的 body 既验不过签名，也不该被下游当成一份合法但缩水的请求。
func readSyncBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, controlsync.SyncMaxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > controlsync.SyncMaxBodyBytes {
		return nil, errSyncBodyTooLarge
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

// handleControlSyncHead 返回变更流水位。探活也打这条：只判状态码、不看内容。
func (h *HTTPHandler) handleControlSyncHead(w http.ResponseWriter, r *http.Request) {
	if h.ControlSyncFeed == nil {
		respondError(w, http.StatusNotFound, errInvalidRequest("control sync feed not configured"))
		return
	}
	head, err := h.ControlSyncFeed.Head(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]int64{"head_rev": head})
}

// handleControlSyncChanges 返回 since 之后的一页变更。
//
// since 缺省 0（从头拉）；limit 缺省 ChangePageDefault、超上限夹住。
// 参数不合法一律 400：静默改写成默认值会让「空页」被读成「已追平」。
func (h *HTTPHandler) handleControlSyncChanges(w http.ResponseWriter, r *http.Request) {
	if h.ControlSyncFeed == nil {
		respondError(w, http.StatusNotFound, errInvalidRequest("control sync feed not configured"))
		return
	}
	q := r.URL.Query()
	since := int64(0)
	if raw := strings.TrimSpace(q.Get("since")); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			respondError(w, http.StatusBadRequest, errInvalidRequest("since must be a non-negative integer"))
			return
		}
		since = v
	}
	limit := 0
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			respondError(w, http.StatusBadRequest, errInvalidRequest("limit must be a positive integer"))
			return
		}
		limit = v
	}
	changes, head, err := h.ControlSyncFeed.Page(r.Context(), since, limit)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err)
		return
	}
	if changes == nil {
		changes = []controlsync.RemoteChange{}
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"changes":  changes,
		"head_rev": head,
	})
}

// handleControlSyncStatus 返回四态模式、水位与最后一次探测信息。
// 两种模式（online / offline-local）都要能读到它，否则断线时前端连横幅都点不出来。
func (h *HTTPHandler) handleControlSyncStatus(w http.ResponseWriter, r *http.Request) {
	if h.ControlSync == nil {
		respondError(w, http.StatusNotFound, errInvalidRequest("control sync not configured"))
		return
	}
	status, err := h.ControlSync.Status(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, err)
		return
	}
	respondJSON(w, http.StatusOK, status)
}

// handleControlSyncMode 接受用户动作：{action:"local"|"retry"}。
//
// retry 探不通时仍回 200 + 最新 status：动作本身被接受了，结果（仍是 offline-local、
// last_error 写明原因）由 status 表达。用 5xx 表达「主节点不通」会让前端把
// 「自己按的按钮失败了」和「页面请求失败了」混成一件事。
func (h *HTTPHandler) handleControlSyncMode(w http.ResponseWriter, r *http.Request) {
	if h.ControlSync == nil {
		respondError(w, http.StatusNotFound, errInvalidRequest("control sync not configured"))
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10))
	if err := dec.Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid body"))
		return
	}
	action := strings.TrimSpace(body.Action)
	switch action {
	case "local":
		if err := h.ControlSync.SwitchLocal(r.Context()); err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
	case "retry":
		if _, err := h.ControlSync.Retry(r.Context()); err != nil {
			// 探不通不是接口错误：状态里已经写明 reason，照常回 status。
			_ = err
		}
	default:
		respondError(w, http.StatusBadRequest, errInvalidRequest("action must be local or retry"))
		return
	}
	status, err := h.ControlSync.Status(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, err)
		return
	}
	respondJSON(w, http.StatusOK, status)
}

// handleControlSyncPush 接收边缘的待同步变更，逐条回结论。
//
// 请求体上限按一整批 tombstone/小对象估算给 8 MiB：超限直接 413，
// 而不是让解码器在超大 JSON 上慢慢吃内存。
func (h *HTTPHandler) handleControlSyncPush(w http.ResponseWriter, r *http.Request) {
	if h.ControlSyncPush == nil {
		respondError(w, http.StatusNotFound, errInvalidRequest("control sync push not configured"))
		return
	}
	var req controlsync.PushRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
	if err := dec.Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid push body"))
		return
	}
	batch, err := h.ControlSyncPush.Push(r.Context(), req)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err)
		return
	}
	respondJSON(w, http.StatusOK, batch)
}

// ControlSyncConflicts 是冲突卡的查询与裁决端口。冲突只在「两侧都改过」时产生，
// 裁决永远由人做出 —— 这里不提供自动合并。
type ControlSyncConflicts interface {
	List(ctx context.Context) ([]controlsync.ConflictView, error)
	Resolve(ctx context.Context, id, choice string, payload []byte) error
}

type controlSyncConflictList struct {
	Conflicts []controlsync.ConflictView `json:"conflicts"`
}

func (h *HTTPHandler) handleControlSyncConflicts(w http.ResponseWriter, r *http.Request) {
	views, err := h.ControlSyncConflicts.List(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, err)
		return
	}
	if views == nil {
		views = []controlsync.ConflictView{}
	}
	respondJSON(w, http.StatusOK, controlSyncConflictList{Conflicts: views})
}

type controlSyncResolveRequest struct {
	Choice  string          `json:"choice"`
	Payload json.RawMessage `json:"payload"`
}

// 冲突卡裁决：`{"choice":"local"|"primary"|"payload","payload":{...}}`。
// 404 / 409 / 400 分别对应「没有这张卡」「已经裁决过」「选择或载荷不合法」。
func (h *HTTPHandler) handleControlSyncConflictResolve(w http.ResponseWriter, r *http.Request) {
	// 冲突 id 里可能带 `/`（prompts 的 key 是 `<账户>/sha256:<hash>`，nodes 是 URL），
	// 前端只能整体 encodeURIComponent。chi 按 RawPath 匹配、URLParam 原样返回转义后的
	// 段，这里必须还原一次，否则这些域的冲突卡点不动。
	id, err := url.PathUnescape(chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid conflict id"))
		return
	}
	if strings.TrimSpace(id) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("conflict id required"))
		return
	}
	var req controlSyncResolveRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	err = h.ControlSyncConflicts.Resolve(r.Context(), id, req.Choice, req.Payload)
	switch {
	case err == nil:
		respondJSON(w, http.StatusOK, map[string]any{"id": id, "choice": req.Choice})
	case errors.Is(err, controlsync.ErrConflictNotFound):
		respondError(w, http.StatusNotFound, err)
	case errors.Is(err, controlsync.ErrConflictResolved):
		respondError(w, http.StatusConflict, err)
	case errors.Is(err, controlsync.ErrConflictChoice):
		respondError(w, http.StatusBadRequest, err)
	default:
		respondError(w, http.StatusInternalServerError, err)
	}
}
