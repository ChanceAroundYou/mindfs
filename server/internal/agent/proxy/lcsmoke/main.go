// Command lcsmoke exercises the live-compact proxy against a real Claude CLI
// session (manual tool, never part of the product build).
//
// Usage, from the repo root:
//
//	go run ./server/internal/agent/proxy/lcsmoke -model ok
//
// It starts an in-process proxy, points a child `claude -p` session at it, and
// prints one [live-compact] line per observed request. The prompt makes the
// child read several large files so candidate tool results appear once they
// fall outside keep_rounds.
//
// With -mode=on it also folds, and then prints the ledger summary the context
// chip reads (SessionContextSaving) — that is the B2 end-to-end path: real CLI
// → proxy → fold → ledger → API summary, with no MindFS server in between.
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
	minFold := flag.Int("min-fold-bytes", 4096, "min_fold_bytes: rejection floor for one fold event")
	keepTail := flag.Int("keep-tail", 32000, "keep_tail_tokens: protected tail size, in estimated tokens")
	rail := flag.Int("rail", 100000, "rail_tokens: calibrated context size that arms folding")
	mode := flag.String("mode", string(proxy.LiveCompactShadow), "live-compact mode: shadow (observe only) or on (fold)")
	prompt := flag.String("prompt", "", "override the child prompt (default: read six repo files one at a time)")
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
		Mode:             *mode,
		KeepRounds:       *keep,
		MinBlockTokens:   2000,
		MinBodyBytes:     *minBody,
		MaxDriftMessages: 8192,
		MinFoldBytes:     *minFold,
		KeepTailTokens:   *keepTail,
		RailTokens:       *rail,
		DisableFile:      filepath.Join(tmp, "DISABLED"),
		ArchiveDir:       filepath.Join(tmp, "context"),
		ArchiveEnabled:   true,
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

	childPrompt := *prompt
	if childPrompt == "" {
		childPrompt = `Use the Read tool to read these files in order, exactly one Read call each, then reply with exactly DONE:
server/app/server.go
server/internal/agent/pool.go
server/internal/agent/proxy/proxy.go
cli/cmd/mindfs.go
server/internal/kanban/workflow_agent_ops.go
server/internal/kanban/service_outbox.go`
	}

	cmd := exec.Command("claude",
		"-p", childPrompt,
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

	// B2 path: read back the ledger exactly as the session page does. In shadow
	// mode this stays empty on purpose (nothing is folded, nothing is recorded).
	// The billing-context totals are the A/B metric: a body is mostly text the
	// fold cannot reach, so bytes compare policies badly. Folded tokens over
	// (context tokens + folded tokens) is what the user actually saves.
	saving := ps.SessionContextSaving("smoke-session")
	share := 0.0
	if saving.TotalContextTokens+saving.TotalFoldedTokens > 0 {
		share = float64(saving.TotalFoldedTokens) / float64(saving.TotalContextTokens+saving.TotalFoldedTokens)
	}
	fmt.Fprintf(os.Stderr, "ledger: requests=%d max_saved=%d rewrite_total=%d latest={saved=%d rewrite=%d blocks=%d}\n",
		saving.Requests, saving.MaxSavedBytes, saving.TotalRewriteTokens,
		saving.Latest.SavedBytes, saving.Latest.RewriteTokens, saving.Latest.Blocks)
	fmt.Fprintf(os.Stderr, "billing: context_tokens=%d folded_tokens=%d samples=%d share=%.3f\n",
		saving.TotalContextTokens, saving.TotalFoldedTokens, saving.ContextTokenSamples, share)
}
