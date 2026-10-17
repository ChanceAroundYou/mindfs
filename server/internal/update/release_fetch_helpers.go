package update

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
)

func (s *Service) fetchLatestRelease(ctx context.Context) (latestRelease, error) {
	body, err := s.fetchRawText(ctx, releaseNotesPath, 1<<20)
	if err != nil {
		return latestRelease{}, err
	}
	tag := parseReleaseNotesVersion(body)
	if tag == "" {
		return latestRelease{}, fmt.Errorf("release check failed: no version found in %s", releaseNotesPath)
	}
	tag = normalizeTag(tag)
	return latestRelease{
		TagName: tag,
		Name:    tag,
		Body:    latestReleaseNotesBody(body),
		HTMLURL: fmt.Sprintf("https://github.com/%s/releases/tag/%s", s.repo, tag),
		Assets:  []releaseAsset{s.releaseAssetForVersion(tag)},
	}, nil
}

func (s *Service) fetchRawText(ctx context.Context, path string, limit int64) (string, error) {
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/main/%s", s.repo, strings.TrimPrefix(path, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "mindfs-update-checker")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("raw file fetch failed: %s", resp.Status)
	}
	if limit <= 0 {
		limit = 64 << 10
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (s *Service) pickAsset(release latestRelease, version string) (releaseAsset, error) {
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	candidates := []string{
		fmt.Sprintf("mindfs_%s_%s_%s%s", version, runtime.GOOS, runtime.GOARCH, ext),
		fmt.Sprintf("mindfs_v%s_%s_%s%s", strings.TrimPrefix(version, "v"), runtime.GOOS, runtime.GOARCH, ext),
	}
	for _, asset := range release.Assets {
		name := strings.TrimSpace(asset.Name)
		for _, candidate := range candidates {
			if name == candidate {
				return asset, nil
			}
		}
	}
	return releaseAsset{}, fmt.Errorf(
		"release asset not found for %s/%s (candidates: %s)",
		runtime.GOOS,
		runtime.GOARCH,
		strings.Join(candidates, ", "),
	)
}

func (s *Service) releaseAssetForVersion(version string) releaseAsset {
	version = normalizeTag(version)
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	name := fmt.Sprintf("mindfs_%s_%s_%s%s", version, runtime.GOOS, runtime.GOARCH, ext)
	return releaseAsset{
		Name:               name,
		BrowserDownloadURL: fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", s.repo, version, name),
	}
}
