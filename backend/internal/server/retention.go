package server

import (
	"context"
	"database/sql"
	"time"
)

type RetentionResult struct {
	Skipped                        bool  `json:"skipped,omitempty"`
	ProfileVersionsDeleted         int64 `json:"profileVersionsDeleted"`
	AccessLogsDeleted              int64 `json:"accessLogsDeleted"`
	AdminAuditLogsDeleted          int64 `json:"adminAuditLogsDeleted"`
	SyncAttemptsDeleted            int64 `json:"syncAttemptsDeleted"`
	SyncMutationsDeleted           int64 `json:"syncMutationsDeleted"`
	ResolvedConflictsDeleted       int64 `json:"resolvedConflictsDeleted"`
	IdempotencyResponsesDeleted    int64 `json:"idempotencyResponsesDeleted"`
	EmailVerificationTokensDeleted int64 `json:"emailVerificationTokensDeleted"`
	PasswordResetTokensDeleted     int64 `json:"passwordResetTokensDeleted"`
	PluginSessionsDeleted          int64 `json:"pluginSessionsDeleted"`
	AdminSessionsDeleted           int64 `json:"adminSessionsDeleted"`
	AdminLoginSessionsDeleted      int64 `json:"adminLoginSessionsDeleted"`
	InstallSessionsDeleted         int64 `json:"installSessionsDeleted"`
	AccessTokensDeleted            int64 `json:"accessTokensDeleted"`
	RefreshTokensDeleted           int64 `json:"refreshTokensDeleted"`
	RefreshFamiliesDeleted         int64 `json:"refreshFamiliesDeleted"`
	DevicesDeleted                 int64 `json:"devicesDeleted"`
}

func (s *Store) RunRetention(ctx context.Context, now time.Time) (RetentionResult, error) {
	return s.runRetention(ctx, now, "")
}

func (s *Store) runRetention(ctx context.Context, now time.Time, excludedMaintenanceJobID string) (RetentionResult, error) {
	s.maintenanceMu.Lock()
	defer s.maintenanceMu.Unlock()
	return s.runRetentionLocked(ctx, now, excludedMaintenanceJobID)
}

func (s *Store) runRetentionLocked(ctx context.Context, now time.Time, excludedMaintenanceJobID string) (RetentionResult, error) {
	versionsPerUser, err := s.PersistedLimit(ctx, "versionsPerUser", 50)
	if err != nil {
		return RetentionResult{}, err
	}
	accessLogDays, err := s.PersistedLimit(ctx, "accessLogDays", 30)
	if err != nil {
		return RetentionResult{}, err
	}
	auditLogDays, err := s.PersistedLimit(ctx, "auditLogDays", 180)
	if err != nil {
		return RetentionResult{}, err
	}
	accessCutoff := now.UTC().Add(-time.Duration(accessLogDays) * 24 * time.Hour).Format(time.RFC3339Nano)
	auditCutoff := now.UTC().Add(-time.Duration(auditLogDays) * 24 * time.Hour).Format(time.RFC3339Nano)
	idempotencyCutoff := now.UTC().Add(-24 * time.Hour).Format(time.RFC3339Nano)
	mutationEvidenceCutoff := now.UTC().Add(-90 * 24 * time.Hour).Format(time.RFC3339Nano)
	nowCutoff := now.UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RetentionResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	active, err := persistedMaintenanceActiveExceptWithQuerier(ctx, tx, excludedMaintenanceJobID)
	if err != nil {
		return RetentionResult{}, err
	}
	if active {
		return RetentionResult{Skipped: true}, nil
	}
	result := RetentionResult{}
	if result.ProfileVersionsDeleted, err = execRetentionDelete(ctx, tx, `DELETE FROM sync_profile_versions WHERE id IN (
		SELECT id FROM (
			SELECT id, ROW_NUMBER() OVER (PARTITION BY user_id ORDER BY version DESC) AS retention_rank
			FROM sync_profile_versions
		) WHERE retention_rank > ?
	)`, versionsPerUser); err != nil {
		return RetentionResult{}, err
	}
	if result.AccessLogsDeleted, err = execRetentionDelete(ctx, tx, `DELETE FROM api_logs WHERE created_at < ?`, accessCutoff); err != nil {
		return RetentionResult{}, err
	}
	if result.AdminAuditLogsDeleted, err = execRetentionDelete(ctx, tx, `DELETE FROM admin_audit_logs WHERE created_at < ?`, auditCutoff); err != nil {
		return RetentionResult{}, err
	}
	if result.SyncAttemptsDeleted, err = execRetentionDelete(ctx, tx, `DELETE FROM sync_attempts WHERE created_at < ?`, accessCutoff); err != nil {
		return RetentionResult{}, err
	}
	if result.SyncMutationsDeleted, err = execRetentionDelete(ctx, tx, `DELETE FROM sync_mutations WHERE created_at < ?`, mutationEvidenceCutoff); err != nil {
		return RetentionResult{}, err
	}
	if result.ResolvedConflictsDeleted, err = execRetentionDelete(ctx, tx, `DELETE FROM sync_conflicts WHERE status = 'resolved' AND resolved_at <> '' AND resolved_at < ?`, auditCutoff); err != nil {
		return RetentionResult{}, err
	}
	if result.IdempotencyResponsesDeleted, err = execRetentionDelete(ctx, tx, `DELETE FROM idempotency_keys WHERE created_at < ?`, idempotencyCutoff); err != nil {
		return RetentionResult{}, err
	}
	if result.EmailVerificationTokensDeleted, err = execRetentionDelete(ctx, tx, `DELETE FROM email_verification_tokens WHERE expires_at <= ? OR consumed_at <> ''`, nowCutoff); err != nil {
		return RetentionResult{}, err
	}
	if result.PasswordResetTokensDeleted, err = execRetentionDelete(ctx, tx, `DELETE FROM password_reset_tokens WHERE expires_at <= ? OR consumed_at <> ''`, nowCutoff); err != nil {
		return RetentionResult{}, err
	}
	if result.PluginSessionsDeleted, err = execRetentionDelete(ctx, tx, `DELETE FROM sessions WHERE expires_at <= ?`, nowCutoff); err != nil {
		return RetentionResult{}, err
	}
	if result.AdminSessionsDeleted, err = execRetentionDelete(ctx, tx, `DELETE FROM admin_sessions WHERE expires_at <= ?`, nowCutoff); err != nil {
		return RetentionResult{}, err
	}
	if result.AdminLoginSessionsDeleted, err = execRetentionDelete(ctx, tx, `DELETE FROM admin_login_sessions WHERE expires_at <= ?`, nowCutoff); err != nil {
		return RetentionResult{}, err
	}
	if result.InstallSessionsDeleted, err = execRetentionDelete(ctx, tx, `DELETE FROM install_sessions WHERE expires_at <= ? OR absolute_expires_at <= ?`, nowCutoff, nowCutoff); err != nil {
		return RetentionResult{}, err
	}
	if result.AccessTokensDeleted, err = execRetentionDelete(ctx, tx, `DELETE FROM access_tokens WHERE expires_at <= ? OR revoked_at <> ''`, nowCutoff); err != nil {
		return RetentionResult{}, err
	}
	if result.RefreshTokensDeleted, err = execRetentionDelete(ctx, tx, `DELETE FROM refresh_tokens WHERE expires_at <= ?`, nowCutoff); err != nil {
		return RetentionResult{}, err
	}
	if result.RefreshFamiliesDeleted, err = execRetentionDelete(ctx, tx, `DELETE FROM refresh_token_families WHERE expires_at <= ? OR revoked_at <> ''`, nowCutoff); err != nil {
		return RetentionResult{}, err
	}
	if result.DevicesDeleted, err = execRetentionDelete(ctx, tx, `DELETE FROM devices WHERE last_seen_at < ?`, mutationEvidenceCutoff); err != nil {
		return RetentionResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return RetentionResult{}, err
	}
	return result, nil
}

// RunRetentionScheduler performs retention once at startup and then at the
// configured interval until ctx is canceled. It blocks so the caller controls
// the goroutine lifecycle explicitly.
func (s *Store) RunRetentionScheduler(ctx context.Context, interval time.Duration, report func(RetentionResult, error)) {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	run := func() {
		result, err := s.RunRetention(ctx, time.Now().UTC())
		if report != nil {
			report(result, err)
		}
	}
	select {
	case <-ctx.Done():
		return
	default:
		run()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func execRetentionDelete(ctx context.Context, tx *sql.Tx, statement string, args ...any) (int64, error) {
	result, err := tx.ExecContext(ctx, statement, args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
