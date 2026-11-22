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

// handleFeishuAnswer is the worker-side data-plane entry for a click relayed
// by the control node. The shared token is compared in constant time and the
// card's node id must name this machine; neither identity comes from a URL.
func (h *HTTPHandler) handleFeishuAnswer(w http.ResponseWriter, r *http.Request) {
	configured := strings.TrimSpace(h.FeishuRelayToken)
	presented := strings.TrimSpace(r.Header.Get(feishu.RelayTokenHeader))
	if feishu.ValidateRelayToken(configured) != nil ||
		len(presented) != len(configured) ||
		subtle.ConstantTimeCompare([]byte(presented), []byte(configured)) != 1 {
		respondError(w, http.StatusUnauthorized, feishu.ErrRelayTokenInvalid)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxFeishuRelayBodyBytes)
	var answer feishu.Answer
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&answer); err != nil {
		respondError(w, http.StatusBadRequest, errors.New("invalid answer body"))
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		respondError(w, http.StatusBadRequest, errors.New("invalid answer body"))
		return
	}

	local := strings.TrimSpace(h.FeishuNodeID)
	if local == "" || strings.TrimSpace(answer.NodeID) != local {
		respondError(w, http.StatusForbidden, feishu.ErrRelayNodeInvalid)
		return
	}

	apply := h.applyFeishuAnswer
	if apply == nil {
		if h.AppContext == nil {
			respondError(w, http.StatusServiceUnavailable, errors.New("feishu: no local workspace"))
			return
		}
		apply = h.AppContext.ApplyFeishuAnswer
	}
	if err := apply(r.Context(), answer); err != nil {
		respondError(w, http.StatusInternalServerError, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}
