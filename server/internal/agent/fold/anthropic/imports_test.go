package anthropic

import (
	"os/exec"
	"strings"
	"testing"
)

// The adapter depends on the kernel, never on the proxy transport that happens
// to host it today. The other direction would make the "universal" fold core
// inherit one harness's package graph.
func TestAdapterDoesNotImportProxy(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "./...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), "/agent/proxy") {
			t.Fatalf("adapter must not depend on the proxy transport: %s", line)
		}
	}
}
