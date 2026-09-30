package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"mindfs/server/internal/nodes"

	"github.com/go-chi/chi/v5"
)

func (h *HTTPHandler) handleNodesList(w http.ResponseWriter, _ *http.Request) {
	store := h.nodesStore()
	if store == nil {
		respondJSON(w, http.StatusOK, []nodes.NodeConnection{})
		return
	}
	respondJSON(w, http.StatusOK, store.List())
}

func (h *HTTPHandler) handleNodesPut(w http.ResponseWriter, r *http.Request) {
	store := h.nodesStore()
	if store == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("nodes store not configured"))
		return
	}
	var payload []nodes.NodeConnection
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&payload); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid nodes payload"))
		return
	}
	if err := store.Replace(payload); err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	h.broadcastNodesChanged()
	respondJSON(w, http.StatusOK, store.List())
}

func (h *HTTPHandler) handleNodesPost(w http.ResponseWriter, r *http.Request) {
	store := h.nodesStore()
	if store == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("nodes store not configured"))
		return
	}
	var node nodes.NodeConnection
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&node); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid node payload"))
		return
	}
	saved, err := store.Upsert(node)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	h.broadcastNodesChanged()
	respondJSON(w, http.StatusOK, saved)
}

func (h *HTTPHandler) handleNodesDelete(w http.ResponseWriter, r *http.Request) {
	store := h.nodesStore()
	if store == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("nodes store not configured"))
		return
	}
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("id required"))
		return
	}
	removed, err := store.Remove(id)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		respondError(w, status, err)
		return
	}
	h.broadcastNodesChanged()
	respondJSON(w, http.StatusOK, removed)
}

func (h *HTTPHandler) nodesStore() *nodes.Store {
	if h == nil || h.AppContext == nil {
		return nil
	}
	return h.AppContext.Nodes
}

func (h *HTTPHandler) broadcastNodesChanged() {
	if h == nil || h.AppContext == nil {
		return
	}
	h.AppContext.GetSessionStreamHub().BroadcastAll(WSResponse{
		Type:    "nodes.changed",
		Payload: map[string]any{},
	})
}
