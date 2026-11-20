package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/kanban"
)

// 适配层只做一件事：把 agent 层的读数换成 kanban 的端口类型。它最容易出的错是
// 字段错位（Input 与 Output 写反）—— 那样数字还在、看着合理，只是全错。

type fakeFoldUsageSource struct {
	rows []agent.FoldUsageRow
	err  error
	got  [2]string
}

func (f *fakeFoldUsageSource) GetSessionFoldUsage(_ context.Context, rootID, key string) ([]agent.FoldUsageRow, error) {
	f.got = [2]string{rootID, key}
	return f.rows, f.err
}

func TestFoldUsageAdapterMapsEveryCounter(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	src := &fakeFoldUsageSource{rows: []agent.FoldUsageRow{{At: at, Input: 11, Output: 22, Cached: 33, Missing: true}}}
	adapter := foldUsageAdapter{source: src}

	got, err := adapter.SessionFoldUsage(context.Background(), "root-1", "sess-1")
	if err != nil {
		t.Fatalf("adapter error: %v", err)
	}
	if src.got != [2]string{"root-1", "sess-1"} {
		t.Fatalf("arguments = %v, want root/session passed through", src.got)
	}
	if len(got) != 1 {
		t.Fatalf("rows = %d, want 1", len(got))
	}
	row := got[0]
	if row.Input != 11 || row.Output != 22 || row.Cached != 33 || !row.Missing || !row.At.Equal(at) {
		t.Fatalf("row = %+v, want input 11 / output 22 / cached 33 / missing / at %v", row, at)
	}
}

func TestFoldUsageAdapterPropagatesErrors(t *testing.T) {
	adapter := foldUsageAdapter{source: &fakeFoldUsageSource{err: errors.New("ledger permission denied")}}
	if _, err := adapter.SessionFoldUsage(context.Background(), "root-1", "sess-1"); err == nil {
		t.Fatal("adapter swallowed the reader error — the completion record would report no data silently")
	}
}

func TestFoldUsageAdapterIsAReader(t *testing.T) {
	var _ kanban.FoldUsageReader = foldUsageAdapter{}
}
