package fold

import "testing"

func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		name string
		text string
		want int
	}{
		{"empty", "", 0},
		{"short ascii rounds up", "abc", 1},
		{"exact four ascii bytes", "abcd", 1},
		{"eight ascii bytes", "abcdefgh", 2},
		{"cjk one token per rune", "中文工具输出", 6},
		{"mixed ascii and cjk", "abc中文", 1 + 2},
		{"newlines count as ascii", "a\nb\nc\nd", 2},
		{"invalid utf8 counts leading bytes", "\xff\xfe\x80", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EstimateTokens(tc.text); got != tc.want {
				t.Fatalf("EstimateTokens(%q) = %d, want %d", tc.text, got, tc.want)
			}
		})
	}
}
