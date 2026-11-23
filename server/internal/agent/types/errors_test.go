package types

import (
	"errors"
	"fmt"
	"testing"
)

func TestTurnTerminalReasonReadsWrappedError(t *testing.T) {
	err := fmt.Errorf("outer: %w", &TurnTerminalError{Reason: "  Blocking_Limit ", Message: "Prompt is too long"})
	if got := TurnTerminalReason(err); got != "blocking_limit" {
		t.Fatalf("TurnTerminalReason = %q, want blocking_limit", got)
	}
}

func TestTurnTerminalReasonWithoutTypedErrorIsEmpty(t *testing.T) {
	cases := []error{
		nil,
		errors.New("Prompt is too long"),
		&TurnTerminalError{Reason: "   ", Message: "x"},
	}
	for _, err := range cases {
		if got := TurnTerminalReason(err); got != "" {
			t.Fatalf("TurnTerminalReason(%v) = %q, want empty", err, got)
		}
	}
}

func TestTurnTerminalErrorErrorMessage(t *testing.T) {
	var nilErr *TurnTerminalError
	if got := nilErr.Error(); got != "" {
		t.Fatalf("nil *TurnTerminalError.Error() = %q, want empty", got)
	}
	if got := (&TurnTerminalError{Reason: "blocking_limit", Message: "Prompt is too long"}).Error(); got != "Prompt is too long" {
		t.Fatalf("Error() = %q, want the harness message verbatim", got)
	}
}
