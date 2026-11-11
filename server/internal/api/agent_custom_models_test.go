package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mindfs/server/internal/preferences"
)

func newAgentCustomModelsHandler(t *testing.T) *HTTPHandler {
	t.Helper()
	store, err := preferences.NewStoreAt(t.TempDir())
	if err != nil {
		t.Fatalf("preferences.NewStoreAt: %v", err)
	}
	return &HTTPHandler{AppContext: &AppContext{Prefs: store}}
}

func TestAgentCustomModelsPutAndGet(t *testing.T) {
	handler := newAgentCustomModelsHandler(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("PUT", "/api/agents/custom-models", strings.NewReader(`{"agent":"codex","models":[" of ","of","gpt-6-luna"]}`))
	handler.handleAgentCustomModelsPut(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("PUT status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var saved agentCustomModelsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Agent != "codex" || len(saved.Models) != 2 || saved.Models[0] != "of" || saved.Models[1] != "gpt-6-luna" {
		t.Fatalf("saved = %#v", saved)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest("GET", "/api/agents/custom-models?agent=codex", nil)
	handler.handleAgentCustomModelsGet(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("GET status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var loaded agentCustomModelsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &loaded); err != nil {
		t.Fatal(err)
	}
	if len(loaded.Models) != 2 || loaded.Models[0] != "of" {
		t.Fatalf("loaded = %#v", loaded)
	}
}

func TestAgentCustomModelsRejectsMissingAgent(t *testing.T) {
	handler := newAgentCustomModelsHandler(t)

	for _, request := range []*http.Request{
		httptest.NewRequest("GET", "/api/agents/custom-models", nil),
		httptest.NewRequest("PUT", "/api/agents/custom-models", strings.NewReader(`{"models":["of"]}`)),
	} {
		recorder := httptest.NewRecorder()
		if request.Method == "GET" {
			handler.handleAgentCustomModelsGet(recorder, request)
		} else {
			handler.handleAgentCustomModelsPut(recorder, request)
		}
		if recorder.Code != 400 {
			t.Fatalf("%s status=%d body=%s", request.Method, recorder.Code, recorder.Body.String())
		}
	}
}
