package main

import (
	"fmt"
	"os"
	"strings"

	"mindfs/server/app"
)

// controlSyncOptions 把 config.json 的 control_sync 段翻成启动参数。
//
// 关闭时不碰 token 文件：配置了路径但暂时关掉同步，不该因为文件暂时不在而
// 让整台机器起不来。打开时才读，读不到就拒绝启动 —— 边缘少了 token 会静默
// 变成「永远在线失败」，那比不启动更难查。
func controlSyncOptions(cfg startupConfig) (*app.ControlSyncOptions, error) {
	if cfg.ControlSync == nil {
		return nil, nil
	}
	cs := cfg.ControlSync
	if !boolValue(cs.Enabled) {
		return nil, nil
	}
	opts := &app.ControlSyncOptions{
		Enabled:    true,
		Role:       strings.TrimSpace(stringValue(cs.Role)),
		InstanceID: strings.TrimSpace(stringValue(cs.InstanceID)),
		PrimaryURL: strings.TrimSpace(stringValue(cs.PrimaryURL)),
		CAFile:     strings.TrimSpace(stringValue(cs.CAFile)),
	}
	token, err := readSyncToken(stringValue(cs.TokenFile))
	if err != nil {
		return nil, err
	}
	opts.Token = token
	return opts, nil
}

// readSyncToken 从 token_file 读密钥。文件内容只有一个 token，允许行尾换行
// （用 `echo` 生成时几乎必然带上）。
func readSyncToken(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("control_sync: read token_file: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("control_sync: token_file %q is empty", path)
	}
	return token, nil
}

func boolValue(p *bool) bool {
	return p != nil && *p
}

func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
