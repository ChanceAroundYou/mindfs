package sessionmaintenance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CleanImages 把原生转录里的图片块整体换成文本占位，保留记录数与 uuid 链。
// 原文件先留 .bak；没有图片时一个字节都不写。
func CleanImages(path string) (CleanResult, error) {
	return CleanImagesWithOptions(path, Options{})
}

// CleanImagesWithOptions 与 CleanImages 等价，只是读 KeepBackups 开关。
// 显式 false 才跳过 .bak；零值仍然保留备份。
func CleanImagesWithOptions(path string, opts Options) (CleanResult, error) {
	result := CleanResult{Path: path}
	info, err := os.Stat(path)
	if err != nil {
		return result, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return result, err
	}
	// 原生转录是 JSONL：一行一条记录。整份 json.Unmarshal 会把两行以上的文件
	// 判为坏 JSON、一个图片都不清——这里必须逐行改写。
	cleaned, removed, err := replaceImagesInTranscript(raw)
	if err != nil {
		return result, err
	}
	if removed == 0 {
		return result, nil
	}
	if opts.keepBackup() {
		backup, err := writeUniqueBackup(path, raw, info.Mode().Perm())
		if err != nil {
			return result, err
		}
		result.BackupPath = backup
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".clean-*")
	if err != nil {
		return result, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(cleaned); err != nil {
		tmp.Close()
		return result, err
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return result, err
	}
	if err := tmp.Close(); err != nil {
		return result, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return result, err
	}
	// 占位只替换图片块，记录数与 uuid/parentUuid 链由逐行 JSON 往返保持。
	result.RemovedImages = removed
	result.ReclaimedBytes = int64(len(raw) - len(cleaned))
	return result, nil
}

func replaceImagesInTranscript(raw []byte) ([]byte, int, error) {
	lines := bytes.Split(raw, []byte("\n"))
	removedTotal := 0
	for i, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var value any
		if err := json.Unmarshal(line, &value); err != nil {
			// 坏行原样保留：维护工具不能顺手重写它不认识的内容。
			continue
		}
		next, removed := replaceImageBlocks(value)
		if removed == 0 {
			continue
		}
		encoded, err := json.Marshal(next)
		if err != nil {
			return nil, 0, err
		}
		lines[i] = encoded
		removedTotal += removed
	}
	return bytes.Join(lines, []byte("\n")), removedTotal, nil
}

// replaceImageBlocks 把 image / input_image 块整体换成单个文本块。
// 只留空 data 会让下游 Importer 报 unknown variant；必须是完整的类型替换。
func replaceImageBlocks(value any) (any, int) {
	switch typed := value.(type) {
	case []any:
		removed := 0
		for i, item := range typed {
			next, n := replaceImageBlocks(item)
			typed[i] = next
			removed += n
		}
		return typed, removed
	case map[string]any:
		kind, _ := typed["type"].(string)
		if kind == "image" || kind == "input_image" {
			return map[string]any{
				"type": "text",
				"text": fmt.Sprintf("[image removed: ~%d bytes]", imageApproxBytes(typed)),
			}, 1
		}
		removed := 0
		for key, item := range typed {
			next, n := replaceImageBlocks(item)
			typed[key] = next
			removed += n
		}
		return typed, removed
	default:
		return value, 0
	}
}

func imageApproxBytes(block map[string]any) int {
	for _, key := range []string{"data", "image", "b64_json"} {
		if text, ok := block[key].(string); ok {
			return len(text)
		}
	}
	if source, ok := block["source"].(map[string]any); ok {
		if text, ok := source["data"].(string); ok {
			return len(text)
		}
	}
	return 0
}

// PurgeEmpty 只在调用方确认后删除「没有任何用户/助手内容」的转录，留 .bak。
// 判空是 fail-closed：只要有一行带着工具调用、payload 或其它非空正文，就不删。
func PurgeEmpty(path string) (CleanResult, error) {
	result := CleanResult{Path: path}
	raw, err := os.ReadFile(path)
	if err != nil {
		return result, err
	}
	if hasConversationText(raw) {
		return result, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return result, err
	}
	backup, err := writeUniqueBackup(path, raw, info.Mode().Perm())
	if err != nil {
		return result, err
	}
	if err := os.Remove(path); err != nil {
		return result, err
	}
	result.Purged = true
	result.ReclaimedBytes = int64(len(raw))
	result.BackupPath = backup
	return result, nil
}

// writeUniqueBackup 永远不覆盖上一份备份：第二次清理留下 .bak-1、第三次 .bak-2……
// 否则「已经清过的文件」会把含原图的初版备份盖掉，用户以为原始正文还在。
func writeUniqueBackup(path string, raw []byte, perm os.FileMode) (string, error) {
	for i := 0; ; i++ {
		candidate := path + ".bak"
		if i > 0 {
			candidate = fmt.Sprintf("%s.bak-%d", path, i)
		}
		file, err := os.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := file.Write(raw); err != nil {
			file.Close()
			os.Remove(candidate)
			return "", err
		}
		if err := file.Close(); err != nil {
			return "", err
		}
		return candidate, nil
	}
}

var maintenanceMetadataKeys = map[string]bool{
	"type": true, "role": true, "uuid": true, "parentUuid": true,
	"sessionId": true, "timestamp": true, "cwd": true, "version": true,
	"gitBranch": true, "id": true, "pid": true,
}

func hasConversationText(raw []byte) bool {
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var record any
		if err := json.Unmarshal(line, &record); err != nil {
			// 读不懂的行按「有内容」处理，宁可不删。
			return true
		}
		if hasMeaningfulValue(record) {
			return true
		}
	}
	return false
}

func hasMeaningfulValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(typed) != ""
	case bool:
		return typed
	case float64:
		return typed != 0
	case []any:
		for _, item := range typed {
			if hasMeaningfulValue(item) {
				return true
			}
		}
		return false
	case map[string]any:
		for key, item := range typed {
			if maintenanceMetadataKeys[key] {
				continue
			}
			if hasMeaningfulValue(item) {
				return true
			}
		}
		return false
	default:
		return true
	}
}
