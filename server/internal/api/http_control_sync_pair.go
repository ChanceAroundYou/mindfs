package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"mindfs/server/internal/controlsync"
)

// ControlSyncPairing 是「把本机接到主控端」这一动作。
//
// 它只在**还没有配对好的边控端**上挂出来 —— 那个状态下的端点里没有 token 可校验，
// 因为 token 正是它要解决的问题。守卫换成「只接受本机回环请求」：配对会改信任锚
// （primary_url）、写入通道密钥、并改 config.json，这三件事凑在一起，等于在
// 0.0.0.0 上开一个无鉴权的「重定向整份控制面」入口。
type ControlSyncPairing interface {
	Pair(ctx context.Context, req controlsync.PairRequest) (controlsync.PairResult, error)
	// PairingState 是给前端的只读探针：这条路由存在就等于「本机是等着配对的边缘」。
	// 少了它，页面拿不到任何信号 —— `/api/control-sync/status` 在未配对时同样 404，
	// 与「这台机器根本不同步」无法区分，表单只能永远不出现。
	PairingState() PairingState
}

// PairingState 只描述「还差什么」，不含 token 与任何路径。
type PairingState struct {
	PendingPairing bool   `json:"pending_pairing"`
	InstanceID     string `json:"instance_id"`
}

// pairRequestBody 是配对表单提交的形状。
type pairRequestBody struct {
	PrimaryURL string `json:"primary_url"`
	Token      string `json:"token"`
	InstanceID string `json:"instance_id"`
	CAFile     string `json:"ca_file"`
}

// handleControlSyncPair 处理一次配对。成功即落盘，但**不重启**：
// 配置要等下一次进程启动才生效，这一点必须如实告诉调用方（restart_required），
// 否则界面会显示「已连接」而实际仍在未配对状态。
func (h *HTTPHandler) handleControlSyncPair(w http.ResponseWriter, r *http.Request) {
	if h.ControlSyncPair == nil {
		respondError(w, http.StatusNotFound, errInvalidRequest("control sync pairing not configured"))
		return
	}
	if !isLoopbackRequest(r) {
		// 这条不是「少个便利」，是安全边界：见 ControlSyncPairing 的注释。
		respondError(w, http.StatusForbidden,
			errInvalidRequest("pairing must be done from the edge machine itself (open its local page)"))
		return
	}
	var body pairRequestBody
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	if err := dec.Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid pairing request body"))
		return
	}
	result, err := h.ControlSyncPair.Pair(r.Context(), controlsync.PairRequest{
		PrimaryURL: strings.TrimSpace(body.PrimaryURL),
		Token:      strings.TrimSpace(body.Token),
		InstanceID: strings.TrimSpace(body.InstanceID),
		CAFile:     strings.TrimSpace(body.CAFile),
	})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, controlsync.ErrPairUnreachable) {
			status = http.StatusBadGateway
		}
		respondError(w, status, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"status":           "paired",
		"restart_required": true,
		"instance_id":      result.InstanceID,
		"primary_url":      result.PrimaryURL,
		"primary_version":  result.PrimaryBuild,
		"head_rev":         result.HeadRev,
	})
}

// handleControlSyncPairState 回一份只读的配对状态。
//
// 不做回环守卫：它只回「本机等着配对」与实例名，这两样在别处本来就能看到
// （`/api/node-info`）。真正会改信任锚的是 POST，那条才必须限本机。
func (h *HTTPHandler) handleControlSyncPairState(w http.ResponseWriter, r *http.Request) {
	if h.ControlSyncPair == nil {
		respondError(w, http.StatusNotFound, errInvalidRequest("control sync pairing not configured"))
		return
	}
	respondJSON(w, http.StatusOK, h.ControlSyncPair.PairingState())
}

// isLoopbackRequest 判断请求是否来自本机。
//
// 只看 RemoteAddr，不信 X-Forwarded-For 之类客户端自带的东西 —— 那类头任何人都能写，
// 拿它当安全判据等于没有判据。反代把配对请求转到回环上确实会让它「看起来是本机」，
// 但那种部署下配对入口本来就该由运维自己收口。
func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}
