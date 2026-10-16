package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"mindfs/server/app"
)

var version = "dev"

var errBrowserUnavailable = errors.New("browser auto-open unavailable")

const (
	daemonEnvKey          = "MINDFS_DAEMON"
	internalRestartEnvKey = "MINDFS_INTERNAL_RESTART"
	maxLogSizeBytes       = 10 * 1024 * 1024
	maxLogBackups         = 3
)

func main() {
	autoStartBoot := autoStartRequested(os.Args[1:])
	if autoStartBoot {
		if err := prepareAutoStartEnvironment(); err != nil {
			log.Printf("[mindfs/autostart] environment restore warning: %v", err)
		}
	}

	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintf(out, "Usage:\n")
		fmt.Fprintf(out, "  mindfs [flags] [root]\n\n")
		fmt.Fprintf(out, "Arguments:\n")
		fmt.Fprintf(out, "  root    Directory to manage. If omitted, MindFS opens without adding a directory.\n\n")
		fmt.Fprintf(out, "Flags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(out, "\nExamples:\n")
		fmt.Fprintf(out, "  mindfs\n")
		fmt.Fprintf(out, "  mindfs /path/to/project\n")
		fmt.Fprintf(out, "  mindfs --foreground\n")
		fmt.Fprintf(out, "  mindfs --status\n")
		fmt.Fprintf(out, "  mindfs --version\n")
		fmt.Fprintf(out, "  mindfs --update\n")
		fmt.Fprintf(out, "  mindfs --uninstall\n")
		fmt.Fprintf(out, "  mindfs --stop\n")
		fmt.Fprintf(out, "  mindfs -addr :9000 /path/to/project\n")
		fmt.Fprintf(out, "  mindfs -remove /path/to/project\n")
		fmt.Fprintf(out, "  mindfs <rootid> -task 12\n")
		fmt.Fprintf(out, "  mindfs <rootid> -task 12 -next\n")
	}

	addr := flag.String("addr", "127.0.0.1:7331", "listen address")
	webPushFlag := flag.Bool("web-push", true, "enable PWA Web Push notifications")
	foreground := flag.Bool("foreground", false, "run in the foreground instead of as a background service")
	autoStart := flag.Bool("autostart", false, "register or refresh automatic startup for this user")
	internalAutoStartFlag := flag.Bool("internal-autostart", false, "internal flag used by the automatic startup entry")
	stop := flag.Bool("stop", false, "stop the background mindfs service")
	restart := flag.Bool("restart", false, "restart the background mindfs service")
	statusFlag := flag.Bool("status", false, "show background service status")
	versionFlag := flag.Bool("version", false, "show version")
	updateFlag := flag.Bool("update", false, "check for and install the latest MindFS release")
	uninstallFlag := flag.Bool("uninstall", false, "print the MindFS uninstall command")
	configFlag := flag.String("config", "", "mindfs startup config file; defaults to <config-dir>/config.json when present. Command-line flags override file values")
	agentConfigFlag := flag.String("agent-config", "", "extra agents.json file for customizable agent(ACP-protocol) and shell")
	notifyScriptFlag := flag.String("notify-script", "", "executable script for notification events; receives JSON payload on stdin")
	roleFlag := flag.String("role", "", "node role: control (default, serves UI + control plane) or worker (data plane only)")
	remove := flag.Bool("remove", false, "remove the managed directory")
	taskNumber := flag.String("task", "", "task number to inspect; prints task detail (read-only)")
	tlsFlag := flag.Bool("tls", false, "enable HTTPS (auto-generates self-signed cert if -cert/-key not provided)")
	certFlag := flag.String("cert", "", "TLS certificate file (PEM); auto-generated if empty with -tls")
	keyFlag := flag.String("key", "", "TLS private key file (PEM); auto-generated if empty with -tls")
	_ = flag.CommandLine.Parse(normalizeTaskRootFirstArgs(os.Args[1:]))
	explicitFlags := visitedFlags(flag.CommandLine)
	startupCfg, err := loadStartupConfig(*configFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	applyStartupConfig(startupCfg, explicitFlags, addr, webPushFlag, foreground, tlsFlag, certFlag, keyFlag, agentConfigFlag, notifyScriptFlag, roleFlag)
	internalAutoStart := autoStartBoot || *internalAutoStartFlag
	if internalAutoStart {
		*foreground = true
		*autoStart = false
	}
	if *versionFlag {
		printVersion()
		return
	}
	if strings.TrimSpace(*taskNumber) != "" {
		rootID := ""
		if flag.NArg() > 0 {
			rootID = flag.Arg(0)
		}
		if err := handleTaskCommand(*addr, *tlsFlag, rootID, strings.TrimSpace(*taskNumber)); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		return
	}
	if *uninstallFlag {
		printUninstallCommand()
		return
	}
	internalRestart := os.Getenv(internalRestartEnvKey) == "1"
	daemonMode := os.Getenv(daemonEnvKey) == "1"
	if internalRestart {
		log.Printf("[mindfs] internal restart detected addr=%s root_arg_count=%d", *addr, flag.NArg())
	}

	hasRootArg := flag.NArg() > 0
	root := "."
	if hasRootArg {
		root = flag.Arg(0)
	}
	absRoot := ""
	stateDir, err := ensureStateDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	pidPath, logPath, err := servicePaths(stateDir, *addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	if *updateFlag {
		if err := handleUpdateCommand(context.Background(), *addr, *tlsFlag); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		return
	}

	if *statusFlag {
		if err := printServiceStatus(*addr, *tlsFlag, pidPath, logPath); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		return
	}
	if *stop {
		if err := stopService(*addr, *tlsFlag, pidPath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				fmt.Fprintln(os.Stdout, "mindfs service already stopped")
				return
			}
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, "mindfs service stopped")
		return
	}
	if *restart {
		if err := stopService(*addr, *tlsFlag, pidPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
	}

	if *remove {
		absRoot, err = filepath.Abs(root)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		if err := handleRemoveRoot(*addr, *tlsFlag, absRoot); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, "removed managed directory:", absRoot)
		return
	}

	if !daemonMode && !internalRestart && !internalAutoStart {
		autoStartExplicit := explicitFlags["autostart"]
		if *autoStart || (!autoStartExplicit && autoStartConfigured()) {
			autoArgs := autoStartArguments(
				*addr,
				*webPushFlag,
				*tlsFlag,
				*certFlag,
				*keyFlag,
				*agentConfigFlag,
				*notifyScriptFlag,
			)
			if _, err := configureAutoStart(logPath, autoArgs); err != nil {
				fmt.Fprintf(os.Stderr, "mindfs autostart warning: %v\n", err)
			}
		} else if autoStartExplicit {
			if err := removeAutoStart(); err != nil {
				fmt.Fprintf(os.Stderr, "mindfs autostart disable warning: %v\n", err)
			}
		}
	}

	if !internalRestart && !*restart && serverRunning(*addr, *tlsFlag) {
		fmt.Fprintf(os.Stdout, "server already running on %s, reusing existing process\n", *addr)
		rootID := ""
		if hasRootArg {
			absRoot, err = filepath.Abs(root)
			if err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(1)
			}
			rootInfo, err := addManagedDir(*addr, *tlsFlag, absRoot)
			if err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(1)
			}
			rootID = rootInfo.ID
			fmt.Fprintln(os.Stdout, "added managed directory:", rootInfo.RootPath)
		}
		if !internalAutoStart {
			if err := openTarget(*addr, *tlsFlag, rootID); err != nil {
				reportOpenTargetError(os.Stderr, err)
			}
		}
		return
	}

	// Resolve TLS certificate/key paths when TLS is enabled.
	resolvedCert, resolvedKey := *certFlag, *keyFlag
	if *tlsFlag && (resolvedCert == "" || resolvedKey == "") {
		resolvedCert, resolvedKey, err = app.EnsureTLSCert(*certFlag, *keyFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
	}

	if !*foreground && !daemonMode && !internalRestart {
		if err := startBackgroundProcess(logPath); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		if err := waitForServer(*addr, *tlsFlag, 8*time.Second); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		rootID := ""
		fmt.Fprintln(os.Stdout, "mindfs service started")
		if hasRootArg {
			absRoot, err = filepath.Abs(root)
			if err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(1)
			}
			rootInfo, err := addManagedDir(*addr, *tlsFlag, absRoot)
			if err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(1)
			}
			rootID = rootInfo.ID
			fmt.Fprintln(os.Stdout, "added managed directory:", rootInfo.RootPath)
		}
		if err := openTarget(*addr, *tlsFlag, rootID); err != nil {
			reportOpenTargetError(os.Stderr, err)
		}
		fmt.Fprintf(os.Stdout, "logs: %s\n", logPath)
		return
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	restoreDefaultSignalsAfterCancellation(ctx, cancel)
	defer cancel()
	if err := writePIDFile(pidPath); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	defer removePIDFile(pidPath)

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Start(ctx, *addr, app.StartOptions{
			Version:         version,
			Args:            os.Args[1:],
			AgentConfigPath: *agentConfigFlag,
			WebPushEnabled:  *webPushFlag,
			NotifyScript:    *notifyScriptFlag,
			UseTLS:          *tlsFlag,
			CertFile:        resolvedCert,
			KeyFile:         resolvedKey,
			Role:            *roleFlag,
		})
	}()
	if err := waitForServer(*addr, *tlsFlag, 8*time.Second); err != nil {
		cancel()
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	rootID := ""
	if hasRootArg {
		absRoot, err = filepath.Abs(root)
		if err != nil {
			cancel()
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		rootInfo, err := addManagedDir(*addr, *tlsFlag, absRoot)
		if err != nil {
			cancel()
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		rootID = rootInfo.ID
		fmt.Fprintln(os.Stdout, "added managed directory:", rootInfo.RootPath)
	}

	if !internalRestart && !internalAutoStart && (*foreground || !daemonMode) {
		if err := openTarget(*addr, *tlsFlag, rootID); err != nil {
			reportOpenTargetError(os.Stderr, err)
		}
	}

	runErr := waitForForegroundAppExit(ctx, errCh)
	if runErr != nil && !errors.Is(runErr, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, runErr.Error())
		os.Exit(1)
	}
}
