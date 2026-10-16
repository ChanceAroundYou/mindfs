package webpush

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mindfs/server/internal/config"

	webpushgo "github.com/SherClockHolmes/webpush-go"
)

const (
	subscriptionsFileName = "web-push-subscriptions.json"
	vapidKeysFileName     = "web-push-vapid.json"
	defaultVAPIDSubject   = "mindfs@example.com"
	defaultTTLSeconds     = 60 * 60
	maxPayloadBytes       = 3800
)

type Config struct {
	Enabled    bool
	PublicKey  string
	PrivateKey string
	Subject    string
}

type persistedVAPIDKeys struct {
	PublicKey  string    `json:"public_key"`
	PrivateKey string    `json:"private_key"`
	Subject    string    `json:"subject,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

func EnsureConfig(enabled bool) (Config, error) {
	subject := defaultVAPIDSubject
	if !enabled {
		return Config{Enabled: false, Subject: subject}, nil
	}
	keys, err := ensureVAPIDKeys(subject)
	if err != nil {
		return Config{Enabled: false, Subject: subject}, err
	}
	return Config{
		Enabled:    keys.PublicKey != "" && keys.PrivateKey != "",
		PublicKey:  keys.PublicKey,
		PrivateKey: keys.PrivateKey,
		Subject:    subject,
	}, nil
}

func ensureVAPIDKeys(subject string) (persistedVAPIDKeys, error) {
	configDir, err := config.MindFSConfigDir()
	if err != nil {
		return persistedVAPIDKeys{}, err
	}
	path := filepath.Join(configDir, vapidKeysFileName)
	if keys, err := readVAPIDKeys(path); err == nil && keys.PublicKey != "" && keys.PrivateKey != "" {
		if strings.TrimSpace(keys.Subject) == "" {
			keys.Subject = subject
		}
		return keys, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return persistedVAPIDKeys{}, err
	}
	privateKey, publicKey, err := webpushgo.GenerateVAPIDKeys()
	if err != nil {
		return persistedVAPIDKeys{}, err
	}
	keys := persistedVAPIDKeys{
		PublicKey:  publicKey,
		PrivateKey: privateKey,
		Subject:    subject,
		CreatedAt:  time.Now().UTC(),
	}
	return keys, writeVAPIDKeys(path, keys)
}

func readVAPIDKeys(path string) (persistedVAPIDKeys, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return persistedVAPIDKeys{}, err
	}
	var keys persistedVAPIDKeys
	if err := json.Unmarshal(b, &keys); err != nil {
		return persistedVAPIDKeys{}, err
	}
	return keys, nil
}

func writeVAPIDKeys(path string, keys persistedVAPIDKeys) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(keys, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(path)
		if retryErr := os.Rename(tmp, path); retryErr != nil {
			return err
		}
	}
	return nil
}
