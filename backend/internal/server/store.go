package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

var (
	ErrNotFound             = errors.New("not found")
	ErrUnauthorized         = errors.New("unauthorized")
	ErrDuplicateEmail       = errors.New("duplicate email")
	ErrEmailNotVerified     = errors.New("email not verified")
	ErrAccountDisabled      = errors.New("account disabled")
	ErrTokenReplay          = errors.New("token replay")
	ErrInvalidToken         = errors.New("invalid token")
	ErrQuotaExceeded        = errors.New("quota exceeded")
	ErrStorageQuotaExceeded = errors.New("storage quota exceeded")
)

type Store struct {
	db            *sql.DB
	backupMu      sync.Mutex
	maintenanceMu sync.Mutex
}

func OpenStore(path string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	}

	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	store := &Store{db: db}
	migrationNeeded, err := inspectSchemaMigrationState(context.Background(), db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if path != ":memory:" {
		if _, err := createPreMigrationBackup(context.Background(), db, path, migrationNeeded); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("prepare migration backup: %w", err)
		}
	}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if path != ":memory:" {
		if err := configureSQLiteRuntime(context.Background(), db); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return store, nil
}

func configureSQLiteRuntime(ctx context.Context, db *sql.DB) error {
	var journalMode string
	if err := db.QueryRowContext(ctx, `PRAGMA journal_mode=WAL`).Scan(&journalMode); err != nil {
		return err
	}
	if !strings.EqualFold(journalMode, "wal") {
		return fmt.Errorf("enable SQLite WAL: journal_mode=%s", journalMode)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA synchronous=NORMAL`); err != nil {
		return err
	}
	var synchronous int
	if err := db.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&synchronous); err != nil {
		return err
	}
	if synchronous != 1 {
		return fmt.Errorf("configure SQLite synchronous=NORMAL: value=%d", synchronous)
	}
	return nil
}

func inspectSchemaMigrationState(ctx context.Context, db *sql.DB) (bool, error) {
	var applicationTables int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&applicationTables); err != nil {
		return false, err
	}
	if applicationTables == 0 {
		return true, nil
	}
	var migrationTable int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&migrationTable); err != nil {
		return false, err
	}
	if migrationTable == 0 {
		return true, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	versions := make([]int, 0, schemaVersion)
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return false, err
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(versions) == 0 {
		return false, fmt.Errorf("unsupported schema migration state: empty migration history")
	}
	for index, version := range versions {
		if version != index+1 || version > schemaVersion {
			return false, fmt.Errorf("unsupported schema migration state: versions must be the contiguous prefix 1..%d", schemaVersion)
		}
	}
	return len(versions) < schemaVersion, nil
}

func createPreMigrationBackup(ctx context.Context, db *sql.DB, livePath string, needsMigration bool) (string, error) {
	if !needsMigration {
		return "", nil
	}
	var applicationTables int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&applicationTables); err != nil {
		return "", err
	}
	if applicationTables == 0 {
		return "", nil
	}
	temporaryPath := fmt.Sprintf("%s.pre-migration-v%d-pending-%s.sqlite", livePath, schemaVersion, newID(""))
	if err := createSQLiteOnlineBackup(ctx, db, temporaryPath); err != nil {
		return "", err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := os.Chmod(temporaryPath, 0o600); err != nil {
		return "", err
	}
	if err := adminV1CheckSQLiteQuickCheck(temporaryPath); err != nil {
		return "", err
	}
	snapshotChecksum, _, err := adminV1FileSHA256(temporaryPath)
	if err != nil {
		return "", err
	}
	backupPath := fmt.Sprintf("%s.pre-migration-v%d-%s.sqlite", livePath, schemaVersion, snapshotChecksum[:16])
	if _, err := os.Stat(backupPath); err == nil {
		if err := adminV1CheckSQLiteQuickCheck(backupPath); err != nil {
			return "", err
		}
		return backupPath, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := renameAdminV1File(temporaryPath, backupPath); err != nil {
		return "", err
	}
	cleanup = false
	return backupPath, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			email TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL,
			created_at TEXT NOT NULL,
			last_login_at TEXT NOT NULL DEFAULT ''
		)`,
		`UPDATE users SET role = 'user' WHERE role = 'admin'`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_single_admin ON users(role) WHERE role = 'admin'`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token_hash TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS profiles (
			user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
			profile_json TEXT NOT NULL,
			version INTEGER NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS profile_versions (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			version INTEGER NOT NULL,
			profile_json TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_profile_versions_user_created ON profile_versions(user_id, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS idempotency_keys (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			idempotency_key TEXT NOT NULL,
			method TEXT NOT NULL,
			path TEXT NOT NULL,
			request_hash TEXT NOT NULL,
			status INTEGER NOT NULL,
			response_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE(user_id, idempotency_key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_idempotency_user_created ON idempotency_keys(user_id, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS web_wallpaper_cache (
			id TEXT PRIMARY KEY,
			provider TEXT NOT NULL,
			source_page_url TEXT NOT NULL,
			title TEXT NOT NULL,
			category TEXT NOT NULL,
			tags_json TEXT NOT NULL,
			preview_url TEXT NOT NULL,
			variants_json TEXT NOT NULL,
			enabled INTEGER NOT NULL,
			cached_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_web_wallpaper_enabled_category ON web_wallpaper_cache(enabled, category, cached_at DESC)`,
		`CREATE TABLE IF NOT EXISTS official_wallpapers (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			category TEXT NOT NULL,
			tags_json TEXT NOT NULL,
			preview_url TEXT NOT NULL,
			variants_json TEXT NOT NULL,
			enabled INTEGER NOT NULL,
			sort_index INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_official_wallpapers_enabled_sort ON official_wallpapers(enabled, sort_index)`,
		`CREATE TABLE IF NOT EXISTS style_packages (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			version TEXT NOT NULL,
			description TEXT NOT NULL,
			preview_url TEXT NOT NULL,
			css TEXT NOT NULL,
			config_json TEXT NOT NULL,
			enabled INTEGER NOT NULL,
			sort_index INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_style_packages_enabled_sort ON style_packages(enabled, sort_index)`,
		`CREATE TABLE IF NOT EXISTS app_releases (
			id TEXT PRIMARY KEY,
			version TEXT NOT NULL,
			channel TEXT NOT NULL,
			notes TEXT NOT NULL,
			download_url TEXT NOT NULL,
			minimum_version TEXT NOT NULL DEFAULT '',
			schema_version INTEGER NOT NULL DEFAULT 2,
			status TEXT NOT NULL DEFAULT 'published',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL DEFAULT '',
			published_at TEXT NOT NULL DEFAULT '',
			disabled_at TEXT NOT NULL DEFAULT '',
			UNIQUE(channel, version)
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS api_logs (
			id TEXT PRIMARY KEY,
			created_at TEXT NOT NULL,
			user_id TEXT NOT NULL,
			user_email TEXT NOT NULL,
			role TEXT NOT NULL,
			ip TEXT NOT NULL,
			method TEXT NOT NULL,
			path TEXT NOT NULL,
			route_group TEXT NOT NULL,
			status INTEGER NOT NULL,
			duration_ms INTEGER NOT NULL,
			request_bytes INTEGER NOT NULL,
			response_bytes INTEGER NOT NULL,
			idempotency_key TEXT NOT NULL,
			user_agent TEXT NOT NULL,
			error TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_api_logs_created ON api_logs(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_api_logs_user_created ON api_logs(user_email, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_api_logs_route_status_created ON api_logs(route_group, status, created_at DESC)`,
	}

	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if err := applySecurityMigrationsTx(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func newID(prefix string) string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(bytes[:])
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func intBool(value int) bool {
	return value != 0
}

func encodeJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	return string(data), err
}

func decodeJSONValue[T any](raw string) (T, error) {
	var value T
	err := json.Unmarshal([]byte(raw), &value)
	return value, err
}

func (s *Store) CreateUser(ctx context.Context, email string, password string) (User, error) {
	email = normalizeEmail(email)
	if email == "" || len(password) < 4 {
		return User{}, fmt.Errorf("invalid email or password")
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}

	role := RoleUser

	user := User{
		ID:        newID("user_"),
		Email:     email,
		Role:      role,
		CreatedAt: nowString(),
	}
	_, err = s.db.ExecContext(
		ctx,
		`INSERT INTO users (id, email, password_hash, role, created_at, last_login_at) VALUES (?, ?, ?, ?, ?, '')`,
		user.ID,
		user.Email,
		string(passwordHash),
		string(user.Role),
		user.CreatedAt,
	)
	if err != nil {
		if isDuplicateUserEmailError(err) {
			return User{}, ErrDuplicateEmail
		}
		return User{}, err
	}
	return user, nil
}

func isDuplicateUserEmailError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: users.email")
}

func (s *Store) CheckPassword(ctx context.Context, email string, password string) bool {
	_, err := s.AuthenticateUser(ctx, email, password)
	return err == nil
}

func (s *Store) AuthenticateUser(ctx context.Context, email string, password string) (User, error) {
	email = normalizeEmail(email)
	row := s.db.QueryRowContext(ctx, `SELECT id, email, password_hash, role, created_at, last_login_at FROM users WHERE email = ?`, email)

	var user User
	var hash string
	if err := row.Scan(&user.ID, &user.Email, &hash, &user.Role, &user.CreatedAt, &user.LastLoginAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrUnauthorized
		}
		return User{}, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return User{}, ErrUnauthorized
	}

	user.LastLoginAt = nowString()
	_, _ = s.db.ExecContext(ctx, `UPDATE users SET last_login_at = ? WHERE id = ?`, user.LastLoginAt, user.ID)
	return user, nil
}

func (s *Store) CreateSession(ctx context.Context, userID string, ttl time.Duration) (string, error) {
	token := newID("sess_")
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		tokenHash(token),
		userID,
		nowString(),
		time.Now().UTC().Add(ttl).Format(time.RFC3339),
	)
	if err != nil {
		return "", err
	}
	return token, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash(token))
	return err
}

func (s *Store) UserBySession(ctx context.Context, token string) (User, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT u.id, u.email, u.role, u.created_at, u.last_login_at
		 FROM sessions s
		 JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash = ? AND s.expires_at > ?`,
		tokenHash(token),
		nowString(),
	)

	var user User
	if err := row.Scan(&user.ID, &user.Email, &user.Role, &user.CreatedAt, &user.LastLoginAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrUnauthorized
		}
		return User{}, err
	}
	return user, nil
}

func (s *Store) SaveProfile(ctx context.Context, userID string, profile json.RawMessage) (ProfileRecord, error) {
	if !json.Valid(profile) {
		return ProfileRecord{}, fmt.Errorf("profile must be valid json")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ProfileRecord{}, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var currentVersion int
	scanErr := tx.QueryRowContext(ctx, `SELECT version FROM profiles WHERE user_id = ?`, userID).Scan(&currentVersion)
	if scanErr != nil && !errors.Is(scanErr, sql.ErrNoRows) {
		err = scanErr
		return ProfileRecord{}, err
	}
	nextVersion := currentVersion + 1
	updatedAt := nowString()

	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO profiles (user_id, profile_json, version, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET profile_json = excluded.profile_json, version = excluded.version, updated_at = excluded.updated_at`,
		userID,
		string(profile),
		nextVersion,
		updatedAt,
	)
	if err != nil {
		return ProfileRecord{}, err
	}

	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO profile_versions (id, user_id, version, profile_json, created_at) VALUES (?, ?, ?, ?, ?)`,
		newID("pver_"),
		userID,
		nextVersion,
		string(profile),
		updatedAt,
	)
	if err != nil {
		return ProfileRecord{}, err
	}

	if err = tx.Commit(); err != nil {
		return ProfileRecord{}, err
	}

	return ProfileRecord{UserID: userID, ProfileJSON: profile, Version: nextVersion, UpdatedAt: updatedAt}, nil
}

func (s *Store) GetProfile(ctx context.Context, userID string) (ProfileRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT profile_json, version, updated_at FROM profiles WHERE user_id = ?`, userID)
	var raw string
	var record ProfileRecord
	if err := row.Scan(&raw, &record.Version, &record.UpdatedAt); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return ProfileRecord{}, err
		}
		if err := s.db.QueryRowContext(ctx, `SELECT profile_json, version, updated_at FROM sync_profiles WHERE user_id = ?`, userID).Scan(&raw, &record.Version, &record.UpdatedAt); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ProfileRecord{}, ErrNotFound
			}
			return ProfileRecord{}, err
		}
	}
	record.UserID = userID
	record.ProfileJSON = json.RawMessage(raw)
	return record, nil
}

func (s *Store) ListProfileVersions(ctx context.Context, userID string, limit int) ([]ProfileVersion, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, user_id, version, profile_json, created_at
		 FROM profile_versions
		 WHERE user_id = ?
		 ORDER BY version DESC
		 LIMIT ?`,
		userID,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var versions []ProfileVersion
	for rows.Next() {
		var version ProfileVersion
		var raw string
		if err := rows.Scan(&version.ID, &version.UserID, &version.Version, &raw, &version.CreatedAt); err != nil {
			return nil, err
		}
		version.ProfileJSON = json.RawMessage(raw)
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

func (s *Store) RestoreProfileVersion(ctx context.Context, userID string, versionID string) (ProfileRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT profile_json FROM profile_versions WHERE id = ? AND user_id = ?`, versionID, userID)
	var raw string
	if err := row.Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProfileRecord{}, ErrNotFound
		}
		return ProfileRecord{}, err
	}
	return s.SaveProfile(ctx, userID, json.RawMessage(raw))
}

func (s *Store) GetIdempotencyResponse(ctx context.Context, userID string, key string) (IdempotencyRecord, error) {
	key = strings.TrimSpace(key)
	if userID == "" || key == "" {
		return IdempotencyRecord{}, ErrNotFound
	}
	row := s.db.QueryRowContext(
		ctx,
		`SELECT id, user_id, idempotency_key, method, path, request_hash, status, response_json, created_at
		 FROM idempotency_keys
		 WHERE user_id = ? AND idempotency_key = ?`,
		userID,
		key,
	)
	var record IdempotencyRecord
	if err := row.Scan(
		&record.ID,
		&record.UserID,
		&record.IdempotencyKey,
		&record.Method,
		&record.Path,
		&record.RequestHash,
		&record.Status,
		&record.ResponseJSON,
		&record.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IdempotencyRecord{}, ErrNotFound
		}
		return IdempotencyRecord{}, err
	}
	return record, nil
}

func (s *Store) SaveIdempotencyResponse(ctx context.Context, record IdempotencyRecord) error {
	record.IdempotencyKey = strings.TrimSpace(record.IdempotencyKey)
	if record.UserID == "" || record.IdempotencyKey == "" || record.RequestHash == "" {
		return fmt.Errorf("user, idempotency key, and request hash are required")
	}
	if record.ID == "" {
		record.ID = newID("idem_")
	}
	if record.CreatedAt == "" {
		record.CreatedAt = nowString()
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO idempotency_keys
		 (id, user_id, idempotency_key, method, path, request_hash, status, response_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(user_id, idempotency_key) DO NOTHING`,
		record.ID,
		record.UserID,
		record.IdempotencyKey,
		record.Method,
		record.Path,
		record.RequestHash,
		record.Status,
		record.ResponseJSON,
		record.CreatedAt,
	)
	return err
}

func (s *Store) UpsertWebWallpaper(ctx context.Context, record WebWallpaperRecord) error {
	record.ID = strings.TrimSpace(record.ID)
	record.Provider = strings.TrimSpace(record.Provider)
	record.Title = strings.TrimSpace(record.Title)
	record.Category = strings.TrimSpace(record.Category)
	if record.ID == "" || record.Provider == "" || record.Title == "" || record.Category == "" {
		return fmt.Errorf("id, provider, title, and category are required")
	}
	tags, err := encodeJSON(record.Tags)
	if err != nil {
		return err
	}
	variants, err := encodeJSON(record.Variants)
	if err != nil {
		return err
	}
	now := nowString()
	if record.CachedAt == "" {
		record.CachedAt = now
	}

	_, err = s.db.ExecContext(
		ctx,
		`INSERT INTO web_wallpaper_cache
		 (id, provider, source_page_url, title, category, tags_json, preview_url, variants_json, enabled, cached_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   provider = excluded.provider,
		   source_page_url = excluded.source_page_url,
		   title = excluded.title,
		   category = excluded.category,
		   tags_json = excluded.tags_json,
		   preview_url = excluded.preview_url,
		   variants_json = excluded.variants_json,
		   enabled = excluded.enabled,
		   cached_at = excluded.cached_at,
		   updated_at = excluded.updated_at`,
		record.ID,
		record.Provider,
		record.SourcePageURL,
		record.Title,
		record.Category,
		tags,
		record.PreviewURL,
		variants,
		boolInt(record.Enabled),
		record.CachedAt,
		now,
	)
	return err
}

func (s *Store) ListAdminWebWallpapers(ctx context.Context) ([]WebWallpaperRecord, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, provider, source_page_url, title, category, tags_json, preview_url, variants_json, enabled, cached_at, updated_at
		 FROM web_wallpaper_cache
		 ORDER BY cached_at DESC, title ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []WebWallpaperRecord
	for rows.Next() {
		record, err := scanWebWallpaper(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) ListPublicWebWallpapers(ctx context.Context, filter WallpaperListFilter) (PublicWebWallpaperPage, error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	offset := (page - 1) * pageSize
	category := strings.TrimSpace(filter.Category)

	where := `WHERE enabled = 1`
	args := []any{}
	if category != "" && category != "all" {
		where += ` AND category = ?`
		args = append(args, category)
	}

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM web_wallpaper_cache `+where, args...).Scan(&total); err != nil {
		return PublicWebWallpaperPage{}, err
	}

	args = append(args, pageSize, offset)
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, provider, title, category, tags_json, variants_json
		 FROM web_wallpaper_cache `+where+`
		 ORDER BY cached_at DESC, title ASC
		 LIMIT ? OFFSET ?`,
		args...,
	)
	if err != nil {
		return PublicWebWallpaperPage{}, err
	}
	defer rows.Close()

	pageResult := PublicWebWallpaperPage{Items: []PublicWebWallpaper{}, Page: page, PageSize: pageSize, Total: total}
	for rows.Next() {
		var item PublicWebWallpaper
		var tagsRaw string
		var variantsRaw string
		if err := rows.Scan(&item.ID, &item.Provider, &item.Title, &item.Category, &tagsRaw, &variantsRaw); err != nil {
			return PublicWebWallpaperPage{}, err
		}
		item.Tags, _ = decodeJSONValue[[]string](tagsRaw)
		privateVariants, _ := decodeJSONValue[[]WallpaperVariantRecord](variantsRaw)
		item.Variants = sanitizeVariants(privateVariants)
		pageResult.Items = append(pageResult.Items, item)
	}
	return pageResult, rows.Err()
}

func (s *Store) ListClientWebWallpapers(ctx context.Context, filter WallpaperListFilter) (ClientWebWallpaperPage, error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	offset := (page - 1) * pageSize
	category := strings.TrimSpace(filter.Category)

	where := `WHERE enabled = 1`
	args := []any{}
	if category != "" && category != "all" {
		where += ` AND category = ?`
		args = append(args, category)
	}

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM web_wallpaper_cache `+where, args...).Scan(&total); err != nil {
		return ClientWebWallpaperPage{}, err
	}

	args = append(args, pageSize, offset)
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, provider, source_page_url, title, category, tags_json, preview_url, variants_json
		 FROM web_wallpaper_cache `+where+`
		 ORDER BY cached_at DESC, title ASC
		 LIMIT ? OFFSET ?`,
		args...,
	)
	if err != nil {
		return ClientWebWallpaperPage{}, err
	}
	defer rows.Close()

	pageResult := ClientWebWallpaperPage{Items: []ClientWebWallpaper{}, Page: page, PageSize: pageSize, Total: total}
	for rows.Next() {
		var item ClientWebWallpaper
		var tagsRaw string
		var variantsRaw string
		if err := rows.Scan(&item.ID, &item.Provider, &item.SourcePageURL, &item.Title, &item.Category, &tagsRaw, &item.PreviewURL, &variantsRaw); err != nil {
			return ClientWebWallpaperPage{}, err
		}
		item.Tags, _ = decodeJSONValue[[]string](tagsRaw)
		item.Variants, _ = decodeJSONValue[[]WallpaperVariantRecord](variantsRaw)
		pageResult.Items = append(pageResult.Items, item)
	}
	return pageResult, rows.Err()
}

func (s *Store) DeleteWebWallpaper(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM web_wallpaper_cache WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpsertOfficialWallpaper(ctx context.Context, record OfficialWallpaperRecord) error {
	record.ID = strings.TrimSpace(record.ID)
	record.Title = strings.TrimSpace(record.Title)
	record.Category = strings.TrimSpace(record.Category)
	if record.ID == "" || record.Title == "" || record.Category == "" {
		return fmt.Errorf("id, title, and category are required")
	}
	tags, err := encodeJSON(record.Tags)
	if err != nil {
		return err
	}
	variants, err := encodeJSON(record.Variants)
	if err != nil {
		return err
	}
	now := nowString()
	if record.CreatedAt == "" {
		record.CreatedAt = now
	}
	_, err = s.db.ExecContext(
		ctx,
		`INSERT INTO official_wallpapers
		 (id, title, category, tags_json, preview_url, variants_json, enabled, sort_index, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   title = excluded.title,
		   category = excluded.category,
		   tags_json = excluded.tags_json,
		   preview_url = excluded.preview_url,
		   variants_json = excluded.variants_json,
		   enabled = excluded.enabled,
		   sort_index = excluded.sort_index,
		   updated_at = excluded.updated_at`,
		record.ID,
		record.Title,
		record.Category,
		tags,
		record.PreviewURL,
		variants,
		boolInt(record.Enabled),
		record.SortIndex,
		record.CreatedAt,
		now,
	)
	return err
}

func (s *Store) ListAdminOfficialWallpapers(ctx context.Context) ([]OfficialWallpaperRecord, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, title, category, tags_json, preview_url, variants_json, enabled, sort_index, created_at, updated_at
		 FROM official_wallpapers
		 ORDER BY sort_index ASC, title ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []OfficialWallpaperRecord
	for rows.Next() {
		record, err := scanOfficialWallpaper(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) ListPublicOfficialWallpapers(ctx context.Context) ([]PublicOfficialWallpaper, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, title, category, tags_json, preview_url, variants_json
		 FROM official_wallpapers
		 WHERE enabled = 1
		 ORDER BY sort_index ASC, title ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []PublicOfficialWallpaper
	for rows.Next() {
		var record PublicOfficialWallpaper
		var tagsRaw string
		var variantsRaw string
		if err := rows.Scan(&record.ID, &record.Title, &record.Category, &tagsRaw, &record.PreviewURL, &variantsRaw); err != nil {
			return nil, err
		}
		record.Tags, _ = decodeJSONValue[[]string](tagsRaw)
		privateVariants, _ := decodeJSONValue[[]WallpaperVariantRecord](variantsRaw)
		record.Variants = sanitizeVariants(privateVariants)
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) DeleteOfficialWallpaper(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM official_wallpapers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpsertStylePackage(ctx context.Context, record StylePackageRecord) error {
	record.ID = strings.TrimSpace(record.ID)
	record.Name = strings.TrimSpace(record.Name)
	record.Version = strings.TrimSpace(record.Version)
	if record.ID == "" || record.Name == "" || record.Version == "" {
		return fmt.Errorf("id, name, and version are required")
	}
	if len(record.ConfigJSON) == 0 {
		record.ConfigJSON = json.RawMessage(`{}`)
	}
	if !json.Valid(record.ConfigJSON) {
		return fmt.Errorf("config must be valid json")
	}
	now := nowString()
	if record.CreatedAt == "" {
		record.CreatedAt = now
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO style_packages
		 (id, name, version, description, preview_url, css, config_json, enabled, sort_index, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   name = excluded.name,
		   version = excluded.version,
		   description = excluded.description,
		   preview_url = excluded.preview_url,
		   css = excluded.css,
		   config_json = excluded.config_json,
		   enabled = excluded.enabled,
		   sort_index = excluded.sort_index,
		   updated_at = excluded.updated_at`,
		record.ID,
		record.Name,
		record.Version,
		record.Description,
		record.PreviewURL,
		record.CSS,
		string(record.ConfigJSON),
		boolInt(record.Enabled),
		record.SortIndex,
		record.CreatedAt,
		now,
	)
	return err
}

func (s *Store) ListAdminStylePackages(ctx context.Context) ([]StylePackageRecord, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, name, version, description, preview_url, css, config_json, enabled, sort_index, created_at, updated_at
		 FROM style_packages
		 ORDER BY sort_index ASC, name ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := []StylePackageRecord{}
	for rows.Next() {
		record, err := scanStylePackage(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) ListPublicStylePackages(ctx context.Context) ([]PublicStylePackage, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, name, version, description, preview_url, css, config_json
		 FROM style_packages
		 WHERE enabled = 1
		 ORDER BY sort_index ASC, name ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := []PublicStylePackage{}
	for rows.Next() {
		var record PublicStylePackage
		var configRaw string
		if err := rows.Scan(
			&record.ID,
			&record.Name,
			&record.Version,
			&record.Description,
			&record.PreviewURL,
			&record.CSS,
			&configRaw,
		); err != nil {
			return nil, err
		}
		record.Config = json.RawMessage(configRaw)
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) DeleteStylePackage(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM style_packages WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, email, role, created_at, last_login_at
		 FROM users
		 WHERE role = ?
		 ORDER BY created_at DESC`,
		string(RoleUser),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var user User
		if err := rows.Scan(&user.ID, &user.Email, &user.Role, &user.CreatedAt, &user.LastLoginAt); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Store) AddRelease(ctx context.Context, release ReleaseRecord) (ReleaseRecord, error) {
	release.Version = strings.TrimSpace(release.Version)
	if release.Version == "" {
		return ReleaseRecord{}, fmt.Errorf("version is required")
	}
	if release.ID == "" {
		release.ID = newID("rel_")
	}
	if release.Channel == "" {
		release.Channel = "stable"
	}
	if release.SchemaVersion == 0 {
		release.SchemaVersion = 2
	}
	if release.Status == "" {
		release.Status = "published"
	}
	release.CreatedAt = nowString()
	release.UpdatedAt = release.CreatedAt
	if release.Status == "published" {
		release.PublishedAt = release.CreatedAt
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO app_releases (id,version,channel,notes,download_url,minimum_version,schema_version,status,created_at,updated_at,published_at,disabled_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		release.ID,
		release.Version,
		release.Channel,
		release.Notes,
		release.DownloadURL,
		release.MinimumVersion,
		release.SchemaVersion,
		release.Status,
		release.CreatedAt,
		release.UpdatedAt,
		release.PublishedAt,
		release.DisabledAt,
	)
	return release, err
}

func (s *Store) ListReleases(ctx context.Context) ([]ReleaseRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,version,channel,notes,download_url,minimum_version,schema_version,status,created_at,updated_at,published_at,disabled_at
		FROM app_releases ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var releases []ReleaseRecord
	for rows.Next() {
		var release ReleaseRecord
		if err := rows.Scan(&release.ID, &release.Version, &release.Channel, &release.Notes, &release.DownloadURL, &release.MinimumVersion, &release.SchemaVersion, &release.Status, &release.CreatedAt, &release.UpdatedAt, &release.PublishedAt, &release.DisabledAt); err != nil {
			return nil, err
		}
		releases = append(releases, release)
	}
	return releases, rows.Err()
}

func (s *Store) LatestPublishedRelease(ctx context.Context, channel string) (ReleaseRecord, error) {
	var release ReleaseRecord
	err := s.db.QueryRowContext(ctx, `SELECT id,version,channel,notes,download_url,minimum_version,schema_version,status,created_at,updated_at,published_at,disabled_at
		FROM app_releases WHERE channel=? AND status='published' AND disabled_at='' ORDER BY published_at DESC, created_at DESC LIMIT 1`, channel).
		Scan(&release.ID, &release.Version, &release.Channel, &release.Notes, &release.DownloadURL, &release.MinimumVersion, &release.SchemaVersion, &release.Status, &release.CreatedAt, &release.UpdatedAt, &release.PublishedAt, &release.DisabledAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ReleaseRecord{}, ErrNotFound
	}
	return release, err
}

func (s *Store) InsertAPILog(ctx context.Context, log APILogRecord) error {
	if log.ID == "" {
		log.ID = newID("log_")
	}
	if log.CreatedAt == "" {
		log.CreatedAt = nowString()
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO api_logs
		 (id, created_at, user_id, user_email, role, ip, method, path, route_group, status, duration_ms, request_bytes, response_bytes, idempotency_key, user_agent, error)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		log.ID,
		log.CreatedAt,
		log.UserID,
		log.UserEmail,
		log.Role,
		log.IP,
		log.Method,
		log.Path,
		log.RouteGroup,
		log.Status,
		log.DurationMS,
		log.RequestBytes,
		log.ResponseBytes,
		log.IdempotencyKey,
		log.UserAgent,
		log.Error,
	)
	return err
}

func (s *Store) ListAPILogs(ctx context.Context, filter APILogFilter) ([]APILogRecord, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	where := []string{"1 = 1"}
	args := []any{}
	if userEmail := normalizeEmail(filter.UserEmail); userEmail != "" {
		where = append(where, "user_email = ?")
		args = append(args, userEmail)
	}
	if routeGroup := strings.TrimSpace(filter.RouteGroup); routeGroup != "" {
		where = append(where, "route_group = ?")
		args = append(args, routeGroup)
	}
	if filter.Status > 0 {
		where = append(where, "status = ?")
		args = append(args, filter.Status)
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, created_at, user_id, user_email, role, ip, method, path, route_group, status, duration_ms, request_bytes, response_bytes, idempotency_key, user_agent, error
		 FROM api_logs
		 WHERE `+strings.Join(where, " AND ")+`
		 ORDER BY created_at DESC, id DESC
		 LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	logs := []APILogRecord{}
	for rows.Next() {
		var log APILogRecord
		if err := rows.Scan(
			&log.ID,
			&log.CreatedAt,
			&log.UserID,
			&log.UserEmail,
			&log.Role,
			&log.IP,
			&log.Method,
			&log.Path,
			&log.RouteGroup,
			&log.Status,
			&log.DurationMS,
			&log.RequestBytes,
			&log.ResponseBytes,
			&log.IdempotencyKey,
			&log.UserAgent,
			&log.Error,
		); err != nil {
			return nil, err
		}
		logs = append(logs, log)
	}
	return logs, rows.Err()
}

func (s *Store) AdminSummary(ctx context.Context) (AdminSummary, error) {
	var summary AdminSummary
	counts := []struct {
		query string
		dest  *int
	}{
		{`SELECT COUNT(*) FROM users WHERE role = 'user'`, &summary.Users},
		{`SELECT COUNT(*) FROM profiles`, &summary.Profiles},
		{`SELECT COUNT(*) FROM profile_versions`, &summary.ProfileVersions},
		{`SELECT COUNT(*) FROM official_wallpapers`, &summary.OfficialWallpapers},
		{`SELECT COUNT(*) FROM web_wallpaper_cache`, &summary.WebWallpapers},
		{`SELECT COUNT(*) FROM app_releases`, &summary.Releases},
		{`SELECT COUNT(*) FROM style_packages`, &summary.Styles},
		{`SELECT COUNT(*) FROM api_logs`, &summary.APILogs},
	}
	for _, item := range counts {
		if err := s.db.QueryRowContext(ctx, item.query).Scan(item.dest); err != nil {
			return AdminSummary{}, err
		}
	}
	return summary, nil
}

func sanitizeVariants(variants []WallpaperVariantRecord) []PublicWallpaperVariant {
	result := make([]PublicWallpaperVariant, 0, len(variants))
	for _, variant := range variants {
		result = append(result, PublicWallpaperVariant{ID: variant.ID, Label: variant.Label})
	}
	return result
}

func normalizePage(page int, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanWebWallpaper(row rowScanner) (WebWallpaperRecord, error) {
	var record WebWallpaperRecord
	var tagsRaw string
	var variantsRaw string
	var enabled int
	if err := row.Scan(
		&record.ID,
		&record.Provider,
		&record.SourcePageURL,
		&record.Title,
		&record.Category,
		&tagsRaw,
		&record.PreviewURL,
		&variantsRaw,
		&enabled,
		&record.CachedAt,
		&record.UpdatedAt,
	); err != nil {
		return WebWallpaperRecord{}, err
	}
	record.Tags, _ = decodeJSONValue[[]string](tagsRaw)
	record.Variants, _ = decodeJSONValue[[]WallpaperVariantRecord](variantsRaw)
	record.Enabled = intBool(enabled)
	return record, nil
}

func scanOfficialWallpaper(row rowScanner) (OfficialWallpaperRecord, error) {
	var record OfficialWallpaperRecord
	var tagsRaw string
	var variantsRaw string
	var enabled int
	if err := row.Scan(
		&record.ID,
		&record.Title,
		&record.Category,
		&tagsRaw,
		&record.PreviewURL,
		&variantsRaw,
		&enabled,
		&record.SortIndex,
		&record.CreatedAt,
		&record.UpdatedAt,
	); err != nil {
		return OfficialWallpaperRecord{}, err
	}
	record.Tags, _ = decodeJSONValue[[]string](tagsRaw)
	record.Variants, _ = decodeJSONValue[[]WallpaperVariantRecord](variantsRaw)
	record.Enabled = intBool(enabled)
	return record, nil
}

func scanStylePackage(row rowScanner) (StylePackageRecord, error) {
	var record StylePackageRecord
	var enabled int
	var configRaw string
	if err := row.Scan(
		&record.ID,
		&record.Name,
		&record.Version,
		&record.Description,
		&record.PreviewURL,
		&record.CSS,
		&configRaw,
		&enabled,
		&record.SortIndex,
		&record.CreatedAt,
		&record.UpdatedAt,
	); err != nil {
		return StylePackageRecord{}, err
	}
	record.ConfigJSON = json.RawMessage(configRaw)
	record.Enabled = intBool(enabled)
	return record, nil
}
