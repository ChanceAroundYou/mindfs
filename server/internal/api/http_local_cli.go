package api

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
)

func (h *HTTPHandler) isLocalCLIRequest(r *http.Request) bool {
	token := strings.TrimSpace(h.LocalCLIToken)
	if token == "" || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(r.Header.Get(localCLIHeaderName))), []byte(token)) != 1 {
		return false
	}
	if !isLocalCLIPath(r) {
		return false
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isLocalCLIPath(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	if strings.HasSuffix(r.URL.Path, "/approve-plan") {
		return false
	}
	if r.Method == http.MethodPost {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if len(parts) == 4 && parts[0] == "api" && parts[1] == "sessions" && parts[2] != "" && parts[3] == "messages" {
			return true
		}
	}
	if r.Method == http.MethodGet && (r.URL.Path == "/api/agents" || r.URL.Path == "/api/task-templates" || r.URL.Path == "/api/tasks") {
		return true
	}
	if r.Method == http.MethodPost && r.URL.Path == "/api/tasks" {
		return true
	}
	if (r.Method == http.MethodPatch || r.Method == http.MethodDelete) && isLocalCLITaskPath(r.URL.Path) {
		return true
	}
	switch r.Method {
	case http.MethodPost:
		return r.URL.Path == "/api/dirs" || isLocalCLITaskPath(r.URL.Path)
	case http.MethodDelete:
		return r.URL.Path == "/api/dirs"
	case http.MethodGet:
		return isLocalCLITaskPath(r.URL.Path)
	default:
		return false
	}
}

func isLocalCLITaskPath(path string) bool {
	if !strings.HasPrefix(path, "/api/tasks/") {
		return false
	}
	if strings.TrimPrefix(path, "/api/tasks/") == "" {
		return false
	}
	return true
}

func (h *HTTPHandler) broadcastRootChanged(action, rootID string, extra ...map[string]any) {
	if h.AppContext == nil {
		return
	}
	payload := map[string]any{
		"action":  action,
		"root_id": rootID,
	}
	for _, fields := range extra {
		for key, value := range fields {
			payload[key] = value
		}
	}
	h.AppContext.GetSessionStreamHub().BroadcastAll(WSResponse{
		Type:    "root.changed",
		Payload: payload,
	})
}
