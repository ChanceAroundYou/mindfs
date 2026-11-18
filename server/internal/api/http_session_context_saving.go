package api

import (
	"net/http"

	"mindfs/server/internal/api/usecase"

	"github.com/go-chi/chi/v5"
)

// handleSessionContextSaving serves the read-only live-compact ledger for one
// session: what the proxy actually forwarded versus what the CLI sent.
//
// It is intentionally thin and never starts a session: a conversation that was
// never folded has no ledger file, which reads as an empty summary.
func (h *HTTPHandler) handleSessionContextSaving(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	rootID := r.URL.Query().Get("root")
	saving, err := h.service().GetSessionContextSaving(r.Context(), usecase.GetSessionContextSavingInput{
		RootID: rootID,
		Key:    key,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, saving)
}
