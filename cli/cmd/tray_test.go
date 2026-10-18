package main

import "testing"

func TestTrayIsWindowsOnlyAndOpensThePageOnQuit(t *testing.T) {
	if !shouldShowTray("windows") {
		t.Fatal("Windows background instance has no terminal and no UI action to start it, so the tray must show there")
	}
	for _, goos := range []string{"linux", "darwin"} {
		if shouldShowTray(goos) {
			t.Fatalf("tray must not show on %s: terminal or systemd already covers it", goos)
		}
	}

	if got := trayTooltip("0.0.0.0:7332"); got != "mindfs 0.0.0.0:7332" {
		t.Fatalf("tooltip = %q, want the addr in it so a multi-node box is distinguishable", got)
	}

	items := trayMenuItems()
	if len(items) != 2 || items[0].ID != menuOpenID || items[1].ID != menuQuitID {
		t.Fatalf("menu must be exactly open + quit, got %v", items)
	}
}
