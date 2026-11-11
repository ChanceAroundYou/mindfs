package codexsync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ProviderMetadata struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	BaseURL       string   `json:"baseUrl"`
	Protocols     []string `json:"protocols,omitempty"`
	ModelFamilies []string `json:"modelFamilies"`
	Models        []string `json:"models"`
	CreatedAt     string   `json:"createdAt"`
	UpdatedAt     string   `json:"updatedAt"`
}

func ReadProviderMetadata(path string) ([]ProviderMetadata, error) {
	payload, err := readOptional(path)
	if err != nil {
		return nil, err
	}
	if payload == nil || len(bytes.TrimSpace(payload)) == 0 {
		return []ProviderMetadata{}, nil
	}
	if err := ValidateProviderMetadata(payload); err != nil {
		return nil, err
	}
	var providers []ProviderMetadata
	if err := json.Unmarshal(payload, &providers); err != nil {
		return nil, err
	}
	if err := validateProviderMetadataEntries(providers); err != nil {
		return nil, err
	}
	return providers, nil
}

func WriteProviderMetadata(path string, providers []ProviderMetadata) error {
	payload, err := json.MarshalIndent(providers, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if err := ValidateProviderMetadata(payload); err != nil {
		return err
	}
	return atomicWriteFile(path, payload, 0o644)
}

func ReadLocalProviderSecrets(path string) (map[string]string, error) {
	payload, err := readOptional(path)
	if err != nil {
		return nil, err
	}
	if payload == nil || len(bytes.TrimSpace(payload)) == 0 {
		return map[string]string{}, nil
	}
	if trimmed := bytes.TrimSpace(payload); trimmed[0] != '{' {
		return nil, fmt.Errorf("local provider secrets must contain one JSON object")
	}
	var secrets map[string]string
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&secrets); err != nil {
		return nil, fmt.Errorf("decode local provider secrets: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("local provider secrets must contain one JSON object")
	}
	if secrets == nil {
		secrets = map[string]string{}
	}
	return secrets, nil
}

func WriteLocalProviderSecrets(path string, secrets map[string]string) error {
	if secrets == nil {
		secrets = map[string]string{}
	}
	payload, err := json.MarshalIndent(secrets, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return atomicWriteFile(path, payload, 0o600)
}

func validateProviderMetadataEntries(providers []ProviderMetadata) error {
	seen := make(map[string]struct{}, len(providers))
	for _, provider := range providers {
		if strings.TrimSpace(provider.ID) == "" || strings.TrimSpace(provider.Name) == "" || strings.TrimSpace(provider.BaseURL) == "" {
			return fmt.Errorf("provider metadata entry is missing an id, name, or baseUrl")
		}
		if _, exists := seen[provider.ID]; exists {
			return fmt.Errorf("duplicate provider id %q", provider.ID)
		}
		seen[provider.ID] = struct{}{}
	}
	return nil
}

func encodeProviderMetadata(providers []ProviderMetadata) ([]byte, error) {
	if providers == nil {
		providers = []ProviderMetadata{}
	}
	if err := validateProviderMetadataEntries(providers); err != nil {
		return nil, err
	}
	sort.SliceStable(providers, func(i, j int) bool { return providers[i].ID < providers[j].ID })
	payload, err := json.MarshalIndent(providers, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(payload, '\n'), nil
}

func readLegacyProviders(path string) ([]ProviderMetadata, map[string]string, error) {
	payload, err := readOptional(path)
	if err != nil || payload == nil {
		return nil, map[string]string{}, err
	}
	if trimmed := bytes.TrimSpace(payload); len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, nil, fmt.Errorf("legacy provider file must contain one JSON array")
	}
	type legacyProvider struct {
		ProviderMetadata
		APIKey string `json:"apiKey"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var legacy []legacyProvider
	if err := decoder.Decode(&legacy); err != nil {
		return nil, nil, fmt.Errorf("decode legacy provider file: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, nil, fmt.Errorf("legacy provider file must contain one JSON array")
	}
	providers := make([]ProviderMetadata, 0, len(legacy))
	secrets := make(map[string]string, len(legacy))
	for _, item := range legacy {
		providers = append(providers, item.ProviderMetadata)
		if strings.TrimSpace(item.APIKey) != "" {
			secrets[item.ID] = item.APIKey
		}
	}
	if err := validateProviderMetadataEntries(providers); err != nil && len(providers) > 0 {
		return nil, nil, err
	}
	return providers, secrets, nil
}

func atomicWriteFile(path string, payload []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".codexsync-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err == nil {
		return nil
	} else {
		backup := path + ".replace-backup"
		if _, statErr := os.Stat(path); statErr != nil {
			return err
		}
		_ = os.Remove(backup)
		if renameErr := os.Rename(path, backup); renameErr != nil {
			return err
		}
		if renameErr := os.Rename(tmpPath, path); renameErr != nil {
			_ = os.Rename(backup, path)
			return renameErr
		}
		_ = os.Remove(backup)
		return nil
	}
}
