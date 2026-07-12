package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type MailMessage struct {
	To      string
	Kind    string
	Token   string
	BaseURL string
}

type Mailer interface {
	Send(context.Context, MailMessage) error
}

type TokenPair struct {
	UserID           string `json:"-"`
	AccessToken      string `json:"accessToken"`
	RefreshToken     string `json:"refreshToken"`
	ExpiresIn        int    `json:"expiresIn"`
	TokenType        string `json:"tokenType"`
	AccessExpiresAt  string `json:"accessExpiresAt"`
	RefreshExpiresAt string `json:"refreshExpiresAt"`
	Scope            string `json:"scope"`
}

const (
	AccessScopeFull          = "full"
	AccessScopeMigrationRead = "migration_read"
)

func validatePluginPassword(password string) error {
	if len(password) < 8 || len(password) > 1024 {
		return fmt.Errorf("password must contain between 8 and 1024 characters")
	}
	weak := map[string]struct{}{
		"password": {}, "12345678": {}, "qwerty123": {}, "11111111": {},
	}
	if _, found := weak[strings.ToLower(password)]; found {
		return fmt.Errorf("password is too common")
	}
	return nil
}

func (s *Store) CreatePendingPluginUser(ctx context.Context, email, password string) (User, string, error) {
	email = normalizeEmail(email)
	if !validEmail(email) {
		return User{}, "", fmt.Errorf("invalid email")
	}
	if err := validatePluginPassword(password); err != nil {
		return User{}, "", err
	}
	if err := enforcePluginUserQuota(ctx, s.db); err != nil {
		return User{}, "", err
	}
	passwordHash, err := hashPasswordContext(ctx, password)
	if err != nil {
		return User{}, "", err
	}
	now := nowString()
	user := User{ID: newID("user_"), Email: email, Role: RoleUser, CreatedAt: now, Status: "pending_verification"}
	verificationToken := newID("verify_")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, "", err
	}
	defer func() { _ = tx.Rollback() }()
	if err := enforcePluginUserQuota(ctx, tx); err != nil {
		return User{}, "", err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, role, created_at, last_login_at, status, email_verified_at, updated_at)
		 VALUES (?, ?, ?, 'user', ?, '', 'pending_verification', '', ?)`,
		user.ID, user.Email, passwordHash, now, now,
	)
	if err != nil {
		if isDuplicateUserEmailError(err) {
			return User{}, "", ErrDuplicateEmail
		}
		return User{}, "", err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO email_verification_tokens (token_hash, user_id, created_at, expires_at, consumed_at) VALUES (?, ?, ?, ?, '')`,
		tokenHash(verificationToken), user.ID, now, time.Now().UTC().Add(30*time.Minute).Format(time.RFC3339Nano),
	); err != nil {
		return User{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return User{}, "", err
	}
	return user, verificationToken, nil
}

func (s *Store) DeletePendingPluginUser(ctx context.Context, userID, verificationToken string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM users
		WHERE id = ?
		  AND role = 'user'
		  AND status = 'pending_verification'
		  AND EXISTS (
			SELECT 1 FROM email_verification_tokens token
			WHERE token.user_id = users.id
			  AND token.token_hash = ?
			  AND token.consumed_at = ''
		  )`, userID, tokenHash(verificationToken))
	return err
}

func (s *Store) VerifyPluginEmail(ctx context.Context, token string) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var userID string
	err = tx.QueryRowContext(ctx,
		`SELECT user_id FROM email_verification_tokens WHERE token_hash = ? AND consumed_at = '' AND expires_at > ?`,
		tokenHash(token), nowString(),
	).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrInvalidToken
	}
	if err != nil {
		return User{}, err
	}
	now := nowString()
	result, err := tx.ExecContext(ctx,
		`UPDATE email_verification_tokens SET consumed_at = ? WHERE token_hash = ? AND consumed_at = ''`,
		now, tokenHash(token),
	)
	if err != nil {
		return User{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return User{}, ErrInvalidToken
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET status = 'active', email_verified_at = ?, updated_at = ? WHERE id = ?`, now, now, userID,
	); err != nil {
		return User{}, err
	}
	var user User
	if err := tx.QueryRowContext(ctx,
		`SELECT id, email, role, created_at, last_login_at, status, email_verified_at FROM users WHERE id = ?`, userID,
	).Scan(&user.ID, &user.Email, &user.Role, &user.CreatedAt, &user.LastLoginAt, &user.Status, &user.EmailVerifiedAt); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return user, nil
}

func (s *Store) RotateVerificationToken(ctx context.Context, email string) (string, string, bool, error) {
	email = normalizeEmail(email)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", false, err
	}
	defer func() { _ = tx.Rollback() }()
	var userID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE email = ? AND status IN ('pending_verification','legacy_unverified')`, email).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx,
		`UPDATE email_verification_tokens SET consumed_at = ? WHERE user_id = ? AND consumed_at = ''`, now.Format(time.RFC3339Nano), userID,
	); err != nil {
		return "", "", false, err
	}
	token := newID("verify_")
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO email_verification_tokens (token_hash, user_id, created_at, expires_at, consumed_at) VALUES (?, ?, ?, ?, '')`,
		tokenHash(token), userID, now.Format(time.RFC3339Nano), now.Add(30*time.Minute).Format(time.RFC3339Nano),
	); err != nil {
		return "", "", false, err
	}
	if err := tx.Commit(); err != nil {
		return "", "", false, err
	}
	return token, email, true, nil
}

func (s *Store) AuthenticatePlugin(ctx context.Context, email, password string) (User, error) {
	email = normalizeEmail(email)
	var user User
	var passwordHash string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, email, password_hash, role, created_at, last_login_at, status, email_verified_at FROM users WHERE email = ?`, email,
	).Scan(&user.ID, &user.Email, &passwordHash, &user.Role, &user.CreatedAt, &user.LastLoginAt, &user.Status, &user.EmailVerifiedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUnauthorized
	}
	if err != nil {
		return User{}, err
	}
	valid, legacy := verifyPasswordContext(ctx, passwordHash, password)
	if !valid {
		return User{}, ErrUnauthorized
	}
	if user.Role != RoleUser {
		return User{}, ErrUnauthorized
	}
	switch user.Status {
	case "pending_verification":
		return User{}, ErrEmailNotVerified
	case "active", "legacy_unverified":
	default:
		return User{}, ErrAccountDisabled
	}
	if legacy {
		if upgraded, hashErr := hashPasswordContext(ctx, password); hashErr == nil {
			_, _ = s.db.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, upgraded, nowString(), user.ID)
		}
	}
	user.LastLoginAt = nowString()
	_, _ = s.db.ExecContext(ctx, `UPDATE users SET last_login_at = ?, updated_at = ? WHERE id = ?`, user.LastLoginAt, user.LastLoginAt, user.ID)
	return user, nil
}

func (s *Store) CreateTokenFamily(ctx context.Context, userID, deviceID string) (TokenPair, error) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" || len(deviceID) > 128 {
		return TokenPair{}, fmt.Errorf("deviceId is required")
	}
	now := time.Now().UTC()
	familyID := newID("family_")
	access := newID("access_")
	accessExpiry := now.Add(15 * time.Minute)
	var status, verifiedAt string
	if err := s.db.QueryRowContext(ctx, `SELECT status,email_verified_at FROM users WHERE id=? AND role='user'`, userID).Scan(&status, &verifiedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TokenPair{}, ErrUnauthorized
		}
		return TokenPair{}, err
	}
	scope := AccessScopeMigrationRead
	if status == "active" && verifiedAt != "" {
		scope = AccessScopeFull
	} else if status != "legacy_unverified" && !(status == "active" && verifiedAt == "") {
		return TokenPair{}, ErrUnauthorized
	}
	refresh := ""
	familyExpiry := accessExpiry
	if scope == AccessScopeFull {
		refresh = newID("refresh_")
		familyExpiry = now.Add(30 * 24 * time.Hour)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TokenPair{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO refresh_token_families (id, user_id, device_id, scope, created_at, expires_at, revoked_at) VALUES (?, ?, ?, ?, ?, ?, '')`,
		familyID, userID, deviceID, scope, now.Format(time.RFC3339Nano), familyExpiry.Format(time.RFC3339Nano),
	); err != nil {
		return TokenPair{}, err
	}
	if scope == AccessScopeFull {
		if err := insertTokenPair(ctx, tx, userID, familyID, deviceID, scope, access, refresh, now, familyExpiry); err != nil {
			return TokenPair{}, err
		}
	} else if err := insertAccessToken(ctx, tx, userID, familyID, deviceID, scope, access, now); err != nil {
		return TokenPair{}, err
	}
	if err := tx.Commit(); err != nil {
		return TokenPair{}, err
	}
	return tokenPair(userID, scope, access, refresh, accessExpiry, familyExpiry), nil
}

func insertAccessToken(ctx context.Context, tx *sql.Tx, userID, familyID, deviceID, scope, access string, now time.Time) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO access_tokens (token_hash, user_id, family_id, device_id, scope, created_at, expires_at, revoked_at) VALUES (?, ?, ?, ?, ?, ?, ?, '')`,
		tokenHash(access), userID, familyID, deviceID, scope, now.Format(time.RFC3339Nano), now.Add(15*time.Minute).Format(time.RFC3339Nano),
	); err != nil {
		return err
	}
	return nil
}

func insertTokenPair(ctx context.Context, tx *sql.Tx, userID, familyID, deviceID, scope, access, refresh string, now, refreshExpiry time.Time) error {
	if err := insertAccessToken(ctx, tx, userID, familyID, deviceID, scope, access, now); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO refresh_tokens (token_hash, family_id, created_at, expires_at, used_at, replaced_by_hash) VALUES (?, ?, ?, ?, '', '')`,
		tokenHash(refresh), familyID, now.Format(time.RFC3339Nano), refreshExpiry.Format(time.RFC3339Nano),
	)
	return err
}

const refreshRecoveryWindow = 60 * time.Second

func refreshRecoveryWithinWindow(usedAt, now time.Time) bool {
	return !usedAt.After(now) && now.Before(usedAt.Add(refreshRecoveryWindow))
}

func (s *Store) RotateRefreshToken(ctx context.Context, refreshToken, requestID string, derivationKey []byte) (TokenPair, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || len(requestID) > 128 || len(derivationKey) < 32 {
		return TokenPair{}, fmt.Errorf("requestId and a 256-bit derivation key are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TokenPair{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var familyID, userID, deviceID, familyScope, tokenExpiry, familyExpiry, usedAt, revokedAt, replacedByHash, rotationRequestID, userStatus, verifiedAt string
	err = tx.QueryRowContext(ctx,
		`SELECT t.family_id, f.user_id, f.device_id, f.scope, t.expires_at, f.expires_at, t.used_at, f.revoked_at, t.replaced_by_hash, t.rotation_request_id, u.status, u.email_verified_at
		 FROM refresh_tokens t JOIN refresh_token_families f ON f.id = t.family_id JOIN users u ON u.id = f.user_id
		 WHERE t.token_hash = ?`, tokenHash(refreshToken),
	).Scan(&familyID, &userID, &deviceID, &familyScope, &tokenExpiry, &familyExpiry, &usedAt, &revokedAt, &replacedByHash, &rotationRequestID, &userStatus, &verifiedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return TokenPair{}, ErrUnauthorized
	}
	if err != nil {
		return TokenPair{}, err
	}
	if familyScope != AccessScopeFull || userStatus != "active" || verifiedAt == "" {
		return TokenPair{}, ErrUnauthorized
	}
	now := time.Now().UTC()
	if usedAt != "" {
		access := deriveRotatedToken("access", familyID, requestID, derivationKey)
		refresh := deriveRotatedToken("refresh", familyID, requestID, derivationKey)
		recoverable := rotationRequestID == requestID && revokedAt == "" && replacedByHash == tokenHash(refresh)
		usedTime, usedTimeErr := time.Parse(time.RFC3339Nano, usedAt)
		if usedTimeErr != nil || !refreshRecoveryWithinWindow(usedTime, now) {
			recoverable = false
		}

		var accessExpiry, accessRevokedAt, childRefreshExpiry, childRefreshUsedAt string
		if recoverable {
			err := tx.QueryRowContext(ctx,
				`SELECT a.expires_at,a.revoked_at,c.expires_at,c.used_at
				 FROM access_tokens a JOIN refresh_tokens c ON c.family_id=a.family_id
				 WHERE a.token_hash=? AND a.family_id=? AND a.scope=? AND c.token_hash=?`,
				tokenHash(access), familyID, familyScope, tokenHash(refresh),
			).Scan(&accessExpiry, &accessRevokedAt, &childRefreshExpiry, &childRefreshUsedAt)
			if errors.Is(err, sql.ErrNoRows) {
				recoverable = false
			} else if err != nil {
				return TokenPair{}, err
			}
		}

		parsedAccessExpiry, accessExpiryErr := time.Parse(time.RFC3339Nano, accessExpiry)
		parsedChildRefreshExpiry, childRefreshExpiryErr := time.Parse(time.RFC3339Nano, childRefreshExpiry)
		parsedTokenExpiry, tokenExpiryErr := time.Parse(time.RFC3339Nano, tokenExpiry)
		parsedFamilyExpiry, familyExpiryErr := time.Parse(time.RFC3339Nano, familyExpiry)
		if accessExpiryErr != nil || childRefreshExpiryErr != nil || tokenExpiryErr != nil || familyExpiryErr != nil ||
			accessRevokedAt != "" || childRefreshUsedAt != "" || !parsedAccessExpiry.After(now) ||
			!parsedChildRefreshExpiry.After(now) || !parsedTokenExpiry.After(now) || !parsedFamilyExpiry.After(now) {
			recoverable = false
		}
		if recoverable {
			if err := tx.Commit(); err != nil {
				return TokenPair{}, err
			}
			return tokenPair(userID, familyScope, access, refresh, parsedAccessExpiry, parsedChildRefreshExpiry), nil
		}
		if err := revokeRefreshFamilyTx(ctx, tx, familyID, now); err != nil {
			return TokenPair{}, err
		}
		if err := tx.Commit(); err != nil {
			return TokenPair{}, err
		}
		return TokenPair{}, ErrTokenReplay
	}
	if revokedAt != "" || tokenExpiry <= now.Format(time.RFC3339Nano) || familyExpiry <= now.Format(time.RFC3339Nano) {
		return TokenPair{}, ErrUnauthorized
	}
	access := deriveRotatedToken("access", familyID, requestID, derivationKey)
	refresh := deriveRotatedToken("refresh", familyID, requestID, derivationKey)
	refreshExpiry, err := time.Parse(time.RFC3339Nano, familyExpiry)
	if err != nil {
		return TokenPair{}, err
	}
	result, err := tx.ExecContext(ctx,
		`UPDATE refresh_tokens SET used_at = ?, replaced_by_hash = ?, rotation_request_id = ? WHERE token_hash = ? AND used_at = ''`,
		now.Format(time.RFC3339Nano), tokenHash(refresh), requestID, tokenHash(refreshToken),
	)
	if err != nil {
		return TokenPair{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return TokenPair{}, ErrTokenReplay
	}
	if err := insertTokenPair(ctx, tx, userID, familyID, deviceID, familyScope, access, refresh, now, refreshExpiry); err != nil {
		return TokenPair{}, err
	}
	if err := tx.Commit(); err != nil {
		return TokenPair{}, err
	}
	return tokenPair(userID, familyScope, access, refresh, now.Add(15*time.Minute), refreshExpiry), nil
}

func revokeRefreshFamilyTx(ctx context.Context, tx *sql.Tx, familyID string, now time.Time) error {
	revokedAt := now.Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `UPDATE refresh_token_families SET revoked_at = ? WHERE id = ?`, revokedAt, familyID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE access_tokens SET revoked_at = ? WHERE family_id = ? AND revoked_at = ''`, revokedAt, familyID)
	return err
}

func deriveRotatedToken(kind, familyID, requestID string, key []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(kind + "\x00" + familyID + "\x00" + requestID))
	return kind + "_" + hex.EncodeToString(mac.Sum(nil))
}

func tokenPair(userID, scope, access, refresh string, accessExpiry, refreshExpiry time.Time) TokenPair {
	return TokenPair{
		UserID:           userID,
		AccessToken:      access,
		RefreshToken:     refresh,
		ExpiresIn:        15 * 60,
		TokenType:        "Bearer",
		AccessExpiresAt:  accessExpiry.UTC().Format(time.RFC3339Nano),
		RefreshExpiresAt: refreshExpiry.UTC().Format(time.RFC3339Nano),
		Scope:            scope,
	}
}

func pluginTokenResponse(user User, pair TokenPair) map[string]any {
	response := map[string]any{
		"user": user, "accessToken": pair.AccessToken, "scope": pair.Scope,
		"expiresIn": pair.ExpiresIn, "tokenType": pair.TokenType, "accessExpiresAt": pair.AccessExpiresAt,
	}
	if pair.RefreshToken != "" {
		response["refreshToken"] = pair.RefreshToken
		response["refreshExpiresAt"] = pair.RefreshExpiresAt
	}
	return response
}

func (s *Store) PluginUserByID(ctx context.Context, userID string) (User, error) {
	var user User
	err := s.db.QueryRowContext(ctx,
		`SELECT id, email, role, created_at, last_login_at, status, email_verified_at
		 FROM users WHERE id = ? AND role = 'user' AND status IN ('active','legacy_unverified')`, userID,
	).Scan(&user.ID, &user.Email, &user.Role, &user.CreatedAt, &user.LastLoginAt, &user.Status, &user.EmailVerifiedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUnauthorized
	}
	return user, err
}

func (s *Store) UserByAccessToken(ctx context.Context, token string) (User, error) {
	var user User
	err := s.db.QueryRowContext(ctx,
		`SELECT u.id, u.email, u.role, u.created_at, u.last_login_at, u.status, u.email_verified_at
		 FROM access_tokens a
		 JOIN refresh_token_families f ON f.id = a.family_id
		 JOIN users u ON u.id = a.user_id
		 WHERE a.token_hash = ? AND a.expires_at > ? AND a.revoked_at = '' AND f.revoked_at = '' AND f.expires_at > ? AND u.status IN ('active','legacy_unverified')`,
		tokenHash(token), nowString(), nowString(),
	).Scan(&user.ID, &user.Email, &user.Role, &user.CreatedAt, &user.LastLoginAt, &user.Status, &user.EmailVerifiedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUnauthorized
	}
	return user, err
}

func (s *Store) VerifiedUserByAccessToken(ctx context.Context, token string) (User, error) {
	var user User
	err := s.db.QueryRowContext(ctx,
		`SELECT u.id, u.email, u.role, u.created_at, u.last_login_at, u.status, u.email_verified_at
		 FROM access_tokens a
		 JOIN refresh_token_families f ON f.id = a.family_id
		 JOIN users u ON u.id = a.user_id
		 WHERE a.token_hash = ? AND a.scope = ? AND a.expires_at > ? AND a.revoked_at = ''
		   AND f.scope = ? AND f.revoked_at = '' AND f.expires_at > ?
		   AND u.status = 'active' AND u.email_verified_at <> ''`,
		tokenHash(token), AccessScopeFull, nowString(), AccessScopeFull, nowString(),
	).Scan(&user.ID, &user.Email, &user.Role, &user.CreatedAt, &user.LastLoginAt, &user.Status, &user.EmailVerifiedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUnauthorized
	}
	return user, err
}

func (s *Store) CreatePasswordResetToken(ctx context.Context, email string) (string, string, bool, error) {
	email = normalizeEmail(email)
	var userID, verifiedAt string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, email_verified_at FROM users WHERE email = ? AND status = 'active'`, email,
	).Scan(&userID, &verifiedAt)
	if errors.Is(err, sql.ErrNoRows) || verifiedAt == "" {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	token := newID("reset_")
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO password_reset_tokens (token_hash, user_id, created_at, expires_at, consumed_at) VALUES (?, ?, ?, ?, '')`,
		tokenHash(token), userID, now.Format(time.RFC3339Nano), now.Add(30*time.Minute).Format(time.RFC3339Nano),
	)
	if err != nil {
		return "", "", false, err
	}
	return token, email, true, nil
}

func (s *Store) ResetPluginPassword(ctx context.Context, token, password string) error {
	var expectedUserID string
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id FROM password_reset_tokens WHERE token_hash = ? AND consumed_at = '' AND expires_at > ?`,
		tokenHash(token), nowString(),
	).Scan(&expectedUserID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidToken
	}
	if err != nil {
		return err
	}
	if err := validatePluginPassword(password); err != nil {
		return err
	}
	passwordHash, err := hashPasswordContext(ctx, password)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var userID string
	err = tx.QueryRowContext(ctx,
		`SELECT user_id FROM password_reset_tokens WHERE token_hash = ? AND consumed_at = '' AND expires_at > ?`,
		tokenHash(token), nowString(),
	).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidToken
	}
	if err != nil {
		return err
	}
	if userID != expectedUserID {
		return ErrInvalidToken
	}
	now := nowString()
	result, err := tx.ExecContext(ctx,
		`UPDATE password_reset_tokens SET consumed_at = ? WHERE user_id = ? AND consumed_at = ''`, now, userID,
	)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected < 1 {
		return ErrInvalidToken
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, passwordHash, now, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE refresh_token_families SET revoked_at = ? WHERE user_id = ? AND revoked_at = ''`, now, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE access_tokens SET revoked_at = ? WHERE user_id = ? AND revoked_at = ''`, now, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RevokeTokenFamilyByRefresh(ctx context.Context, refreshToken string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var familyID string
	err = tx.QueryRowContext(ctx, `SELECT family_id FROM refresh_tokens WHERE token_hash = ?`, tokenHash(refreshToken)).Scan(&familyID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	now := nowString()
	if _, err := tx.ExecContext(ctx, `UPDATE refresh_token_families SET revoked_at = ? WHERE id = ? AND revoked_at = ''`, now, familyID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE access_tokens SET revoked_at = ? WHERE family_id = ? AND revoked_at = ''`, now, familyID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RevokeTokenFamilyByAccess(ctx context.Context, accessToken string) error {
	if strings.TrimSpace(accessToken) == "" {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var familyID string
	err = tx.QueryRowContext(ctx, `SELECT family_id FROM access_tokens WHERE token_hash = ?`, tokenHash(accessToken)).Scan(&familyID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := revokeRefreshFamilyTx(ctx, tx, familyID, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *App) handleRegisterV1(w http.ResponseWriter, r *http.Request) {
	registrationOpen, publicBaseURL, mailer := a.runtimeAuthSettings()
	if !registrationOpen {
		writeAPIError(w, http.StatusForbidden, "REGISTRATION_CLOSED", "注册已关闭")
		return
	}
	if mailer == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "MAIL_UNAVAILABLE", "邮件服务不可用")
		return
	}
	var input struct{ Email, Password string }
	if !a.decodeJSON(w, r, &input) {
		return
	}
	user, token, err := a.store.CreatePendingPluginUser(r.Context(), input.Email, input.Password)
	if err != nil {
		if errors.Is(err, ErrQuotaExceeded) {
			writeAPIError(w, http.StatusForbidden, "USER_QUOTA_EXCEEDED", "用户数量已达到配置上限")
			return
		}
		if errors.Is(err, ErrDuplicateEmail) {
			writeAPIData(w, http.StatusCreated, map[string]string{"status": "pending_verification"})
			return
		}
		writeAPIError(w, http.StatusBadRequest, "INVALID_REGISTRATION", err.Error())
		return
	}
	if err := mailer.Send(r.Context(), MailMessage{To: user.Email, Kind: "verify_email", Token: token, BaseURL: publicBaseURL}); err != nil {
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer cancel()
		_ = a.store.DeletePendingPluginUser(cleanupContext, user.ID, token)
		writeAPIError(w, http.StatusBadGateway, "MAIL_SEND_FAILED", "验证邮件发送失败")
		return
	}
	writeAPIData(w, http.StatusCreated, map[string]string{"status": "pending_verification"})
}

func (a *App) handleVerifyEmailV1(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token string `json:"token"`
	}
	if !a.decodeJSON(w, r, &input) {
		return
	}
	user, err := a.store.VerifyPluginEmail(r.Context(), input.Token)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_VERIFICATION_TOKEN", "验证链接无效或已过期")
		return
	}
	writeAPIData(w, http.StatusOK, map[string]any{"user": user})
}

func (a *App) handleResendVerificationV1(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email string `json:"email"`
	}
	if !a.decodeJSON(w, r, &input) {
		return
	}
	token, email, found, err := a.store.RotateVerificationToken(r.Context(), input.Email)
	_, publicBaseURL, mailer := a.runtimeAuthSettings()
	if err == nil && found && mailer != nil {
		_ = mailer.Send(r.Context(), MailMessage{To: email, Kind: "verify_email", Token: token, BaseURL: publicBaseURL})
	}
	writeAPIData(w, http.StatusAccepted, map[string]bool{"accepted": true})
}

func (a *App) handlePluginLoginV1(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		DeviceID string `json:"deviceId"`
	}
	if !a.decodeJSON(w, r, &input) {
		return
	}
	user, err := a.store.AuthenticatePlugin(r.Context(), input.Email, input.Password)
	if err != nil {
		switch {
		case errors.Is(err, ErrEmailNotVerified):
			writeAPIError(w, http.StatusForbidden, "EMAIL_NOT_VERIFIED", "请先验证邮箱")
		case errors.Is(err, ErrAccountDisabled):
			writeAPIError(w, http.StatusForbidden, "ACCOUNT_DISABLED", "账号已暂停或封禁")
		default:
			writeAPIError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "邮箱或密码错误")
		}
		return
	}
	pair, err := a.store.CreateTokenFamily(r.Context(), user.ID, input.DeviceID)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_DEVICE", err.Error())
		return
	}
	writeAPIData(w, http.StatusOK, pluginTokenResponse(user, pair))
}

func (a *App) handlePluginRefreshV1(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RefreshToken string `json:"refreshToken"`
		RequestID    string `json:"requestId"`
	}
	if !a.decodeJSON(w, r, &input) {
		return
	}
	pair, err := a.store.RotateRefreshToken(r.Context(), input.RefreshToken, input.RequestID, a.config.TokenDerivationKey)
	if err != nil {
		if errors.Is(err, ErrTokenReplay) {
			writeAPIError(w, http.StatusUnauthorized, "REFRESH_REPLAY", "检测到 refresh token 重放，已撤销该设备会话")
			return
		}
		writeAPIError(w, http.StatusUnauthorized, "INVALID_REFRESH_TOKEN", "refresh token 无效或已过期")
		return
	}
	user, err := a.store.PluginUserByID(r.Context(), pair.UserID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "TOKEN_RESPONSE_FAILED", "无法读取刷新后的账号")
		return
	}
	writeAPIData(w, http.StatusOK, pluginTokenResponse(user, pair))
}

func (a *App) handlePluginMeV1(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	user, err := a.store.UserByAccessToken(r.Context(), token)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "登录已失效")
		return
	}
	writeAPIData(w, http.StatusOK, map[string]any{"user": user})
}

func (a *App) handleForgotPasswordV1(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email string `json:"email"`
	}
	if !a.decodeJSON(w, r, &input) {
		return
	}
	token, email, found, err := a.store.CreatePasswordResetToken(r.Context(), input.Email)
	_, publicBaseURL, mailer := a.runtimeAuthSettings()
	if err == nil && found && mailer != nil {
		_ = mailer.Send(r.Context(), MailMessage{To: email, Kind: "reset_password", Token: token, BaseURL: publicBaseURL})
	}
	writeAPIData(w, http.StatusAccepted, map[string]bool{"accepted": true})
}

func (a *App) handleResetPasswordV1(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !a.decodeJSON(w, r, &input) {
		return
	}
	if err := a.store.ResetPluginPassword(r.Context(), input.Token, input.Password); err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_RESET_TOKEN", "重置链接无效、已使用或密码不符合要求")
		return
	}
	writeAPIData(w, http.StatusOK, map[string]bool{"reset": true})
}

func (a *App) handlePluginLogoutV1(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RefreshToken string `json:"refreshToken"`
	}
	if !a.decodeJSON(w, r, &input) {
		return
	}
	var err error
	if strings.TrimSpace(input.RefreshToken) != "" {
		err = a.store.RevokeTokenFamilyByRefresh(r.Context(), input.RefreshToken)
	} else {
		err = a.store.RevokeTokenFamilyByAccess(r.Context(), bearerToken(r))
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "LOGOUT_FAILED", "无法撤销会话")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
