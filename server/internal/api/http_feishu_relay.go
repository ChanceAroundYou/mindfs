package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"mindfs/server/internal/channels/feishu"
)

// 64 KiB is generous for a card: anything larger is a runaway tool result,
// not a notification, and must not be parsed into the control node.
const maxFeishuRelayBodyBytes = 64 << 10

// handleFeishuRelay accepts a card from a configured worker and records it in
// the control node's Feishu store. The target is read from the node table,
// never from the request body, and the shared token is compared in constant
// time. The token is never written to a response or a log.
func (h *HTTPHandler) handleFeishuRelay(w http.ResponseWriter, r *http.Request) {
	configured := strings.TrimSpace(h.FeishuRelayToken)
	presented := strings.TrimSpace(r.Header.Get(feishu.RelayTokenHeader))
	if feishu.ValidateRelayToken(configured) != nil ||
		len(presented) != len(configured) ||
		subtle.ConstantTimeCompare([]byte(presented), []byte(configured)) != 1 {
		respondError(w, http.StatusUnauthorized, feishu.ErrRelayTokenInvalid)
		return
	}
	if h.AppContext == nil || h.AppContext.Feishu == nil {
		respondError(w, http.StatusServiceUnavailable, feishu.ErrRelayServiceUnavailable)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxFeishuRelayBodyBytes)
	var req feishu.RelayRequest
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errors.New("invalid relay body"))
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		respondError(w, http.StatusBadRequest, errors.New("invalid relay body"))
		return
	}

	var lister feishu.NodeLister
	if h.AppContext.Nodes != nil {
		lister = h.AppContext.Nodes
	}
	if err := feishu.AddRelayNotice(r.Context(), h.AppContext.Feishu, req.NodeID, req.Notice, lister); err != nil {
		switch {
		case errors.Is(err, feishu.ErrRelayNodeInvalid), errors.Is(err, feishu.ErrRelayCardInvalid):
			respondError(w, http.StatusBadRequest, err)
		case errors.Is(err, feishu.ErrRelayServiceUnavailable):
			respondError(w, http.StatusServiceUnavailable, err)
		default:
			respondError(w, http.StatusInternalServerError, err)
		}
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}
