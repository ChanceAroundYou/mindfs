package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func openTarget(addr string, useTLS bool, rootID string) error {
	return openBrowser(localOpenURL(addr, useTLS, rootID))
}

func reportOpenTargetError(w io.Writer, err error) {
	if err == nil || w == nil {
		return
	}
	if errors.Is(err, errBrowserUnavailable) {
		fmt.Fprintln(w, err.Error())
		return
	}
	fmt.Fprintln(w, err.Error())
}

func localOpenURL(addr string, useTLS bool, rootID string) string {
	base := addrToURL(addr, "", useTLS)
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	if strings.TrimSpace(rootID) != "" {
		q.Set("root", rootID)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func openBrowser(target string) error {
	if strings.TrimSpace(target) == "" {
		return nil
	}
	if runtime.GOOS == "linux" && strings.TrimSpace(os.Getenv("DISPLAY")) == "" && strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) == "" {
		return fmt.Errorf("%w; open this URL manually: %s", errBrowserUnavailable, target)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	configureBrowserCommand(cmd)
	return cmd.Start()
}

func addrToURL(addr, path string, useTLS bool) string {
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return strings.TrimRight(addr, "/") + path
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host = "localhost"
		port = strings.TrimPrefix(addr, ":")
	}
	if host == "" {
		host = "localhost"
	}
	// "::" 和 "0.0.0.0" 都是监听通配地址，不能直接拿去连：前者拼出来是
	// https://:::7331 这种非法 URL，后者在本机以外的语境下也不可达。
	if host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	if port == "" {
		port = "7331"
	}
	scheme := "http"
	if useTLS {
		scheme = "https"
	}
	// JoinHostPort 而不是 "%s:%s"：IPv6 字面量必须加方括号，
	// 否则拼出的是 https://::1:7331 这种非法 URL。
	return fmt.Sprintf("%s://%s%s", scheme, net.JoinHostPort(host, port), path)
}
