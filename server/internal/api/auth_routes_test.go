package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
	dir := t.TempDir()
	// 用一份 login.json 固定初始管理员密码，避免随机密码不可断言
	legacy, err := json.Marshal(map[string]string{"password": "root-secret"})
	if err != nil {
		t.Fatalf("marshal legacy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "login.json"), legacy, 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	store, err := auth.EnsureStoreAt(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatalf("EnsureStoreAt: %v", err)
	}
	return store
}

func doJSON(t *testing.T, handler http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	handler.ServeHTTP(rec, httptest.NewRequest(method, target, reader))
	return rec
}

func decodeInto(t *testing.T, rec *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
}

func TestAuthStatusWithoutStore(t *testing.T) {
	// 静态托管 / 测试环境没有账户表时必须放行，否则前端白屏
	handler := newAuthTestHandler(t, nil)
	rec := doJSON(t, handler, http.MethodGet, "/api/auth/status", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var payload struct {
		Required bool `json:"required"`
	}
	decodeInto(t, rec, &payload)
	if payload.Required {
		t.Fatal("nil store must report required=false")
	}
}

func TestAuthStatusRequiresLoginWhenStorePresent(t *testing.T) {
	handler := newAuthTestHandler(t, newAuthTestStore(t))
	rec := doJSON(t, handler, http.MethodGet, "/api/auth/status", "")
	var payload struct {
		Required bool `json:"required"`
	}
	decodeInto(t, rec, &payload)
	if !payload.Required {
		t.Fatal("store present must report required=true")
	}
}

func TestLoginWithUsernameAndPassword(t *testing.T) {
	handler := newAuthTestHandler(t, newAuthTestStore(t))

	rec := doJSON(t, handler, http.MethodPost, "/api/auth/login", `{"username":"admin","password":"root-secret"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var ok struct {
		User auth.PublicUser `json:"user"`
	}
	decodeInto(t, rec, &ok)
	if ok.User.Username != "admin" || ok.User.Role != auth.RoleAdmin {
		t.Fatalf("login user = %#v", ok.User)
	}

	if rec := doJSON(t, handler, http.MethodPost, "/api/auth/login", `{"username":"admin","password":"nope"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d, want 401", rec.Code)
	}
	if rec := doJSON(t, handler, http.MethodPost, "/api/auth/login", `{"username":"ghost","password":"nope"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown user = %d, want 401", rec.Code)
	}
	if rec := doJSON(t, handler, http.MethodPost, "/api/auth/login", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed body = %d, want 400", rec.Code)
	}
}

func TestUserCRUDRoutes(t *testing.T) {
	store := newAuthTestStore(t)
	handler := newAuthTestHandler(t, store)

	// 列表：只有迁移出来的 admin
	rec := doJSON(t, handler, http.MethodGet, "/api/users", "")
	var listed struct {
		Users []auth.PublicUser `json:"users"`
	}
	decodeInto(t, rec, &listed)
	if len(listed.Users) != 1 || listed.Users[0].Role != auth.RoleAdmin {
		t.Fatalf("initial users = %#v", listed.Users)
	}

	// 新建
	rec = doJSON(t, handler, http.MethodPost, "/api/users", `{"username":"alice","password":"alice-secret","role":"user"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var created struct {
		User auth.PublicUser `json:"user"`
	}
	decodeInto(t, rec, &created)

	// 重名 → 409
	if rec := doJSON(t, handler, http.MethodPost, "/api/users", `{"username":"alice","password":"alice-secret"}`); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate = %d, want 409", rec.Code)
	}
	// 密码过短 → 400
	if rec := doJSON(t, handler, http.MethodPost, "/api/users", `{"username":"bob","password":"x"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("short password = %d, want 400", rec.Code)
	}

	// 新账户能登录
	if rec := doJSON(t, handler, http.MethodPost, "/api/auth/login", `{"username":"alice","password":"alice-secret"}`); rec.Code != http.StatusOK {
		t.Fatalf("alice login = %d, want 200", rec.Code)
	}

	// 改密 + 禁用
	rec = doJSON(t, handler, http.MethodPut, "/api/users/"+created.User.ID, `{"password":"alice-2nd","disabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, handler, http.MethodPost, "/api/auth/login", `{"username":"alice","password":"alice-2nd"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled login = %d, want 401", rec.Code)
	}

	// 未知 id → 404
	if rec := doJSON(t, handler, http.MethodPut, "/api/users/u_missing", `{"disabled":true}`); rec.Code != http.StatusNotFound {
		t.Fatalf("update unknown = %d, want 404", rec.Code)
	}

	// 删除
	if rec := doJSON(t, handler, http.MethodDelete, "/api/users/"+created.User.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", rec.Code)
	}
	rec = doJSON(t, handler, http.MethodGet, "/api/users", "")
	decodeInto(t, rec, &listed)
	if len(listed.Users) != 1 {
		t.Fatalf("users after delete = %#v", listed.Users)
	}
}

func TestLastAdminCannotBeDeletedViaRoute(t *testing.T) {
	store := newAuthTestStore(t)
	handler := newAuthTestHandler(t, store)
	admin := store.List()[0]

	if rec := doJSON(t, handler, http.MethodDelete, "/api/users/"+admin.ID, ""); rec.Code != http.StatusConflict {
		t.Fatalf("delete last admin = %d, want 409", rec.Code)
	}
	if !store.Exists(admin.ID) {
		t.Fatal("last admin was removed")
	}
}
