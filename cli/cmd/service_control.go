package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"mindfs/internal/deploy"
	"mindfs/server/app"
)

func restoreDefaultSignalsAfterCancellation(ctx context.Context, stop func()) {
	go func() {
		<-ctx.Done()
		// The first signal starts graceful cleanup. Stop intercepting signals at
		// that point so a second Ctrl-C uses the platform's default behavior.
		stop()
	}()
}

func waitForForegroundAppExit(ctx context.Context, errCh <-chan error) error {
	select {
	case <-ctx.Done():
		// Ctrl-C starts graceful server shutdown. Wait for app.Start to return so
		// Agent runtimes are closed before the foreground process exits.
		return <-errCh
	case err := <-errCh:
		return err
	}
}

func stopService(addr string, useTLS bool, pidPath string) error {
	pid, err := resolveServicePID(addr, useTLS, pidPath)
	if err != nil {
		return err
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := stopProcess(proc, pid); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			_ = os.Remove(pidPath)
			return nil
		}
		return err
	}
	for i := 0; i < 50; i++ {
		if !processExists(pid) {
			_ = os.Remove(pidPath)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timed out stopping process %d", pid)
}

func printVersion() {
	fmt.Fprintf(os.Stdout, "mindfs version: %s\n", version)
}

func printUninstallCommand() {
	if runtime.GOOS == "windows" {
		fmt.Fprintln(os.Stdout, "Run one of the following commands manually:")
		fmt.Fprintln(os.Stdout, "")
		fmt.Fprintln(os.Stdout, "Uninstall MindFS, keeping user config and project data:")
		fmt.Fprintln(os.Stdout, "Download scripts/install.ps1 from https://github.com/a9gent/mindfs, then run it with -Uninstall.")
		fmt.Fprintln(os.Stdout, "")
		fmt.Fprintln(os.Stdout, "Purge MindFS, including user config and logs:")
		fmt.Fprintln(os.Stdout, "Download scripts/install.ps1 from https://github.com/a9gent/mindfs, then run it with -Uninstall -Purge.")
		return
	}
	fmt.Fprintln(os.Stdout, "Run one of the following commands manually:")
	fmt.Fprintln(os.Stdout, "")
	fmt.Fprintln(os.Stdout, "Uninstall MindFS, keeping user config and project data:")
	fmt.Fprintln(os.Stdout, `installer="${TMPDIR:-/tmp}/mindfs-install.sh"`)
	fmt.Fprintln(os.Stdout, `curl -fsSL https://raw.githubusercontent.com/a9gent/mindfs/main/scripts/install.sh -o "$installer"`)
	fmt.Fprintln(os.Stdout, `bash "$installer" --uninstall`)
	fmt.Fprintln(os.Stdout, "")
	fmt.Fprintln(os.Stdout, "Purge MindFS, including user config and logs:")
	fmt.Fprintln(os.Stdout, `installer="${TMPDIR:-/tmp}/mindfs-install.sh"`)
	fmt.Fprintln(os.Stdout, `curl -fsSL https://raw.githubusercontent.com/a9gent/mindfs/main/scripts/install.sh -o "$installer"`)
	fmt.Fprintln(os.Stdout, `bash "$installer" --uninstall --purge`)
}

func handleUpdateCommand(ctx context.Context, addr string, useTLS bool) error {
	if runtime.GOOS == "windows" && serverRunning(addr, useTLS) {
		return errors.New("mindfs service is running. Stop it before updating:\n  mindfs -stop\n  mindfs -update\n  mindfs")
	}
	fmt.Fprintln(os.Stdout, "Checking for updates...")
	result, err := app.UpdateNow(ctx, app.UpdateOptions{
		Version:  version,
		Progress: os.Stdout,
	})
	if strings.TrimSpace(result.CurrentVersion) != "" {
		fmt.Fprintf(os.Stdout, "current version: %s\n", result.CurrentVersion)
	}
	if strings.TrimSpace(result.LatestVersion) != "" {
		fmt.Fprintf(os.Stdout, "latest version: %s\n", result.LatestVersion)
	}
	if err != nil {
		return err
	}
	if !result.HasUpdate {
		fmt.Fprintln(os.Stdout, "mindfs is already up to date")
		return nil
	}
	if result.Installed {
		fmt.Fprintln(os.Stdout, "Restart MindFS to use the new version:")
		fmt.Fprintln(os.Stdout, "  mindfs -restart")
	}
	return nil
}

func printServiceStatus(addr string, useTLS bool, pidPath, logPath string) error {
	pid, err := resolveServicePID(addr, useTLS, pidPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(os.Stdout, "mindfs status: stopped")
			fmt.Fprintf(os.Stdout, "version: %s\n", version)
			return nil
		}
		return err
	}
	fmt.Fprintln(os.Stdout, "mindfs status: running")
	fmt.Fprintf(os.Stdout, "pid: %d\n", pid)
	fmt.Fprintf(os.Stdout, "addr: %s\n", addrToURL(addr, "", useTLS))
	fmt.Fprintf(os.Stdout, "log file: %s\n", logPath)
	fmt.Fprintf(os.Stdout, "version: %s\n", version)
	return nil
}

func resolveServicePID(addr string, useTLS bool, pidPath string) (int, error) {
	pid, err := readPIDFile(pidPath)
	if err == nil {
		if processExists(pid) {
			return pid, nil
		}
		_ = os.Remove(pidPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}

	if strings.TrimSpace(addr) == "" {
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
		return 0, os.ErrNotExist
	}

	pid, err = findListeningMindfsPID(addr)
	if err != nil {
		return 0, err
	}
	if pid <= 0 && !serverRunning(addr, useTLS) {
		return 0, os.ErrNotExist
	}
	if pid <= 0 {
		return 0, os.ErrNotExist
	}
	if writeErr := writePIDValue(pidPath, pid); writeErr != nil {
		return 0, writeErr
	}
	return pid, nil
}

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	return processExistsPlatform(pid)
}

func serverRunning(addr string, useTLS bool) bool {
	url := addrToURL(addr, deploy.PrefixedPath("/health"), useTLS)
	client := newHTTPClient(useTLS, 800*time.Millisecond)
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func waitForServer(addr string, useTLS bool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if serverRunning(addr, useTLS) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("server did not become ready on %s within %s", addr, timeout)
}
