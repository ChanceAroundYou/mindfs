// Command convert_stages_to_conversation 把一份任务节点 SQLite 里的历史阶段
// 任务转成对话 + WorkflowRun。
//
// 用法：
//
//	go run ./server/scripts/convert_stages_to_conversation --db /path/to/task-kanban.db --dry-run
//	go run ./server/scripts/convert_stages_to_conversation --db /path/to/task-kanban.db
//
// 只应针对**副本**运行；生产库由服务启动时自动执行同一迁移。转换是单向的，
// 旧行会先复制到 tasks_backup_legacy_stages / stage_runs_backup_legacy。
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mindfs/server/internal/kanban"

	_ "modernc.org/sqlite"
)

func main() {
	dbPath := flag.String("db", "", "task-kanban.db 路径（必填）")
	dryRun := flag.Bool("dry-run", false, "只统计，不写库（在临时副本上执行）")
	logPath := flag.String("log", "", "迁移日志路径（默认放在 DB 同目录）")
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

	target := path
	var cleanup func()
	if *dryRun {
		dir, err := os.MkdirTemp("", "mindfs-stage-convert-")
		if err != nil {
			fmt.Fprintf(os.Stderr, "dry-run temp dir: %v\n", err)
			os.Exit(1)
		}
		cleanup = func() { _ = os.RemoveAll(dir) }
		defer cleanup()
		target = filepath.Join(dir, "task-kanban.db")
		if err := vacuumInto(path, target); err != nil {
			fmt.Fprintf(os.Stderr, "dry-run snapshot: %v\n", err)
			os.Exit(1)
		}
	}

	db, err := sql.Open("sqlite", target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx := context.Background()
	// dry-run 在临时副本上补齐 schema，原库一个字节都不动；非 dry-run 直接补齐目标库。
	if err := kanban.MigrateTaskSchema(db); err != nil {
		fmt.Fprintf(os.Stderr, "migrate schema: %v\n", err)
		os.Exit(1)
	}

	stats, err := kanban.ConvertStagesToConversation(ctx, db, kanban.StageConversionOptions{
		DryRun:  *dryRun,
		Now:     time.Now().UTC(),
		LogPath: *logPath,
		Logf: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "convert: %v\n", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(stats); err != nil {
		fmt.Fprintf(os.Stderr, "encode stats: %v\n", err)
		os.Exit(1)
	}
	if *dryRun {
		fmt.Fprintln(os.Stderr, "dry-run: executed on a temporary snapshot, source db unchanged")
	}
}

func vacuumInto(source, target string) error {
	db, err := sql.Open("sqlite", source)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	quoted := strings.ReplaceAll(target, "'", "''")
	_, err = db.Exec("VACUUM INTO '" + quoted + "'")
	return err
}