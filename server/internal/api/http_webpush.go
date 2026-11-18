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
		Endpoint:  strings.TrimSpace(req.Endpoint),
		Keys:      req.Keys,
		UserAgent: strings.TrimSpace(r.UserAgent()),
		Platform:  strings.TrimSpace(req.Platform),
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"subscription": sub,
	})
}

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
