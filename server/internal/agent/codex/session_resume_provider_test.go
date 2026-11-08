package codex

import "testing"

func TestIsCodexProviderMismatch(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "provider switched",
			err:  errString(`app server error (-32600): This chat uses probe-provider, but your current provider is openai. Switch to probe-provider to continue.`),
			want: true,
		},
		{
			name: "provider no longer configured",
			err:  errString(`app server error (-32600): This chat uses model provider "llmux", which is no longer configured. Restore this provider's configuration to continue.`),
			want: true,
		},
		{
			name: "unrelated app server error",
			err:  errString(`app server error (-32600): no rollout found for thread id 01a1`),
			want: false,
		},
		{name: "nil error", err: nil, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCodexProviderMismatch(tc.err); got != tc.want {
				t.Fatalf("isCodexProviderMismatch() = %v, want %v", got, tc.want)
			}
		})
	}
}

type errString string

func (e errString) Error() string { return string(e) }
