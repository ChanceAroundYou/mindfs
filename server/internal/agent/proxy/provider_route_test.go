package proxy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newProviderTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := NewServerWithLiveCompact(LiveCompactConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func postToProvider(t *testing.T, s *Server, path, body string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d%s", s.Port(), path), "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, out
}

// The route replaces its own prefix with the real base URL and leaves the rest
// of the path, the query, the body, and the headers alone: the provider must
// see the request Codex built, not a rewritten one.
func TestProviderRouteForwardsToRegisteredBaseURL(t *testing.T) {
	var gotPath, gotQuery, gotAuth string
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	s := newProviderTestServer(t)
	// Trailing slash and the `/v1` the provider was configured with: the route
	// carries `/v1/...` itself, so neither may be duplicated.
	s.RegisterProvider("llmux", upstream.URL+"/v1/")

	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/c/llmux/v1/responses?trace=1", s.Port()),
		bytes.NewReader([]byte(`{"input":[]}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if gotPath != "/v1/responses" {
		t.Fatalf("upstream path = %q", gotPath)
	}
	if gotQuery != "trace=1" {
		t.Fatalf("upstream query = %q", gotQuery)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	if string(gotBody) != `{"input":[]}` {
		t.Fatalf("upstream body = %s", gotBody)
	}
}

// An unregistered provider is our wiring mistake, not an upstream failure.
func TestProviderRouteUnknownNameIs404(t *testing.T) {
	s := newProviderTestServer(t)
	resp, body := postToProvider(t, s, "/c/nope/v1/responses", `{}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
}

// Upstream status and body pass through untouched, so the CLI can read the
// provider's own error message.
func TestProviderRoutePassesUpstreamErrorThrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":{"message":"upstream boom"}}`))
	}))
	defer upstream.Close()

	s := newProviderTestServer(t)
	s.RegisterProvider("llmux", upstream.URL+"/v1")
	resp, body := postToProvider(t, s, "/c/llmux/v1/responses", `{}`)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !bytes.Contains(body, []byte("upstream boom")) {
		t.Fatalf("body = %s", body)
	}
}

// ProviderProxyURL is what gets written into the Codex override, so it has to
// point back at this server.
func TestProviderProxyURLPointsAtThisServer(t *testing.T) {
	s := newProviderTestServer(t)
	want := fmt.Sprintf("http://127.0.0.1:%d/c/llmux/v1", s.Port())
	if got := s.ProviderProxyURL("llmux"); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// ProviderProxyURL is what gets written into the Codex override, so it has to
// mirror the shape of the configured base URL: Codex appends the API path to
// whatever base it is handed, and the ChatGPT backend (`.../backend-api/codex`)
// has no version segment to reproduce.
func TestProviderProxyURLKeepsConfiguredPathShape(t *testing.T) {
	s := newProviderTestServer(t)
	s.RegisterProvider("llmux", "https://up.example/llmux/v1/")
	s.RegisterProvider("chatgpt", "https://chatgpt.com/backend-api/codex")
	if got, want := s.ProviderProxyURL("llmux"), fmt.Sprintf("http://127.0.0.1:%d/c/llmux/v1", s.Port()); got != want {
		t.Fatalf("llmux = %q want %q", got, want)
	}
	if got, want := s.ProviderProxyURL("chatgpt"), fmt.Sprintf("http://127.0.0.1:%d/c/chatgpt", s.Port()); got != want {
		t.Fatalf("chatgpt = %q want %q", got, want)
	}
}

// The same shape must survive a real request: a provider registered without
// /v1 receives exactly the path Codex built, with no segment inserted.
func TestProviderRouteForwardsBaseWithoutVersionSegment(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	s := newProviderTestServer(t)
	s.RegisterProvider("chatgpt", upstream.URL+"/backend-api/codex")
	resp, body := postToProvider(t, s, "/c/chatgpt/responses", `{}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	if gotPath != "/backend-api/codex/responses" {
		t.Fatalf("upstream path = %q", gotPath)
	}
}
