package server

import (
	"context"
	"fmt"
)

// DevelopmentAccounts are the local-only accounts created by the dev command.
// They are intentionally supplied by the command rather than hard-coded.
type DevelopmentAccounts struct {
	AdminEmail        string
	PluginEmail       string
	Password          string
	AllowWeakPassword bool // Set only by the loopback-only dev CLI.
}

// EnsureDevelopmentInstallation atomically prepares an otherwise empty store
// for loopback-only development. It deliberately does not alter stores that
// have already been installed, nor does it relax the regular installation flow.
func (s *Store) EnsureDevelopmentInstallation(ctx context.Context, accounts DevelopmentAccounts) error {
	accounts.AdminEmail = normalizeEmail(accounts.AdminEmail)
	accounts.PluginEmail = normalizeEmail(accounts.PluginEmail)
	if !validEmail(accounts.AdminEmail) || !validEmail(accounts.PluginEmail) || accounts.AdminEmail == accounts.PluginEmail || !meetsMinimumPluginPasswordLength(accounts.Password) {
		return fmt.Errorf("invalid local development accounts")
	}
	if err := validatePluginPassword(accounts.Password); err != nil && !accounts.AllowWeakPassword {
		return fmt.Errorf("invalid local development password: %w", err)
	}

	state, err := s.InstallationState(ctx)
	if err != nil {
		return err
	}
	if state != "installed" && state != "uninitialized" {
		return fmt.Errorf("local development initialization requires an uninitialized store")
	}

	adminHash, err := hashPasswordContext(ctx, accounts.Password)
	if err != nil {
		return err
	}
	pluginHash, err := hashPasswordContext(ctx, accounts.Password)
	if err != nil {
		return err
	}
	if state == "installed" {
		return s.resetDevelopmentAccounts(ctx, accounts, adminHash, pluginHash)
	}
	now := nowString()
	admin := AdminUser{
		ID:          newID("admin_"),
		Email:       accounts.AdminEmail,
		DisplayName: "Local developer",
		Status:      "active",
		CreatedAt:   now,
	}
	pluginID := newID("user_")

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var lockedState string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM installation_state WHERE id = 1`).Scan(&lockedState); err != nil {
		return err
	}
	if lockedState != "uninitialized" {
		if lockedState == "installed" {
			return nil
		}
		return fmt.Errorf("local development initialization cannot start from state %s", lockedState)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO admin_users (id, email, display_name, password_hash, status, created_at, updated_at) VALUES (?, ?, ?, ?, 'active', ?, ?)`,
		admin.ID, admin.Email, admin.DisplayName, adminHash, now, now,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, role, created_at, last_login_at, status, email_verified_at, updated_at)
		 VALUES (?, ?, ?, 'user', ?, '', 'active', ?, ?)`,
		pluginID, accounts.PluginEmail, pluginHash, now, now, now,
	); err != nil {
		return err
	}
	for key, value := range map[string]string{
		"external_base_url":    "http://127.0.0.1:8787",
		"allowed_origins":      "",
		"extension_ids":        "[]",
		"registration_enabled": "false",
		"limits":               "{}",
		"smtp_config":          "null",
	} {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			key, value, now,
		); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE installation_state SET state = 'installed', updated_at = ? WHERE id = 1`, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) resetDevelopmentAccounts(ctx context.Context, accounts DevelopmentAccounts, adminHash, pluginHash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := nowString()
	adminResult, err := tx.ExecContext(ctx, `UPDATE admin_users SET password_hash = ?, updated_at = ? WHERE email = ? AND status = 'active'`, adminHash, now, accounts.AdminEmail)
	if err != nil {
		return err
	}
	if updated, err := adminResult.RowsAffected(); err != nil || updated != 1 {
		return fmt.Errorf("local development administrator is missing")
	}
	pluginResult, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE email = ? AND role = 'user' AND status = 'active'`, pluginHash, now, accounts.PluginEmail)
	if err != nil {
		return err
	}
	if updated, err := pluginResult.RowsAffected(); err != nil || updated != 1 {
		return fmt.Errorf("local development plugin user is missing")
	}
	return tx.Commit()
}
