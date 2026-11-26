package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Opt-in end-to-end check with the real Codex CLI:
//
//	MINDFS_TEST_CODEX_PROVIDER_IDS=1 go test ./server/internal/agent/proxy/ -run CodexProviderSwitchItemIDsLive -v
//
// Two mock Responses upstreams sit behind the proxy. `llmux` answers normally;
// `strict` reproduces the ChatGPT backend's contract: with store:false an
// input item carrying an id minted by another response is rejected with 404
// "Item with id … not found". Codex runs real turns and forks the thread
// across providers; the proxy must strip the stale ids in both directions and
// on later turns.
//
// No credentials are needed: both providers are local `env_key` providers.
func TestCodexProviderSwitchItemIDsLive(t *testing.T) {
	if os.Getenv("MINDFS_TEST_CODEX_PROVIDER_IDS") == "" {
		t.Skip("set MINDFS_TEST_CODEX_PROVIDER_IDS=1 to run against the installed codex")
	}
	codexBin, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex is not installed")
	}
	home := t.TempDir()
	proj := t.TempDir()

	var mu sync.Mutex
	var llmuxBodies, strictBodies []string
	llmux := httptest.NewServer(responsesSSEHandler(t, &mu, &llmuxBodies, false))
	defer llmux.Close()
	strict := httptest.NewServer(responsesSSEHandler(t, &mu, &strictBodies, true))
	defer strict.Close()

	s, err := NewServerWithLiveCompact(LiveCompactConfig{ArchiveDir: t.TempDir()})
	if err != nil {
		t.Fatalf("proxy: %v", err)
	}
	defer func() { _ = s.Close() }()
	s.RegisterProvider("llmux", llmux.URL)
	s.RegisterProvider("strict", strict.URL)
	// MindFS registers the thread the app server reports and marks it after a
	// provider switch. Standalone Codex cannot do that, so the harness marks
	// every thread it sees; that is the state the pool puts a forked thread in.
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if threadID := providerThreadID(r); threadID != "" {
			s.RegisterThread(threadID, "e2e-"+threadID)
			s.MarkProviderSwitch(threadID)
		}
		s.handleProvider(w, r)
	}))
	defer front.Close()

	env := append(os.Environ(),
		"CODEX_HOME="+home,
		"MOCK_API_KEY=test-key",
	)
	writeCodexConfig(t, home, "llmux", front.URL)
	first := runCodex(t, codexBin, proj, env, "first turn")

	writeCodexConfig(t, home, "strict", front.URL)
	second := runCodex(t, codexBin, proj, env, "fork", first, "second turn")

	// Multi-turn on the forked thread: the marker must keep stripping.
	runCodex(t, codexBin, proj, env, "resume", second, "third turn")

	// Reverse switch: default-like → llmux.
	writeCodexConfig(t, home, "llmux", front.URL)
	runCodex(t, codexBin, proj, env, "fork", second, "fourth turn")

	mu.Lock()
	defer mu.Unlock()
	if len(strictBodies) != 2 {
		t.Fatalf("strict requests = %d, want 2", len(strictBodies))
	}
	for i, body := range strictBodies {
		if ids := inputItemIDs(body); len(ids) != 0 {
			t.Fatalf("strict request %d carried stale item ids %v: %s", i, ids, body)
		}
	}
	if len(llmuxBodies) < 2 {
		t.Fatalf("llmux requests = %d, want at least 2", len(llmuxBodies))
	}
	for i, body := range llmuxBodies[1:] {
		if ids := inputItemIDs(body); len(ids) != 0 {
			t.Fatalf("llmux request %d carried stale item ids %v: %s", i+1, ids, body)
		}
	}
}

func writeCodexConfig(t *testing.T, home, provider, proxyURL string) {
	t.Helper()
	cfg := fmt.Sprintf(`model = "mock-model"
model_provider = %q

[model_providers.llmux]
name = "llmux"
base_url = "%s/c/llmux"
wire_api = "responses"
env_key = "MOCK_API_KEY"

[model_providers.strict]
name = "strict"
base_url = "%s/c/strict"
wire_api = "responses"
env_key = "MOCK_API_KEY"
`, provider, proxyURL, proxyURL)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// responsesSSEHandler answers one assistant message (preceded, on the first
// answer, by a reasoning item that carries an id and no usable ciphertext —
// the exact shape llmux produced in the reported failure). When rejectIDs is
// set it first scans the request the way the ChatGPT backend does: any input
// item carrying an id is rejected with 404.
func responsesSSEHandler(t *testing.T, mu *sync.Mutex, bodies *[]string, rejectIDs bool) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		mu.Lock()
		*bodies = append(*bodies, string(body))
		n := len(*bodies)
		mu.Unlock()
		if rejectIDs {
			if ids := inputItemIDs(string(body)); len(ids) > 0 {
				msg := fmt.Sprintf("Item with id '%s' not found. Items are not persisted when `store` is set to false. "+
					"Try again with `store` set to true, or remove this item from your input.", ids[0])
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = fmt.Fprintf(w, `{"error":{"message":%q}}`, msg)
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, responsesSSE(n))
	}
}

// responsesSSE streams one completed turn. Item ids are namespaced by the
// upstream's answer number so a later strict upstream can tell that they were
// minted elsewhere.
func responsesSSE(n int) string {
	tag := fmt.Sprint(n)
	var reasoning string
	if n == 1 {
		reasoning = `event: response.output_item.done` + "\n" +
			`data: {"type":"response.output_item.done","item":{"type":"reasoning","id":"rs_llmux_live","summary":[{"type":"summary_text","text":"thinking"}],"encrypted_content":""}}` + "\n\n"
	}
	return reasoning +
		"event: response.created\n" +
		`data: {"type":"response.created","response":{"id":"resp_e2e_` + tag + `"}}` + "\n\n" +
		"event: response.output_item.done\n" +
		`data: {"type":"response.output_item.done","item":{"type":"message","role":"assistant","id":"msg_e2e_` + tag + `","content":[{"type":"output_text","text":"OK ` + tag + `"}]}}` + "\n\n" +
		"event: response.completed\n" +
		`data: {"type":"response.completed","response":{"id":"resp_e2e_` + tag + `","usage":{"input_tokens":10,"input_tokens_details":{"cached_tokens":0},"output_tokens":2,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":12}}}` + "\n\n"
}

// runCodex runs one `codex exec [--flags] <command> …` and returns the thread
// id announced by the JSON event stream.
func runCodex(t *testing.T, codexBin, proj string, env []string, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{"exec", "--skip-git-repo-check", "--json", "-C", proj}, args...)
	cmd := exec.Command(codexBin, cmdArgs...)
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("codex %v: %v\n%s", args, err, out)
	}
	threadID := ""
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		var event struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
			Message  string `json:"message"`
			Error    struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		switch event.Type {
		case "thread.started":
			threadID = event.ThreadID
		case "turn.failed", "error":
			message := event.Message
			if message == "" {
				message = event.Error.Message
			}
			if message != "" && !strings.Contains(message, "Model metadata") {
				t.Fatalf("codex turn failed: %s\n%s", message, out)
			}
		}
	}
	if threadID == "" {
		t.Fatalf("codex did not announce a thread: %s", out)
	}
	return threadID
}

// inputItemIDs returns the top-level ids of the request's input items; the
// strict upstream rejects the request when this list is non-empty.
func inputItemIDs(body string) []string {
	var request struct {
		Input []map[string]any `json:"input"`
	}
	if json.Unmarshal([]byte(body), &request) != nil {
		return nil
	}
	var ids []string
	for _, item := range request.Input {
		if id, ok := item["id"].(string); ok && id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}
