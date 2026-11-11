package codexsync

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

func migrate(paths Paths) error {
	completed, err := migrationCompleted(paths.StateDir)
	if err != nil {
		return err
	}
	if completed {
		return nil
	}
	defaultSnapshot, err := readOptional(paths.DefaultSnapshot)
	if err != nil {
		return fmt.Errorf("read default snapshot: %w", err)
	}
	llmuxSnapshot, err := readOptional(paths.LLMuxSnapshot)
	if err != nil {
		return fmt.Errorf("read llmux snapshot: %w", err)
	}
	var defaultSeed, llmuxSeed []byte
	if defaultSnapshot != nil {
		defaultSeed, err = ProjectProfile("default", defaultSnapshot)
		if err != nil {
			return fmt.Errorf("default snapshot validation: %w", err)
		}
	}
	if llmuxSnapshot != nil {
		llmuxSeed, err = ProjectProfile("llmux", llmuxSnapshot)
		if err != nil {
			return fmt.Errorf("llmux snapshot validation: %w", err)
		}
	}
	existingDefault, err := readOptional(paths.SharedDefault)
	if err != nil {
		return fmt.Errorf("read shared default profile: %w", err)
	}
	existingLLMux, err := readOptional(paths.SharedLLMux)
	if err != nil {
		return fmt.Errorf("read shared llmux profile: %w", err)
	}
	nextDefault, _, err := mergeSharedProfile("default", existingDefault, defaultSeed)
	if err != nil {
		return err
	}
	nextLLMux, _, err := mergeSharedProfile("llmux", existingLLMux, llmuxSeed)
	if err != nil {
		return err
	}
	if err := validateSharedCandidate("default", nextDefault); err != nil {
		return err
	}
	if err := validateSharedCandidate("llmux", nextLLMux); err != nil {
		return err
	}

	sharedProviders, err := readProviderMetadataOptional(paths.SharedProviders)
	if err != nil {
		return err
	}
	providerSeeded, err := providerMigrationCompleted(paths.StateDir)
	if err != nil {
		return err
	}
	var legacyProviders []ProviderMetadata
	legacySecrets := map[string]string{}
	if !providerSeeded {
		legacyProviders, legacySecrets, err = readLegacyProviders(paths.LegacyProviders)
		if err != nil {
			return err
		}
	}
	nextProviders := sharedProviders
	if !providerSeeded {
		nextProviders, err = mergeProviderMetadata(sharedProviders, legacyProviders)
		if err != nil {
			return err
		}
	}
	providerBytes, err := encodeProviderMetadata(nextProviders)
	if err != nil {
		return err
	}
	secrets, err := ReadLocalProviderSecrets(paths.LocalSecrets)
	if err != nil {
		return err
	}
	if !providerSeeded {
		for id, key := range legacySecrets {
			if _, exists := secrets[id]; !exists {
				secrets[id] = key
			}
		}
	}
	secretBytes, err := json.MarshalIndent(secrets, "", "  ")
	if err != nil {
		return err
	}
	secretBytes = append(secretBytes, '\n')

	// All source snapshots and shared candidates are parsed and validated before the
	// first write. The legacy provider file and complete Codex snapshots remain intact.
	writes := []struct {
		path    string
		payload []byte
		mode    os.FileMode
	}{
		{paths.SharedDefault, nextDefault, 0o644},
		{paths.SharedLLMux, nextLLMux, 0o644},
		// Keep an empty shared source explicit so API reads do not fall back to the
		// legacy provider file after this one-time migration.
		{paths.SharedProviders, providerBytes, 0o644},
		{paths.LocalSecrets, secretBytes, 0o600},
	}
	for _, item := range writes {
		if item.payload == nil {
			continue
		}
		if filesEqual(item.path, item.payload) {
			if item.path != paths.LocalSecrets {
				continue
			}
			info, statErr := os.Lstat(item.path)
			if statErr == nil && info.Mode().IsRegular() && info.Mode().Perm() == 0o600 {
				continue
			}
			if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
				return statErr
			}
		}
		if err := saveRollbackCopy(paths.StateDir, item.path); err != nil {
			return err
		}
		if err := atomicWriteFile(item.path, item.payload, item.mode); err != nil {
			return fmt.Errorf("write shared migration file %s: %w", filepath.Base(item.path), err)
		}
	}
	if err := MarkProviderMigrationComplete(paths); err != nil {
		return err
	}
	return atomicWriteFile(migrationMarkerPath(paths.StateDir), []byte("{\"version\":1}\n"), 0o600)
}

func migrationMarkerPath(stateDir string) string {
	return filepath.Join(stateDir, "migration-v1.json")
}

func migrationCompleted(stateDir string) (bool, error) {
	payload, err := readOptional(migrationMarkerPath(stateDir))
	if err != nil || payload == nil {
		return false, err
	}
	var marker struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(payload, &marker); err != nil || marker.Version != 1 {
		return false, errors.New("Codex sync migration marker is invalid")
	}
	return true, nil
}

func providerMigrationMarkerPath(stateDir string) string {
	return filepath.Join(stateDir, "providers-v1.json")
}

func providerMigrationCompleted(stateDir string) (bool, error) {
	payload, err := readOptional(providerMigrationMarkerPath(stateDir))
	if err != nil || payload == nil {
		return false, err
	}
	var marker struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(payload, &marker); err != nil || marker.Version != 1 {
		return false, errors.New("Codex provider migration marker is invalid")
	}
	return true, nil
}

func MarkProviderMigrationComplete(paths Paths) error {
	return atomicWriteFile(providerMigrationMarkerPath(paths.StateDir), []byte("{\"version\":1}\n"), 0o600)
}

func mergeSharedProfile(kind string, existing, seed []byte) ([]byte, bool, error) {
	if existing == nil {
		if seed == nil {
			return nil, false, nil
		}
		if err := ValidateSharedProfile(kind, seed); err != nil {
			return nil, false, err
		}
		return append([]byte(nil), seed...), true, nil
	}
	if err := ValidateSharedProfile(kind, existing); err != nil {
		return nil, false, fmt.Errorf("existing shared %s profile is invalid: %w", kind, err)
	}
	if seed == nil {
		return append([]byte(nil), existing...), false, nil
	}
	base, err := decodeTOML(existing)
	if err != nil {
		return nil, false, err
	}
	seedRoot, err := decodeTOML(seed)
	if err != nil {
		return nil, false, err
	}
	changed := false
	mergeKey := func(path string, dst, src map[string]any, key string) error {
		value, hasSeed := src[key]
		if !hasSeed {
			return nil
		}
		if current, exists := dst[key]; exists {
			if !reflect.DeepEqual(current, value) {
				return fmt.Errorf("migration conflict at %s", path)
			}
			return nil
		}
		dst[key] = value
		changed = true
		return nil
	}
	for _, key := range []string{"model", "model_reasoning_effort"} {
		if err := mergeKey(key, base, seedRoot, key); err != nil {
			return nil, false, err
		}
	}
	if kind == "default" {
		seedFeatures := tomlTable(seedRoot["features"])
		if seedFeatures != nil {
			baseFeatures := tomlTable(base["features"])
			if baseFeatures == nil {
				baseFeatures = map[string]any{}
				base["features"] = baseFeatures
			}
			if err := mergeKey("features.goals", baseFeatures, seedFeatures, "goals"); err != nil {
				return nil, false, err
			}
		}
	} else {
		for _, key := range []string{"model_provider"} {
			if err := mergeKey(key, base, seedRoot, key); err != nil {
				return nil, false, err
			}
		}
		seedProviders := tomlTable(seedRoot["model_providers"])
		seedLLMux := tomlTable(seedProviders["llmux"])
		baseProviders := tomlTable(base["model_providers"])
		if baseProviders == nil {
			baseProviders = map[string]any{}
			base["model_providers"] = baseProviders
		}
		baseLLMux := tomlTable(baseProviders["llmux"])
		if baseLLMux == nil {
			baseLLMux = map[string]any{}
			baseProviders["llmux"] = baseLLMux
		}
		for _, key := range []string{"name", "base_url", "wire_api", "env_key"} {
			if err := mergeKey("model_providers.llmux."+key, baseLLMux, seedLLMux, key); err != nil {
				return nil, false, err
			}
		}
	}
	if !changed {
		return append([]byte(nil), existing...), false, nil
	}
	merged, err := toml.Marshal(base)
	if err != nil {
		return nil, false, err
	}
	if err := ValidateSharedProfile(kind, merged); err != nil {
		return nil, false, err
	}
	return merged, true, nil
}

func validateSharedCandidate(kind string, payload []byte) error {
	if payload == nil {
		return nil
	}
	if err := ValidateSharedProfile(kind, payload); err != nil {
		return fmt.Errorf("migration produced invalid shared %s profile: %w", kind, err)
	}
	return nil
}

func readProviderMetadataOptional(path string) ([]ProviderMetadata, error) {
	payload, err := readOptional(path)
	if err != nil || payload == nil {
		return nil, err
	}
	var providers []ProviderMetadata
	if err := json.Unmarshal(payload, &providers); err != nil {
		return nil, fmt.Errorf("decode shared providers.json: %w", err)
	}
	if err := ValidateProviderMetadata(payload); err != nil {
		return nil, err
	}
	return providers, nil
}

func mergeProviderMetadata(existing, seed []ProviderMetadata) ([]ProviderMetadata, error) {
	merged := append([]ProviderMetadata(nil), existing...)
	index := make(map[string]int, len(merged))
	for i, item := range merged {
		index[item.ID] = i
	}
	for _, incoming := range seed {
		currentIndex, found := index[incoming.ID]
		if !found {
			index[incoming.ID] = len(merged)
			merged = append(merged, incoming)
			continue
		}
		current := merged[currentIndex]
		fields := []struct {
			name string
			old  any
			new  any
		}{
			{"name", current.Name, incoming.Name},
			{"baseUrl", current.BaseURL, incoming.BaseURL},
			{"protocols", current.Protocols, incoming.Protocols},
			{"modelFamilies", current.ModelFamilies, incoming.ModelFamilies},
			{"models", current.Models, incoming.Models},
			{"createdAt", current.CreatedAt, incoming.CreatedAt},
			{"updatedAt", current.UpdatedAt, incoming.UpdatedAt},
		}
		for _, field := range fields {
			if !emptyValue(field.old) && !emptyValue(field.new) && !reflect.DeepEqual(field.old, field.new) {
				return nil, fmt.Errorf("migration conflict at providers.%s.%s", incoming.ID, field.name)
			}
		}
		if current.Name == "" {
			current.Name = incoming.Name
		}
		if current.BaseURL == "" {
			current.BaseURL = incoming.BaseURL
		}
		if len(current.Protocols) == 0 {
			current.Protocols = incoming.Protocols
		}
		if len(current.ModelFamilies) == 0 {
			current.ModelFamilies = incoming.ModelFamilies
		}
		if len(current.Models) == 0 {
			current.Models = incoming.Models
		}
		if current.CreatedAt == "" {
			current.CreatedAt = incoming.CreatedAt
		}
		if current.UpdatedAt == "" {
			current.UpdatedAt = incoming.UpdatedAt
		}
		merged[currentIndex] = current
	}
	return merged, validateProviderMetadataEntries(merged)
}

func emptyValue(value any) bool {
	switch typed := value.(type) {
	case string:
		return typed == ""
	case []string:
		return len(typed) == 0
	default:
		return value == nil
	}
}

func filesEqual(path string, payload []byte) bool {
	current, err := os.ReadFile(path)
	return err == nil && bytes.Equal(current, payload)
}

func saveRollbackCopy(stateDir, sourcePath string) error {
	if _, err := os.Lstat(sourcePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	payload, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	rollbackDir := filepath.Join(stateDir, "rollback")
	if err := os.MkdirAll(rollbackDir, 0o700); err != nil {
		return err
	}
	base := filepath.Base(sourcePath)
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	destination := filepath.Join(rollbackDir, base+"."+stamp+".bak")
	if err := os.WriteFile(destination, payload, 0o600); err != nil {
		return err
	}
	return nil
}
