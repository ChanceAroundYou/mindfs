package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"mindfs/internal/codexsync"
)

func runAgentConfigSyncCommand(args []string) (bool, error) {
	return runAgentConfigSyncCommandWith(args, os.Stdout, codexsync.Run)
}

func runAgentConfigSyncCommandWith(
	args []string,
	stdout io.Writer,
	run func(context.Context, codexsync.Mode) (codexsync.Result, error),
) (bool, error) {
	if len(args) == 0 || args[0] != "agents-config-sync" {
		return false, nil
	}
	flags := flag.NewFlagSet("agents-config-sync", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	check := flags.Bool("check", false, "inspect local sync state without changing files")
	apply := flags.Bool("apply", false, "migrate local snapshots into shared sources and render local config")
	sync := flags.Bool("sync", false, "fetch, integrate, push, and render shared configuration")
	if err := flags.Parse(args[1:]); err != nil {
		return true, errors.New("usage: mindfs agents-config-sync --check|--apply|--sync")
	}
	if flags.NArg() != 0 || boolCount(*check, *apply, *sync) != 1 {
		return true, errors.New("usage: mindfs agents-config-sync --check|--apply|--sync")
	}
	mode := codexsync.ModeSync
	switch {
	case *check:
		mode = codexsync.ModeCheck
	case *apply:
		mode = codexsync.ModeApply
	}
	result, err := run(context.Background(), mode)
	encoded, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return true, errors.New("could not encode sync status")
	}
	if _, writeErr := fmt.Fprintln(stdout, string(encoded)); writeErr != nil {
		return true, errors.New("could not write sync status")
	}
	return true, err
}

func boolCount(values ...bool) int {
	count := 0
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
}
