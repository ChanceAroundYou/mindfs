package proxy

import "mindfs/server/internal/agent/fold"

// The proxy is one harness transport on top of the fold kernel. The config
// type and the mode constants keep their old names here so every existing call
// site reads unchanged while the implementation lives in fold.
//
// Only what the proxy itself names is aliased: the storage types its Server
// struct holds, the archive capacity it writes with, and the estimator helpers
// the Phase A wire analysis reports with. Everything else is the kernel's and
// is named as such.

type LiveCompactConfig = fold.Config

const (
	LiveCompactOff    = fold.ModeOff
	LiveCompactShadow = fold.ModeShadow
	LiveCompactOn     = fold.ModeOn
)

type (
	archiveStore = fold.ArchiveStore
	pointerCache = fold.PointerCache
	foldCommits  = fold.Commits
	commitLog    = fold.CommitLog
)

const archiveMaxBytes = fold.DefaultMaxArchiveBytes

var (
	newArchiveStore    = fold.NewArchiveStore
	newPointerCache    = fold.NewPointerCache
	newFoldCommitsFor  = fold.NewCommitsFor
	newCommitLog       = fold.NewCommitLog
	estimateTokens     = fold.EstimateTokens
	estimateByteTokens = fold.EstimateByteTokens
	looksLikeErrorText = fold.LooksLikeErrorText
)
