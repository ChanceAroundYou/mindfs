package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"mindfs/server/internal/webpush"
)

type webPushSubscriptionRequest struct {
	Endpoint string                   `json:"endpoint"`
	Keys     webpush.SubscriptionKeys `json:"keys"`
	Platform string                   `json:"platform"`
	// Presence 由新版客户端声明：此后只有页面还开着（有心跳）时才给这条订阅发通知。
	Presence bool   `json:"presence"`
	ClientID string `json:"client_id"`
}

func (h *HTTPHandler) webPushService() (*webpush.Service, error) {
	if h == nil || h.AppContext == nil || h.AppContext.GetWebPushService() == nil {
		return nil, errServiceUnavailable("web push service not configured")
	}
	return h.AppContext.GetWebPushService(), nil
}

func (h *HTTPHandler) handleWebPushStatus(w http.ResponseWriter, r *http.Request) {
	svc, err := h.webPushService()
	if err != nil {
		respondJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	respondJSON(w, http.StatusOK, svc.Status())
}

func (h *HTTPHandler) handleWebPushSubscriptionSave(w http.ResponseWriter, r *http.Request) {
	svc, err := h.webPushService()
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, err)
		return
	}
	var req webPushSubscriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid web push subscription payload"))
		return
	}
	sub, err := svc.SaveSubscription(webpush.Subscription{
		Endpoint:        strings.TrimSpace(req.Endpoint),
		Keys:            req.Keys,
		UserAgent:       strings.TrimSpace(r.UserAgent()),
		Platform:        strings.TrimSpace(req.Platform),
		PresenceCapable: req.Presence,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		return
	}
	if req.Presence {
		// 刚上报订阅的页面此刻一定开着，立刻记一次在场，省掉等到下一次心跳的窗口。
		_ = svc.ReportPresence(sub.Endpoint, req.ClientID, false)
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"subscription": sub,
	})
}

// handleWebPushPresence 接收页面心跳与关页上报。body: {endpoint, client_id, leave}。
// 返回 active 让调用方（service worker 自愈）能直接看出服务端是否仍认为页面在场。
func (h *HTTPHandler) handleWebPushPresence(w http.ResponseWriter, r *http.Request) {
	svc, err := h.webPushService()
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, err)
		return
	}
	if !svc.Enabled() {
		// 没开推送的节点收到心跳是正常现象（页面照常工作），不该让 beacon 报错。
		respondJSON(w, http.StatusOK, map[string]any{"ok": false, "active": false})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWebPushPresenceBodyBytes)
	var req struct {
		Endpoint string `json:"endpoint"`
		ClientID string `json:"client_id"`
		Leave    bool   `json:"leave"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid web push presence payload"))
		return
	}
	endpoint := strings.TrimSpace(req.Endpoint)
	if endpoint == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("web push subscription endpoint is required"))
		return
	}
	if err := svc.ReportPresence(endpoint, req.ClientID, req.Leave); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"active": svc.PresenceActive(endpoint),
	})
}

// maxWebPushPresenceBodyBytes 限制心跳请求体：只有三个短字段。
const maxWebPushPresenceBodyBytes = 4 << 10

func (h *HTTPHandler) handleWebPushSubscriptionDelete(w http.ResponseWriter, r *http.Request) {
	svc, err := h.webPushService()
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, err)
		return
	}
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := svc.DeleteSubscription(req.Endpoint); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *HTTPHandler) handleWebPushTest(w http.ResponseWriter, r *http.Request) {
	svc, err := h.webPushService()
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, err)
		return
	}
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if err := svc.SendTestToEndpoint(r.Context(), req.Endpoint); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// maxRelayBodyBytes 限制中继请求体：通知载荷本身不到 4KB，留足余量即可。
const maxRelayBodyBytes = 64 << 10

// handleWebPushRelay 接收 worker 转来的通知，用本节点的 VAPID 密钥与订阅表代发。
//
// 只有控制节点有订阅表（/api/web-push/* 在 worker 上 403），所以 worker 自己发不出去；
// 它把载荷交给这里。API 层不鉴权（用户决策），因此只做「不可能是本系统产生的载荷」校验，
// 见 webpush.ValidateRelayedPayload。
func (h *HTTPHandler) handleWebPushRelay(w http.ResponseWriter, r *http.Request) {
	svc, err := h.webPushService()
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, err)
		return
	}
	var req webpush.RelayRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRelayBodyBytes)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid web push relay payload"))
		return
	}
	if err := webpush.ValidateRelayedPayload(req.Payload); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		return
	}
	// context.Background()：发送在异步分支里进行，不能用请求的 ctx —— handler 返回后
	// 它就被取消了，推送会半路中断。
	svc.NotifyPayload(context.Background(), req.EventID, req.Payload)
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}
