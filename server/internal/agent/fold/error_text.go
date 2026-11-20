package fold

import "strings"

// Error-text detection.
//
// Some tools report failure as normal output, with no is_error flag. The card
// says so and widens its excerpt; it never changes eligibility.

// LooksLikeErrorText is a heuristic for the is_error=false-but-it-clearly-failed
// case. It only widens the pointer excerpt, so a false positive costs a few
// lines, not correctness.
//
// Markers must start a line (after trimming). Searching anywhere matched Go
// source that merely contains fmt.Errorf / "failed" in string literals: real
// traffic flagged 2 of 3 file reads as error output (2026-11-18).
func LooksLikeErrorText(text string) bool {
	const headLines = 20
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) > 2*headLines {
		tail := lines[len(lines)-headLines:]
		lines = append(append([]string{}, lines[:headLines]...), tail...)
	}
	for _, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		for _, marker := range []string{
			"traceback (most recent call last)",
			"panic:",
			"fatal:",
			"exception:",
			"exit status",
			"exit code",
			"[stderr]",
			"permission denied",
			"no such file or directory",
			"command not found",
			"error:",
			"failed",
		} {
			if strings.HasPrefix(lower, marker) {
				return true
			}
		}
	}
	return false
}
