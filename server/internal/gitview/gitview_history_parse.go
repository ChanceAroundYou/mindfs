package gitview

import (
	"bufio"
	"context"
	"fmt"
	"strings"
)

func (r repoContext) commitNameStatus(ctx context.Context, commit string) ([]porcelainItem, error) {
	output, err := runGit(ctx, r.repoRoot, "diff-tree", "--root", "--no-commit-id", "--name-status", "-r", "-z", "-M", commit)
	if err != nil {
		return nil, err
	}
	return parseNameStatusZ(output), nil
}

func (r repoContext) nameStatusBetween(ctx context.Context, base, target string) ([]porcelainItem, error) {
	output, err := runGit(ctx, r.repoRoot, "diff", "--name-status", "-z", "-M", base, target)
	if err != nil {
		return nil, err
	}
	return parseNameStatusZ(output), nil
}

func parseNameStatusZ(output string) []porcelainItem {
	parts := strings.Split(output, "\x00")
	items := make([]porcelainItem, 0)
	for i := 0; i < len(parts); {
		status := strings.TrimSpace(parts[i])
		i++
		if status == "" {
			continue
		}
		code := status
		if len(code) > 1 {
			code = code[:1]
		}
		normalized := "M"
		switch code {
		case "A":
			normalized = "A"
		case "D":
			normalized = "D"
		case "R", "C":
			normalized = "R"
		}
		if normalized == "R" {
			if i+1 >= len(parts) {
				break
			}
			oldPath := strings.TrimSpace(parts[i])
			newPath := strings.TrimSpace(parts[i+1])
			i += 2
			if newPath != "" {
				items = append(items, porcelainItem{Path: newPath, OldPath: oldPath, Status: normalized})
			}
			continue
		}
		if i >= len(parts) {
			break
		}
		path := strings.TrimSpace(parts[i])
		i++
		if path != "" {
			items = append(items, porcelainItem{Path: path, Status: normalized})
		}
	}
	return items
}

func (r repoContext) commitNumstat(ctx context.Context, commit string) (map[string][2]int, error) {
	output, err := runGit(ctx, r.repoRoot, "diff-tree", "--root", "--no-commit-id", "--numstat", "-r", "-M", commit)
	if err != nil {
		return nil, err
	}
	return parseNumstat(output)
}

func (r repoContext) numstatBetween(ctx context.Context, base, target string) (map[string][2]int, error) {
	output, err := runGit(ctx, r.repoRoot, "diff", "--numstat", "-M", base, target)
	if err != nil {
		return nil, err
	}
	return parseNumstat(output)
}

func parseNumstat(output string) (map[string][2]int, error) {
	result := make(map[string][2]int)
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) < 3 {
			continue
		}
		var add, del int
		if fields[0] != "-" {
			fmt.Sscanf(fields[0], "%d", &add)
		}
		if fields[1] != "-" {
			fmt.Sscanf(fields[1], "%d", &del)
		}
		path := fields[len(fields)-1]
		result[path] = [2]int{add, del}
	}
	return result, scanner.Err()
}
