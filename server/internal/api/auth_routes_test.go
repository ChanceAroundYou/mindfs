package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/auth"
)

func newAuthTestHandler(t *testing.T, store *auth.Store) http.Handler {
	t.Helper()
	return (&HTTPHandler{AppContext: &AppContext{Auth: store}}).Routes()
}

func newAuthTestStore(t *testing.T) *auth.Store {
	t.Helper()
	store, err := auth.EnsureStoreAt(filepath.Join(t.TempDir(), "login.json"))
	if err != nil {
		t.Fatalf("EnsureStoreAt: %v", err)
	}
	return store
}

func authStatus(t *testing.T, handler http.Handler, token string) (required, authed bool) {
	t.Helper()
	target := "/api/auth/status"
	if token != "" {
		target += "?token=" + token
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", rec.Code)
	}
	var payload struct {
		Required bool `json:"required"`
		Authed   bool `json:"authed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	return payload.Required, payload.Authed
}

func authLogin(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body)))
	return rec
}

func TestAuthGateRoutesWithoutStore(t *testing.T) {
	// 静态托管 / 测试环境没有 Auth store 时必须放行，否则前端会白屏
	handler := newAuthTestHandler(t, nil)
	if required, authed := authStatus(t, handler, ""); required || !authed {
		t.Fatalf("nil store status = required:%v authed:%v, want false/true", required, authed)
	}
	if rec := authLogin(t, handler, `{"password":"anything"}`); rec.Code != http.StatusOK {
		t.Fatalf("nil store login status = %d, want 200", rec.Code)
	}
}

func TestAuthGateLocksAnonymousBrowser(t *testing.T) {
	handler := newAuthTestHandler(t, newAuthTestStore(t))
	if required, authed := authStatus(t, handler, ""); !required || authed {
		t.Fatalf("anonymous status = required:%v authed:%v, want true/false", required, authed)
	}
	if required, authed := authStatus(t, handler, "bogus"); !required || authed {
		t.Fatalf("bogus token status = required:%v authed:%v, want true/false", required, authed)
	}
}

func TestAuthLoginRejectsBadInput(t *testing.T) {
	handler := newAuthTestHandler(t, newAuthTestStore(t))
	if rec := authLogin(t, handler, `{"password":"nope"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d, want 401", rec.Code)
	}
	if rec := authLogin(t, handler, "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed body status = %d, want 400", rec.Code)
	}
}

func TestAuthLoginIssuedTokenUnlocksStatus(t *testing.T) {
	store := newAuthTestStore(t)
	handler := newAuthTestHandler(t, store)

	rec := authLogin(t, handler, `{"password":"`+store.Password()+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if payload.Token == "" {
		t.Fatal("login returned empty token")
	}
	if required, authed := authStatus(t, handler, payload.Token); !required || !authed {
		t.Fatalf("status with token = required:%v authed:%v, want true/true", required, authed)
	}
}
