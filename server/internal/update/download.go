package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func (s *Service) downloadFile(ctx context.Context, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "mindfs-updater")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download failed: %s", resp.Status)
	}
	file, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(file, resp.Body)
	return err
}

func (s *Service) downloadReleaseAsset(ctx context.Context, asset releaseAsset, dst string) error {
	primaryURL := strings.TrimSpace(asset.BrowserDownloadURL)
	if primaryURL == "" {
		return errors.New("release asset download URL unavailable")
	}
	// 只认 GitHub 给的 asset 地址：本地 fork 已删掉 relay 镜像回退
	// （上游 relay 域名不再是本 fork 的依赖，见 G-H）。
	return s.downloadFile(ctx, primaryURL, dst)
}

func (s *Service) fetchURL(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "mindfs-updater")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download failed: %s", resp.Status)
	}
	reader := io.Reader(resp.Body)
	if limit > 0 {
		reader = io.LimitReader(resp.Body, limit+1)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if limit > 0 && int64(len(body)) > limit {
		return nil, fmt.Errorf("download exceeds size limit: %d bytes", limit)
	}
	return body, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func bytesEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var diff byte
	for i := range left {
		diff |= left[i] ^ right[i]
	}
	return diff == 0
}
