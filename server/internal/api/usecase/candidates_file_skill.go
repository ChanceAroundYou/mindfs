package usecase

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	rootfs "mindfs/server/internal/fs"

	"gopkg.in/yaml.v3"
)

type FileCandidateProvider struct{}

func NewFileCandidateProvider() *FileCandidateProvider {
	return &FileCandidateProvider{}
}

type SkillCandidateProvider struct{}

func NewSkillCandidateProvider() *SkillCandidateProvider {
	return &SkillCandidateProvider{}
}

func discoverSkillCandidates(ctx context.Context, dir, query string) ([]CandidateItem, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if isMissingSkillScanDir(err) {
			return nil, nil
		}
		return nil, err
	}
	items := make([]CandidateItem, 0, len(entries))
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if !isSkillDirectoryEntry(dir, entry) {
			continue
		}
		name := entry.Name()
		if name == "" || strings.HasPrefix(name, ".") {
			continue
		}
		skillDir := filepath.Join(dir, name)
		skillPath := filepath.Join(skillDir, "SKILL.md")
		if skillFileExists(skillPath) {
			if matchesCandidateName(name, query) {
				items = append(items, CandidateItem{
					Type:        CandidateTypeSkill,
					Name:        name,
					Description: readSkillDescription(skillPath),
				})
			}
			continue
		}
		namespacedItems, err := discoverNamespacedSkillCandidates(ctx, name, skillDir, query)
		if err != nil {
			return nil, err
		}
		items = append(items, namespacedItems...)
	}
	return items, nil
}

func discoverNamespacedSkillCandidates(ctx context.Context, namespace, dir, query string) ([]CandidateItem, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if isMissingSkillScanDir(err) {
			return nil, nil
		}
		return nil, err
	}
	items := make([]CandidateItem, 0, len(entries))
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if !isSkillDirectoryEntry(dir, entry) {
			continue
		}
		childName := entry.Name()
		if childName == "" || strings.HasPrefix(childName, ".") {
			continue
		}
		candidateName := namespace + ":" + childName
		if !matchesCandidateName(candidateName, query) && !matchesCandidateName(childName, query) && !matchesCandidateName(namespace, query) {
			continue
		}
		skillPath := filepath.Join(dir, childName, "SKILL.md")
		if !skillFileExists(skillPath) {
			continue
		}
		items = append(items, CandidateItem{
			Type:        CandidateTypeSkill,
			Name:        candidateName,
			Description: readSkillDescription(skillPath),
		})
	}
	return items, nil
}

func isMissingSkillScanDir(err error) bool {
	return os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR)
}

func skillFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func isSkillDirectoryEntry(parent string, entry os.DirEntry) bool {
	if entry.IsDir() {
		return true
	}
	if entry.Type()&fs.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(filepath.Join(parent, entry.Name()))
	if err != nil {
		return false
	}
	return info.IsDir()
}

func skillScanDirs(root rootfs.RootInfo, agent string) []string {
	homeDir, _ := os.UserHomeDir()
	rootDir, _ := root.RootDir()
	switch strings.TrimSpace(strings.ToLower(agent)) {
	case "codex":
		dirs := []string{
			filepath.Join(homeDir, ".codex", "skills"),
			filepath.Join(homeDir, ".codex", "skills", ".system"),
			filepath.Join(homeDir, ".agents", "skills"),
			filepath.Join(rootDir, ".agents", "skills"),
			filepath.Join(rootDir, ".codex", "skills"),
		}
		dirs = append(dirs, codexPluginSkillScanDirs(filepath.Join(homeDir, ".codex", "plugins", "cache"))...)
		return dirs
	case "claude":
		dirs := []string{
			filepath.Join(homeDir, ".claude", "skills"),
			filepath.Join(homeDir, ".agents", "skills"),
			filepath.Join(rootDir, ".claude", "skills"),
		}
		marketplacesRoot := filepath.Join(homeDir, ".claude", "plugins", "marketplaces")
		entries, err := os.ReadDir(marketplacesRoot)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				name := entry.Name()
				if name == "" || strings.HasPrefix(name, ".") {
					continue
				}
				dirs = append(dirs, filepath.Join(marketplacesRoot, name, "skills"))
			}
		}
		return dirs
	case "gemini":
		return []string{
			filepath.Join(homeDir, ".gemini", "skills"),
			filepath.Join(homeDir, ".agents", "skills"),
			filepath.Join(rootDir, ".gemini", "skills"),
		}
	default:
		return []string{
			filepath.Join(homeDir, ".agents", "skills"),
		}
	}
}

func codexPluginSkillScanDirs(cacheRoot string) []string {
	marketplaces, err := os.ReadDir(cacheRoot)
	if err != nil {
		return nil
	}
	dirs := make([]string, 0)
	for _, marketplace := range marketplaces {
		if !marketplace.IsDir() {
			continue
		}
		marketplaceName := marketplace.Name()
		if marketplaceName == "" || strings.HasPrefix(marketplaceName, ".") {
			continue
		}
		marketplaceRoot := filepath.Join(cacheRoot, marketplaceName)
		plugins, err := os.ReadDir(marketplaceRoot)
		if err != nil {
			continue
		}
		for _, plugin := range plugins {
			if !isSkillDirectoryEntry(marketplaceRoot, plugin) {
				continue
			}
			pluginName := plugin.Name()
			if pluginName == "" || strings.HasPrefix(pluginName, ".") {
				continue
			}
			pluginRoot := filepath.Join(marketplaceRoot, pluginName)
			version, ok := activeCodexPluginVersion(pluginRoot)
			if !ok {
				continue
			}
			dirs = append(dirs, filepath.Join(pluginRoot, version, "skills"))
		}
	}
	return dirs
}

func activeCodexPluginVersion(pluginRoot string) (string, bool) {
	versions, err := os.ReadDir(pluginRoot)
	if err != nil {
		return "", false
	}
	active := ""
	for _, version := range versions {
		if !isSkillDirectoryEntry(pluginRoot, version) {
			continue
		}
		name := version.Name()
		if !validCodexPluginVersionSegment(name) {
			continue
		}
		if name == "local" {
			return name, true
		}
		if active == "" || compareCodexPluginVersions(active, name) < 0 {
			active = name
		}
	}
	return active, active != ""
}

func validCodexPluginVersionSegment(version string) bool {
	if version == "" || version == "." || version == ".." {
		return false
	}
	for _, r := range version {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		if r == '.' || r == '+' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func compareCodexPluginVersions(left, right string) int {
	if isDottedNumericVersion(left) && isDottedNumericVersion(right) {
		leftParts := strings.Split(left, ".")
		rightParts := strings.Split(right, ".")
		maxParts := len(leftParts)
		if len(rightParts) > maxParts {
			maxParts = len(rightParts)
		}
		for i := 0; i < maxParts; i++ {
			leftPart := 0
			rightPart := 0
			if i < len(leftParts) {
				leftPart = atoiVersionPart(leftParts[i])
			}
			if i < len(rightParts) {
				rightPart = atoiVersionPart(rightParts[i])
			}
			if leftPart < rightPart {
				return -1
			}
			if leftPart > rightPart {
				return 1
			}
		}
		return 0
	}
	return strings.Compare(left, right)
}

func isDottedNumericVersion(version string) bool {
	if version == "" {
		return false
	}
	for _, r := range version {
		if r >= '0' && r <= '9' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func atoiVersionPart(part string) int {
	value := 0
	for _, r := range part {
		if r < '0' || r > '9' {
			return value
		}
		value = value*10 + int(r-'0')
	}
	return value
}

func readSkillDescription(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	content := string(data)
	if !strings.HasPrefix(content, "---\n") {
		return ""
	}
	rest := strings.TrimPrefix(content, "---\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return ""
	}
	var frontmatter struct {
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(rest[:end]), &frontmatter); err != nil {
		return ""
	}
	return strings.TrimSpace(frontmatter.Description)
}
