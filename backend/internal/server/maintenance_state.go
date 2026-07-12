package server

import (
	"context"
	"database/sql"
	"fmt"
)

type maintenanceStateQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// persistedMaintenanceActive is deliberately fail-closed at each caller. The
// admin tables are initialized lazily, so a missing table means no persisted
// admin operation rather than an error.
func (s *Store) persistedMaintenanceActive(ctx context.Context) (bool, error) {
	return s.persistedMaintenanceActiveExcept(ctx, "")
}

func (s *Store) persistedMaintenanceActiveExcept(ctx context.Context, excludedMaintenanceJobID string) (bool, error) {
	return persistedMaintenanceActiveExceptWithQuerier(ctx, s.db, excludedMaintenanceJobID)
}

func persistedMaintenanceActiveExceptWithQuerier(ctx context.Context, querier maintenanceStateQuerier, excludedMaintenanceJobID string) (bool, error) {
	for _, candidate := range []struct {
		table  string
		status string
	}{
		{table: "maintenance_jobs", status: "running"},
		{table: "backup_records", status: "restoring"},
	} {
		var exists int
		if err := querier.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, candidate.table).Scan(&exists); err != nil {
			return false, fmt.Errorf("inspect %s table: %w", candidate.table, err)
		}
		if exists == 0 {
			continue
		}
		var active int
		query := `SELECT COUNT(*) FROM ` + candidate.table + ` WHERE status=?`
		args := []any{candidate.status}
		if candidate.table == "maintenance_jobs" && excludedMaintenanceJobID != "" {
			query += ` AND id<>?`
			args = append(args, excludedMaintenanceJobID)
		}
		if err := querier.QueryRowContext(ctx, query, args...).Scan(&active); err != nil {
			return false, fmt.Errorf("inspect %s state: %w", candidate.table, err)
		}
		if active != 0 {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) claimMaintenanceJob(ctx context.Context, jobID, kind, startedAt string) (bool, error) {
	s.maintenanceMu.Lock()
	defer s.maintenanceMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	active, err := persistedMaintenanceActiveExceptWithQuerier(ctx, tx, "")
	if err != nil {
		return false, err
	}
	if active {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO maintenance_jobs(id,kind,status,detail,error,created_at,started_at,finished_at) VALUES(?,?,'running','','',?,?, '')`, jobID, kind, startedAt, startedAt); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
