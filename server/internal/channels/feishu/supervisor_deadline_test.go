package feishu

import (
	"testing"
	"time"
)

// The answer relay makes four attempts with 10s/30s/2m backoffs. A 2m click
// deadline cut the fourth send off; the default must cover all of them.
func TestSupervisorDefaultHandlerTimeoutAllowsFourAnswerAttempts(t *testing.T) {
	if defaultClickTimeout < 3*time.Minute {
		t.Fatalf("default handler timeout = %s, want at least 3m for four answer attempts", defaultClickTimeout)
	}
}
