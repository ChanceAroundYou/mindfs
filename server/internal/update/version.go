package update

import (
	"regexp"
	"strconv"
	"strings"
)

func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	return v
}

func normalizeTag(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return ""
	}
	return "v" + v
}

func parseReleaseNotesVersion(text string) string {
	firstLine := strings.TrimSpace(strings.TrimSuffix(strings.SplitN(text, "\n", 2)[0], "\r"))
	const prefix = "# MindFS "
	if !strings.HasPrefix(firstLine, prefix) {
		return ""
	}
	fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(firstLine, prefix)))
	if len(fields) == 0 {
		return ""
	}
	version := fields[0]
	if _, ok := parseVersionParts(version); ok {
		return version
	}
	return ""
}

func latestReleaseNotesBody(text string) string {
	lines := strings.SplitAfter(text, "\n")
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(strings.TrimSuffix(lines[i], "\r"))
		if strings.HasPrefix(line, "# MindFS ") && parseReleaseNotesVersion(line) != "" {
			return strings.TrimSpace(strings.Join(lines[:i], ""))
		}
	}
	return strings.TrimSpace(text)
}

func isNewerVersion(latest, current string) bool {
	latestParts, latestOK := parseVersionParts(latest)
	if !latestOK {
		return false
	}
	currentParts, currentOK := parseVersionParts(current)
	if !currentOK {
		return true
	}
	maxLen := len(latestParts)
	if len(currentParts) > maxLen {
		maxLen = len(currentParts)
	}
	for i := 0; i < maxLen; i++ {
		var latestPart, currentPart int
		if i < len(latestParts) {
			latestPart = latestParts[i]
		}
		if i < len(currentParts) {
			currentPart = currentParts[i]
		}
		if latestPart > currentPart {
			return true
		}
		if latestPart < currentPart {
			return false
		}
	}
	return false
}

// describeDecoration 是 git describe 追加在标签后面的装饰：<commits>-g<hash>。
// 必须整块剥掉：v0.5.5-1115-1-gabc1234 的标签身份是 v0.5.5-1115，
// 逐段解析会把装饰里的提交数读成一个版本段。
var describeDecoration = regexp.MustCompile(`-[0-9]+-g[0-9a-fA-F]+$`)

// parseVersionParts 把版本身份切成整数段。
//
// 关键点：本 fork 的发布标签带构建号（v0.5.5-1115，见 Makefile 的 TAG），所以 '-' 与 '.'
// 一样是分段符。旧实现把版本截断到第一个非数字/点，于是 v0.5.5-1115 与 v0.5.5-1116 都变成
// [0,5,5] —— 同一 semver 核心下的构建号更新对更新器完全不可见，装了 1115 的节点永远显示
// 「已是最新」，更新按钮点了也只回「no update available」。
//
// 遇到非数字段（rc1、裸哈希、dev）就停止：那部分不是可比较的版本段，
// 但已经解析出来的前导段仍然有效（保持旧行为，不把整个版本判为无效）。
func parseVersionParts(v string) ([]int, bool) {
	v = normalizeVersion(v)
	v = strings.TrimSuffix(v, "-dirty")
	v = describeDecoration.ReplaceAllString(v, "")
	if v == "" {
		return nil, false
	}
	segments := strings.FieldsFunc(v, func(r rune) bool { return r == '.' || r == '-' })
	parts := make([]int, 0, len(segments))
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		value, err := strconv.Atoi(segment)
		if err != nil {
			break
		}
		parts = append(parts, value)
	}
	return parts, len(parts) > 0
}
