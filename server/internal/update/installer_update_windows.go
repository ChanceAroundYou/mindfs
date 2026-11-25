//go:build windows

package update

import (
	"fmt"
	"os/exec"
	"strings"

	"mindfs/internal/deploy"
)

func platformRunUpdateTask() error {
	out, err := exec.Command("schtasks.exe", "/run", "/tn", deploy.SetupUpdateTaskName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks /run /tn %s failed: %v: %s", deploy.SetupUpdateTaskName, err, strings.TrimSpace(string(out)))
	}
	return nil
}
