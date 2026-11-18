// Command lcsmoke exercises the live-compact proxy against a real Claude CLI
// session (manual tool, never part of the product build).
//
// Usage, from the repo root:
//
//	go run ./server/internal/agent/proxy/lcsmoke -model ok
//
// It starts an in-process proxy in shadow mode, points a child `claude -p`
// session at it, and prints one [live-compact] line per observed request. The
// prompt makes the child read several large files so candidate tool results
// appear once they fall outside keep_rounds.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"mindfs/server/internal/agent/proxy"
)

func userUpstream() string {
	if v := os.Getenv("REAL_UPSTREAM"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	payload, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		panic(err)
	}
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(payload, &settings); err != nil {
		panic(err)
	}
	return settings.Env["ANTHROPIC_BASE_URL"]
}

func main() {
	model := flag.String("model", "ok", "Claude model alias to drive the smoke session")
	dir := flag.String("dir", "", "working directory for the child session (default: current directory)")
	minBody := flag.Int("min-body", 2048, "min_body_bytes for the probe proxy")
	keep := flag.Int("keep", 3, "keep_rounds for the probe proxy")
	flag.Parse()

	upstream := userUpstream()
	if upstream == "" {
		panic("no ANTHROPIC_BASE_URL")
	}
	if *dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			panic(err)
		}
		*dir = cwd
	}

	tmp, err := os.MkdirTemp("", "lcsmoke-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)

	ps, err := proxy.NewServerWithLiveCompact(proxy.LiveCompactConfig{
		Mode:             proxy.LiveCompactShadow,
		KeepRounds:       *keep,
		MinBlockTokens:   2000,
		MinBodyBytes:     *minBody,
		MaxDriftMessages: 8192,
		DisableFile:      filepath.Join(tmp, "DISABLED"),
	})
	if err != nil {
		panic(err)
	}
	defer ps.Close()

	token := "smoke01"
	ps.RegisterSessionWithKey(token, upstream, "smoke-session")
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d/p/%s", ps.Port(), token)

	settingsPath := filepath.Join(tmp, "settings.json")
	settings := fmt.Sprintf(`{"env":{"ANTHROPIC_BASE_URL":%q}}`, proxyURL)
	if err := os.WriteFile(settingsPath, []byte(settings), 0o600); err != nil {
		panic(err)
	}

	prompt := `Use the Read tool to read these files in order, exactly one Read call each, then reply with exactly DONE:
server/app/server.go
server/internal/agent/pool.go
server/internal/agent/proxy/proxy.go
cli/cmd/mindfs.go
server/internal/kanban/workflow_agent_ops.go
server/internal/kanban/service_outbox.go`

	cmd := exec.Command("claude",
		"-p", prompt,
		"--model", *model,
		"--settings", settingsPath,
		"--allowedTools", "Read",
	)
	cmd.Dir = *dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()
	// Give the proxy handler a moment to finish logging the last request.
	time.Sleep(500 * time.Millisecond)
	fmt.Fprintf(os.Stderr, "claude exit: %v\n", runErr)
}
