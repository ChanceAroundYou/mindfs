package main

import (
	"strings"
	"testing"
)

func TestResolveClientTLSExplicitScheme(t *testing.T) {
	for _, addr := range []string{"https://127.0.0.1:7331", "http://127.0.0.1:7331"} {
		want := strings.HasPrefix(addr, "https://")
		if got, err := resolveClientTLS(addr, !want, true); err != nil || got != want {
			t.Fatalf("%s: TLS = %v, %v", addr, got, err)
		}
	}
}

func TestAddrToURLIPv6(t *testing.T) {
	for addr, want := range map[string]string{
		"[::1]:7331": "https://[::1]:7331/health",
		"[::]:7331":  "https://127.0.0.1:7331/health",
		":7331":      "https://localhost:7331/health",
	} {
		if got := addrToURL(addr, "/health", true); got != want {
			t.Fatalf("%s: got %s, want %s", addr, got, want)
		}
	}
}
