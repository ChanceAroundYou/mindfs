// Command router_shadow_report 汇总一份任务库里 Task Router 的 shadow 决策。
//
// 用法：
//
//	go run ./server/scripts/router_shadow_report --db /path/to/task-kanban.db
//	go run ./server/scripts/router_shadow_report --db /path/to/task-kanban.db --json
//
// 只读：以 mode=ro 打开 SQLite，绝不写库。P0 的验收方式是先在隔离实例上把
// Router 设为 shadow 跑一批任务，再用这个脚本核对「有没有用户可见变化之外
// 的新行为」与 agreement / 延迟分布。
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

type report struct {
	DB          string         `json:"db"`
	RootID      string         `json:"root_id,omitempty"`
	Total       int            `json:"total"`
	ByStatus    map[string]int `json:"by_status"`
	ByAgreement map[string]int `json:"by_agreement"`
	ByTrigger   map[string]int `json:"by_trigger"`
	ByActions   map[string]int `json:"by_action_kind"`
	LatencyMS   latencyStats   `json:"latency_ms"`
	TokenTotals tokenTotals    `json:"tokens"`
	Problems    []problem      `json:"problems,omitempty"`
}

type latencyStats struct {
	P50 int `json:"p50"`
	P95 int `json:"p95"`
	Max int `json:"max"`
}

type tokenTotals struct {
	Prompt     int `json:"prompt"`
	Completion int `json:"completion"`
}

type problem struct {
	ID          string `json:"id"`
	TaskID      string `json:"task_id"`
	Status      string `json:"status"`
	Agreement   string `json:"agreement"`
	Error       string `json:"error,omitempty"`
	CreatedAt   string `json:"created_at"`
	LatencyMS   int    `json:"latency_ms"`
	ActionKinds string `json:"action_kinds,omitempty"`
}

func main() {
	dbPath := flag.String("db", "", "task-kanban.db 路径（必填）")
	rootID := flag.String("root", "", "只统计某个 root（可选）")
	jsonOut := flag.Bool("json", false, "输出 JSON")
	problemLimit := flag.Int("problems", 20, "最多列出多少条异常决策")
	flag.Parse()

	path := strings.TrimSpace(*dbPath)
	if path == "" {
		fmt.Fprintln(os.Stderr, "missing --db")
		os.Exit(2)
	}
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(os.Stderr, "db not readable: %v\n", err)
		os.Exit(2)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	result, err := buildReport(db, path, strings.TrimSpace(*rootID), *problemLimit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "report: %v\n", err)
		os.Exit(1)
	}
	if *jsonOut {
		raw, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "encode json: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(raw))
		return
	}
	printReport(result)
}

func buildReport(db *sql.DB, dbPath, rootID string, problemLimit int) (*report, error) {
	query := `SELECT id, task_id, trigger_kind, status, agreement, error, latency_ms,
	                 prompt_tokens, completion_tokens, actions_json, created_at
	          FROM router_decisions`
	args := []any{}
	if rootID != "" {
		query += ` WHERE root_id = ?`
		args = append(args, rootID)
	}
	query += ` ORDER BY created_at DESC`
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := &report{
		DB:          dbPath,
		RootID:      rootID,
		ByStatus:    map[string]int{},
		ByAgreement: map[string]int{},
		ByTrigger:   map[string]int{},
		ByActions:   map[string]int{},
	}
	latencies := make([]int, 0)
	for rows.Next() {
		var (
			id, taskID, trigger, status, agreement, errText, actionsRaw, createdAt string
			latency, promptTokens, completionTokens                                int
		)
		if err := rows.Scan(&id, &taskID, &trigger, &status, &agreement, &errText,
			&latency, &promptTokens, &completionTokens, &actionsRaw, &createdAt); err != nil {
			return nil, err
		}
		result.Total++
		result.ByStatus[status]++
		result.ByAgreement[agreement]++
		result.ByTrigger[trigger]++
		result.LatencyMS.Max = maxInt(result.LatencyMS.Max, latency)
		latencies = append(latencies, latency)
		result.TokenTotals.Prompt += promptTokens
		result.TokenTotals.Completion += completionTokens

		kinds := actionKinds(actionsRaw)
		for _, kind := range kinds {
			result.ByActions[kind]++
		}
		if len(result.Problems) < problemLimit && (errText != "" || status == "fallback" || status == "rejected") {
			result.Problems = append(result.Problems, problem{
				ID: id, TaskID: taskID, Status: status, Agreement: agreement,
				Error: errText, CreatedAt: createdAt, LatencyMS: latency,
				ActionKinds: strings.Join(kinds, ","),
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Ints(latencies)
	result.LatencyMS.P50 = percentile(latencies, 50)
	result.LatencyMS.P95 = percentile(latencies, 95)
	return result, nil
}

func actionKinds(raw string) []string {
	var actions []struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(raw), &actions); err != nil {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(actions))
	for _, action := range actions {
		if action.Kind == "" || seen[action.Kind] {
			continue
		}
		seen[action.Kind] = true
		out = append(out, action.Kind)
	}
	return out
}

func percentile(sorted []int, p int) int {
	if len(sorted) == 0 {
		return 0
	}
	idx := (len(sorted) - 1) * p / 100
	return sorted[idx]
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func printReport(result *report) {
	fmt.Printf("Router shadow report · %s\n", result.DB)
	if result.RootID != "" {
		fmt.Printf("root: %s\n", result.RootID)
	}
	fmt.Printf("total: %d\n", result.Total)
	printCounter("status", result.ByStatus)
	printCounter("agreement", result.ByAgreement)
	printCounter("trigger", result.ByTrigger)
	printCounter("actions", result.ByActions)
	fmt.Printf("latency_ms: p50=%d p95=%d max=%d\n", result.LatencyMS.P50, result.LatencyMS.P95, result.LatencyMS.Max)
	fmt.Printf("tokens: prompt=%d completion=%d\n", result.TokenTotals.Prompt, result.TokenTotals.Completion)
	if len(result.Problems) > 0 {
		fmt.Println("problems:")
		for _, item := range result.Problems {
			line := fmt.Sprintf("  %s task=%s status=%s agreement=%s latency=%dms actions=%s",
				item.ID, item.TaskID, item.Status, item.Agreement, item.LatencyMS, item.ActionKinds)
			if item.Error != "" {
				line += " error=" + firstLine(item.Error, 160)
			}
			fmt.Println(line)
		}
	}
}

func printCounter(label string, counts map[string]int) {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	fmt.Printf("%s: %s\n", label, strings.Join(parts, " "))
}

func firstLine(value string, limit int) string {
	value = strings.TrimSpace(strings.SplitN(value, "\n", 2)[0])
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}
