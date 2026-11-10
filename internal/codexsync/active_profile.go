package codexsync

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const activeProfileMarker = "active-profile"

func ActiveProfile() (string, error) {
	paths, err := ResolvePaths()
	if err != nil {
		return "", err
	}
	return ActiveProfileAt(paths)
}

func ActiveProfileAt(paths Paths) (string, error) {
	name, exists, err := readActiveProfile(paths)
	if err != nil {
		return "", err
	}
	if !exists {
		return "default", nil
	}
	return name, nil
}

func InitializeActiveProfile(name string) (string, error) {
	paths, err := ResolvePaths()
	if err != nil {
		return "", err
	}
	return InitializeActiveProfileAt(paths, name)
}

func InitializeActiveProfileAt(paths Paths, name string) (string, error) {
	name, err := normalizeProfileName(name)
	if err != nil {
		return "", err
	}
	return initializeActiveProfileAt(paths, name)
}

func InitializeActiveProfileFromRuntimeAt(paths Paths) (string, error) {
	return initializeActiveProfileAt(paths, "")
}

func initializeActiveProfileAt(paths Paths, name string) (string, error) {
	var active string
	err := WithSyncLock(paths, func() error {
		current, exists, err := readActiveProfile(paths)
		if err != nil {
			return err
		}
		if exists {
			active = current
			return nil
		}
		if name == "" {
			name, err = activeProfileForRender(paths, false)
			if err != nil {
				return err
			}
		}
		if err := writeActiveProfile(paths, name); err != nil {
			return err
		}
		active = name
		return nil
	})
	return active, err
}

func normalizeProfileName(name string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "default":
		return "default", nil
	case "llmux":
		return "llmux", nil
	default:
		return "", errors.New("Codex profile must be default or llmux")
	}
}

func activeProfilePath(paths Paths) string {
	return filepath.Join(paths.StateDir, activeProfileMarker)
}

func readActiveProfile(paths Paths) (string, bool, error) {
	payload, err := os.ReadFile(activeProfilePath(paths))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	name, err := normalizeProfileName(string(payload))
	if err != nil {
		return "", true, fmt.Errorf("invalid local Codex profile marker: %w", err)
	}
	return name, true, nil
}

func writeActiveProfile(paths Paths, name string) error {
	name, err := normalizeProfileName(name)
	if err != nil {
		return err
	}
	return atomicWriteFile(activeProfilePath(paths), []byte(name+"\n"), 0o600)
}

func activeProfileForRender(paths Paths, persist bool) (string, error) {
	name, exists, err := readActiveProfile(paths)
	if err != nil {
		return "", err
	}
	if exists {
		return name, nil
	}
	name = "default"
	current, err := readOptional(paths.RuntimeConfig)
	if err != nil {
		return "", err
	}
	if len(current) > 0 {
		root, err := decodeTOML(current)
		if err != nil {
			return "", fmt.Errorf("local Codex config is invalid: %w", err)
		}
		if root["model_provider"] == "llmux" {
			name = "llmux"
		}
	}
	if persist {
		if err := writeActiveProfile(paths, name); err != nil {
			return "", err
		}
	}
	return name, nil
}

func setActiveProfile(paths Paths, name string) error {
	return writeActiveProfile(paths, name)
}
