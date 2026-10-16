package update

import (
	"errors"
	"net/http"
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
