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
	ContextSaving    = fold.ContextSaving
	LedgerEntry      = fold.LedgerEntry
	span             = fold.Span
	pointerInput     = fold.PointerInput
)

var (
	newArchiveStore           = fold.NewArchiveStore
	newPointerCache           = fold.NewPointerCache
	pointerCacheKeyFor        = fold.NewPointerCacheKey
	pointerCacheKeyForVariant = fold.NewPointerCacheKeyVariant
	newFoldCommits            = fold.NewCommits
	newFoldCommitsFor         = fold.NewCommitsFor
	newCommitLog              = fold.NewCommitLog
	errScanMalformed          = fold.ErrScanMalformed
	scanSkipSpace             = fold.ScanSkipSpace
	scanValueEnd              = fold.ScanValueEnd
	scanEachMember            = fold.ScanEachMember
	scanEachElement           = fold.ScanEachElement
	memberKeyIs               = fold.MemberKeyIs
	jsonStringAt              = fold.JSONStringAt
	summarizeToolInput        = fold.SummarizeToolInput
	estimateTokens            = fold.EstimateTokens
	estimateByteTokens        = fold.EstimateByteTokens
	encodeJSONStringLiteral   = fold.EncodeJSONStringLiteral
	looksLikeErrorText        = fold.LooksLikeErrorText
)

const (
	pointerVariantFailed = fold.PointerVariantFailed
	pointerCacheLimit    = fold.PointerCacheLimit
	archiveMaxBytes      = fold.DefaultMaxArchiveBytes
	archiveRetention     = fold.DefaultRetention
	usageSourceResponse  = fold.UsageSourceResponse
	usageSourceMissing   = fold.UsageSourceMissing
	coldReasonCompact    = fold.ColdReasonCompact
	coldReasonIdle       = fold.ColdReasonIdle
	coldReasonNoRead     = fold.ColdReasonNoRead
	coldNoReadStreak     = fold.ColdNoReadStreak
)
