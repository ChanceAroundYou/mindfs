package proxy

import "mindfs/server/internal/agent/fold"

// The proxy is one harness transport on top of the fold kernel. The config
// type and the mode constants keep their old names here so every existing call
// site (and every existing test) reads unchanged while the implementation
// lives in fold.

type LiveCompactConfig = fold.Config

const (
	LiveCompactOff    = fold.ModeOff
	LiveCompactShadow = fold.ModeShadow
	LiveCompactOn     = fold.ModeOn
)

// Storage types follow the same rule: the kernel owns the implementation, the
// proxy keeps the short names its planner and tests were written against.
// Task 8 replaces the planner itself, at which point these aliases go away.
type (
	archiveStore     = fold.ArchiveStore
	pointerCache     = fold.PointerCache
	pointerCacheKey  = fold.PointerCacheKey
	cachedPointer    = fold.CachedPointer
	foldCommits      = fold.Commits
	commitLog        = fold.CommitLog
	foldCommitRecord = fold.CommitRecord
)

var (
	newArchiveStore           = fold.NewArchiveStore
	newPointerCache           = fold.NewPointerCache
	pointerCacheKeyFor        = fold.NewPointerCacheKey
	pointerCacheKeyForVariant = fold.NewPointerCacheKeyVariant
	newFoldCommits            = fold.NewCommits
	newFoldCommitsFor         = fold.NewCommitsFor
	newCommitLog              = fold.NewCommitLog
)

const (
	pointerVariantFailed = fold.PointerVariantFailed
	pointerCacheLimit    = fold.PointerCacheLimit
	archiveMaxBytes      = fold.DefaultMaxArchiveBytes
	archiveRetention     = fold.DefaultRetention
)
