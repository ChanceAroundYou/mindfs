package codexsync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func RunAt(ctx context.Context, mode Mode, paths Paths) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validatePaths(paths); err != nil {
		return Result{State: "unavailable", Message: "local configuration paths are incomplete"}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	switch mode {
	case ModeCheck:
		result, err := check(paths)
		result.LastAttemptAt = now
		return result, err
	case ModeCheckRuntime:
		if err := CheckRuntimeAt(paths); err != nil {
			return Result{State: "pending", LastAttemptAt: now, Message: "local Codex runtime config differs from the active shared profile"}, err
		}
		return Result{State: "synced", LastAttemptAt: now, Message: "local Codex runtime config matches the active shared profile"}, nil
	case ModeRender:
		release, err := acquireLock(paths.StateDir)
		if err != nil {
			return Result{State: "pending", LastAttemptAt: now, Message: "another Codex configuration sync is already running"}, err
		}
		defer release()
		if err := renderRuntimeRequired(paths); err != nil {
			return Result{State: "invalid", LastAttemptAt: now, Message: "local Codex config could not be rendered; the previous runtime file was preserved"}, err
		}
		return Result{State: "synced", LastAttemptAt: now, Message: "local Codex runtime config rendered from its active profile"}, nil
	case ModeApply, ModeSync:
		release, err := acquireLock(paths.StateDir)
		if err != nil {
			return Result{State: "pending", LastAttemptAt: now, Message: "another Codex configuration sync is already running"}, err
		}
		defer release()
		if err := ensureRepository(paths.AgentsRoot); err != nil {
			return Result{State: "unavailable", LastAttemptAt: now, Message: ".agents Git checkout is unavailable"}, err
		}
		if mode == ModeApply {
			if err := prepareLocalProfileEdit(paths); err != nil {
				return Result{State: "conflict", LastAttemptAt: now, Message: "local and shared llmux profiles changed; both copies were preserved"}, err
			}
			if err := migrate(paths); err != nil {
				state := "invalid"
				if strings.Contains(err.Error(), "conflict") {
					state = "conflict"
				}
				return Result{State: state, LastAttemptAt: now, Message: safeMigrationMessage(err)}, err
			}
			if err := renderRuntime(paths); err != nil {
				return Result{State: "invalid", LastAttemptAt: now, Message: "local Codex config could not be rendered; the previous runtime file was preserved"}, err
			}
			if err := ensureLLMuxProfile(paths); err != nil {
				state := "pending"
				message := "shared llmux profile was prepared, but the local profile link could not be installed"
				if strings.Contains(strings.ToLower(err.Error()), "conflict") {
					state = "conflict"
					message = "an existing local llmux profile differs from shared settings; both copies were preserved"
				}
				return Result{State: state, LastAttemptAt: now, Message: message}, err
			}
			return Result{State: "pending", LastAttemptAt: now, Message: "local migration applied; run sync to publish shared settings"}, nil
		}
		if err := prepareLocalProfileEdit(paths); err != nil {
			return Result{State: "conflict", LastAttemptAt: now, Message: "local and shared llmux profiles changed; both copies were preserved"}, err
		}
		if err := validateSharedSources(paths); err != nil {
			return Result{State: "invalid", LastAttemptAt: now, Message: "shared configuration failed validation; runtime files were not changed"}, err
		}
		result, syncErr := syncRepository(ctx, paths.AgentsRoot)
		result.LastAttemptAt = now
		if syncErr != nil {
			return result, syncErr
		}
		if err := validateSharedSources(paths); err != nil {
			return Result{State: "invalid", CommitIDs: result.CommitIDs, LastAttemptAt: now, Message: "received shared configuration failed validation; runtime files were not changed"}, err
		}
		if err := renderRuntime(paths); err != nil {
			return Result{State: "invalid", CommitIDs: result.CommitIDs, LastAttemptAt: now, Message: "shared data synchronized, but local Codex config could not be rendered; previous runtime files were preserved"}, err
		}
		if err := ensureLLMuxProfile(paths); err != nil {
			state := "pending"
			message := "shared data synchronized, but the local llmux profile could not be installed"
			if strings.Contains(strings.ToLower(err.Error()), "conflict") {
				state = "conflict"
				message = "an existing local llmux profile differs from shared settings; both copies were preserved"
			}
			return Result{State: state, CommitIDs: result.CommitIDs, LastAttemptAt: now, Message: message}, err
		}
		result.LastSuccessAt = time.Now().UTC().Format(time.RFC3339)
		return result, nil
	default:
		return Result{State: "invalid", LastAttemptAt: now, Message: "mode must be check, apply, sync, render, or check-runtime"}, errors.New("unknown Codex sync mode")
	}
}

func validatePaths(paths Paths) error {
	for _, value := range []string{
		paths.AgentsRoot, paths.CodexHome, paths.MindFSConfig, paths.SharedDefault,
		paths.SharedLLMux, paths.SharedProviders, paths.RuntimeConfig, paths.RuntimeLLMux,
		paths.SnapshotsRoot, paths.DefaultSnapshot, paths.LLMuxSnapshot,
		paths.LegacyProviders, paths.LocalSecrets, paths.StateDir,
	} {
		if strings.TrimSpace(value) == "" {
			return errors.New("incomplete Codex sync paths")
		}
	}
	return nil
}

func check(paths Paths) (Result, error) {
	if err := ensureRepository(paths.AgentsRoot); err != nil {
		return Result{State: "unavailable", Message: ".agents Git checkout is unavailable"}, err
	}
	if err := validateSharedSources(paths); err != nil {
		return Result{State: "invalid", Message: "shared configuration failed validation"}, err
	}
	dirty, err := worktreePaths(context.Background(), paths.AgentsRoot)
	if err != nil {
		return Result{State: "pending", Message: "could not inspect .agents worktree"}, err
	}
	if err := ensureAllowedPaths(dirty); err != nil {
		return Result{State: "pending", Message: "unrelated changes are present in the .agents worktree"}, err
	}
	remote, branch, err := configuredUpstream(context.Background(), paths.AgentsRoot)
	if err != nil {
		return Result{State: "unavailable", Message: "configure an upstream branch for this .agents checkout"}, err
	}
	trackingRef := "refs/remotes/" + remote + "/" + branch
	if _, _, err := runGit(context.Background(), paths.AgentsRoot, "rev-parse", "--verify", trackingRef); err != nil {
		return Result{State: "unavailable", Message: "configured upstream has not been fetched in this checkout"}, err
	}
	if err := ensureCommitsAllowed(context.Background(), paths.AgentsRoot, trackingRef); err != nil {
		return Result{State: "pending", Message: "local commits include paths outside the shared-config allowlist"}, err
	}
	counts, _, err := runGit(context.Background(), paths.AgentsRoot, "rev-list", "--left-right", "--count", trackingRef+"...HEAD")
	if err != nil {
		return Result{State: "pending", Message: "could not compare local branch with configured upstream"}, err
	}
	parts := strings.Fields(counts)
	if len(parts) != 2 {
		return Result{State: "pending", Message: "could not compare local branch with configured upstream"}, errors.New("invalid upstream comparison")
	}
	if len(dirty) > 0 || parts[1] != "0" || parts[0] != "0" {
		return Result{State: "pending", Message: fmt.Sprintf("shared worktree has %d changed paths; upstream commits behind/ahead: %s/%s", len(dirty), parts[0], parts[1])}, errors.New("shared configuration is not synchronized")
	}
	return Result{State: "synced", Message: "shared configuration is valid and the local branch matches its fetched upstream"}, nil
}

func validateSharedSources(paths Paths) error {
	for _, item := range []struct {
		path string
		kind string
	}{
		{paths.SharedDefault, "default"},
		{paths.SharedLLMux, "llmux"},
	} {
		payload, err := readOptional(item.path)
		if err != nil {
			return fmt.Errorf("read shared %s profile: %w", item.kind, err)
		}
		if payload == nil {
			continue
		}
		if err := ValidateSharedProfile(item.kind, payload); err != nil {
			return fmt.Errorf("shared %s profile: %w", item.kind, err)
		}
	}
	providers, err := readOptional(paths.SharedProviders)
	if err != nil {
		return fmt.Errorf("read shared provider metadata: %w", err)
	}
	if providers != nil {
		if err := ValidateProviderMetadata(providers); err != nil {
			return err
		}
	}
	return nil
}

func readOptional(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	if info.Size() > maxSharedConfigBytes {
		return nil, fmt.Errorf("%s exceeds 1 MiB", filepath.Base(path))
	}
	return os.ReadFile(path)
}

func safeMigrationMessage(err error) string {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "conflict"):
		return "local snapshot and shared source differ; resolve the reported setting before migration"
	case strings.Contains(message, "credential"), strings.Contains(message, "apikey"), strings.Contains(message, "token"):
		return "migration stopped because shared data contains a forbidden credential field"
	default:
		return "migration validation failed; no runtime configuration was replaced"
	}
}
