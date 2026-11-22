package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"mindfs/server/internal/channels/feishu"
	"mindfs/server/internal/nodeinfo"
)

const feishuAnswerTestToken = "answer-shared-token-0123456789abcdef"

func feishuAnswerTestHandler(applied *[]feishu.Answer) *HTTPHandler {
	return &HTTPHandler{
		FeishuRelayToken: feishuAnswerTestToken,
		FeishuNodeID:     "wsl",
		applyFeishuAnswer: func(_ context.Context, a feishu.Answer) error {
			*applied = append(*applied, a)
			return nil
		},
	}
}

func feishuAnswerRequest(t *testing.T, token string, answer feishu.Answer) *http.Request {
	t.Helper()
	body, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/channels/feishu/answer", bytes.NewReader(body))
	if token != "" {
		req.Header.Set(feishu.RelayTokenHeader, token)
	}
	return req
}

func TestFeishuAnswerRejectsBadTokenOrForeignNode(t *testing.T) {
	answer := feishu.Answer{CardID: "card-1", Kind: feishu.KindGate, NodeID: "wsl", TaskID: "task-1", Decision: "approve"}

	t.Run("missing token", func(t *testing.T) {
		var applied []feishu.Answer
		h := feishuAnswerTestHandler(&applied)
		rec := httptest.NewRecorder()
		h.handleFeishuAnswer(rec, feishuAnswerRequest(t, "", answer))
		if rec.Code != http.StatusUnauthorized || len(applied) != 0 {
			t.Fatalf("status=%d applied=%d", rec.Code, len(applied))
		}
	})
	t.Run("wrong token", func(t *testing.T) {
		var applied []feishu.Answer
		h := feishuAnswerTestHandler(&applied)
		rec := httptest.NewRecorder()
		h.handleFeishuAnswer(rec, feishuAnswerRequest(t, strings.Repeat("x", len(feishuAnswerTestToken)), answer))
		if rec.Code != http.StatusUnauthorized || len(applied) != 0 {
			t.Fatalf("status=%d applied=%d", rec.Code, len(applied))
		}
	})
	t.Run("foreign node", func(t *testing.T) {
		var applied []feishu.Answer
		h := feishuAnswerTestHandler(&applied)
		foreign := answer
		foreign.NodeID = "fn"
		rec := httptest.NewRecorder()
		h.handleFeishuAnswer(rec, feishuAnswerRequest(t, feishuAnswerTestToken, foreign))
		if rec.Code != http.StatusForbidden || len(applied) != 0 {
			t.Fatalf("status=%d applied=%d", rec.Code, len(applied))
		}
	})
	t.Run("no local node id", func(t *testing.T) {
		var applied []feishu.Answer
		h := feishuAnswerTestHandler(&applied)
		h.FeishuNodeID = ""
		rec := httptest.NewRecorder()
		h.handleFeishuAnswer(rec, feishuAnswerRequest(t, feishuAnswerTestToken, answer))
		if rec.Code != http.StatusForbidden || len(applied) != 0 {
			t.Fatalf("status=%d applied=%d", rec.Code, len(applied))
		}
	})
}

func TestFeishuAnswerAppliesLocally(t *testing.T) {
	var applied []feishu.Answer
	h := feishuAnswerTestHandler(&applied)
	answer := feishu.Answer{
		CardID: "gate-2", Kind: feishu.KindGate, AccountID: "u_1", RootID: "root", NodeID: "wsl",
		TaskID: "task-2", WorkflowNodeID: "n1", Decision: "reject",
	}
	rec := httptest.NewRecorder()
	h.handleFeishuAnswer(rec, feishuAnswerRequest(t, feishuAnswerTestToken, answer))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(applied) != 1 || !reflect.DeepEqual(applied[0], answer) {
		t.Fatalf("local apply did not receive the same answer: %+v", applied)
	}
}

func TestFeishuAnswerRouteIsDataPlane(t *testing.T) {
	if nodeinfo.IsControlPlane("/api/channels/feishu/answer") {
		t.Fatal("answer must stay on the data plane so workers can serve it")
	}
	var applied []feishu.Answer
	h := feishuAnswerTestHandler(&applied)
	h.AppContext = &AppContext{}
	h.NodeRole = nodeinfo.RoleWorker
	answer := feishu.Answer{CardID: "gate-3", Kind: feishu.KindGate, NodeID: "wsl", Decision: "approve"}
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, feishuAnswerRequest(t, feishuAnswerTestToken, answer))
	if rec.Code != http.StatusOK || len(applied) != 1 || applied[0].CardID != "gate-3" {
		t.Fatalf("worker route: status=%d applied=%+v body=%s", rec.Code, applied, rec.Body.String())
	}
}
