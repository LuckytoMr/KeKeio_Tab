package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

const pendingRegistrationTTL = 24 * time.Hour

type settingsQueryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type settingsQueryExecer interface {
	settingsQueryRower
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func loadPersistedLimit(ctx context.Context, query settingsQueryRower, key string, fallback int) (int, error) {
	var raw string
	err := query.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'limits'`).Scan(&raw)
	if err == sql.ErrNoRows || raw == "" || raw == "null" {
		return fallback, nil
	}
	if err != nil {
		return 0, err
	}
	var limits map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &limits); err != nil {
		return 0, fmt.Errorf("decode persisted limits: %w", err)
	}
	encoded, found := limits[key]
	if !found {
		return fallback, nil
	}
	var number json.Number
	if err := json.Unmarshal(encoded, &number); err != nil {
		return 0, fmt.Errorf("limit %s must be an integer", key)
	}
	value, err := strconv.Atoi(number.String())
	if err != nil || value < 1 {
		return 0, fmt.Errorf("limit %s must be a positive integer", key)
	}
	return value, nil
}

func (s *Store) PersistedLimit(ctx context.Context, key string, fallback int) (int, error) {
	return loadPersistedLimit(ctx, s.db, key, fallback)
}

func cleanupExpiredPendingPluginUsers(ctx context.Context, query settingsQueryExecer, now time.Time) error {
	now = now.UTC()
	_, err := query.ExecContext(ctx, `
		DELETE FROM users
		WHERE role = 'user'
		  AND status = 'pending_verification'
		  AND created_at <= ?
		  AND NOT EXISTS (
			SELECT 1 FROM email_verification_tokens token
			WHERE token.user_id = users.id
			  AND token.consumed_at = ''
			  AND token.expires_at > ?
		  )`,
		now.Add(-pendingRegistrationTTL).Format(time.RFC3339Nano),
		now.Format(time.RFC3339Nano),
	)
	return err
}

func enforcePluginUserQuota(ctx context.Context, query settingsQueryExecer) error {
	if err := cleanupExpiredPendingPluginUsers(ctx, query, time.Now()); err != nil {
		return err
	}
	limit, err := loadPersistedLimit(ctx, query, "maxUsers", 100)
	if err != nil {
		return err
	}
	var count int
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = 'user'`).Scan(&count); err != nil {
		return err
	}
	if count >= limit {
		return ErrQuotaExceeded
	}
	return nil
}

func enforceSyncStorageQuota(ctx context.Context, query settingsQueryRower, estimatedGrowth int64) error {
	limit, err := loadPersistedLimit(ctx, query, "storageBytes", 512<<20)
	if err != nil {
		return err
	}
	var pageCount, pageSize int64
	if err := query.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pageCount); err != nil {
		return fmt.Errorf("read database page count: %w", err)
	}
	if err := query.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		return fmt.Errorf("read database page size: %w", err)
	}
	if pageCount < 0 || pageSize < 0 || (pageSize != 0 && pageCount > (1<<63-1)/pageSize) {
		return fmt.Errorf("database allocation is invalid")
	}
	used := pageCount * pageSize
	if estimatedGrowth < 0 {
		estimatedGrowth = 0
	}
	storageLimit := int64(limit)
	if used > storageLimit || estimatedGrowth > storageLimit-used {
		return ErrStorageQuotaExceeded
	}
	return nil
}
