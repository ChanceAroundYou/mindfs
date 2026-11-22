package agent

import "testing"

func TestCodexThreadRethreaded(t *testing.T) {
	tests := []struct {
		name      string
		persisted string
		current   string
		want      bool
	}{
		{name: "forked to a new thread", persisted: "old-thread", current: "new-thread", want: true},
		{name: "same thread resumed", persisted: "same-thread", current: "same-thread", want: false},
		{name: "brand new session", persisted: "", current: "fresh-thread", want: false},
		{name: "no id at all", persisted: "", current: "", want: false},
		{name: "whitespace is ignored", persisted: "  old-thread  ", current: " new-thread ", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codexThreadRethreaded(tt.persisted, tt.current); got != tt.want {
				t.Fatalf("codexThreadRethreaded(%q, %q) = %v, want %v", tt.persisted, tt.current, got, tt.want)
			}
		})
	}
}
