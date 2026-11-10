package codexsync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type profileLinkState struct {
	SharedSHA256 string `json:"shared_sha256"`
}

func ensureLLMuxProfile(paths Paths) error {
	return ensureLLMuxProfileWith(paths, os.Symlink)
}

func ApplySharedCodexProfile(name string) error {
	paths, err := ResolvePaths()
	if err != nil {
		return err
	}
	return ApplySharedCodexProfileAt(paths, name)
}

func ApplySharedCodexProfileAt(paths Paths, name string) error {
	return WithSyncLock(paths, func() error {
		return applySharedCodexProfileAtLocked(paths, name)
	})
}

func applySharedCodexProfileAtLocked(paths Paths, name string) error {
	name, err := normalizeProfileName(name)
	if err != nil {
		return errors.New("unknown shared Codex profile")
	}
	switch name {
	case "default":
		payload, err := ReadSharedCodexProfileAt(paths, "default")
		if err != nil {
			return err
		}
		if payload == nil {
			return errors.New("shared default Codex profile is missing")
		}
	case "llmux":
		payload, err := ReadSharedCodexProfileAt(paths, "llmux")
		if err != nil {
			return err
		}
		if payload == nil {
			return errors.New("shared llmux Codex profile is missing")
		}
		if err := ensureLLMuxProfile(paths); err != nil {
			return err
		}
	}
	previous, hadPrevious, err := readActiveProfile(paths)
	if err != nil {
		return err
	}
	if err := setActiveProfile(paths, name); err != nil {
		return err
	}
	if err := renderRuntime(paths); err != nil {
		if hadPrevious {
			if restoreErr := setActiveProfile(paths, previous); restoreErr != nil {
				return errors.Join(err, fmt.Errorf("restore previous active profile: %w", restoreErr))
			}
		} else {
			if restoreErr := os.Remove(activeProfilePath(paths)); restoreErr != nil && !errors.Is(restoreErr, os.ErrNotExist) {
				return errors.Join(err, fmt.Errorf("remove failed active profile marker: %w", restoreErr))
			}
		}
		return err
	}
	return nil
}

func ReadSharedCodexProfileAt(paths Paths, name string) ([]byte, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	path := ""
	switch name {
	case "default":
		path = paths.SharedDefault
	case "llmux":
		path = paths.SharedLLMux
	default:
		return nil, errors.New("unknown shared Codex profile")
	}
	payload, err := readOptional(path)
	if err != nil || payload == nil {
		return payload, err
	}
	if err := ValidateSharedProfile(name, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func ensureLLMuxProfileWith(paths Paths, symlink func(string, string) error) error {
	return ensureLLMuxProfileWithFuncs(paths, filepath.Rel, symlink)
}

func ensureLLMuxProfileWithFuncs(paths Paths, relative func(string, string) (string, error), symlink func(string, string) error) error {
	if err := prepareLocalProfileEdit(paths); err != nil {
		return err
	}
	shared, err := readOptional(paths.SharedLLMux)
	if err != nil || shared == nil {
		return err
	}
	if err := ValidateSharedProfile("llmux", shared); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.CodexHome, 0o700); err != nil {
		return err
	}
	relativeTarget, relativeErr := relative(paths.CodexHome, paths.SharedLLMux)
	if info, statErr := os.Lstat(paths.RuntimeLLMux); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			current, linkErr := os.Readlink(paths.RuntimeLLMux)
			if linkErr != nil {
				return linkErr
			}
			if relativeErr == nil && filepath.Clean(current) == filepath.Clean(relativeTarget) {
				return removeProfileLinkState(paths)
			}
			return errors.New("local llmux profile path is a symlink to another target; preserved")
		} else if !info.Mode().IsRegular() {
			return errors.New("local llmux profile path is not a regular file or symlink")
		} else if local, readErr := os.ReadFile(paths.RuntimeLLMux); readErr == nil && hashBytes(local) == hashBytes(shared) {
			return writeProfileLinkState(paths, shared)
		} else if state, stateErr := readProfileLinkState(paths); stateErr != nil {
			return stateErr
		} else if state == nil {
			return errors.New("conflict: existing local llmux profile has no shared baseline; preserved it for review")
		}
		if err := saveRollbackCopy(paths.StateDir, paths.RuntimeLLMux); err != nil {
			return err
		}
		if err := os.Remove(paths.RuntimeLLMux); err != nil {
			return err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	if relativeErr == nil && symlink != nil {
		if err := symlink(relativeTarget, paths.RuntimeLLMux); err == nil {
			return removeProfileLinkState(paths)
		}
	}
	if err := atomicWriteFile(paths.RuntimeLLMux, shared, 0o600); err != nil {
		return fmt.Errorf("materialize local llmux profile: %w", err)
	}
	return writeProfileLinkState(paths, shared)
}

func prepareLocalProfileEdit(paths Paths) error {
	state, err := readProfileLinkState(paths)
	if err != nil || state == nil {
		return err
	}
	info, err := os.Lstat(paths.RuntimeLLMux)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	local, err := os.ReadFile(paths.RuntimeLLMux)
	if err != nil {
		return err
	}
	if hashBytes(local) == state.SharedSHA256 {
		return nil
	}
	shared, err := readOptional(paths.SharedLLMux)
	if err != nil {
		return err
	}
	if shared == nil || hashBytes(shared) != state.SharedSHA256 {
		if shared != nil && hashBytes(shared) == hashBytes(local) {
			return writeProfileLinkState(paths, shared)
		}
		return errors.New("local and shared llmux profile conflict")
	}
	if err := ValidateSharedProfile("llmux", local); err != nil {
		return fmt.Errorf("edited local llmux profile is invalid: %w", err)
	}
	if err := saveRollbackCopy(paths.StateDir, paths.SharedLLMux); err != nil {
		return err
	}
	if err := atomicWriteFile(paths.SharedLLMux, local, 0o644); err != nil {
		return err
	}
	return writeProfileLinkState(paths, local)
}

func writeProfileLinkState(paths Paths, shared []byte) error {
	statePath := profileLinkStatePath(paths)
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		return err
	}
	payload, err := json.Marshal(profileLinkState{SharedSHA256: hashBytes(shared)})
	if err != nil {
		return err
	}
	return atomicWriteFile(statePath, append(payload, '\n'), 0o600)
}

func readProfileLinkState(paths Paths) (*profileLinkState, error) {
	payload, err := readOptional(profileLinkStatePath(paths))
	if err != nil || payload == nil {
		return nil, err
	}
	var state profileLinkState
	if err := json.Unmarshal(payload, &state); err != nil {
		return nil, errors.New("local llmux profile sync state is invalid")
	}
	if strings.TrimSpace(state.SharedSHA256) == "" {
		return nil, errors.New("local llmux profile sync state is incomplete")
	}
	return &state, nil
}

func removeProfileLinkState(paths Paths) error {
	err := os.Remove(profileLinkStatePath(paths))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func profileLinkStatePath(paths Paths) string {
	return filepath.Join(paths.StateDir, "llmux-profile.json")
}

func hashBytes(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
