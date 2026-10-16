package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

func (s *Service) fetchAndVerifyManifest(ctx context.Context, version string) (releaseManifest, error) {
	publicKey, err := updatePublicKey()
	if err != nil {
		return releaseManifest{}, err
	}
	version = normalizeTag(version)
	base := fmt.Sprintf("https://github.com/%s/releases/download/%s/mindfs_%s_manifest.json", s.repo, version, version)
	body, err := s.fetchURL(ctx, base, 4<<20)
	if err != nil {
		return releaseManifest{}, err
	}
	var envelope signedReleaseManifest
	if err := json.Unmarshal(body, &envelope); err != nil {
		return releaseManifest{}, fmt.Errorf("release manifest envelope invalid: %w", err)
	}
	if strings.TrimSpace(envelope.Payload) == "" || strings.TrimSpace(envelope.Signature) == "" {
		return releaseManifest{}, errors.New("release manifest envelope missing payload or signature")
	}
	payload, err := decodeBase64String(envelope.Payload)
	if err != nil {
		return releaseManifest{}, fmt.Errorf("release manifest payload invalid: %w", err)
	}
	signature, err := decodeBase64String(envelope.Signature)
	if err != nil {
		return releaseManifest{}, fmt.Errorf("release manifest signature invalid: %w", err)
	}
	if !ed25519.Verify(publicKey, payload, signature) {
		return releaseManifest{}, errors.New("release manifest signature verification failed")
	}
	var manifest releaseManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return releaseManifest{}, fmt.Errorf("release manifest invalid: %w", err)
	}
	if normalizeTag(manifest.Version) != version {
		return releaseManifest{}, fmt.Errorf("release manifest version mismatch: got %q want %q", manifest.Version, version)
	}
	return manifest, nil
}

func updatePublicKey() (ed25519.PublicKey, error) {
	encoded := strings.TrimSpace(releaseManifestPublicKey)
	if encoded == "" {
		return nil, errors.New("auto update requires a configured release manifest public key")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil {
		return nil, fmt.Errorf("release manifest public key invalid: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("release manifest public key invalid length: got %d want %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

func (m releaseManifest) findArtifact(name string) (manifestArtifact, error) {
	for _, artifact := range m.Artifacts {
		if strings.TrimSpace(artifact.Name) == name {
			if _, err := parseSHA256Hex(artifact.SHA256); err != nil {
				return manifestArtifact{}, fmt.Errorf("release manifest artifact %s invalid sha256: %w", name, err)
			}
			return artifact, nil
		}
	}
	return manifestArtifact{}, fmt.Errorf("release manifest missing artifact: %s", name)
}

func verifyFileSHA256(path, wantHex string, wantSize int64) error {
	want, err := parseSHA256Hex(wantHex)
	if err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return err
	}
	if wantSize > 0 && size != wantSize {
		return fmt.Errorf("downloaded artifact size mismatch: got %d want %d", size, wantSize)
	}
	if got := hash.Sum(nil); !bytesEqual(got, want) {
		return errors.New("downloaded artifact sha256 verification failed")
	}
	return nil
}

func parseSHA256Hex(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if len(value) != sha256.Size*2 {
		return nil, fmt.Errorf("got %d hex chars want %d", len(value), sha256.Size*2)
	}
	out, err := hex.DecodeString(value)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func decodeBase64String(value string) ([]byte, error) {
	encoded := strings.TrimSpace(value)
	out, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		out, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	return out, err
}
