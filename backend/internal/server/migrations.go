package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const schemaVersion = 5

func (s *Store) applySecurityMigrations(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := applySecurityMigrationsTx(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func applySecurityMigrationsTx(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	var appliedCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version BETWEEN 1 AND ?`, schemaVersion).Scan(&appliedCount); err != nil {
		return err
	}
	if appliedCount == schemaVersion {
		return nil
	}
	var versionOneApplied int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 1`).Scan(&versionOneApplied); err != nil {
		return err
	}
	if versionOneApplied > 0 {
		if err := addInstallSessionSMTPVerificationColumns(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (2, ?)`, nowString()); err != nil {
			return err
		}
		if err := createLegacyProfileBackupTable(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (3, ?)`, nowString()); err != nil {
			return err
		}
		if err := addTokenScopeColumns(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (4, ?)`, nowString()); err != nil {
			return err
		}
		if err := addReleaseLifecycle(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (5, ?)`, nowString()); err != nil {
			return err
		}
		return nil
	}

	statements := []string{
		`CREATE TABLE IF NOT EXISTS installation_state (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			state TEXT NOT NULL CHECK (state IN ('uninitialized','installing','installed','requires_admin_reset')),
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS admin_users (
			id TEXT PRIMARY KEY,
			email TEXT NOT NULL UNIQUE,
			display_name TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			status TEXT NOT NULL CHECK (status IN ('active','disabled')),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS install_sessions (
			token_hash TEXT PRIMARY KEY,
			csrf_hash TEXT NOT NULL,
			created_at TEXT NOT NULL,
			last_seen_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			absolute_expires_at TEXT NOT NULL,
			smtp_verified_hash TEXT NOT NULL DEFAULT '',
			smtp_verified_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS admin_sessions (
			token_hash TEXT PRIMARY KEY,
			admin_id TEXT NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
			csrf_hash TEXT NOT NULL,
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			last_seen_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS admin_login_sessions (
			token_hash TEXT PRIMARY KEY,
			csrf_hash TEXT NOT NULL,
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS admin_audit_logs (
			id TEXT PRIMARY KEY,
			created_at TEXT NOT NULL,
			admin_id TEXT NOT NULL,
			action TEXT NOT NULL,
			target_type TEXT NOT NULL,
			target_id TEXT NOT NULL,
			request_id TEXT NOT NULL,
			ip TEXT NOT NULL,
			details_json TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_admin_audit_created ON admin_audit_logs(created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS email_verification_tokens (
			token_hash TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			consumed_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS password_reset_tokens (
			token_hash TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			consumed_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS refresh_token_families (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			device_id TEXT NOT NULL,
			scope TEXT NOT NULL DEFAULT 'full',
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			revoked_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS refresh_tokens (
			token_hash TEXT PRIMARY KEY,
			family_id TEXT NOT NULL REFERENCES refresh_token_families(id) ON DELETE CASCADE,
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			used_at TEXT NOT NULL DEFAULT '',
			replaced_by_hash TEXT NOT NULL DEFAULT ''
			,rotation_request_id TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_refresh_family ON refresh_tokens(family_id, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS access_tokens (
			token_hash TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			family_id TEXT NOT NULL REFERENCES refresh_token_families(id) ON DELETE CASCADE,
			device_id TEXT NOT NULL,
			scope TEXT NOT NULL DEFAULT 'full',
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			revoked_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_access_user_expires ON access_tokens(user_id, expires_at)`,
		`CREATE TABLE IF NOT EXISTS sync_profiles (
			user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
			profile_json TEXT NOT NULL,
			version INTEGER NOT NULL,
			schema_version INTEGER NOT NULL,
			profile_hash TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sync_profile_versions (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			version INTEGER NOT NULL,
			schema_version INTEGER NOT NULL,
			profile_json TEXT NOT NULL,
			profile_hash TEXT NOT NULL,
			mutation_id TEXT NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE(user_id, version)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sync_versions_user_version ON sync_profile_versions(user_id, version DESC)`,
		`CREATE TABLE IF NOT EXISTS sync_mutations (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			mutation_id TEXT NOT NULL,
			route TEXT NOT NULL,
			request_hash TEXT NOT NULL,
			base_version INTEGER NOT NULL,
			result_version INTEGER NOT NULL,
			status INTEGER NOT NULL,
			response_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE(user_id, mutation_id)
		)`,
		`CREATE TABLE IF NOT EXISTS devices (
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			device_id TEXT NOT NULL,
			first_seen_at TEXT NOT NULL,
			last_seen_at TEXT NOT NULL,
			last_version INTEGER NOT NULL,
			revoked_at TEXT NOT NULL DEFAULT '',
			PRIMARY KEY(user_id, device_id)
		)`,
		`CREATE TABLE IF NOT EXISTS sync_attempts (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			device_id TEXT NOT NULL,
			mutation_id TEXT NOT NULL,
			base_version INTEGER NOT NULL,
			result_version INTEGER NOT NULL,
			status INTEGER NOT NULL,
			error_code TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sync_attempts_created ON sync_attempts(created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS sync_conflicts (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			mutation_id TEXT NOT NULL,
			device_id TEXT NOT NULL,
			base_version INTEGER NOT NULL,
			current_version INTEGER NOT NULL,
			status TEXT NOT NULL CHECK(status IN ('open','resolved')),
			resolved_by_mutation_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			resolved_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sync_conflicts_user_status ON sync_conflicts(user_id, status, created_at DESC)`,
		`CREATE TRIGGER IF NOT EXISTS users_role_insert_guard
			BEFORE INSERT ON users
			WHEN NEW.role <> 'user'
			BEGIN SELECT RAISE(ABORT, 'plugin users cannot be administrators'); END`,
		`CREATE TRIGGER IF NOT EXISTS users_role_update_guard
			BEFORE UPDATE OF role ON users
			WHEN NEW.role <> 'user'
			BEGIN SELECT RAISE(ABORT, 'plugin users cannot be administrators'); END`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("security migration: %w", err)
		}
	}
	for _, column := range []struct {
		name       string
		definition string
	}{
		{"status", `TEXT NOT NULL DEFAULT 'active'`},
		{"email_verified_at", `TEXT NOT NULL DEFAULT ''`},
		{"updated_at", `TEXT NOT NULL DEFAULT ''`},
	} {
		if err := addColumnIfMissing(ctx, tx, "users", column.name, column.definition); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET status = CASE WHEN email_verified_at = '' THEN 'legacy_unverified' ELSE status END, updated_at = CASE WHEN updated_at = '' THEN created_at ELSE updated_at END`); err != nil {
		return err
	}

	var existingState string
	err := tx.QueryRowContext(ctx, `SELECT state FROM installation_state WHERE id = 1`).Scan(&existingState)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) {
		var legacyAdmins int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin' OR email = 'lucky'`).Scan(&legacyAdmins); err != nil {
			return err
		}
		state := "uninitialized"
		if legacyAdmins > 0 {
			state = "requires_admin_reset"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO installation_state (id, state, updated_at) VALUES (1, ?, ?)`, state, nowString()); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE role = 'admin' OR email = 'lucky'`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET role = 'user' WHERE role <> 'user'`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (1, ?)`, nowString()); err != nil {
		return err
	}
	if err := addInstallSessionSMTPVerificationColumns(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (2, ?)`, nowString()); err != nil {
		return err
	}
	if err := createLegacyProfileBackupTable(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (3, ?)`, nowString()); err != nil {
		return err
	}
	if err := addTokenScopeColumns(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (4, ?)`, nowString()); err != nil {
		return err
	}
	if err := addReleaseLifecycle(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (5, ?)`, nowString()); err != nil {
		return err
	}
	return nil
}

func addReleaseLifecycle(ctx context.Context, tx *sql.Tx) error {
	for _, column := range []struct {
		name       string
		definition string
	}{
		{"minimum_version", `TEXT NOT NULL DEFAULT ''`},
		{"schema_version", `INTEGER NOT NULL DEFAULT 2`},
		{"status", `TEXT NOT NULL DEFAULT 'published'`},
		{"updated_at", `TEXT NOT NULL DEFAULT ''`},
		{"published_at", `TEXT NOT NULL DEFAULT ''`},
		{"disabled_at", `TEXT NOT NULL DEFAULT ''`},
	} {
		if err := addColumnIfMissing(ctx, tx, "app_releases", column.name, column.definition); err != nil {
			return err
		}
	}
	for _, statement := range []string{
		`UPDATE app_releases SET status='published' WHERE status=''`,
		`UPDATE app_releases SET updated_at=created_at WHERE updated_at=''`,
		`UPDATE app_releases SET published_at=created_at WHERE status='published' AND published_at=''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_app_releases_channel_version ON app_releases(channel, version)`,
		`CREATE TABLE IF NOT EXISTS release_events (
			id TEXT PRIMARY KEY,
			release_id TEXT NOT NULL REFERENCES app_releases(id) ON DELETE CASCADE,
			action TEXT NOT NULL,
			from_status TEXT NOT NULL,
			to_status TEXT NOT NULL,
			admin_id TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_release_events_release_created ON release_events(release_id, created_at)`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO release_events(id,release_id,action,from_status,to_status,admin_id,request_id,created_at)
		SELECT 'migrated_' || id,id,'migrate','',status,'','migration',updated_at FROM app_releases`); err != nil {
		return err
	}
	return nil
}

func addTokenScopeColumns(ctx context.Context, tx *sql.Tx) error {
	for _, table := range []string{"refresh_token_families", "access_tokens"} {
		if err := addColumnIfMissing(ctx, tx, table, "scope", `TEXT NOT NULL DEFAULT 'full'`); err != nil {
			return err
		}
	}
	return nil
}

func createLegacyProfileBackupTable(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS legacy_profile_backups (
		user_id TEXT PRIMARY KEY,
		profile_json TEXT NOT NULL,
		legacy_version INTEGER NOT NULL,
		legacy_updated_at TEXT NOT NULL,
		archived_at TEXT NOT NULL
	)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO legacy_profile_backups(user_id,profile_json,legacy_version,legacy_updated_at,archived_at)
		SELECT p.user_id,p.profile_json,p.version,p.updated_at,?
		FROM profiles p WHERE EXISTS (SELECT 1 FROM sync_profiles s WHERE s.user_id=p.user_id)`, nowString()); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM profiles WHERE EXISTS (SELECT 1 FROM sync_profiles s WHERE s.user_id=profiles.user_id)`)
	return err
}

func addInstallSessionSMTPVerificationColumns(ctx context.Context, tx *sql.Tx) error {
	for _, column := range []struct {
		name       string
		definition string
	}{
		{"smtp_verified_hash", `TEXT NOT NULL DEFAULT ''`},
		{"smtp_verified_at", `TEXT NOT NULL DEFAULT ''`},
	} {
		if err := addColumnIfMissing(ctx, tx, "install_sessions", column.name, column.definition); err != nil {
			return err
		}
	}
	return nil
}

func addColumnIfMissing(ctx context.Context, tx *sql.Tx, table, column, definition string) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = tx.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+column+` `+definition)
	return err
}
