package controlsync

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewPushClientRequiresHTTPSAndToken(t *testing.T) {
	cases := []struct {
		name string
		opts PushClientOptions
		want string
	}{
		{"明文", PushClientOptions{PrimaryURL: "http://home:7331", Token: "t"}, "must be https"},
		{"缺 token", PushClientOptions{PrimaryURL: "https://home:7331"}, "token required"},
		{"缺地址", PushClientOptions{Token: "t"}, "primary_url required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewPushClient(tc.opts); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v，应含 %q", err, tc.want)
			}
		})
	}
}

// 上行必须带 Bearer token、保留部署前缀，并把逐条回执原样解出来。
func TestPushClientSendsAuthorizedRequestAndDecodesResults(t *testing.T) {
	const token = "s3cret"
	var (
		gotAuth     string
		gotPath     string
		gotInstance string
		gotReq      PushRequest
	)
	nonces := NewNonceCache(SyncNonceTTL)
	mux := http.NewServeMux()
	mux.HandleFunc("/mindfs/api/control-sync/push", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotInstance = r.Header.Get(SyncInstanceHeader)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		// 签名覆盖的是实际发送的字节，所以必须先读原文再验，不能边解码边验。
		if err := VerifySyncRequest(r, body, SyncVerifyOptions{Token: token, Nonces: nonces}); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if err := json.Unmarshal(body, &gotReq); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"change_id":"c1","status":"applied","exists":true,"hash":"sha256:new"}],"head_rev":12}`))
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	client, err := NewPushClient(PushClientOptions{
		PrimaryURL: srv.URL + "/mindfs",
		Token:      token,
		InstanceID: "edge-pc",
		CAFile:     writeTempCA(t, srv.Certificate()),
	})
	if err != nil {
		t.Fatalf("NewPushClient: %v", err)
	}
	batch, err := client.Push(t.Context(), PushRequest{
		InstanceID: "edge-pc",
		Changes: []PushChange{{
			ChangeID: "c1", Domain: DomainPreferences, Key: "agents",
			BaseHash: "sha256:old", Payload: json.RawMessage(`{"v":1}`),
		}},
	})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if gotAuth != "Bearer "+token {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotInstance != "edge-pc" {
		t.Fatalf("instance 头 = %q", gotInstance)
	}
	if gotPath != "/mindfs/api/control-sync/push" {
		t.Fatalf("path = %q，部署前缀被丢了", gotPath)
	}
	if gotReq.InstanceID != "edge-pc" || len(gotReq.Changes) != 1 ||
		gotReq.Changes[0].ChangeID != "c1" || string(gotReq.Changes[0].Payload) != `{"v":1}` {
		t.Fatalf("上行请求 = %+v", gotReq)
	}
	if len(batch.Results) != 1 || batch.Results[0].Status != PushApplied ||
		batch.Results[0].Hash != "sha256:new" || batch.HeadRev != 12 {
		t.Fatalf("回执 = %+v", batch)
	}
}

// 非 200 一律整拍失败：状态码看不出部分成功，逐条结论只在响应体里。
func TestPushClientReportsPrimaryFailure(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "primary exploded", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	client, err := NewPushClient(PushClientOptions{
		PrimaryURL: srv.URL,
		Token:      "t",
		CAFile:     writeTempCA(t, srv.Certificate()),
	})
	if err != nil {
		t.Fatalf("NewPushClient: %v", err)
	}
	_, err = client.Push(t.Context(), PushRequest{Changes: []PushChange{{ChangeID: "c1"}}})
	if err == nil || !strings.Contains(err.Error(), "503") || !strings.Contains(err.Error(), "primary exploded") {
		t.Fatalf("err = %v，应带上状态码与响应体", err)
	}
}
