package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"mindfs/server/internal/preferences"
)

type sessionNamingPreferenceRequest struct {
	Agent    string `json:"agent"`
	Model    string `json:"model"`
	Disabled bool   `json:"disabled"`
}

type idleSessionResourceReleasePreferenceRequest struct {
	Hours int `json:"hours"`
}

type newProjectMetaLocationPreferenceRequest struct {
	Location string `json:"location"`
}

func (h *HTTPHandler) handleNewProjectMetaLocationPreferenceGet(w http.ResponseWriter, _ *http.Request) {
	if h.AppContext == nil || h.AppContext.GetPreferences() == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("preferences not configured"))
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"location": h.AppContext.GetPreferences().NewProjectMetaLocation()})
}

func (h *HTTPHandler) handleNewProjectMetaLocationPreferencePut(w http.ResponseWriter, r *http.Request) {
	if h.AppContext == nil || h.AppContext.GetPreferences() == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("preferences not configured"))
		return
	}
	var req newProjectMetaLocationPreferenceRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		return
	}
	if err := h.AppContext.GetPreferences().UpdateNewProjectMetaLocation(req.Location); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"location": h.AppContext.GetPreferences().NewProjectMetaLocation()})
}

func (h *HTTPHandler) handleIdleSessionResourceReleasePreferenceGet(w http.ResponseWriter, _ *http.Request) {
	if h.AppContext == nil || h.AppContext.GetPreferences() == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("preferences not configured"))
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"hours": h.AppContext.GetPreferences().IdleSessionResourceReleaseHours(),
	})
}

func (h *HTTPHandler) handleIdleSessionResourceReleasePreferencePut(w http.ResponseWriter, r *http.Request) {
	if h.AppContext == nil || h.AppContext.GetPreferences() == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("preferences not configured"))
		return
	}
	var req idleSessionResourceReleasePreferenceRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		return
	}
	if req.Hours <= 0 || req.Hours > preferences.MaxIdleSessionResourceReleaseHours {
		respondError(w, http.StatusBadRequest, errInvalidRequest("hours are out of range"))
		return
	}
	if err := h.AppContext.GetPreferences().UpdateIdleSessionResourceReleaseHours(req.Hours); err != nil {
		respondError(w, http.StatusInternalServerError, errInvalidRequest(err.Error()))
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"hours": req.Hours})
}

func (h *HTTPHandler) handleSessionNamingPreferenceGet(w http.ResponseWriter, _ *http.Request) {
	if h.AppContext == nil || h.AppContext.GetPreferences() == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("preferences not configured"))
		return
	}
	pref := h.AppContext.GetPreferences().SessionNamingDefaults()
	respondJSON(w, http.StatusOK, map[string]any{
		"agent":    pref.Agent,
		"model":    pref.Model,
		"disabled": pref.Disabled,
	})
}

func (h *HTTPHandler) handleSessionNamingPreferencePut(w http.ResponseWriter, r *http.Request) {
	if h.AppContext == nil || h.AppContext.GetPreferences() == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("preferences not configured"))
		return
	}
	var req sessionNamingPreferenceRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		return
	}
	req.Agent = strings.TrimSpace(req.Agent)
	req.Model = strings.TrimSpace(req.Model)
	if req.Agent == "" && !req.Disabled {
		respondError(w, http.StatusBadRequest, errInvalidRequest("session naming agent is required"))
		return
	}
	if !req.Disabled {
		if h.AppContext.GetProber() == nil {
			respondError(w, http.StatusServiceUnavailable, errInvalidRequest("agent status not configured"))
			return
		}
		validAgent := false
		validModel := req.Model == ""
		for _, status := range h.AppContext.GetProber().GetInstalledStatuses() {
			if strings.TrimSpace(status.Name) != req.Agent {
				continue
			}
			validAgent = true
			if req.Model != "" {
				for _, model := range status.Models {
					if strings.TrimSpace(model.ID) == req.Model {
						validModel = true
						break
					}
				}
			}
			break
		}
		if !validAgent {
			respondError(w, http.StatusBadRequest, errInvalidRequest("session naming agent is not installed"))
			return
		}
		if !validModel {
			respondError(w, http.StatusBadRequest, errInvalidRequest("session naming model is not supported by the selected agent"))
			return
		}
	}
	if err := h.AppContext.GetPreferences().UpdateSessionNamingDefaults(req.Agent, req.Model, req.Disabled); err != nil {
		respondError(w, http.StatusInternalServerError, errInvalidRequest(err.Error()))
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"agent":    req.Agent,
		"model":    req.Model,
		"disabled": req.Disabled,
	})
}

type corsPreferenceRequest struct {
	Mode         string   `json:"mode"`
	AllowOrigins []string `json:"allow_origins"`
}

func (h *HTTPHandler) handleCORSPreferenceGet(w http.ResponseWriter, _ *http.Request) {
	if h.AppContext == nil || h.AppContext.GetPreferences() == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("preferences not configured"))
		return
	}
	prefs := h.AppContext.GetPreferences()
	mode := prefs.CORSMode()
	if mode == "" {
		mode = "open"
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"mode":          mode,
		"allow_origins": prefs.CORSAllowOrigins(),
	})
}

func (h *HTTPHandler) handleCORSPreferencePut(w http.ResponseWriter, r *http.Request) {
	if h.AppContext == nil || h.AppContext.GetPreferences() == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("preferences not configured"))
		return
	}
	var req corsPreferenceRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		return
	}
	if err := h.AppContext.GetPreferences().UpdateCORSPreferences(req.Mode, req.AllowOrigins); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		return
	}
	prefs := h.AppContext.GetPreferences()
	mode := prefs.CORSMode()
	if mode == "" {
		mode = "open"
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"mode":          mode,
		"allow_origins": prefs.CORSAllowOrigins(),
	})
}
