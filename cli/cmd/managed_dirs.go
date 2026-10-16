package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mindfs/server/app"
)

// resolveClientTLS 判定这次 CLI 调用该不该走 https。
//
// 显式 scheme 优先（用户写了 https:// 就别去读配置），其次是本次显式传入的
// -tls 开关；都没给才回读本机服务登记的 TLS 状态。少了最后一步，CLI 就会拿
// http 去敲一个 https 的本机服务（上游 bab545f 修的就是这个）。
func resolveClientTLS(addr string, useTLS, configured bool) (bool, error) {
	if strings.HasPrefix(addr, "https://") {
		return true, nil
	}
	if strings.HasPrefix(addr, "http://") {
		return false, nil
	}
	if configured {
		return useTLS, nil
	}
	return app.ReadLocalCLITLS(addr)
}

func newHTTPClient(useTLS bool, timeout time.Duration) *http.Client {
	c := &http.Client{Timeout: timeout}
	if useTLS {
		// InsecureSkipVerify is used because these CLI health checks and API
		// calls connect to the local MindFS server (loopback or same machine),
		// which may present a self-signed certificate. No traffic leaves the host.
		c.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	return c
}

type managedDirResponse struct {
	ID       string `json:"id"`
	RootPath string `json:"root_path"`
}

func addManagedDir(addr string, useTLS bool, path string) (managedDirResponse, error) {
	token, err := app.ReadLocalCLIToken(addr)
	if err != nil {
		return managedDirResponse{}, err
	}
	url := addrToURL(addr, "/api/dirs", useTLS)
	body, err := json.Marshal(map[string]any{"path": path})
	if err != nil {
		return managedDirResponse{}, err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return managedDirResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-MindFS-Local-CLI-Token", token)
	client := newHTTPClient(useTLS, 3*time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return managedDirResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var out managedDirResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return managedDirResponse{}, err
		}
		if strings.TrimSpace(out.RootPath) == "" {
			out.RootPath = path
		}
		return out, nil
	}
	message := httpErrorMessage(resp)
	return managedDirResponse{}, fmt.Errorf("failed to add managed directory:\n%s", message)
}

func removeManagedDir(addr string, useTLS bool, path string) error {
	token, err := app.ReadLocalCLIToken(addr)
	if err != nil {
		return err
	}
	endpoint := addrToURL(addr, "/api/dirs?path="+url.QueryEscape(path), useTLS)
	req, err := http.NewRequest(http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-MindFS-Local-CLI-Token", token)
	client := newHTTPClient(useTLS, 3*time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	message := httpErrorMessage(resp)
	return fmt.Errorf("failed to remove managed directory: %s", message)
}

func removeManagedDirFromRegistry(path string) error {
	return app.RemoveManagedDirFromRegistry(path)
}

func handleRemoveRoot(addr string, useTLS bool, path string) error {
	if serverRunning(addr, useTLS) {
		return removeManagedDir(addr, useTLS, path)
	}
	return removeManagedDirFromRegistry(path)
}
