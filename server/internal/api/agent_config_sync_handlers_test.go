package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"mindfs/internal/codexsync"
)

func TestAgentConfigSyncHandlersReturnOnlySafeState(t *testing.T) {
	for _, method := range []string{"GET", "POST"} {
		t.Run(method, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(method, "/api/agent-config/sync", nil)
			handler := &HTTPHandler{}
			if method == "GET" {
				handler.handleAgentConfigSyncStatus(recorder, request)
			} else {
				handler.handleAgentConfigSyncRequest(recorder, request)
			}
			if recorder.Code != 200 && recorder.Code != 202 {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var result codexsync.Result
			if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.State == "" || strings.Contains(recorder.Body.String(), "stderr") || strings.Contains(recorder.Body.String(), "apiKey") {
				t.Fatalf("unsafe sync result: %+v body=%s", result, recorder.Body.String())
			}
		})
	}
}
