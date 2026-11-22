package proxy

import "testing"

// The rail calibrates against the pair (lastActual, lastForwardEst). A request
// that never reports usage may not leave its estimate behind: otherwise the
// next accepted sample pairs its actual with an earlier request's estimate and
// every projection is built on a ratio that never held. A small side request is
// exactly this shape — it carries a fraction of the conversation and its
// response usage is not scanned.
func TestCalibrationPairCommitsTogether(t *testing.T) {
	server, _ := newFoldServer(t, LiveCompactConfig{Mode: LiveCompactOn})
	server.sessions["tok"].lastActual = 100000
	server.sessions["tok"].lastForwardEst = 80000

	// A side request goes out: estimate only, no sample follows.
	server.noteForwardEstimate("tok", 3000)
	if actual, forward := server.sessionCalibration("tok"); actual != 100000 || forward != 80000 {
		t.Fatalf("pair after unsampled request = (%d, %d), want (100000, 80000)", actual, forward)
	}

	// The main request goes out and its usage arrives.
	server.noteForwardEstimate("tok", 90000)
	server.noteActualContext("tok", usageTotals{Seen: true, InputTokens: 90000, CacheReadInputTokens: 10000})
	actual, forward := server.sessionCalibration("tok")
	if actual != 100000 || forward != 90000 {
		t.Fatalf("pair = (%d, %d), want (100000, 90000) — the estimate must be this request's", actual, forward)
	}
}

// A small side request that does report usage still measures a different
// request than the pair describes. Accepting it would make the rail project the
// conversation's size from a tiny context, so the pair must stay put.
func TestAuxiliarySampleDoesNotMoveTheCalibrationPair(t *testing.T) {
	server, _ := newFoldServer(t, LiveCompactConfig{Mode: LiveCompactOn})
	server.sessions["tok"].lastActual = 200000
	server.sessions["tok"].lastForwardEst = 180000

	server.noteForwardEstimate("tok", 3000)
	server.noteActualContext("tok", usageTotals{Seen: true, InputTokens: 6000})

	actual, forward := server.sessionCalibration("tok")
	if actual != 200000 || forward != 180000 {
		t.Fatalf("pair = (%d, %d), want the previous (200000, 180000)", actual, forward)
	}
	// The cold-cache detector still learns the session's latest context; only
	// the calibration pair is protected.
	if got := server.sessions["tok"].live.lastContext; got != 6000 {
		t.Fatalf("lastContext = %d, want 6000", got)
	}
}

// Compaction is the legitimate version of the same shape: the first request
// after it really is a fraction of the previous context, so the harness signal
// must let that sample replace the pair.
func TestCompactionSignalAcceptsSmallerFirstSample(t *testing.T) {
	server, _ := newFoldServer(t, LiveCompactConfig{Mode: LiveCompactOn})
	server.RegisterThread("thread-a", "session-a")
	key := threadStateKey("thread-a")
	server.sessions[key].lastActual = 200000
	server.sessions[key].lastForwardEst = 180000

	server.SignalCompact("thread-a")
	server.noteForwardEstimate(key, 20000)
	server.noteActualContext(key, usageTotals{Seen: true, InputTokens: 25000})

	actual, forward := server.sessionCalibration(key)
	if actual != 25000 || forward != 20000 {
		t.Fatalf("pair after compaction = (%d, %d), want (25000, 20000)", actual, forward)
	}
}

// The first sample of a session has no previous scale to protect: it must
// calibrate normally, or a fresh session would never reach the rail.
func TestFirstSampleCalibratesWithoutPreviousPair(t *testing.T) {
	server, _ := newFoldServer(t, LiveCompactConfig{Mode: LiveCompactOn})
	server.noteForwardEstimate("tok", 10000)
	server.noteActualContext("tok", usageTotals{Seen: true, InputTokens: 12000})

	actual, forward := server.sessionCalibration("tok")
	if actual != 12000 || forward != 10000 {
		t.Fatalf("first pair = (%d, %d), want (12000, 10000)", actual, forward)
	}
}
