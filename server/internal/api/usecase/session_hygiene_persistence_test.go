package usecase

import (
	"testing"
	"time"
)

func TestHygieneActiveTimeSurvivesTrackerRestart(t *testing.T) {
	dir := t.TempDir()
	base := time.Unix(1_760_000_000, 0)

	first := NewHygieneTrackerAt(dir, hygieneConfig())
	first.ObserveAt("root", "s1", "claude", base)
	first.ObserveAt("root", "s1", "claude", base.Add(20*time.Minute))
	if got := first.Snapshot("root", "s1", "claude", 0).ActiveSeconds; got != 1200 {
		t.Fatalf("precondition: active_seconds = %d, want 1200", got)
	}

	second := NewHygieneTrackerAt(dir, hygieneConfig())
	second.ObserveAt("root", "s1", "claude", base.Add(40*time.Minute))
	if got := second.Snapshot("root", "s1", "claude", 0).ActiveSeconds; got != 2400 {
		t.Fatalf("active_seconds after restart = %d, want 2400", got)
	}
}

func TestHygieneCompactionResetsPersistedActiveTime(t *testing.T) {
	dir := t.TempDir()
	base := time.Unix(1_760_000_000, 0)

	first := NewHygieneTrackerAt(dir, hygieneConfig())
	first.ObserveAt("root", "s1", "claude", base)
	first.ObserveAt("root", "s1", "claude", base.Add(30*time.Minute))
	first.markCompactedAt("root", "s1", base.Add(30*time.Minute))
	first.ObserveAt("root", "s1", "claude", base.Add(35*time.Minute))

	second := NewHygieneTrackerAt(dir, hygieneConfig())
	second.ObserveAt("root", "s1", "claude", base.Add(40*time.Minute))
	if got := second.Snapshot("root", "s1", "claude", 0).ActiveSeconds; got != 600 {
		t.Fatalf("active_seconds since compact = %d, want 600", got)
	}
}

func TestHygieneCompactionResetsPersistedStateImmediately(t *testing.T) {
	dir := t.TempDir()
	base := time.Unix(1_760_000_000, 0)

	first := NewHygieneTrackerAt(dir, hygieneConfig())
	first.ObserveAt("root", "s1", "claude", base)
	first.ObserveAt("root", "s1", "claude", base.Add(30*time.Minute))
	first.markCompactedAt("root", "s1", base.Add(30*time.Minute))

	second := NewHygieneTrackerAt(dir, hygieneConfig())
	second.ObserveAt("root", "s1", "claude", base.Add(35*time.Minute))
	if got := second.Snapshot("root", "s1", "claude", 0).ActiveSeconds; got != 300 {
		t.Fatalf("active_seconds after restart following compact = %d, want 300", got)
	}
}

func TestHygieneCompactionRestartsTheIdleAnchor(t *testing.T) {
	dir := t.TempDir()
	base := time.Unix(1_760_000_000, 0)

	tracker := NewHygieneTrackerAt(dir, hygieneConfig())
	tracker.ObserveAt("root", "s1", "claude", base)
	tracker.markCompactedAt("root", "s1", base.Add(30*time.Minute))
	tracker.ObserveAt("root", "s1", "claude", base.Add(35*time.Minute))
	if got := tracker.Snapshot("root", "s1", "claude", 0).ActiveSeconds; got != 300 {
		t.Fatalf("active_seconds since compact = %d, want 300", got)
	}
}

func TestHygienePersistedLastSeenDropsLongIdleGap(t *testing.T) {
	dir := t.TempDir()
	base := time.Unix(1_760_000_000, 0)

	first := NewHygieneTrackerAt(dir, hygieneConfig())
	first.ObserveAt("root", "s1", "claude", base)

	second := NewHygieneTrackerAt(dir, hygieneConfig())
	second.ObserveAt("root", "s1", "claude", base.Add(4*time.Hour))
	if got := second.Snapshot("root", "s1", "claude", 0).ActiveSeconds; got != 0 {
		t.Fatalf("idle gap across restart counted as active: %d", got)
	}
	second.ObserveAt("root", "s1", "claude", base.Add(4*time.Hour+10*time.Minute))
	if got := second.Snapshot("root", "s1", "claude", 0).ActiveSeconds; got != 600 {
		t.Fatalf("active_seconds after idle gap = %d, want 600", got)
	}
}
