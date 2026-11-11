package codexsync

import "errors"

// WithSyncLock serializes API and backup writes to .agents with fetch/rebase/push.
func WithSyncLock(paths Paths, write func() error) error {
	if write == nil {
		return errors.New("Codex sync write operation is missing")
	}
	release, err := acquireLock(paths.StateDir)
	if err != nil {
		return err
	}
	defer release()
	return write()
}
