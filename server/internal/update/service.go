package update

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var releaseManifestPublicKey string

type Status struct {
	CurrentVersion      string    `json:"current_version"`
	LatestVersion       string    `json:"latest_version,omitempty"`
	HasUpdate           bool      `json:"has_update"`
	Status              string    `json:"status"`
	Message             string    `json:"message,omitempty"`
	ReleaseName         string    `json:"release_name,omitempty"`
	ReleaseBody         string    `json:"release_body,omitempty"`
	ReleaseURL          string    `json:"release_url,omitempty"`
	PublishedAt         time.Time `json:"published_at,omitempty"`
	LastCheckedAt       time.Time `json:"last_checked_at,omitempty"`
	AutoUpdateSupported bool      `json:"auto_update_supported"`
}

type Service struct {
	repo          string
	current       string
	executable    string
	args          []string
	checkInterval time.Duration
	client        *http.Client

	mu        sync.RWMutex
	status    Status
	listeners []func(Status)

	// 静默自动更新（见 auto_install.go）：默认关闭，由 Windows 安装包显式打开。
	autoInstall bool
	busyProbe   func() bool
	// checkNow / trigger 是测试接缝：nil 表示走真的 CheckNow / TriggerUpdate。
	// 判据的每条分支都值一次真实事故，不能只靠「读代码看着对」。
	checkNow func(ctx context.Context)
	trigger  func(ctx context.Context) error

	// exitProcess / exitDelay 同样是测试接缝：安装包更新会把进程交给安装包，
	// 真退出前留一点时间把状态和响应发出去。测试里换成一个 channel，
	// 否则 os.Exit 会把整个测试进程带走。
	exitProcess func(int)
	exitDelay   time.Duration
}

func (s *Service) exit(code int) {
	if s.exitProcess != nil {
		s.exitProcess(code)
		return
	}
	os.Exit(code)
}

func (s *Service) installLayout() (installLayout, error) {
	exe := strings.TrimSpace(s.executable)
	if exe == "" {
		return installLayout{}, errors.New("executable path required")
	}
	base := strings.ToLower(filepath.Base(exe))
	if base != "mindfs" && base != "mindfs.exe" {
		return installLayout{}, errors.New("unsupported executable name")
	}
	exeDir := filepath.Dir(exe)
	if strings.ToLower(filepath.Base(exeDir)) == "bin" {
		return installLayout{
			Mode:   "installed",
			Prefix: filepath.Dir(exeDir),
			ExeDir: exeDir,
		}, nil
	}
	return installLayout{
		Mode:   "portable",
		ExeDir: exeDir,
	}, nil
}
