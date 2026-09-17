package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"mindfs/server/internal/auth"
)

// 账户相关端点。刻意不参与 protectedEndpoint / e2ee：
// API 层不做鉴权（见 docs/multi-user-prd.md §1.1），账户只用于前端分区。

func authErrorStatus(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, auth.ErrInvalidCredentials), errors.Is(err, auth.ErrDisabled):
		return http.StatusUnauthorized
	case errors.Is(err, auth.ErrUserNotFound):
		return http.StatusNotFound
	case errors.Is(err, auth.ErrUsernameTaken), errors.Is(err, auth.ErrLastAdmin):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

func writeAuthError(w http.ResponseWriter, err error) {
	status := authErrorStatus(err)
	// 校验失败/冲突用稳定 code，前端据此做 i18n；其余（如解析失败）原样回传
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials),
		errors.Is(err, auth.ErrDisabled),
		errors.Is(err, auth.ErrUserNotFound),
		errors.Is(err, auth.ErrUsernameTaken),
		errors.Is(err, auth.ErrLastAdmin):
		respondError(w, status, errors.New(strings.TrimSpace(err.Error())))
	default:
		respondError(w, status, err)
	}
}

func decodeAuthBody(r *http.Request, out any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(out); err != nil {
		return errInvalidRequest("invalid_payload")
	}
	return nil
}

// handleAuthStatus 只回答「服务端有没有账户表」。
// 登录态由前端自己持有（无 token、无会话），所以不再有 authed 字段。
func (h *HTTPHandler) handleAuthStatus(w http.ResponseWriter, _ *http.Request) {
	store := h.AppContext.GetAuthStore()
	respondJSON(w, http.StatusOK, map[string]any{
		"required": store != nil,
	})
}

func (h *HTTPHandler) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	store := h.AppContext.GetAuthStore()
	if store == nil {
		// 无账户表（静态托管 / 测试）：放行并给一个匿名管理员，前端才不会白屏
		respondJSON(w, http.StatusOK, map[string]any{
			"user": auth.PublicUser{ID: "", Username: "admin", Role: auth.RoleAdmin},
		})
		return
	}
	var payload struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeAuthBody(r, &payload); err != nil {
		writeAuthError(w, err)
		return
	}
	user, err := store.Authenticate(payload.Username, payload.Password)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (h *HTTPHandler) handleUsersList(w http.ResponseWriter, _ *http.Request) {
	store := h.AppContext.GetAuthStore()
	if store == nil {
		respondJSON(w, http.StatusOK, map[string]any{"users": []auth.PublicUser{}})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"users": store.List()})
}

func (h *HTTPHandler) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	store := h.AppContext.GetAuthStore()
	if store == nil {
		writeAuthError(w, errServiceUnavailable("auth store not configured"))
		return
	}
	var payload struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := decodeAuthBody(r, &payload); err != nil {
		writeAuthError(w, err)
		return
	}
	user, err := store.Create(payload.Username, payload.Password, payload.Role)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (h *HTTPHandler) handleUserUpdate(w http.ResponseWriter, r *http.Request) {
	store := h.AppContext.GetAuthStore()
	if store == nil {
		writeAuthError(w, errServiceUnavailable("auth store not configured"))
		return
	}
	var payload struct {
		Username *string `json:"username"`
		Password *string `json:"password"`
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
	}
	if err := decodeAuthBody(r, &payload); err != nil {
		writeAuthError(w, err)
		return
	}
	user, err := store.Update(chi.URLParam(r, "id"), auth.UpdateInput{
		Username: payload.Username,
		Password: payload.Password,
		Role:     payload.Role,
		Disabled: payload.Disabled,
	})
	if err != nil {
		writeAuthError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (h *HTTPHandler) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	store := h.AppContext.GetAuthStore()
	if store == nil {
		writeAuthError(w, errServiceUnavailable("auth store not configured"))
		return
	}
	if err := store.Delete(chi.URLParam(r, "id")); err != nil {
		writeAuthError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
