package api

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"mindfs/server/internal/e2ee"
	"mindfs/server/internal/preferences"
)

// openE2EESessionForTest bootstraps an in-memory E2EE session and returns the
// derived transport key so the test can act as a paired client (build proofs
// and encrypt/decrypt envelopes the same way the browser client does).
func openE2EESessionForTest(t *testing.T, manager *e2ee.Manager, secret, nodeID, clientID string) e2ee.DerivedKey {
	t.Helper()
	clientPriv, clientEphPK, err := e2ee.GenerateECDHKeypair()
	if err != nil {
		t.Fatalf("client keypair: %v", err)
	}
	_, nodeEphPK, err := e2ee.GenerateECDHKeypair()
	if err != nil {
		t.Fatalf("node keypair: %v", err)
	}
	clientPub, err := e2ee.DecodePublicKey(clientEphPK)
	if err != nil {
		t.Fatalf("decode client pk: %v", err)
	}
	nonce := func() string {
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			t.Fatalf("nonce: %v", err)
		}
		return base64.StdEncoding.EncodeToString(raw)
	}
	clientNonce, serverNonce := nonce(), nonce()
	derived, err := e2ee.DeriveKey(secret, nodeID, clientEphPK, nodeEphPK, clientNonce, serverNonce, clientPriv, clientPub)
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}
	if _, err := manager.OpenSessionForClient(clientID, derived); err != nil {
		t.Fatalf("open session: %v", err)
	}
	return derived
}

func TestProtectedEndpointPassthroughWhenE2EEDisabled(t *testing.T) {
	handler := &HTTPHandler{AppContext: &AppContext{E2EE: e2ee.NewManager(e2ee.Config{Enabled: false})}}
	final := handler.protectedEndpoint(func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	rec := httptest.NewRecorder()
	final.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if rec.Header().Get(e2eeHeaderName) != "" {
		t.Fatal("plaintext mode must not mark responses as E2EE-protected")
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response not plaintext JSON: %v", err)
	}
	if body["ok"] != true {
		t.Fatalf("unexpected body: %v", body)
	}
}

func TestProtectedEndpointRequiresE2EEHeadersWhenEnabled(t *testing.T) {
	manager := e2ee.NewManager(e2ee.Config{Enabled: true, NodeID: "node-1", PairingSecret: "topsecret"})
	handler := &HTTPHandler{AppContext: &AppContext{E2EE: manager}}
	final := handler.protectedEndpoint(func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	rec := httptest.NewRecorder()
	final.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req2.Header.Set(e2eeHeaderName, "1")
	req2.Header.Set(clientIDHeaderName, "client-abc")
	rec2 := httptest.NewRecorder()
	final.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec2.Code)
	}
}

func TestProtectedEndpointRoundTripWhenE2EEEnabled(t *testing.T) {
	manager := e2ee.NewManager(e2ee.Config{Enabled: true, NodeID: "node-1", PairingSecret: "topsecret"})
	handler := &HTTPHandler{AppContext: &AppContext{E2EE: manager}}
	clientID := "client-abc"
	derived := openE2EESessionForTest(t, manager, "topsecret", "node-1", clientID)

	var gotBody map[string]any
	final := handler.protectedEndpoint(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		respondJSON(w, http.StatusOK, map[string]any{"echo": gotBody["in"]})
	})

	path := "/api/x"
	ts := time.Now().UTC().Format(time.RFC3339)
	proof := e2ee.BuildRequestProof(derived.Transport, http.MethodPost, path, ts, clientID)
	env, err := e2ee.EncryptJSON(derived.Transport, map[string]any{"in": "hello"})
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	bodyBytes, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(bodyBytes))
	req.Header.Set(e2eeHeaderName, "1")
	req.Header.Set(clientIDHeaderName, clientID)
	req.Header.Set(e2eeTSHeaderName, ts)
	req.Header.Set(e2eeProofHeaderName, proof)
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = int64(len(bodyBytes))
	rec := httptest.NewRecorder()
	final.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get(e2eeHeaderName) != "1" {
		t.Fatal("expected E2EE-protected response marker")
	}
	if gotBody["in"] != "hello" {
		t.Fatalf("request body not decrypted: %v", gotBody)
	}
	var respEnv e2ee.CipherEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &respEnv); err != nil {
		t.Fatalf("response not an envelope: %v", err)
	}
	var out map[string]any
	if err := e2ee.DecryptJSON(derived.Transport, &respEnv, &out); err != nil {
		t.Fatalf("decrypt response: %v", err)
	}
	if out["echo"] != "hello" {
		t.Fatalf("unexpected echo: %v", out)
	}
}

func TestCORSLocalModeEmitsNoACAO(t *testing.T) {
	// default plaintext mode is now "open" (emit ACAO) so multi-node home↔pc works;
	// disabled mode is the explicit same-origin lock.
	for _, tc := range []struct {
		name string
		mode string
		want string
	}{
		{"open", "open", "https://cross.example"},
		{"allowlist_miss", "allowlist", ""},
		{"disabled", "disabled", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prefs := &preferences.Store{}
			if tc.mode == "allowlist" {
				_ = prefs.UpdateCORSPreferences("allowlist", []string{"https://other.example"})
			} else {
				_ = prefs.UpdateCORSPreferences(tc.mode, nil)
			}
			// empty mode defaults to open -> reflect origin
			if tc.name == "open" {
				prefs2 := &preferences.Store{}
				// nil mode => middleware treats as open
				handler := &HTTPHandler{AppContext: &AppContext{E2EE: e2ee.NewManager(e2ee.Config{Enabled: false}), Prefs: prefs2}}
				r := chi.NewRouter()
				r.Use(handler.corsMiddleware)
				r.Get("/api/x", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
				req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
				req.Header.Set("Origin", "https://cross.example")
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, req)
				if got := rec.Header().Get("Access-Control-Allow-Origin"); got != tc.want {
					t.Fatalf("ACAO = %q, want %q", got, tc.want)
				}
				return
			}
			handler := &HTTPHandler{AppContext: &AppContext{E2EE: e2ee.NewManager(e2ee.Config{Enabled: false}), Prefs: prefs}}
			r := chi.NewRouter()
			r.Use(handler.corsMiddleware)
			r.Get("/api/x", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
			req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
			req.Header.Set("Origin", "https://cross.example")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != tc.want {
				t.Fatalf("mode=%s ACAO = %q, want %q", tc.mode, got, tc.want)
			}
		})
	}
}

func TestCORSOptionsPreflight(t *testing.T) {
	prefs := &preferences.Store{}
	_ = prefs.UpdateCORSPreferences("open", nil)
	handler := &HTTPHandler{AppContext: &AppContext{E2EE: e2ee.NewManager(e2ee.Config{Enabled: false}), Prefs: prefs}}
	r := chi.NewRouter()
	r.Use(handler.corsMiddleware)
	r.Get("/api/x", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodOptions, "/api/x", nil)
	req.Header.Set("Origin", "https://cross.example")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("preflight code = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://cross.example" {
		t.Fatalf("preflight ACAO = %q, want https://cross.example", got)
	}
}

func TestCORSAllowlistHit(t *testing.T) {
	prefs := &preferences.Store{}
	_ = prefs.UpdateCORSPreferences("allowlist", []string{"https://cross.example"})
	handler := &HTTPHandler{AppContext: &AppContext{E2EE: e2ee.NewManager(e2ee.Config{Enabled: false}), Prefs: prefs}}
	r := chi.NewRouter()
	r.Use(handler.corsMiddleware)
	r.Get("/api/x", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.Header.Set("Origin", "https://cross.example")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://cross.example" {
		t.Fatalf("allowlist hit ACAO = %q, want https://cross.example", got)
	}
}

func TestCORSE2EEModeReflectsOrigin(t *testing.T) {
	handler := &HTTPHandler{AppContext: &AppContext{E2EE: e2ee.NewManager(e2ee.Config{Enabled: true, NodeID: "n", PairingSecret: "s"})}}
	r := chi.NewRouter()
	r.Use(handler.corsMiddleware)
	r.Get("/api/x", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.Header.Set("Origin", "https://pair.example")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://pair.example" {
		t.Fatalf("ACAO = %q, want https://pair.example", got)
	}
	if rec.Header().Get("Vary") != "Origin" {
		t.Fatal("expected Vary: Origin in E2EE CORS mode")
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("expected credentials allow in E2EE CORS mode")
	}
}
