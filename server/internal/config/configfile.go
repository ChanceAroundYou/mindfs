package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// UpdateJSONFile 读入 path 的 JSON 对象，交给 mutate 就地修改，再原子写回。
//
// 三条约束都来自 config.json 的实际使用方式：
//   - **文件可以不存在**：配对前边控端往往压根没有 config.json（缺省路径不存在是常态）。
//     按空对象处理，而不是报错。
//   - **未知键必须原样保留**：这是人手写的文件，一次写回丢掉 router / live_compact /
//     channels 任何一个段，都表现为「重启后配置莫名其妙变回默认」，而且没有任何报错。
//   - **数字不许被浮点改写**：decoder 默认把 JSON 数字解成 float64，大整数（毫秒、
//     token 计数）写回时会变成科学计数法。用 UseNumber 保留原文。
//
// 写回走同目录临时文件 + rename：中途崩溃不会留下半个 JSON，而半个 JSON 会让下一次
// 启动直接失败。
func UpdateJSONFile(path string, mutate func(map[string]any) error) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("config: config path is empty")
	}
	if mutate == nil {
		return errors.New("config: mutate function is nil")
	}
	doc, mode, err := readJSONObject(path)
	if err != nil {
		return err
	}
	if err := mutate(doc); err != nil {
		return err
	}
	return writeJSONObject(path, doc, mode)
}

// readJSONObject 读出对象形态的 JSON。返回的 mode 是写回时要沿用的权限位。
func readJSONObject(path string) (map[string]any, os.FileMode, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, 0o600, nil
		}
		return nil, 0, fmt.Errorf("config: read %s: %w", path, err)
	}
	mode := os.FileMode(0o600)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, mode, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, 0, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if doc == nil {
		// 文件内容是 `null`：当成空对象，否则下面每一次写入都会 panic。
		doc = map[string]any{}
	}
	return doc, mode, nil
}

// writeJSONObject 原子写回。临时文件与目标同目录（跨目录 rename 不是原子操作）。
func writeJSONObject(path string, doc map[string]any, mode os.FileMode) error {
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("config: encode %s: %w", path, err)
	}
	encoded = append(encoded, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("config: create dir for %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("config: create temp for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: chmod temp for %s: %w", path, err)
	}
	if _, err := tmp.Write(encoded); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: sync %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: close temp for %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("config: replace %s: %w", path, err)
	}
	return nil
}
