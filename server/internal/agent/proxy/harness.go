package proxy

import (
	"path/filepath"
	"strings"
	"time"

	"mindfs/server/internal/agent/fold"
	"mindfs/server/internal/agent/fold/anthropic"
	"mindfs/server/internal/agent/fold/responses"
)

// Agent harnesses. The policy can differ per harness (see
// fold.Config.Harnesses), and so must the storage: two harnesses writing into
// one archive directory would overwrite each other's commit logs and mix one
// conversation's savings into the other's chip.
const (
	harnessClaude = "claude"
	harnessCodex  = "codex"
)

// Wire-format names, used by the fold entry points. One adapter per harness.
const (
	adapterAnthropic = "anthropic"
	adapterResponses = "responses"
)

// harnessForProvider maps a /c/{provider}/ route to a harness. That route only
// serves the Responses wire format, and Codex is the harness that speaks it.
func harnessForProvider(string) string { return harnessCodex }

// harnessForAdapter maps a wire format to the harness that uses it.
func harnessForAdapter(adapter string) string {
	if adapter == adapterResponses {
		return harnessCodex
	}
	return harnessClaude
}

// harnessForPoolKey reads the harness off a pool-level session key
// (`<agent>-<session>`). The ledger is keyed by exactly that string, and it is
// what the context chip asks for, so the read path must resolve the harness the
// same way the write path did.
func harnessForPoolKey(key string) string {
	name, _, ok := strings.Cut(strings.ToLower(strings.TrimSpace(key)), "-")
	if ok && name == harnessCodex {
		return harnessCodex
	}
	return harnessClaude
}

// harnessRuntime is one harness's view of live compact: the node configuration
// with that harness's overrides applied, plus the stores and the engine built
// from it. The default harness shares the server's own stores (its policy is
// the node policy); every other harness gets a subdirectory of the archive so
// the two histories stay apart.
type harnessRuntime struct {
	cfg          LiveCompactConfig
	archive      *archiveStore
	pointerCache *pointerCache
	ledger       *fold.LedgerStore
	engine       *fold.Engine
}

// harnessRuntimeFor returns a harness's runtime, building it on first use. An
// unlisted harness runs with the node configuration unchanged and gets its own
// storage, which is harmless: only requests attributed to it ever read it.
func (s *Server) harnessRuntimeFor(harness string) *harnessRuntime {
	harness = strings.ToLower(strings.TrimSpace(harness))
	if harness == "" {
		harness = harnessClaude
	}
	s.harnessMu.Lock()
	defer s.harnessMu.Unlock()
	if rt, ok := s.harnessRuntimes[harness]; ok {
		return rt
	}
	if s.harnessRuntimes == nil {
		s.harnessRuntimes = make(map[string]*harnessRuntime)
	}
	cfg := s.liveCompact.ForHarness(harness)
	rt := &harnessRuntime{cfg: cfg}
	if harness == harnessClaude {
		// The node's archive, ledger and pointer cache were created with the
		// server; reuse them rather than opening the same directory twice.
		rt.archive = s.archive
		rt.pointerCache = s.pointerCache
		rt.ledger = s.ledger
	} else {
		dir := cfg.ArchiveDir
		if dir != "" {
			dir = filepath.Join(dir, harness)
		}
		if cfg.ArchiveEnabled && dir != "" {
			rt.archive = newArchiveStore(dir,
				time.Duration(cfg.RetentionDays)*24*time.Hour, cfg.MaxArchiveBytes)
			rt.pointerCache = newPointerCache()
			rt.ledger = fold.NewLedgerStore(filepath.Join(dir, fold.LedgerDirName))
		}
	}
	rt.engine = fold.NewEngine(cfg, adapterForHarness(harness), rt.archive, rt.pointerCache)
	s.harnessRuntimes[harness] = rt
	return rt
}

func adapterForHarness(harness string) fold.Adapter {
	if harness == harnessCodex {
		return responses.New()
	}
	return anthropic.New()
}
