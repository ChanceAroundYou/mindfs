// Package fold is the protocol-independent core of MindFS live compact.
//
// The problem it solves is the same for every agent harness: a long session
// resends every historical tool result on every turn, and past a certain size
// the harness compacts the whole transcript into a summary the user cannot see
// or steer. Folding replaces the oldest tool results with a short pointer plus
// a content-addressed archive the model can re-read on demand, so the session
// keeps its own history instead of losing it to a summary.
//
// What is protocol-independent lives here: configuration, sizing, candidate
// eligibility, the rail budget, pointer rendering, archiving, the monotone
// commitment set, the ledger, and the cold-cache policy. What is not — how to
// find a tool result in a request body, how to splice it back, how usage is
// reported — belongs to a per-harness adapter.
//
// Invariant: this package never imports a harness transport. The dependency
// runs one way, harness → kernel (see imports_test.go).
package fold

import (
	"log"
	"strings"
	"time"
)

// Modes. The zero value normalizes to off, so an unconfigured node pays
// nothing.
const (
	ModeOff    = "off"
	ModeShadow = "shadow"
	ModeOn     = "on"
)

// Archive defaults. The archive store itself moves here in the next step; the
// numbers live beside Config.Normalized because that is what needs them.
const (
	// DefaultRetention is how long a folded original is kept. The archive is a
	// convenience for the model, not a backup.
	DefaultRetention = 7 * 24 * time.Hour
	// DefaultMaxArchiveBytes caps the archive directory. Folding stops writing
	// new archives when the cap is hit; already-folded pointers keep working
	// because their files stay until the GC removes them.
	DefaultMaxArchiveBytes = int64(2 << 30)
)

// Config is the node-local context-management configuration. The zero value
// normalizes to off, so an unconfigured node pays nothing.
type Config struct {
	Mode string
	// KeepRounds is the number of newest completed tool rounds left verbatim.
	KeepRounds int
	// KeepTailTokens bounds the same protected tail by size, not just by round
	// count (design §12.7(d), dsh's keepRoundTokens). Three rounds of small
	// reads is a fine tail; three rounds holding 90 KB reads is 75k tokens of
	// tail on a 200k window, and then a rail that fires at 160k has only one
	// aged-out round to cut — a shallow fold every turn, which §12.7(b) measured
	// as more expensive than not folding. The newest round always stays.
	// Zero means the default.
	KeepTailTokens int
	// MinBlockTokens is the size a *failed* (is_error) tool result needs to
	// become a candidate. Ordinary results are judged per block by how many
	// bytes the pointer actually saves (foldMinBlockSavings in the planner),
	// because a token floor of this size rejected most real sessions' 1-7 KB
	// results and left rail-triggered events with nothing to fold.
	MinBlockTokens int
	// MinBodyBytes skips JSON parsing for requests too small to contain a
	// foldable result. This keeps ordinary short turns on the cheap path.
	MinBodyBytes int
	// MaxDriftMessages bounds the per-message fingerprint history kept for
	// detecting CLI-side history rewrites. A zero value means the default.
	MaxDriftMessages int
	// RailTokens is the upstream-token trigger for folding new blocks (design
	// §12.7). Below it, already-committed folds are still applied but no new
	// block is committed, because a fold invalidates the upstream prefix cache
	// from the boundary onward. Zero means the default (100k). The rail is the
	// resident-context ceiling the user asked for: 160k measured within noise of
	// 100k on cost but let requests reach 130k, while 100k peaked at 95k.
	RailTokens int
	// TargetTokens is the low-water line one fold event commits down to. It
	// sits far below the rail on purpose: the model is "fold deep, fold rarely"
	// (§12.7(b) — a shallow fold cannot repay the cache rewrite it costs). In
	// practice keep_rounds and keep_tail_tokens are the binding constraints; the
	// target only stops an event early when the context was already small. At a
	// 100k rail it is also never the binding one: the tail alone can be 50k+.
	TargetTokens int
	// MinFoldBytes rejects a fold event too small to be worth a cache break.
	MinFoldBytes int
	// MaxFoldBytes caps one event. Zero means the default cap (262144); no
	// caller can accidentally disable the cap by leaving it unset.
	MaxFoldBytes int
	// LargeBlockTokens lets a single oversized tool result be folded even below
	// the rail: one such result dominates the context on its own.
	LargeBlockTokens int
	// DisableFile is the emergency brake. When the file exists, folding and
	// observation stop for the running process without a service restart.
	// Empty means no brake file is wired (default off, nothing to stop).
	DisableFile string
	// ArchiveDir is where folded tool output is stored. Empty disables
	// archiving, which also disables folding: a pointer must never name a file
	// that does not exist.
	ArchiveDir string
	// ArchiveEnabled defaults to true when ArchiveDir is set. It exists so a
	// node can keep observing while storage is unavailable.
	ArchiveEnabled bool
	// RetentionDays and MaxArchiveBytes bound the archive directory. Zero
	// values mean the defaults (7 days, 2 GiB).
	RetentionDays   int
	MaxArchiveBytes int64
	// PrefixGuard refuses to fold requests whose model runs Anthropic's
	// preserved-thinking prefix check (design 2026-11-20 §4). That check
	// compares what the client *sent*, so a proxy-side rewrite invalidates
	// every later thinking block on those models. Tri-state: nil means on,
	// because a node that never configured it must still be protected.
	PrefixGuard *bool
	// PrefixUnsafeModels lists model ids (usually aliases such as llmux's
	// "ok") that map to a preserved-thinking model upstream. Matching is on the
	// normalized family, so an alias is written exactly as it appears in the
	// request body.
	PrefixUnsafeModels []string
	// ColdFold allows folding below the rail when the upstream prompt cache is
	// already cold (design 2026-11-20 §5): a fold then costs no cache rewrite
	// because there is no warm prefix to invalidate. Tri-state: nil means on.
	ColdFold *bool
	// ColdRailTokens is the context size at which a cold fold is worth doing.
	// Below it the fold buys no window headroom and only costs continuity.
	// Zero means the default (60000).
	ColdRailTokens int
	// CacheTTLSeconds is how long an idle gap must be before the next request's
	// prefix is assumed evicted upstream. Zero means the default (300, the
	// Anthropic 5-minute cache TTL).
	CacheTTLSeconds int
}

// PrefixGuardOn reports whether the preserved-thinking guard is enabled. The
// zero value is on: protection must not depend on a node opting in.
func (c Config) PrefixGuardOn() bool {
	return c.PrefixGuard == nil || *c.PrefixGuard
}

// ColdFoldOn reports whether cold-cache folding is enabled. Zero value is on.
func (c Config) ColdFoldOn() bool {
	return c.ColdFold == nil || *c.ColdFold
}

func (c Config) Normalized() Config {
	c.Mode = strings.ToLower(strings.TrimSpace(c.Mode))
	switch c.Mode {
	case ModeOff, ModeShadow, ModeOn:
	case "":
		c.Mode = ModeOff
	default:
		log.Printf("[live-compact] unknown mode %q, using off", c.Mode)
		c.Mode = ModeOff
	}
	if c.KeepRounds <= 0 {
		c.KeepRounds = 3
	}
	if c.KeepTailTokens <= 0 {
		c.KeepTailTokens = 32000
	}
	if c.MinBlockTokens <= 0 {
		c.MinBlockTokens = 2000
	}
	if c.MinBodyBytes <= 0 {
		c.MinBodyBytes = 100 << 10
	}
	if c.MaxDriftMessages <= 0 {
		c.MaxDriftMessages = 8192
	}
	// A negative rail disables the gate; zero means the default.
	//
	// The defaults are the "fold deep, fold rarely" shape measured in design
	// §12.7: cut all the way down (target 20k; the protected tail — keep_rounds
	// plus keep_tail_tokens ≈ 32k — is what actually stops the cut) so one event
	// repays its cache rewrite in a round or two. The rail is 100k, not the
	// CLI's ~167k auto-compact threshold: the seven-arm uniform-read bench put
	// 100k within noise of 160k on cost (339,933–380,111 vs 340,190–367,914) but
	// capped the resident request at 95.2k instead of 129.7k. Going lower is not
	// free: 50k folds one block per turn, re-prices the whole tail every turn,
	// and measured ~+10% (plan, B4 Step 8). The earlier 130k/100k pair folded
	// every ~3 rounds by ~15%, which needs ~33 rounds to break even and measured
	// 2.26x the cost of not folding at all.
	if c.RailTokens == 0 {
		c.RailTokens = 100000
	}
	if c.RailTokens > 0 {
		if c.TargetTokens <= 0 {
			c.TargetTokens = 20000
		}
		// A target at or above the rail would make one event commit nothing and
		// leave the next request above the rail again.
		if c.TargetTokens >= c.RailTokens {
			c.TargetTokens = c.RailTokens * 3 / 4
		}
	}
	// dsh's chunkMin is 1000 tokens for one cut; below that the cache break is
	// pure loss (design §12.7(b)).
	if c.MinFoldBytes <= 0 {
		c.MinFoldBytes = 4096
	}
	// Zero means the default cap, not "unlimited": one event must not fold the
	// whole history (design §12.6 budget). A value below the minimum would make
	// every event impossible, so it is raised to the minimum.
	//
	// This caps one chunk; one event may drain up to foldMaxChunksPerEvent of
	// them (design §12.7(d)). A whole-event cap would stop the drain after ~2
	// blocks, which is the shallow fold §12.7(b) measured as worse than none.
	if c.MaxFoldBytes == 0 {
		c.MaxFoldBytes = 262144
	}
	if c.MaxFoldBytes > 0 && c.MaxFoldBytes < c.MinFoldBytes {
		c.MaxFoldBytes = c.MinFoldBytes
	}
	if c.LargeBlockTokens <= 0 {
		c.LargeBlockTokens = 32000
	}
	// 60k: below this there is still ~100k of room to the CLI's auto-compact
	// threshold, so a cold fold buys no headroom and only risks continuity.
	if c.ColdRailTokens <= 0 {
		c.ColdRailTokens = 60000
	}
	if c.CacheTTLSeconds <= 0 {
		c.CacheTTLSeconds = 300
	}
	if c.RetentionDays <= 0 {
		c.RetentionDays = int(DefaultRetention / (24 * time.Hour))
	}
	if c.MaxArchiveBytes <= 0 {
		c.MaxArchiveBytes = DefaultMaxArchiveBytes
	}
	if c.ArchiveDir == "" {
		c.ArchiveEnabled = false
	}
	return c
}

// Observes reports whether requests should be parsed for measurement. Both
// shadow and on parse; only on may rewrite.
func (c Config) Observes() bool {
	return c.Mode == ModeShadow || c.Mode == ModeOn
}
