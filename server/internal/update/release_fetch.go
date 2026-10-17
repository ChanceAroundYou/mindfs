package update

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

func NewService(repo, currentVersion, executable string, args []string, interval time.Duration) *Service {
	if interval <= 0 {
		interval = defaultCheckInterval
	}
	currentVersion = strings.TrimSpace(currentVersion)
	if currentVersion == "" {
		currentVersion = "dev"
	}
	s := &Service{
		repo:          strings.TrimSpace(repo),
		current:       currentVersion,
		executable:    strings.TrimSpace(executable),
		args:          append([]string(nil), args...),
		checkInterval: interval,
		client: &http.Client{
			Timeout: 10 * time.Minute,
		},
	}
	st := Status{
		CurrentVersion:      currentVersion,
		Status:              "idle",
		AutoUpdateSupported: s.canAutoUpdate(),
	}
	if !st.AutoUpdateSupported {
		st.Message = s.unsupportedMessage()
	}
	s.status = st
	return s
}

func (s *Service) Start(ctx context.Context) {
	if s == nil || strings.TrimSpace(s.repo) == "" {
		return
	}
	go func() {
		log.Printf("[update] checker.start repo=%s current=%s interval=%s auto_update_supported=%t", s.repo, s.current, s.checkInterval, s.canAutoUpdate())
		s.CheckNow(context.Background())
		ticker := time.NewTicker(s.checkInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.CheckNow(context.Background())
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (s *Service) AddListener(listener func(Status)) {
	if s == nil || listener == nil {
		return
	}
	s.mu.Lock()
	s.listeners = append(s.listeners, listener)
	s.mu.Unlock()
}

func (s *Service) GetStatus() Status {
	if s == nil {
		return Status{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

func (s *Service) CheckNow(ctx context.Context) {
	if s == nil || strings.TrimSpace(s.repo) == "" {
		return
	}
	log.Printf("[update] check.begin repo=%s current=%s", s.repo, s.current)
	release, err := s.fetchLatestRelease(ctx)
	if err != nil {
		log.Printf("[update] check.error repo=%s current=%s err=%v", s.repo, s.current, err)
		s.updateStatus(func(st *Status) {
			st.LastCheckedAt = time.Now().UTC()
			if st.Status == "downloading" || st.Status == "installing" || st.Status == "restarting" {
				return
			}
			if st.HasUpdate {
				return
			}
			st.Status = "idle"
			if !st.AutoUpdateSupported {
				st.Message = s.unsupportedMessage()
			}
		})
		return
	}
	latest := normalizeVersion(release.TagName)
	current := normalizeVersion(s.current)
	hasUpdate := isNewerVersion(latest, current)
	log.Printf("[update] check.result repo=%s current=%s latest=%s has_update=%t", s.repo, current, latest, hasUpdate)
	s.updateStatus(func(st *Status) {
		st.CurrentVersion = s.current
		st.LatestVersion = latest
		st.ReleaseName = strings.TrimSpace(release.Name)
		st.ReleaseBody = strings.TrimSpace(release.Body)
		st.ReleaseURL = strings.TrimSpace(release.HTMLURL)
		st.PublishedAt = release.PublishedAt
		st.LastCheckedAt = time.Now().UTC()
		st.HasUpdate = hasUpdate
		st.AutoUpdateSupported = s.canAutoUpdate()
		if !st.AutoUpdateSupported {
			st.Message = s.unsupportedMessage()
		} else if hasUpdate && (st.Status == "" || st.Status == "idle" || st.Status == "available") {
			st.Message = ""
		}
		if st.Status == "downloading" || st.Status == "installing" || st.Status == "restarting" {
			return
		}
		if hasUpdate {
			st.Status = "available"
			return
		}
		st.Status = "idle"
		if st.AutoUpdateSupported {
			st.Message = ""
		}
	})
}

func (s *Service) TriggerUpdate(ctx context.Context) error {
	if s == nil {
		return errors.New("update service not configured")
	}
	st := s.GetStatus()
	if !st.AutoUpdateSupported {
		return errors.New(firstNonEmpty(st.Message, "auto update is not supported in this install mode"))
	}
	if !st.HasUpdate || strings.TrimSpace(st.LatestVersion) == "" {
		return errors.New("no update available")
	}
	if st.Status == "downloading" || st.Status == "installing" || st.Status == "restarting" {
		return errors.New("update already in progress")
	}

	s.updateStatus(func(st *Status) {
		st.Status = "downloading"
		st.Message = "Downloading update..."
	})

	go s.runUpdate(context.WithoutCancel(ctx), st.LatestVersion)
	return nil
}

func (s *Service) InstallLatest(ctx context.Context) (Status, error) {
	if s == nil {
		return Status{}, errors.New("update service not configured")
	}
	release, err := s.fetchLatestRelease(ctx)
	if err != nil {
		s.updateStatus(func(st *Status) {
			st.LastCheckedAt = time.Now().UTC()
			st.Status = "failed"
			st.Message = err.Error()
		})
		return s.GetStatus(), err
	}
	latest := normalizeVersion(release.TagName)
	current := normalizeVersion(s.current)
	hasUpdate := isNewerVersion(latest, current)
	s.updateStatus(func(st *Status) {
		st.CurrentVersion = s.current
		st.LatestVersion = latest
		st.ReleaseName = strings.TrimSpace(release.Name)
		st.ReleaseBody = strings.TrimSpace(release.Body)
		st.ReleaseURL = strings.TrimSpace(release.HTMLURL)
		st.PublishedAt = release.PublishedAt
		st.LastCheckedAt = time.Now().UTC()
		st.HasUpdate = hasUpdate
		st.AutoUpdateSupported = s.canAutoUpdate()
		if !st.AutoUpdateSupported {
			st.Status = "idle"
			st.Message = s.unsupportedMessage()
			return
		}
		st.Message = ""
		if hasUpdate {
			st.Status = "available"
			return
		}
		st.Status = "idle"
	})

	st := s.GetStatus()
	if !st.AutoUpdateSupported {
		return st, errors.New(firstNonEmpty(st.Message, "auto update is not supported in this install mode"))
	}
	if !st.HasUpdate || strings.TrimSpace(st.LatestVersion) == "" {
		return st, nil
	}
	if st.Status == "downloading" || st.Status == "installing" || st.Status == "restarting" {
		return st, errors.New("update already in progress")
	}

	s.updateStatus(func(st *Status) {
		st.Status = "downloading"
		st.Message = "Downloading update..."
	})
	if err := s.installUpdate(ctx, st.LatestVersion, false); err != nil {
		s.fail(err)
		return s.GetStatus(), err
	}
	s.updateStatus(func(st *Status) {
		st.Status = "installed"
		st.Message = "Update installed. Restart MindFS to use the new version."
	})
	return s.GetStatus(), nil
}

func (s *Service) runUpdate(ctx context.Context, version string) {
	if err := s.installUpdate(ctx, version, true); err != nil {
		s.fail(err)
	}
}

func (s *Service) fail(err error) {
	msg := "update failed"
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		msg = err.Error()
	}
	log.Printf("[update] run.error current=%s latest=%s err=%s", s.current, s.GetStatus().LatestVersion, msg)
	s.updateStatus(func(st *Status) {
		st.Status = "failed"
		st.Message = msg
	})
}

func (s *Service) updateStatus(apply func(*Status)) {
	s.mu.Lock()
	st := s.status
	apply(&st)
	s.status = st
	listeners := append([]func(Status){}, s.listeners...)
	s.mu.Unlock()
	for _, listener := range listeners {
		listener(st)
	}
}
