package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"mindfs/internal/codexsync"
	"mindfs/server/internal/apperr"
	configpkg "mindfs/server/internal/config"
)

func readAgentAPIProviders() ([]agentAPIProvider, error) {
	paths, err := codexsync.ResolvePaths()
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(paths.SharedProviders); err == nil {
		metadata, err := codexsync.ReadProviderMetadata(paths.SharedProviders)
		if err != nil {
			return nil, err
		}
		secrets, err := codexsync.ReadLocalProviderSecrets(paths.LocalSecrets)
		if err != nil {
			return nil, err
		}
		providers := make([]agentAPIProvider, 0, len(metadata))
		for _, item := range metadata {
			provider := agentAPIProvider{
				ID:            item.ID,
				Name:          item.Name,
				BaseURL:       item.BaseURL,
				APIKey:        secrets[item.ID],
				Protocols:     append([]string(nil), item.Protocols...),
				ModelFamilies: append([]string(nil), item.ModelFamilies...),
				Models:        append([]string(nil), item.Models...),
				CreatedAt:     item.CreatedAt,
				UpdatedAt:     item.UpdatedAt,
			}
			provider.Protocols = agentAPIProviderProtocols(provider)
			providers = append(providers, provider)
		}
		return providers, nil
	} else if !os.IsNotExist(err) {
		return nil, apperr.Wrap("stat", paths.SharedProviders, err)
	}
	// Before explicit --apply migration, keep the legacy full local file readable.
	return readLegacyAgentAPIProviders()
}

func writeAgentAPIProviders(providers []agentAPIProvider) error {
	paths, err := codexsync.ResolvePaths()
	if err != nil {
		return err
	}
	if err := codexsync.WithSyncLock(paths, func() error {
		return writeAgentAPIProvidersLocked(paths, providers)
	}); err != nil {
		return err
	}
	codexsync.Request(context.Background())
	return nil
}

func writeAgentAPIProvidersLocked(paths codexsync.Paths, providers []agentAPIProvider) error {
	return writeAgentAPIProvidersLockedWith(paths, providers, codexsync.WriteProviderMetadata, codexsync.MarkProviderMigrationComplete)
}

func writeAgentAPIProvidersLockedWith(
	paths codexsync.Paths,
	providers []agentAPIProvider,
	writeMetadata func(string, []codexsync.ProviderMetadata) error,
	markMigration func(codexsync.Paths) error,
) error {
	metadata := make([]codexsync.ProviderMetadata, 0, len(providers))
	secrets := make(map[string]string, len(providers))
	for _, provider := range providers {
		metadata = append(metadata, codexsync.ProviderMetadata{
			ID:            provider.ID,
			Name:          provider.Name,
			BaseURL:       provider.BaseURL,
			Protocols:     agentAPIProviderProtocols(provider),
			ModelFamilies: append([]string(nil), provider.ModelFamilies...),
			Models:        append([]string(nil), provider.Models...),
			CreatedAt:     provider.CreatedAt,
			UpdatedAt:     provider.UpdatedAt,
		})
		if strings.TrimSpace(provider.APIKey) != "" {
			secrets[provider.ID] = provider.APIKey
		}
	}
	preflight, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	if err := codexsync.ValidateProviderMetadata(preflight); err != nil {
		return err
	}
	oldMetadata, err := codexsync.ReadProviderMetadata(paths.SharedProviders)
	if err != nil {
		return err
	}
	oldSecrets, err := codexsync.ReadLocalProviderSecrets(paths.LocalSecrets)
	if err != nil {
		return err
	}
	metadataExisted, err := localFileExists(paths.SharedProviders)
	if err != nil {
		return err
	}
	secretsExisted, err := localFileExists(paths.LocalSecrets)
	if err != nil {
		return err
	}
	if len(secrets) < len(providers) {
		for _, provider := range providers {
			if secrets[provider.ID] == "" && oldSecrets[provider.ID] != "" {
				secrets[provider.ID] = oldSecrets[provider.ID]
			}
		}
	}
	rollback := func(cause error) error {
		if restoreErr := restoreAgentAPIProviderFiles(paths, oldMetadata, oldSecrets, metadataExisted, secretsExisted); restoreErr != nil {
			return errors.Join(cause, errors.New("provider update rollback also failed"), restoreErr)
		}
		return cause
	}
	// Preserve old keys and metadata if either half of the local/shared update fails.
	if err := codexsync.WriteLocalProviderSecrets(paths.LocalSecrets, secrets); err != nil {
		return rollback(err)
	}
	if err := writeMetadata(paths.SharedProviders, metadata); err != nil {
		return rollback(err)
	}
	if err := markMigration(paths); err != nil {
		return rollback(err)
	}
	return nil
}

func localFileExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func restoreAgentAPIProviderFiles(
	paths codexsync.Paths,
	metadata []codexsync.ProviderMetadata,
	secrets map[string]string,
	metadataExisted, secretsExisted bool,
) error {
	var restoreErrors []error
	if metadataExisted {
		if err := codexsync.WriteProviderMetadata(paths.SharedProviders, metadata); err != nil {
			restoreErrors = append(restoreErrors, err)
		}
	} else if err := os.Remove(paths.SharedProviders); err != nil && !errors.Is(err, os.ErrNotExist) {
		restoreErrors = append(restoreErrors, err)
	}
	if secretsExisted {
		if err := codexsync.WriteLocalProviderSecrets(paths.LocalSecrets, secrets); err != nil {
			restoreErrors = append(restoreErrors, err)
		}
	} else if err := os.Remove(paths.LocalSecrets); err != nil && !errors.Is(err, os.ErrNotExist) {
		restoreErrors = append(restoreErrors, err)
	}
	return errors.Join(restoreErrors...)
}

func withAgentAPIProviderStorageLock(write func(paths codexsync.Paths) error) error {
	paths, err := codexsync.ResolvePaths()
	if err != nil {
		return err
	}
	return codexsync.WithSyncLock(paths, func() error {
		return write(paths)
	})
}

func readLegacyAgentAPIProviders() ([]agentAPIProvider, error) {
	path, err := agentAPIProvidersPath()
	if err != nil {
		return nil, err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []agentAPIProvider{}, nil
		}
		return nil, apperr.Wrap("read", path, err)
	}
	if len(strings.TrimSpace(string(payload))) == 0 {
		return []agentAPIProvider{}, nil
	}
	var providers []agentAPIProvider
	if err := json.Unmarshal(payload, &providers); err != nil {
		return nil, err
	}
	for i := range providers {
		providers[i].Protocols = agentAPIProviderProtocols(providers[i])
	}
	return providers, nil
}

func agentAPIProvidersPath() (string, error) {
	configDir, err := configpkg.MindFSConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "api-providers.json"), nil
}
