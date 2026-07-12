package server

import (
	"context"
	"net/http"
	"time"
)

const healthReadyTimeout = time.Second

func (a *App) handleReady(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), healthReadyTimeout)
	defer cancel()

	if reason := a.readinessFailureReason(ctx); reason != "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "reason": reason})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (a *App) readinessFailureReason(ctx context.Context) string {
	var databaseProbe int
	if err := a.store.db.QueryRowContext(ctx, `SELECT 1`).Scan(&databaseProbe); err != nil || databaseProbe != 1 {
		return "database"
	}

	var migrationCount, minimumMigration, maximumMigration int
	if err := a.store.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(MIN(version),0),COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&migrationCount, &minimumMigration, &maximumMigration); err != nil ||
		migrationCount != schemaVersion || minimumMigration != 1 || maximumMigration != schemaVersion {
		return "schema"
	}

	state, err := a.store.InstallationState(ctx)
	if err != nil || state != "installed" {
		return "installation"
	}

	var activeOperations int
	if err := a.store.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM maintenance_jobs WHERE status='running') +
		(SELECT COUNT(*) FROM backup_records WHERE status='restoring')`).Scan(&activeOperations); err != nil || activeOperations != 0 {
		return "maintenance"
	}
	return ""
}
