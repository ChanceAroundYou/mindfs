package main

import "testing"

func TestWindowsBackgroundCreationFlagsDetachFromConsole(t *testing.T) {
	const (
		createNewProcessGroup = uint32(0x00000200)
		detachedProcess       = uint32(0x00000008)
		createNoWindow        = uint32(0x08000000)
	)

	flags := windowsBackgroundCreationFlags()
	if flags&createNewProcessGroup == 0 {
		t.Fatalf("windows background process flags = %#x, want CREATE_NEW_PROCESS_GROUP", flags)
	}
	if flags&detachedProcess == 0 {
		t.Fatalf("windows background process flags = %#x, want DETACHED_PROCESS so the service is not tied to the launching terminal", flags)
	}
	if flags&createNoWindow == 0 {
		t.Fatalf("windows background process flags = %#x, want CREATE_NO_WINDOW: DETACHED_PROCESS alone leaves a console app without a console, so Windows allocates a new one (the black window the user sees at every login)", flags)
	}
}
