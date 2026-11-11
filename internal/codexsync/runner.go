package codexsync

import (
	"context"
	"errors"
	"sync"
	"time"
)

const defaultSyncTimeout = 2 * time.Minute

type Runner struct {
	run      func(context.Context) (Result, error)
	interval time.Duration
	timeout  time.Duration
	pending  chan struct{}

	mu      sync.RWMutex
	status  Result
	started bool
}

func NewRunner(run func(context.Context) (Result, error), interval, timeout time.Duration) *Runner {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	if timeout <= 0 {
		timeout = defaultSyncTimeout
	}
	return &Runner{
		run:      run,
		interval: interval,
		timeout:  timeout,
		pending:  make(chan struct{}, 1),
		status:   Result{State: "idle", Message: "automatic configuration sync has not run yet"},
	}
}

func (r *Runner) Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return
	}
	r.started = true
	r.mu.Unlock()
	go r.loop(ctx)
	r.enqueue()
}

func (r *Runner) Request(ctx context.Context) Result {
	if ctx != nil && ctx.Err() != nil {
		return r.Status()
	}
	r.mu.Lock()
	started := r.started
	if started {
		r.status.State = "syncing"
		r.status.Message = "configuration sync requested"
	}
	status := cloneResult(r.status)
	r.mu.Unlock()
	if !started {
		return Result{State: "unavailable", Message: "configuration sync runner is not running"}
	}
	r.enqueue()
	return status
}

func (r *Runner) Status() Result {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneResult(r.status)
}

func (r *Runner) loop(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.enqueue()
		case <-r.pending:
			r.runOnce(ctx)
		}
	}
}

func (r *Runner) enqueue() {
	select {
	case r.pending <- struct{}{}:
	default:
	}
}

func (r *Runner) runOnce(parent context.Context) {
	now := time.Now().UTC().Format(time.RFC3339)
	r.mu.Lock()
	previousSuccess := r.status.LastSuccessAt
	r.status.State = "syncing"
	r.status.LastAttemptAt = now
	r.status.Message = "synchronizing shared Codex configuration"
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(parent, r.timeout)
	defer cancel()
	result, err := r.run(ctx)
	if result.State == "" {
		result.State = "pending"
	}
	if !validSyncState(result.State) {
		result.State = "pending"
		result.Message = "sync returned an unknown status; retry later"
	}
	if result.LastAttemptAt == "" {
		result.LastAttemptAt = now
	}
	if result.LastSuccessAt == "" {
		result.LastSuccessAt = previousSuccess
	}
	if err != nil && result.Message == "" {
		result.Message = "sync failed; retry later"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) && result.State == "syncing" {
		result.State = "pending"
		result.Message = "sync timed out; retry later"
	}
	if err == nil && result.State == "synced" && result.LastSuccessAt == previousSuccess {
		result.LastSuccessAt = time.Now().UTC().Format(time.RFC3339)
	}
	r.mu.Lock()
	r.status = cloneResult(result)
	r.mu.Unlock()
}

func validSyncState(state string) bool {
	switch state {
	case "idle", "syncing", "synced", "pending", "conflict", "invalid", "unavailable":
		return true
	default:
		return false
	}
}

func cloneResult(result Result) Result {
	result.CommitIDs = append([]string(nil), result.CommitIDs...)
	return result
}

var processRunner struct {
	sync.Mutex
	runner *Runner
}

func StartRunner(ctx context.Context, interval time.Duration) {
	processRunner.Lock()
	if processRunner.runner != nil {
		processRunner.Unlock()
		return
	}
	runner := NewRunner(func(runCtx context.Context) (Result, error) {
		return Run(runCtx, ModeSync)
	}, interval, defaultSyncTimeout)
	processRunner.runner = runner
	processRunner.Unlock()
	runner.Start(ctx)
	if ctx != nil {
		go func() {
			<-ctx.Done()
			processRunner.Lock()
			if processRunner.runner == runner {
				processRunner.runner = nil
			}
			processRunner.Unlock()
		}()
	}
}

func Request(ctx context.Context) Result {
	processRunner.Lock()
	runner := processRunner.runner
	processRunner.Unlock()
	if runner == nil {
		return Result{State: "unavailable", Message: "configuration sync runner is not running"}
	}
	return runner.Request(ctx)
}

func CurrentStatus() Result {
	processRunner.Lock()
	runner := processRunner.runner
	processRunner.Unlock()
	if runner == nil {
		return Result{State: "idle", Message: "automatic configuration sync has not started"}
	}
	return runner.Status()
}
