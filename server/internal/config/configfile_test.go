package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func readObject(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("decode back: %v", err)
	}
	return doc
}

func TestUpdateJSONFilePreservesUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, `{
  "role": "control",
  "router": {"mode": "coordinate", "turn_timeout": 300000},
  "live_compact": {"mode": "on", "rail_tokens": 100000},
  "channels": {"feishu": {"enabled": true}}
}`)

	err := UpdateJSONFile(path, func(doc map[string]any) error {
		cs, _ := doc["control_sync"].(map[string]any)
		if cs == nil {
			cs = map[string]any{}
			doc["control_sync"] = cs
		}
		cs["role"] = "edge"
		return nil
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	doc := readObject(t, path)
	if doc["role"] != "control" {
		t.Fatalf("role lost: %#v", doc["role"])
	}
	router, ok := doc["router"].(map[string]any)
	if !ok || router["mode"] != "coordinate" {
		t.Fatalf("router lost: %#v", doc["router"])
	}
	channels, ok := doc["channels"].(map[string]any)
	if !ok || channels["feishu"] == nil {
		t.Fatalf("channels lost: %#v", doc["channels"])
	}
	cs, ok := doc["control_sync"].(map[string]any)
	if !ok || cs["role"] != "edge" {
		t.Fatalf("control_sync not written: %#v", doc["control_sync"])
	}
}

// 大整数经过 float64 会被写成 3.000000000001e+08 之类的形状；UseNumber 的意义就在这条。
func TestUpdateJSONFilePreservesLargeNumbers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, `{"big": 9007199254740993}`)

	if err := UpdateJSONFile(path, func(doc map[string]any) error {
		doc["touched"] = true
		return nil
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var probe struct {
		Big json.Number `json:"big"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if probe.Big.String() != "9007199254740993" {
		t.Fatalf("large number rewritten: %q", probe.Big.String())
	}
}

func TestUpdateJSONFileCreatesMissingFileWith0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "config.json")
	if err := UpdateJSONFile(path, func(doc map[string]any) error {
		doc["role"] = "control"
		return nil
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("new file perm = %o, want 600", perm)
	}
	if got := readObject(t, path)["role"]; got != "control" {
		t.Fatalf("role = %#v", got)
	}
}

// 已有文件的权限位要沿用：用户可能就是 0644 建的，写回顺手改成 0600 会让别的进程读不到。
func TestUpdateJSONFileKeepsExistingMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, `{"role":"control"}`)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := UpdateJSONFile(path, func(doc map[string]any) error {
		doc["touched"] = true
		return nil
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Fatalf("perm = %o, want 644", perm)
	}
}

// 坏 JSON 必须原样留住 —— 覆盖它等于把用户手写的配置换成一个只有 mutate 结果的空壳。
func TestUpdateJSONFileLeavesInvalidJSONUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	const broken = `{"role": "control",`
	writeFile(t, path, broken)

	err := UpdateJSONFile(path, func(doc map[string]any) error {
		doc["role"] = "edge"
		return nil
	})
	if err == nil {
		t.Fatal("expected parse error")
	}
	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read back: %v", readErr)
	}
	if string(raw) != broken {
		t.Fatalf("file was rewritten: %q", string(raw))
	}
}

func TestUpdateJSONFileRejectsNonObject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, `[1,2,3]`)

	if err := UpdateJSONFile(path, func(doc map[string]any) error { return nil }); err == nil {
		t.Fatal("expected error for array document")
	}
}

// mutate 报错时不能落盘：调用方可能在半路发现前提不成立（比如 URL 不是 https）。
func TestUpdateJSONFileSkipsWriteWhenMutateFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	const body = `{"role":"control"}`
	writeFile(t, path, body)

	wantErr := os.ErrInvalid
	err := UpdateJSONFile(path, func(doc map[string]any) error {
		doc["role"] = "edge"
		return wantErr
	})
	if err != wantErr {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != body {
		t.Fatalf("file changed despite mutate error: %q", string(raw))
	}
}
