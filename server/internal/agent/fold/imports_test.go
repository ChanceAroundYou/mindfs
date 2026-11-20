package fold

import (
	"os/exec"
	"strings"
	"testing"
)

// The kernel must not depend on any harness transport. The dependency runs one
// way (harness → kernel); the moment fold imports a transport, the "universal"
// part stops being universal and every new adapter inherits the old one's
// package graph.
func TestFoldDoesNotImportProxy(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "./...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), "/agent/proxy") {
			t.Fatalf("fold must not depend on the proxy transport: %s", line)
		}
	}
}
