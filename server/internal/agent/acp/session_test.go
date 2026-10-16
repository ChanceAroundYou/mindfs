package acp

import (
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

func TestIsExpectedStreamCloseError(t *testing.T) {
	for _, err := range []error{nil, os.ErrClosed, io.ErrClosedPipe, &os.PathError{Op: "read", Path: "|0", Err: os.ErrClosed}} {
		if !isExpectedStreamCloseError(err) {
			t.Fatalf("error %v should be treated as an expected stream close", err)
		}
	}
	if isExpectedStreamCloseError(errors.New("unexpected read failure")) {
		t.Fatal("unexpected read failure was suppressed")
	}
}

func TestCloseProcessesConcurrentlyDoesNotSerializeWaits(t *testing.T) {
	procs := []*Process{{agentName: "first"}, {agentName: "second"}}
	started := make(chan string, len(procs))
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		closeProcessesConcurrently(procs, func(proc *Process) error {
			started <- proc.agentLabel()
			<-release
			return nil
		})
		close(done)
	}()

	seen := make(map[string]bool, len(procs))
	for range procs {
		select {
		case name := <-started:
			seen[name] = true
		case <-time.After(time.Second):
			t.Fatal("process closes were serialized")
		}
	}
	if !seen["first"] || !seen["second"] {
		t.Fatalf("started closes = %v", seen)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("concurrent process closes did not complete")
	}
}
