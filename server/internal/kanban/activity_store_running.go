package kanban

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// MarkRunningIfActive moves a pending/waiting reply to running without
// overwriting a terminal status. The workflow start runs in a goroutine, so a
// completion reported by the runtime may land before the start path gets to
// write running; terminal states always win.
func (s *ActivityStore) MarkRunningIfActive(ctx context.Context, id string) (bool, error) {
	id = strings.TrimSpace(id)
	res, err := s.db.ExecContext(ctx,
		`UPDATE task_activities SET status = ?, updated_at = ?
		 WHERE id = ? AND status IN (?, ?)`,
		ActivityStatusRunning, time.Now().UTC().Format(time.RFC3339Nano),
		id, ActivityStatusPending, ActivityStatusWaitingUser)
	if err != nil {
		return false, err
	}
	updated, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if updated > 0 {
		return true, nil
	}
	var exists int
	err = s.db.QueryRowContext(ctx, `SELECT 1 FROM task_activities WHERE id = ?`, id).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrActivityNotFound
	}
	if err != nil {
		return false, err
	}
	return false, nil
}
