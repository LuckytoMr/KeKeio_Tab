package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"strings"
	"time"
)

const smtpVerificationTTL = 30 * time.Minute

type AdminUser struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	Status      string `json:"status"`
	CreatedAt   string `json:"createdAt"`
}

type InstallationInput struct {
	Mode                string         `json:"mode"`
	Email               string         `json:"email"`
	DisplayName         string         `json:"displayName"`
	Password            string         `json:"password"`
	ExternalBaseURL     string         `json:"externalBaseUrl"`
	AllowedOrigins      []string       `json:"allowedOrigins"`
	ExtensionIDs        []string       `json:"extensionIds,omitempty"`
	RegistrationEnabled bool           `json:"registrationEnabled,omitempty"`
	Limits              map[string]any `json:"limits,omitempty"`
	SMTP                *SMTPSettings  `json:"smtp,omitempty"`
}

type SMTPSettings struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	TLS      string `json:"tls"`
	From     string `json:"from"`
	Username string `json:"username"`
}

type RuntimeSettings struct {
	PublicBaseURL    string         `json:"publicBaseUrl"`
	AllowedOrigins   []string       `json:"allowedOrigins"`
	RegistrationOpen bool           `json:"registrationOpen"`
	SMTP             *SMTPSettings  `json:"smtp,omitempty"`
	Limits           map[string]any `json:"limits"`
}

func (s *Store) InstallationState(ctx context.Context) (string, error) {
	var state string
	if err := s.db.QueryRowContext(ctx, `SELECT state FROM installation_state WHERE id = 1`).Scan(&state); err != nil {
		return "", err
	}
	return state, nil
}

func (s *Store) RequireAdminReset(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM installation_state WHERE id=1`).Scan(&state); err != nil {
		return err
	}
	if state != "installed" && state != "requires_admin_reset" {
		return fmt.Errorf("admin reset requires an installed service")
	}
	var adminID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM admin_users ORDER BY created_at LIMIT 1`).Scan(&adminID); err != nil {
		return fmt.Errorf("admin reset requires an existing administrator: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE installation_state SET state='requires_admin_reset',updated_at=? WHERE id=1`, nowString()); err != nil {
		return err
	}
	for _, statement := range []string{`DELETE FROM admin_sessions`, `DELETE FROM admin_login_sessions`, `DELETE FROM install_sessions`} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if err := insertAdminAuditTx(ctx, tx, adminID, "auth.admin_reset_required", "admin", adminID, newID("cli_"), "local-cli", map[string]any{"source": "local_cli"}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CreateInstallSession(ctx context.Context, idleTTL, absoluteTTL time.Duration) (string, string, string, error) {
	state, err := s.InstallationState(ctx)
	if err != nil {
		return "", "", "", err
	}
	if state == "installed" {
		return "", "", "", ErrNotFound
	}
	token := newID("inst_")
	csrf := newID("csrf_")
	now := time.Now().UTC()
	expiresAt := now.Add(idleTTL)
	absoluteExpiresAt := now.Add(absoluteTTL)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO install_sessions (token_hash, csrf_hash, created_at, last_seen_at, expires_at, absolute_expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		tokenHash(token), tokenHash(csrf), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), expiresAt.Format(time.RFC3339Nano), absoluteExpiresAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return "", "", "", err
	}
	return token, csrf, expiresAt.Format(time.RFC3339Nano), nil
}

func (s *Store) ValidateInstallSession(ctx context.Context, token, csrf string) error {
	var wantCSRF string
	err := s.db.QueryRowContext(ctx,
		`SELECT csrf_hash FROM install_sessions WHERE token_hash = ? AND expires_at > ?`,
		tokenHash(token), nowString(),
	).Scan(&wantCSRF)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}
	got := tokenHash(csrf)
	if subtle.ConstantTimeCompare([]byte(got), []byte(wantCSRF)) != 1 {
		return ErrUnauthorized
	}
	return s.TouchInstallSession(ctx, token)
}

func (s *Store) TouchInstallSession(ctx context.Context, token string) error {
	var absoluteRaw string
	err := s.db.QueryRowContext(ctx,
		`SELECT absolute_expires_at FROM install_sessions WHERE token_hash = ? AND expires_at > ? AND absolute_expires_at > ?`,
		tokenHash(token), nowString(), nowString(),
	).Scan(&absoluteRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}
	absolute, err := time.Parse(time.RFC3339Nano, absoluteRaw)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	nextExpiry := now.Add(30 * time.Minute)
	if nextExpiry.After(absolute) {
		nextExpiry = absolute
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE install_sessions SET last_seen_at = ?, expires_at = ? WHERE token_hash = ?`,
		now.Format(time.RFC3339Nano), nextExpiry.Format(time.RFC3339Nano), tokenHash(token),
	)
	return err
}

func normalizeSMTPVerificationInput(input SMTPTestInput) (SMTPTestInput, error) {
	input.Host = strings.ToLower(strings.TrimSpace(input.Host))
	input.TLS = strings.ToLower(strings.TrimSpace(input.TLS))
	input.From = normalizeEmail(input.From)
	input.Username = strings.TrimSpace(input.Username)
	input.Recipient = normalizeEmail(input.Recipient)
	if input.Host == "" || input.Port < 1 || input.Port > 65535 || !oneOf(input.TLS, "none", "starttls", "tls") || !validEmail(input.From) {
		return SMTPTestInput{}, fmt.Errorf("invalid SMTP settings")
	}
	return input, nil
}

func smtpVerificationHash(input SMTPTestInput) (string, error) {
	normalized, err := normalizeSMTPVerificationInput(input)
	if err != nil {
		return "", err
	}
	if !validEmail(normalized.Recipient) {
		return "", fmt.Errorf("invalid SMTP recipient")
	}
	canonical := struct {
		Host      string `json:"host"`
		Port      int    `json:"port"`
		TLS       string `json:"tls"`
		From      string `json:"from"`
		Username  string `json:"username"`
		Password  string `json:"password"`
		Recipient string `json:"recipient"`
	}{
		Host: normalized.Host, Port: normalized.Port, TLS: normalized.TLS, From: normalized.From,
		Username: normalized.Username, Password: normalized.Password, Recipient: normalized.Recipient,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func (s *Store) MarkInstallSMTPVerified(ctx context.Context, token, verificationHash string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE install_sessions SET smtp_verified_hash = ?, smtp_verified_at = ? WHERE token_hash = ? AND expires_at > ? AND absolute_expires_at > ?`,
		verificationHash, nowString(), tokenHash(token), nowString(), nowString(),
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return ErrUnauthorized
	}
	return nil
}

func (s *Store) ValidateInstallSMTPVerification(ctx context.Context, token, verificationHash string, ttl time.Duration) error {
	var storedHash, verifiedAtRaw string
	err := s.db.QueryRowContext(ctx,
		`SELECT smtp_verified_hash, smtp_verified_at FROM install_sessions WHERE token_hash = ? AND expires_at > ? AND absolute_expires_at > ?`,
		tokenHash(token), nowString(), nowString(),
	).Scan(&storedHash, &verifiedAtRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if storedHash == "" || subtle.ConstantTimeCompare([]byte(storedHash), []byte(verificationHash)) != 1 {
		return ErrUnauthorized
	}
	verifiedAt, err := time.Parse(time.RFC3339Nano, verifiedAtRaw)
	if err != nil || verifiedAt.Before(time.Now().UTC().Add(-ttl)) || verifiedAt.After(time.Now().UTC().Add(time.Minute)) {
		return ErrUnauthorized
	}
	return nil
}

func (s *Store) BeginInstallation(ctx context.Context, input InstallationInput) (AdminUser, error) {
	return s.applyInstallation(ctx, input, "installing", false)
}

// CommitInstallation applies the database portion of an installation in one
// transaction. The HTTP installation workflow uses this after all external
// prerequisites (notably the atomic secrets write) have succeeded, so a crash
// cannot leave the database permanently stuck in the installing state.
func (s *Store) CommitInstallation(ctx context.Context, input InstallationInput) (AdminUser, error) {
	return s.applyInstallation(ctx, input, "installed", true)
}

func (s *Store) applyInstallation(ctx context.Context, input InstallationInput, finalState string, closeInstallSessions bool) (AdminUser, error) {
	input.Email = normalizeEmail(input.Email)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if !validEmail(input.Email) || input.DisplayName == "" || len(input.Password) < 12 {
		return AdminUser{}, fmt.Errorf("invalid administrator details")
	}
	state, err := s.InstallationState(ctx)
	if err != nil {
		return AdminUser{}, err
	}
	isReset := state == "requires_admin_reset" && input.Mode == "admin_reset"
	if state == "uninitialized" && input.Mode != "fresh_install" && input.Mode != "" {
		return AdminUser{}, fmt.Errorf("fresh installation requires fresh_install mode")
	}
	if state == "requires_admin_reset" && !isReset {
		return AdminUser{}, fmt.Errorf("administrator reset requires admin_reset mode")
	}
	if !isReset {
		baseURL, parseErr := url.Parse(input.ExternalBaseURL)
		if parseErr != nil || baseURL.Scheme != "https" || baseURL.Host == "" || baseURL.User != nil {
			return AdminUser{}, fmt.Errorf("externalBaseUrl must be an absolute HTTPS URL")
		}
		for _, origin := range input.AllowedOrigins {
			if !validAllowedOrigin(origin) {
				return AdminUser{}, fmt.Errorf("invalid allowed origin")
			}
		}
		if input.RegistrationEnabled {
			if input.SMTP == nil || strings.TrimSpace(input.SMTP.Host) == "" || input.SMTP.Port < 1 || input.SMTP.Port > 65535 || !oneOf(input.SMTP.TLS, "none", "starttls", "tls") || !validEmail(normalizeEmail(input.SMTP.From)) {
				return AdminUser{}, fmt.Errorf("valid SMTP settings are required when registration is enabled")
			}
		}
	}
	passwordHash, err := hashPasswordContext(ctx, input.Password)
	if err != nil {
		return AdminUser{}, err
	}
	admin := AdminUser{
		ID:          newID("admin_"),
		Email:       input.Email,
		DisplayName: input.DisplayName,
		Status:      "active",
		CreatedAt:   nowString(),
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminUser{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var lockedState string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM installation_state WHERE id = 1`).Scan(&lockedState); err != nil {
		return AdminUser{}, err
	}
	if lockedState != state || (lockedState != "uninitialized" && lockedState != "requires_admin_reset") {
		return AdminUser{}, fmt.Errorf("installation cannot start from state %s", lockedState)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_sessions`); err != nil {
		return AdminUser{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_users`); err != nil {
		return AdminUser{}, err
	}
	if isReset {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions`); err != nil {
			return AdminUser{}, err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO admin_users (id, email, display_name, password_hash, status, created_at, updated_at) VALUES (?, ?, ?, ?, 'active', ?, ?)`,
		admin.ID, admin.Email, admin.DisplayName, passwordHash, admin.CreatedAt, admin.CreatedAt,
	); err != nil {
		return AdminUser{}, err
	}
	if !isReset {
		origins := strings.Join(input.AllowedOrigins, "\n")
		extensionIDs, _ := json.Marshal(input.ExtensionIDs)
		limits, _ := json.Marshal(input.Limits)
		smtpConfig, _ := json.Marshal(input.SMTP)
		for key, value := range map[string]string{
			"external_base_url":    input.ExternalBaseURL,
			"allowed_origins":      origins,
			"extension_ids":        string(extensionIDs),
			"registration_enabled": fmt.Sprintf("%t", input.RegistrationEnabled),
			"limits":               string(limits),
			"smtp_config":          string(smtpConfig),
		} {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
				key, value, nowString(),
			); err != nil {
				return AdminUser{}, err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE installation_state SET state = ?, updated_at = ? WHERE id = 1`, finalState, nowString()); err != nil {
		return AdminUser{}, err
	}
	if closeInstallSessions {
		if _, err := tx.ExecContext(ctx, `DELETE FROM install_sessions`); err != nil {
			return AdminUser{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return AdminUser{}, err
	}
	return admin, nil
}

func (s *Store) LoadRuntimeSettings(ctx context.Context) (RuntimeSettings, error) {
	settings := RuntimeSettings{Limits: map[string]any{}}
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings WHERE key IN ('external_base_url','allowed_origins','registration_enabled','limits','smtp_config')`)
	if err != nil {
		return settings, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return settings, err
		}
		switch key {
		case "external_base_url":
			settings.PublicBaseURL = value
		case "allowed_origins":
			for _, origin := range strings.Split(value, "\n") {
				if origin = strings.TrimSpace(origin); origin != "" {
					settings.AllowedOrigins = append(settings.AllowedOrigins, origin)
				}
			}
		case "registration_enabled":
			settings.RegistrationOpen = value == "true"
		case "limits":
			if value != "" && value != "null" {
				if err := json.Unmarshal([]byte(value), &settings.Limits); err != nil {
					return settings, err
				}
			}
		case "smtp_config":
			if value != "" && value != "null" {
				var smtp SMTPSettings
				if err := json.Unmarshal([]byte(value), &smtp); err != nil {
					return settings, err
				}
				settings.SMTP = &smtp
			}
		}
	}
	return settings, rows.Err()
}

func (s *Store) FinishInstallation(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx,
		`UPDATE installation_state SET state = 'installed', updated_at = ? WHERE id = 1 AND state = 'installing'`,
		nowString(),
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return fmt.Errorf("installation is not ready to finish")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM install_sessions`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AuthenticateAdmin(ctx context.Context, email, password string) (AdminUser, error) {
	email = normalizeEmail(email)
	var admin AdminUser
	var passwordHash string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, email, display_name, password_hash, status, created_at FROM admin_users WHERE email = ?`, email,
	).Scan(&admin.ID, &admin.Email, &admin.DisplayName, &passwordHash, &admin.Status, &admin.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminUser{}, ErrUnauthorized
	}
	if err != nil {
		return AdminUser{}, err
	}
	valid, legacy := verifyPasswordContext(ctx, passwordHash, password)
	if !valid || admin.Status != "active" {
		return AdminUser{}, ErrUnauthorized
	}
	if legacy {
		if upgraded, err := hashPasswordContext(ctx, password); err == nil {
			_, _ = s.db.ExecContext(ctx, `UPDATE admin_users SET password_hash = ?, updated_at = ? WHERE id = ?`, upgraded, nowString(), admin.ID)
		}
	}
	return admin, nil
}

func (s *Store) CreateAdminLoginSession(ctx context.Context, ttl time.Duration) (string, string, error) {
	token := newID("preauth_")
	csrf := newID("csrf_")
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO admin_login_sessions (token_hash, csrf_hash, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		tokenHash(token), tokenHash(csrf), nowString(), time.Now().UTC().Add(ttl).Format(time.RFC3339Nano),
	)
	return token, csrf, err
}

func (s *Store) ValidateAdminLoginSession(ctx context.Context, token, csrf string) error {
	var wantCSRF string
	err := s.db.QueryRowContext(ctx,
		`SELECT csrf_hash FROM admin_login_sessions WHERE token_hash = ? AND expires_at > ?`, tokenHash(token), nowString(),
	).Scan(&wantCSRF)
	if err != nil || subtle.ConstantTimeCompare([]byte(tokenHash(csrf)), []byte(wantCSRF)) != 1 {
		return ErrUnauthorized
	}
	return nil
}

func (s *Store) ConsumeAdminLoginSession(ctx context.Context, token, csrf string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var wantCSRF string
	err = tx.QueryRowContext(ctx,
		`SELECT csrf_hash FROM admin_login_sessions WHERE token_hash = ? AND expires_at > ?`, tokenHash(token), nowString(),
	).Scan(&wantCSRF)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil || subtle.ConstantTimeCompare([]byte(tokenHash(csrf)), []byte(wantCSRF)) != 1 {
		return ErrUnauthorized
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_login_sessions WHERE token_hash = ?`, tokenHash(token)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CreateAdminSession(ctx context.Context, adminID string, ttl time.Duration, csrfKeys ...[]byte) (string, string, error) {
	token := newID("adminsess_")
	csrf := newID("csrf_")
	if len(csrfKeys) != 0 {
		var err error
		csrf, err = deriveAdminSessionCSRF(csrfKeys[0], token)
		if err != nil {
			return "", "", err
		}
	}
	now := nowString()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO admin_sessions (token_hash, admin_id, csrf_hash, created_at, expires_at, last_seen_at) VALUES (?, ?, ?, ?, ?, ?)`,
		tokenHash(token), adminID, tokenHash(csrf), now, time.Now().UTC().Add(ttl).Format(time.RFC3339Nano), now,
	)
	return token, csrf, err
}

func deriveAdminSessionCSRF(key []byte, sessionToken string) (string, error) {
	if len(key) != 32 || sessionToken == "" {
		return "", fmt.Errorf("admin session CSRF derivation requires a 256-bit key and session token")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("fullpro-admin-session-csrf-v1\x00"))
	_, _ = mac.Write([]byte(sessionToken))
	return "csrf_" + hex.EncodeToString(mac.Sum(nil)), nil
}

func (s *Store) AdminBySession(ctx context.Context, token string) (AdminUser, string, error) {
	var admin AdminUser
	var csrfHash string
	err := s.db.QueryRowContext(ctx,
		`SELECT a.id, a.email, a.display_name, a.status, a.created_at, s.csrf_hash
		 FROM admin_sessions s
		 JOIN admin_users a ON a.id = s.admin_id
		 WHERE s.token_hash = ? AND s.expires_at > ? AND a.status = 'active'`,
		tokenHash(token), nowString(),
	).Scan(&admin.ID, &admin.Email, &admin.DisplayName, &admin.Status, &admin.CreatedAt, &csrfHash)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminUser{}, "", ErrUnauthorized
	}
	if err != nil {
		return AdminUser{}, "", err
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE admin_sessions SET last_seen_at = ? WHERE token_hash = ?`, nowString(), tokenHash(token))
	return admin, csrfHash, nil
}

func (s *Store) RotateAdminSessionCSRF(ctx context.Context, token string) (string, error) {
	csrf := newID("csrf_")
	result, err := s.db.ExecContext(ctx,
		`UPDATE admin_sessions SET csrf_hash = ?, last_seen_at = ? WHERE token_hash = ? AND expires_at > ?`,
		tokenHash(csrf), nowString(), tokenHash(token), nowString(),
	)
	if err != nil {
		return "", err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return "", ErrUnauthorized
	}
	return csrf, nil
}

func (s *Store) AdminSessionCSRF(ctx context.Context, token string, key []byte) (string, error) {
	csrf, err := deriveAdminSessionCSRF(key, token)
	if err != nil {
		return "", err
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE admin_sessions SET csrf_hash = ?, last_seen_at = ? WHERE token_hash = ? AND expires_at > ?`,
		tokenHash(csrf), nowString(), tokenHash(token), nowString(),
	)
	if err != nil {
		return "", err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return "", ErrUnauthorized
	}
	return csrf, nil
}

func (s *Store) DeleteAdminSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM admin_sessions WHERE token_hash = ?`, tokenHash(token))
	return err
}

func (s *Store) InsertAdminAudit(ctx context.Context, adminID, action, targetType, targetID, requestID, ip string, details any) error {
	detailsJSON, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO admin_audit_logs (id,created_at,admin_id,action,target_type,target_id,request_id,ip,details_json) VALUES (?,?,?,?,?,?,?,?,?)`,
		newID("audit_"), nowString(), adminID, action, targetType, targetID, requestID, ip, string(detailsJSON),
	)
	return err
}

func validEmail(email string) bool {
	parsed, err := mail.ParseAddress(email)
	return err == nil && parsed.Address == email && strings.Contains(email, "@") && len(email) <= 254
}

func validAllowedOrigin(origin string) bool {
	parsed, err := url.Parse(strings.TrimSpace(origin))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return parsed.Scheme == "https" || parsed.Scheme == "http" || parsed.Scheme == "chrome-extension" || parsed.Scheme == "moz-extension"
}

func (a *App) handleInstallStatus(w http.ResponseWriter, r *http.Request) {
	state, err := a.store.InstallationState(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "INSTALL_STATE_UNAVAILABLE", "安装状态不可用")
		return
	}
	if state == "installed" {
		http.NotFound(w, r)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]any{"state": state})
}

func (a *App) handleInstallSession(w http.ResponseWriter, r *http.Request) {
	var input struct {
		InstallCode string `json:"installCode"`
	}
	if !a.decodeJSON(w, r, &input) {
		return
	}
	if len(a.config.InstallCode) < 32 || subtle.ConstantTimeCompare([]byte(input.InstallCode), []byte(a.config.InstallCode)) != 1 {
		writeAPIError(w, http.StatusUnauthorized, "INVALID_INSTALL_CODE", "安装码无效")
		return
	}
	token, csrf, expiresAt, err := a.store.CreateInstallSession(r.Context(), 30*time.Minute, 2*time.Hour)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "INSTALL_SESSION_FAILED", "无法创建安装会话")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     a.config.InstallCookieName,
		Value:    token,
		Path:     "/install/",
		HttpOnly: true,
		Secure:   a.secureCookie(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   2 * 60 * 60,
	})
	state, _ := a.store.InstallationState(r.Context())
	mode := "fresh_install"
	if state == "requires_admin_reset" {
		mode = "admin_reset"
	}
	writeAPIData(w, http.StatusCreated, map[string]string{"mode": mode, "csrfToken": csrf, "expiresAt": expiresAt})
}

func (a *App) handleInstallPreflight(w http.ResponseWriter, r *http.Request) {
	if !a.originAllowed(r) {
		writeAPIError(w, http.StatusForbidden, "ORIGIN_REJECTED", "请求来源无效")
		return
	}
	cookie, err := r.Cookie(a.config.InstallCookieName)
	if err != nil || a.store.ValidateInstallSession(r.Context(), cookie.Value, r.Header.Get("X-CSRF-Token")) != nil {
		writeAPIError(w, http.StatusUnauthorized, "INSTALL_SESSION_EXPIRED", "安装会话已失效，请重新输入一次性安装码")
		return
	}
	writeAPIData(w, http.StatusOK, map[string]any{
		"checks": []map[string]string{
			{"label": "database", "status": "pass", "detail": "SQLite 可写，schema migration 可用"},
			{"label": "system_time", "status": "pass", "detail": time.Now().UTC().Format(time.RFC3339)},
			{"label": "https", "status": map[bool]string{true: "pass", false: "warning"}[a.secureCookie(r)], "detail": "非 loopback 部署必须由可信反向代理提供 HTTPS"},
			{"label": "trusted_proxies", "status": "pass", "detail": fmt.Sprintf("configured=%d", len(a.config.TrustedProxyCIDRs))},
		},
		"database":          map[string]any{"writable": true, "schemaVersion": schemaVersion},
		"systemTime":        map[string]any{"valid": time.Now().Year() >= 2024},
		"https":             map[string]any{"configured": a.secureCookie(r)},
		"trustedProxyCount": len(a.config.TrustedProxyCIDRs),
	})
}

func (a *App) handleInstallSMTPTest(w http.ResponseWriter, r *http.Request) {
	if !a.originAllowed(r) {
		writeAPIError(w, http.StatusForbidden, "ORIGIN_REJECTED", "请求来源无效")
		return
	}
	cookie, err := r.Cookie(a.config.InstallCookieName)
	if err != nil || a.store.ValidateInstallSession(r.Context(), cookie.Value, r.Header.Get("X-CSRF-Token")) != nil {
		writeAPIError(w, http.StatusUnauthorized, "INSTALL_SESSION_EXPIRED", "安装会话已失效，请重新输入一次性安装码")
		return
	}
	var input SMTPTestInput
	if !a.decodeJSON(w, r, &input) {
		return
	}
	verificationHash, err := smtpVerificationHash(input)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "SMTP_INVALID", "SMTP 配置或测试收件人无效")
		return
	}
	tester := a.config.SMTPTester
	if tester == nil {
		tester = TestSMTP
	}
	if err := tester(r.Context(), input); err != nil {
		writeAPIError(w, http.StatusBadGateway, "SMTP_TEST_FAILED", "SMTP 测试失败")
		return
	}
	if err := a.store.MarkInstallSMTPVerified(r.Context(), cookie.Value, verificationHash); err != nil {
		writeAPIError(w, http.StatusUnauthorized, "INSTALL_SESSION_EXPIRED", "安装会话已失效，请重新输入一次性安装码")
		return
	}
	writeAPIData(w, http.StatusOK, map[string]bool{"verified": true})
}

func (a *App) handleInstallComplete(w http.ResponseWriter, r *http.Request) {
	if !a.originAllowed(r) {
		writeAPIError(w, http.StatusForbidden, "ORIGIN_REJECTED", "请求来源无效")
		return
	}
	cookie, err := r.Cookie(a.config.InstallCookieName)
	if err != nil || a.store.ValidateInstallSession(r.Context(), cookie.Value, r.Header.Get("X-CSRF-Token")) != nil {
		writeAPIError(w, http.StatusUnauthorized, "INSTALL_SESSION_EXPIRED", "安装会话已失效，请重新输入一次性安装码")
		return
	}
	var payload struct {
		Mode  string `json:"mode"`
		Admin struct {
			Email       string `json:"email"`
			DisplayName string `json:"displayName"`
			Password    string `json:"password"`
		} `json:"admin"`
		PublicAPI struct {
			BaseURL      string   `json:"baseUrl"`
			ExtensionIDs []string `json:"extensionIds"`
			WebOrigins   []string `json:"webOrigins"`
		} `json:"publicApi"`
		Registration struct {
			Enabled bool `json:"enabled"`
		} `json:"registration"`
		SMTP *struct {
			Host     string `json:"host"`
			Port     int    `json:"port"`
			TLS      string `json:"tls"`
			From     string `json:"from"`
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"smtp"`
		Limits          map[string]any `json:"limits"`
		Email           string         `json:"email"`
		DisplayName     string         `json:"displayName"`
		Password        string         `json:"password"`
		ExternalBaseURL string         `json:"externalBaseUrl"`
		AllowedOrigins  []string       `json:"allowedOrigins"`
	}
	if !a.decodeJSON(w, r, &payload) {
		return
	}
	input := InstallationInput{
		Mode:                payload.Mode,
		Email:               payload.Admin.Email,
		DisplayName:         payload.Admin.DisplayName,
		Password:            payload.Admin.Password,
		ExternalBaseURL:     payload.PublicAPI.BaseURL,
		AllowedOrigins:      append([]string(nil), payload.PublicAPI.WebOrigins...),
		ExtensionIDs:        append([]string(nil), payload.PublicAPI.ExtensionIDs...),
		RegistrationEnabled: payload.Registration.Enabled,
		Limits:              payload.Limits,
	}
	if payload.SMTP != nil {
		input.SMTP = &SMTPSettings{Host: payload.SMTP.Host, Port: payload.SMTP.Port, TLS: payload.SMTP.TLS, From: payload.SMTP.From, Username: payload.SMTP.Username}
	}
	if input.Email == "" {
		input.Email = payload.Email
		input.DisplayName = payload.DisplayName
		input.Password = payload.Password
		input.ExternalBaseURL = payload.ExternalBaseURL
		input.AllowedOrigins = append([]string(nil), payload.AllowedOrigins...)
	}
	for _, extensionID := range input.ExtensionIDs {
		input.AllowedOrigins = append(input.AllowedOrigins, "chrome-extension://"+extensionID)
	}
	if input.RegistrationEnabled {
		if payload.SMTP == nil {
			writeAPIError(w, http.StatusPreconditionFailed, "SMTP_TEST_REQUIRED", "启用开放注册前必须验证 SMTP 配置")
			return
		}
		verificationHash, hashErr := smtpVerificationHash(SMTPTestInput{
			Host: payload.SMTP.Host, Port: payload.SMTP.Port, TLS: payload.SMTP.TLS, From: payload.SMTP.From,
			Username: payload.SMTP.Username, Password: payload.SMTP.Password, Recipient: input.Email,
		})
		if hashErr != nil || a.store.ValidateInstallSMTPVerification(r.Context(), cookie.Value, verificationHash, smtpVerificationTTL) != nil {
			writeAPIError(w, http.StatusPreconditionFailed, "SMTP_TEST_REQUIRED", "SMTP 配置必须由当前安装会话重新测试")
			return
		}
	}
	a.installMu.Lock()
	defer a.installMu.Unlock()
	if err := a.store.ValidateInstallSession(r.Context(), cookie.Value, r.Header.Get("X-CSRF-Token")); err != nil {
		writeAPIError(w, http.StatusConflict, "INSTALL_ALREADY_COMPLETED", "安装已由另一个请求完成或当前会话已失效")
		return
	}
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	var preservedRuntime RuntimeSettings
	var preservedSMTPPassword string
	if input.Mode == "admin_reset" {
		var loadErr error
		preservedRuntime, loadErr = a.store.LoadRuntimeSettings(r.Context())
		if loadErr != nil {
			writeAPIError(w, http.StatusInternalServerError, "RUNTIME_SETTINGS_UNAVAILABLE", "无法加载需保留的运行时设置")
			return
		}
		secrets, _, secretErr := LoadOrCreateSecrets(a.config.SecretsPath)
		if secretErr != nil {
			writeAPIError(w, http.StatusInternalServerError, "SECRETS_WRITE_FAILED", "无法加载需保留的运行时 secrets")
			return
		}
		preservedSMTPPassword = secrets.SMTPPassword
	}
	var previousSecrets Secrets
	secretsChanged := false
	if payload.SMTP != nil && payload.SMTP.Password != "" {
		secrets, _, secretErr := LoadOrCreateSecrets(a.config.SecretsPath)
		if secretErr != nil {
			writeAPIError(w, http.StatusInternalServerError, "SECRETS_WRITE_FAILED", "无法加载 secrets 文件")
			return
		}
		previousSecrets = secrets
		secrets.SMTPPassword = payload.SMTP.Password
		if secretErr := SaveSecrets(a.config.SecretsPath, secrets); secretErr != nil {
			writeAPIError(w, http.StatusInternalServerError, "SECRETS_WRITE_FAILED", "无法保存 SMTP secret")
			return
		}
		secretsChanged = true
	}
	if a.beforeInstallCommit != nil {
		a.beforeInstallCommit()
	}
	admin, err := a.store.CommitInstallation(r.Context(), input)
	if err != nil {
		if secretsChanged {
			_ = SaveSecrets(a.config.SecretsPath, previousSecrets)
		}
		writeAPIError(w, http.StatusBadRequest, "INSTALL_INVALID", err.Error())
		return
	}
	smtpPassword := ""
	if payload.SMTP != nil {
		smtpPassword = payload.SMTP.Password
	}
	if input.Mode == "admin_reset" {
		a.applyRuntimeSettings(preservedRuntime, preservedSMTPPassword)
	} else {
		a.applyInstalledRuntime(input, smtpPassword)
	}
	if a.config.InstallCodePath != "" {
		if err := os.Remove(a.config.InstallCodePath); err != nil && !os.IsNotExist(err) {
			writeAPIError(w, http.StatusInternalServerError, "INSTALL_CODE_CLEANUP_FAILED", "安装已完成，但安装码清理失败")
			return
		}
	}
	writeAPIData(w, http.StatusCreated, admin)
}

func (a *App) handleAdminPreauthSession(w http.ResponseWriter, r *http.Request) {
	state, err := a.store.InstallationState(r.Context())
	if err != nil || state != "installed" {
		http.NotFound(w, r)
		return
	}
	if cookie, cookieErr := r.Cookie(a.config.CookieName); cookieErr == nil && cookie.Value != "" {
		if admin, _, adminErr := a.store.AdminBySession(r.Context(), cookie.Value); adminErr == nil {
			csrf, csrfErr := a.store.AdminSessionCSRF(r.Context(), cookie.Value, a.config.TokenDerivationKey)
			if csrfErr != nil {
				writeAPIError(w, http.StatusInternalServerError, "SESSION_FAILED", "无法更新管理员会话")
				return
			}
			writeAPIData(w, http.StatusOK, map[string]any{"authenticated": true, "user": admin, "csrfToken": csrf})
			return
		}
	}
	token, csrf, err := a.store.CreateAdminLoginSession(r.Context(), 10*time.Minute)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "SESSION_FAILED", "无法创建登录会话")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     strings.TrimSuffix(a.config.CookieName, "_session") + "_preauth",
		Value:    token,
		Path:     "/api/admin/v1/auth/",
		HttpOnly: true,
		Secure:   a.secureCookie(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   10 * 60,
	})
	writeAPIData(w, http.StatusOK, map[string]any{"authenticated": false, "user": nil, "csrfToken": csrf})
}

func (a *App) handleAdminLoginV1(w http.ResponseWriter, r *http.Request) {
	if !a.originAllowed(r) {
		writeAPIError(w, http.StatusForbidden, "ORIGIN_REJECTED", "请求来源无效")
		return
	}
	preauthName := strings.TrimSuffix(a.config.CookieName, "_session") + "_preauth"
	cookie, err := r.Cookie(preauthName)
	if err != nil || a.store.ValidateAdminLoginSession(r.Context(), cookie.Value, r.Header.Get("X-CSRF-Token")) != nil {
		writeAPIError(w, http.StatusForbidden, "CSRF_REJECTED", "登录会话或 CSRF 无效")
		return
	}
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !a.decodeJSON(w, r, &input) {
		return
	}
	admin, err := a.store.AuthenticateAdmin(r.Context(), input.Email, input.Password)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "邮箱或密码错误")
		return
	}
	if a.store.ConsumeAdminLoginSession(r.Context(), cookie.Value, r.Header.Get("X-CSRF-Token")) != nil {
		writeAPIError(w, http.StatusForbidden, "CSRF_REJECTED", "登录会话或 CSRF 已被使用")
		return
	}
	token, csrf, err := a.store.CreateAdminSession(r.Context(), admin.ID, 12*time.Hour, a.config.TokenDerivationKey)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "SESSION_FAILED", "无法创建管理员会话")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     a.config.CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.secureCookie(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   12 * 60 * 60,
	})
	writeAPIData(w, http.StatusOK, map[string]any{"user": admin, "csrfToken": csrf})
}

type adminContextKey struct{}

func (a *App) requireAdminV1(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(a.config.CookieName)
		if err != nil || cookie.Value == "" {
			writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "管理员会话无效")
			return
		}
		admin, csrfHash, err := a.store.AdminBySession(r.Context(), cookie.Value)
		if err != nil {
			writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "管理员会话无效")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !a.originAllowed(r) {
				writeAPIError(w, http.StatusForbidden, "ORIGIN_REJECTED", "请求来源无效")
				return
			}
			if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
				writeAPIError(w, http.StatusUnsupportedMediaType, "JSON_REQUIRED", "写请求必须使用 application/json")
				return
			}
			if subtle.ConstantTimeCompare([]byte(tokenHash(r.Header.Get("X-CSRF-Token"))), []byte(csrfHash)) != 1 {
				writeAPIError(w, http.StatusForbidden, "CSRF_REJECTED", "CSRF token 无效")
				return
			}
		}
		next(w, r.WithContext(context.WithValue(r.Context(), adminContextKey{}, admin)))
	}
}

func adminFromContext(ctx context.Context) (AdminUser, bool) {
	admin, ok := ctx.Value(adminContextKey{}).(AdminUser)
	return admin, ok
}

func (a *App) handleAdminLogoutV1(w http.ResponseWriter, r *http.Request) {
	admin, _ := adminFromContext(r.Context())
	cookie, _ := r.Cookie(a.config.CookieName)
	if err := a.store.DeleteAdminSession(r.Context(), cookie.Value); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "LOGOUT_FAILED", "无法撤销管理员会话")
		return
	}
	ip, _ := a.clientIP(r)
	_ = a.store.InsertAdminAudit(r.Context(), admin.ID, "auth.logout", "admin_session", "current", newID("req_"), ip.String(), map[string]any{})
	http.SetCookie(w, &http.Cookie{
		Name: a.config.CookieName, Value: "", Path: "/", HttpOnly: true, Secure: a.secureCookie(r),
		SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0),
	})
	w.WriteHeader(http.StatusNoContent)
}
