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
	"runtime"
	"strings"
	"syscall"
	"time"

	"mindfs/internal/brand"
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
	if handled, err := runAgentConfigSyncCommand(os.Args[1:]); handled {
		if err != nil {
			os.Exit(1)
		}
		return
	}
	// 安装向导的两个纯命令行入口。必须在 attachParentConsole 之前：GUI 子系统下
	// 这里不该弹控制台，结果走 --out 文件与退出码。
	if handled, err := runNodeNameCommand(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if handled, err := runInitConfigCommand(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if handled, err := runServiceTaskCommand(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	attachParentConsole()
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
		fmt.Fprintf(out, "  root    Directory to manage. If omitted, %s opens without adding a directory.\n\n", brand.Name())
		fmt.Fprintf(out, "Flags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(out, "\nExamples:\n")
		fmt.Fprintf(out, "  mindfs\n")
		fmt.Fprintf(out, "  mindfs /path/to/project\n")
		fmt.Fprintf(out, "  mindfs --foreground\n")
		fmt.Fprintf(out, "  mindfs --desktop\n")
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
	desktopFlag := flag.Bool("desktop", false, "open the desktop window (the default when started without arguments on Windows)")
	serveFlag := flag.Bool("serve", false, "run the HTTP service in this process (used by the desktop shell and the scheduled task)")
	autoStart := flag.Bool("autostart", false, "register or refresh automatic startup for this user")
	internalAutoStartFlag := flag.Bool("internal-autostart", false, "internal flag used by the automatic startup entry")
	stop := flag.Bool("stop", false, "stop the background mindfs service")
	restart := flag.Bool("restart", false, "restart the background mindfs service")
	statusFlag := flag.Bool("status", false, "show background service status")
	versionFlag := flag.Bool("version", false, "show version")
	updateFlag := flag.Bool("update", false, "check for and install the latest "+brand.Name()+" release")
	uninstallFlag := flag.Bool("uninstall", false, "print the "+brand.Name()+" uninstall command")
	configFlag := flag.String("config", "", "mindfs startup config file; defaults to <config-dir>/config.json when present. Command-line flags override file values")
	agentConfigFlag := flag.String("agent-config", "", "extra agents.json file for customizable agent(ACP-protocol) and shell")
	notifyScriptFlag := flag.String("notify-script", "", "executable script for notification events; receives JSON payload on stdin")
	roleFlag := flag.String("role", "", "node role: control (default, serves UI + control plane) or worker (data plane only)")
	routerModeFlag := flag.String("router-mode", "", "task router mode: off (default), shadow, route, coordinate")
	routerAgentFlag := flag.String("router-agent", "", "task router agent name (default claude)")
	routerModelFlag := flag.String("router-model", "", "task router model; empty = agent default")
	routerEffortFlag := flag.String("router-effort", "", "task router effort level (default low)")
	routerTurnTimeoutFlag := flag.Duration("router-turn-timeout", 0, "task router turn timeout, e.g. 90s (default 90s)")
	routerIdleCloseFlag := flag.Duration("router-idle-close", 0, "close idle router sessions after this duration, e.g. 30m (default 30m)")
	workflowParallelMainTreeFlag := flag.Bool("workflow-parallel-main-tree", true, "run tasks without independent worktrees in parallel on the shared main tree")
	liveCompactModeFlag := flag.String("live-compact-mode", "", "context live-compact mode: off (default), shadow (measure only), on")
	liveCompactKeepRoundsFlag := flag.Int("live-compact-keep-rounds", 0, "newest completed tool rounds kept verbatim (default 3)")
	liveCompactKeepTailTokens := 0
	liveCompactMinBlockTokensFlag := flag.Int("live-compact-min-block-tokens", 0, "minimum estimated tokens for a tool result to be folded (default 2000)")
	liveCompactMinBodyBytes := 0
	liveCompactMaxDriftMessages := 0
	liveCompactRailTokens := 0
	liveCompactMinFoldBytes := 0
	liveCompactMaxFoldBytes := 0
	liveCompactLargeBlockTokens := 0
	// 前缀闸门与冷缓存择时没有 CLI flag：只由 config.json 的 live_compact 段填充。
	// 初值就是默认值（都开），配置文件存在时覆盖它。
	liveCompactPrefixGuard := true
	var liveCompactPrefixUnsafeModels []string
	liveCompactColdFold := true
	liveCompactColdRailTokens := 0
	liveCompactCacheTTLSeconds := 0
	// 只由 config.json 填充；没有对应 flag（见 startupFlags.WebPushRelayURL）。
	webPushRelayURL := ""
	var feishuBindings []app.FeishuBinding
	feishuRootID, feishuToken, feishuRelayURL, feishuNodeID := "", "", "", ""
	feishuTTLHours, feishuPickerMax, feishuPerMinute, feishuBurst := 0, 0, 0, 0
	// 任务派发默认 Agent/模型，同样只走 config.json（task 段）。
	taskAgent, taskModel := "", ""
	// 主动监管开关与预算，只走 config.json（router.supervision 段）。
	routerSupervisionMode, routerSupervisionRetries, routerSupervisionRunActions, routerSupervisionActions, routerSupervisionQuotaHold := "", 0, 0, 0, 0
	// 飞书通道，只走 config.json（channels.feishu 段）。缺省全关。
	feishuEnabled, feishuOwnerOpenID, feishuLarkCLI, feishuCardTTLHours := false, "", "", 0
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
	if err := applyStartupConfig(startupCfg, explicitFlags, startupFlags{
		Addr:                          addr,
		WebPush:                       webPushFlag,
		Foreground:                    foreground,
		TLS:                           tlsFlag,
		Cert:                          certFlag,
		Key:                           keyFlag,
		AgentConfig:                   agentConfigFlag,
		NotifyScript:                  notifyScriptFlag,
		Role:                          roleFlag,
		RouterMode:                    routerModeFlag,
		RouterAgent:                   routerAgentFlag,
		RouterModel:                   routerModelFlag,
		RouterEffort:                  routerEffortFlag,
		RouterTurnTimeout:             routerTurnTimeoutFlag,
		RouterIdleClose:               routerIdleCloseFlag,
		WorkflowParallelMainTree:      workflowParallelMainTreeFlag,
		LiveCompactMode:               liveCompactModeFlag,
		LiveCompactKeepRounds:         liveCompactKeepRoundsFlag,
		LiveCompactKeepTailTokens:     &liveCompactKeepTailTokens,
		LiveCompactMinBlockTokens:     liveCompactMinBlockTokensFlag,
		LiveCompactMinBodyBytes:       &liveCompactMinBodyBytes,
		LiveCompactMaxDriftMessages:   &liveCompactMaxDriftMessages,
		LiveCompactRailTokens:         &liveCompactRailTokens,
		LiveCompactMinFoldBytes:       &liveCompactMinFoldBytes,
		LiveCompactMaxFoldBytes:       &liveCompactMaxFoldBytes,
		LiveCompactLargeBlockTokens:   &liveCompactLargeBlockTokens,
		LiveCompactPrefixGuard:        &liveCompactPrefixGuard,
		LiveCompactPrefixUnsafeModels: &liveCompactPrefixUnsafeModels,
		LiveCompactColdFold:           &liveCompactColdFold,
		LiveCompactColdRailTokens:     &liveCompactColdRailTokens,
		LiveCompactCacheTTLSeconds:    &liveCompactCacheTTLSeconds,
		WebPushRelayURL:               &webPushRelayURL,
		FeishuBindings:                &feishuBindings,
		FeishuDefaultRootID:           &feishuRootID,
		FeishuPickerTTLHours:          &feishuTTLHours,
		FeishuPickerMax:               &feishuPickerMax,
		FeishuInboundPerMinute:        &feishuPerMinute,
		FeishuInboundBurst:            &feishuBurst,
		FeishuRelayToken:              &feishuToken,
		FeishuRelayURL:                &feishuRelayURL,
		FeishuNodeID:                  &feishuNodeID,
		TaskAgent:                     &taskAgent,
		TaskModel:                     &taskModel,
		FeishuEnabled:                 &feishuEnabled,
		FeishuOwnerOpenID:             &feishuOwnerOpenID,
		FeishuLarkCLI:                 &feishuLarkCLI,
		FeishuCardTTLHours:            &feishuCardTTLHours,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	routerSupervisionMode, routerSupervisionRetries, routerSupervisionRunActions, routerSupervisionActions, routerSupervisionQuotaHold = routerSupervision(startupCfg)
	internalAutoStart := autoStartBoot || *internalAutoStartFlag
	if internalAutoStart {
		*foreground = true
		*autoStart = false
	}
	if *serveFlag {
		*foreground = true
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
	// 后台实例没有终端可看（Windows 上连黑框都没有了），日志落盘，和 systemd/launchd 一致。
	if internalAutoStart {
		redirectBackgroundLogs(logPath)
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

	// 套壳模式：Windows 下双击（无参数）/ 显式 --desktop 都走这里。
	// 显式带其它 flag 或位置参数时保持旧行为（起服务 + 浏览器），脚本与老快捷方式不受影响。
	desktopMode := desktopEntry(runtime.GOOS, len(os.Args), *desktopFlag, *serveFlag, *foreground,
		internalAutoStart, internalRestart, daemonMode)
	if desktopMode {
		_, nodeName := nodeIdentity(startupCfg)
		if strings.TrimSpace(nodeName) == "" {
			nodeName, _ = os.Hostname()
		}
		if err := runDesktopShell(desktopShellConfig{
			Addr:     *addr,
			UseTLS:   *tlsFlag,
			NodeName: nodeName,
			StateDir: stateDir,
			LogPath:  logPath,
		}); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		return
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

	// 控制面同步配置在起服务之前解析：token 读不到就别起，而不是等健康检查
	// 超时后只留一句「服务没起来」。角色此时已由 applyStartupConfig 填好 ——
	// 默认开启只对控制面节点成立，worker 上整段跳过。
	controlSyncCfg, err := controlSyncOptions(startupCfg, *roleFlag, effectiveConfigPath(*configFlag))
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	// 本机身份：显式 node.id/name 原样交给 app，缺省规则（主控 home/主控，
	// 边缘取 control_sync.instance_id，worker 取主机名）与 mindfs-server 共用一份。
	nodeID, nodeName := nodeIdentity(startupCfg)

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
			Version:                            version,
			Args:                               os.Args[1:],
			NodeID:                             nodeID,
			NodeName:                           nodeName,
			AgentConfigPath:                    *agentConfigFlag,
			WebPushEnabled:                     *webPushFlag,
			WebPushRelayURL:                    webPushRelayURL,
			FeishuRelayURL:                     feishuRelayURL,
			FeishuNodeID:                       feishuNodeID,
			NotifyScript:                       *notifyScriptFlag,
			UseTLS:                             *tlsFlag,
			CertFile:                           resolvedCert,
			KeyFile:                            resolvedKey,
			Role:                               *roleFlag,
			RouterMode:                         *routerModeFlag,
			RouterAgent:                        *routerAgentFlag,
			RouterModel:                        *routerModelFlag,
			RouterEffort:                       *routerEffortFlag,
			RouterTurnTimeout:                  *routerTurnTimeoutFlag,
			RouterIdleClose:                    *routerIdleCloseFlag,
			RouterRoots:                        routerRoots(startupCfg),
			RouterSupervisionMode:              routerSupervisionMode,
			RouterSupervisionMaxRetriesPerNode: routerSupervisionRetries,
			RouterSupervisionMaxActionsPerRun:  routerSupervisionRunActions,
			RouterSupervisionMaxActionsPerHour: routerSupervisionActions,
			RouterSupervisionQuotaHoldSeconds:  routerSupervisionQuotaHold,
			TaskAgent:                          taskAgent,
			TaskModel:                          taskModel,
			WorkflowParallelMainTree:           *workflowParallelMainTreeFlag,
			LiveCompactMode:                    *liveCompactModeFlag,
			LiveCompactHarnesses:               liveCompactHarnessOverrides(startupCfg),
			LiveCompactKeepRounds:              *liveCompactKeepRoundsFlag,
			LiveCompactKeepTailTokens:          liveCompactKeepTailTokens,
			LiveCompactMinBlockTokens:          *liveCompactMinBlockTokensFlag,
			LiveCompactMinBodyBytes:            liveCompactMinBodyBytes,
			LiveCompactMaxDriftMessages:        liveCompactMaxDriftMessages,
			LiveCompactRailTokens:              liveCompactRailTokens,
			LiveCompactMinFoldBytes:            liveCompactMinFoldBytes,
			LiveCompactMaxFoldBytes:            liveCompactMaxFoldBytes,
			LiveCompactLargeBlockTokens:        liveCompactLargeBlockTokens,
			LiveCompactPrefixGuard:             &liveCompactPrefixGuard,
			LiveCompactPrefixUnsafeModels:      liveCompactPrefixUnsafeModels,
			LiveCompactColdFold:                &liveCompactColdFold,
			LiveCompactColdRailTokens:          liveCompactColdRailTokens,
			LiveCompactCacheTTLSeconds:         liveCompactCacheTTLSeconds,
			ControlSync:                        controlSyncCfg,
			SessionHygiene:                     sessionHygieneOptions(startupCfg),
			Worktree:                           worktreeOptions(startupCfg),
			SessionMaintenance:                 sessionMaintenanceOptions(startupCfg),
			UpdateAutoInstall:                  updateAutoInstall(startupCfg),
			Feishu: feishuOptions(startupFlags{
				FeishuEnabled:          &feishuEnabled,
				FeishuOwnerOpenID:      &feishuOwnerOpenID,
				FeishuLarkCLI:          &feishuLarkCLI,
				FeishuCardTTLHours:     &feishuCardTTLHours,
				FeishuBindings:         &feishuBindings,
				FeishuDefaultRootID:    &feishuRootID,
				FeishuPickerTTLHours:   &feishuTTLHours,
				FeishuPickerMax:        &feishuPickerMax,
				FeishuInboundPerMinute: &feishuPerMinute,
				FeishuInboundBurst:     &feishuBurst,
				FeishuRelayToken:       &feishuToken,
			}),
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
