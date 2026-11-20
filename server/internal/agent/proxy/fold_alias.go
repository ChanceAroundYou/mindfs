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
