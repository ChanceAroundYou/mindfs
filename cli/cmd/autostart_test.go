package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestEnvironmentMapPreservesValuesContainingEquals(t *testing.T) {
	got := environmentMap([]string{"PLAIN=value", "TOKEN=a=b=c", "BROKEN"})
	want := map[string]string{"PLAIN": "value", "TOKEN": "a=b=c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environmentMap() = %#v, want %#v", got, want)
	}
}

func TestRestoredEnvironmentFiltersSessionAndInternalValues(t *testing.T) {
	for _, key := range []string{"PWD", "SSH_AUTH_SOCK", "TERM_SESSION_ID", "MINDFS_DAEMON", "mindfs_internal_restart"} {
		if !shouldSkipRestoredEnvironment(key) {
			t.Errorf("shouldSkipRestoredEnvironment(%q) = false", key)
		}
	}
	if shouldSkipRestoredEnvironment("OPENAI_API_KEY") {
		t.Fatal("OPENAI_API_KEY should be restored")
	}
}

func TestApplyEnvironmentPreservesCurrentSystemIdentity(t *testing.T) {
	t.Setenv("HOME", "/current/home")
	t.Setenv("MINDFS_AUTOSTART_TEST", "old")
	t.Setenv("PATH", os.Getenv("PATH"))
	applyEnvironment(map[string]string{
		"HOME":                  "/snapshot/home",
		"MINDFS_AUTOSTART_TEST": "new",
		"PATH":                  "/snapshot/bin",
	}, true)
	if got := os.Getenv("HOME"); got != "/current/home" {
		t.Fatalf("HOME = %q, want current system value", got)
	}
	if got := os.Getenv("MINDFS_AUTOSTART_TEST"); got != "old" {
		t.Fatalf("internal test variable = %q, want skipped current value", got)
	}
	if got := os.Getenv("PATH"); got != "/snapshot/bin" {
		t.Fatalf("PATH = %q, want snapshot value", got)
	}
}

func TestSaveAutoStartSnapshotUsesPrivatePermissions(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	path, err := saveAutoStartSnapshot(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := readAutoStartSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != 1 || snapshot.OS != runtime.GOOS || snapshot.Environment == nil {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("snapshot mode = %o, want 600", got)
		}
	}
}

func TestReadShellEnvironmentIgnoresRCOutputBeforeMarker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper is a POSIX script")
	}
	shell := filepath.Join(t.TempDir(), "zsh")
	script := "#!/bin/sh\nprintf 'rc banner\\n" + autoStartMarker + "\\0PATH=/from-rc\\0TOKEN=a=b\\0'\n"
	if err := os.WriteFile(shell, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	env, err := readShellEnvironment(shell, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if env["PATH"] != "/from-rc" || env["TOKEN"] != "a=b" {
		t.Fatalf("shell environment = %#v", env)
	}
}

func TestPrepareAutoStartEnvironmentWithoutSnapshotIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("AppData", dir)
	} else {
		t.Setenv("XDG_CONFIG_HOME", dir)
	}
	// 自动启动项没有快照 = 没有需要从交互式 shell 继承的环境：必须是 no-op。
	if err := prepareAutoStartEnvironment(); err != nil {
		t.Fatalf("missing snapshot should be a no-op, got %v", err)
	}
	// 快照存在但内容坏了仍然是真故障：不能被同一条放行规则吞掉。
	configDir, err := autoStartConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(configDir, autoStartSnapshotName)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareAutoStartEnvironment(); err == nil {
		t.Fatal("corrupt snapshot must still fail")
	}
}

func TestAutoStartArgumentsContainOnlyPersistentServerOptions(t *testing.T) {
	got := autoStartArguments("127.0.0.1:9000", false, true, "/cert.pem", "/key.pem", "/agents.json", "/notify")
	joined := strings.Join(got, " ")
	for _, expected := range []string{"--internal-autostart", "--addr 127.0.0.1:9000", "--web-push=false", "--tls", "--cert /cert.pem", "--key /key.pem", "--agent-config /agents.json", "--notify-script /notify"} {
		if !strings.Contains(joined, expected) {
			t.Errorf("arguments %q do not contain %q", joined, expected)
		}
	}
	for _, transient := range []string{"--restart", "--foreground"} {
		if strings.Contains(joined, transient) {
			t.Errorf("arguments %q unexpectedly contain %q", joined, transient)
		}
	}
}
