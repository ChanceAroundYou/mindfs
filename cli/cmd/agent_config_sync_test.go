package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"mindfs/internal/codexsync"
)

func TestAgentConfigSyncCommandDispatchesSingleMode(t *testing.T) {
	for _, test := range []struct {
		flag string
		mode codexsync.Mode
	}{
		{"--check", codexsync.ModeCheck},
		{"--apply", codexsync.ModeApply},
		{"--sync", codexsync.ModeSync},
		{"--render", codexsync.ModeRender},
		{"--check-runtime", codexsync.ModeCheckRuntime},
	} {
		t.Run(test.flag, func(t *testing.T) {
			var output bytes.Buffer
			handled, err := runAgentConfigSyncCommandWith([]string{"agents-config-sync", test.flag}, &output, func(_ context.Context, mode codexsync.Mode) (codexsync.Result, error) {
				if mode != test.mode {
					t.Fatalf("mode = %q, want %q", mode, test.mode)
				}
				return codexsync.Result{State: "synced", Message: "safe"}, nil
			})
			if err != nil || !handled {
				t.Fatalf("handled=%t err=%v", handled, err)
			}
			var result codexsync.Result
			if err := json.Unmarshal(output.Bytes(), &result); err != nil {
				t.Fatalf("command output is not JSON: %v", err)
			}
			if result.State != "synced" {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestAgentConfigSyncCommandRejectsAmbiguousMode(t *testing.T) {
	for _, args := range [][]string{
		{"agents-config-sync"},
		{"agents-config-sync", "--check", "--sync"},
		{"agents-config-sync", "--apply", "extra"},
		{"agents-config-sync", "--render", "--sync"},
	} {
		handled, err := runAgentConfigSyncCommandWith(args, &bytes.Buffer{}, func(context.Context, codexsync.Mode) (codexsync.Result, error) {
			t.Fatal("runner called for invalid command")
			return codexsync.Result{}, nil
		})
		if !handled || err == nil {
			t.Fatalf("args %v: handled=%t err=%v", args, handled, err)
		}
	}
}

func TestAgentConfigSyncCommandPrintsSafeStateWhenRunFails(t *testing.T) {
	var output bytes.Buffer
	secret := "do-not-print-this"
	handled, err := runAgentConfigSyncCommandWith([]string{"agents-config-sync", "--sync"}, &output, func(context.Context, codexsync.Mode) (codexsync.Result, error) {
		return codexsync.Result{State: "pending", Message: "sync pending"}, errors.New(secret)
	})
	if !handled || err == nil {
		t.Fatalf("handled=%t err=%v", handled, err)
	}
	if bytes.Contains(output.Bytes(), []byte(secret)) {
		t.Fatal("command output included raw sync error")
	}
}
