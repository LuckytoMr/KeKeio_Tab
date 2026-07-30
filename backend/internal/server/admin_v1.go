package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var adminV1ScheduleRestore = func(app *App, livePath, stagedPath, liveSecretsPath, stagedSecretsPath string) {
	go func() {
		time.Sleep(100 * time.Millisecond)
		app.shutdownRequests <- func() error {
			return applyAdminV1Restore(app, livePath, stagedPath, liveSecretsPath, stagedSecretsPath)
		}
	}()
}

func (a *App) registerAdminV1Routes(mux *http.ServeMux) {
	if err := a.ensureAdminV1Schema(context.Background()); err != nil {
		panic(fmt.Sprintf("initialize admin v1 schema: %v", err))
	}
	protect := func(handler http.HandlerFunc) http.HandlerFunc {
		return a.requireAdminNetwork(a.requireAdminV1(handler))
	}
	mux.HandleFunc("GET /api/admin/v1/overview", protect(a.handleAdminOverviewV1))
	mux.HandleFunc("GET /api/admin/v1/users", protect(a.handleAdminUsersV1))
	mux.HandleFunc("GET /api/admin/v1/users/{id}", protect(a.handleAdminUserDetailV1))
	mux.HandleFunc("POST /api/admin/v1/users/{id}/status", protect(a.handleAdminUserStatusV1))
	mux.HandleFunc("POST /api/admin/v1/users/{id}/sessions/{sessionId}/revoke", protect(a.handleAdminUserSessionRevokeV1))
	mux.HandleFunc("POST /api/admin/v1/users/{id}/versions/{versionId}/restore", protect(a.handleAdminUserVersionRestoreV1))
	mux.HandleFunc("GET /api/admin/v1/sync/attempts", protect(a.handleAdminSyncAttemptsV1))
	mux.HandleFunc("GET /api/admin/v1/sync/conflicts", protect(a.handleAdminSyncConflictsV1))
	mux.HandleFunc("GET /api/admin/v1/catalog/{type}", protect(a.handleAdminCatalogListV1))
	mux.HandleFunc("POST /api/admin/v1/catalog/{type}", protect(a.handleAdminCatalogCreateV1))
	mux.HandleFunc("GET /api/admin/v1/catalog/{type}/{id}", protect(a.handleAdminCatalogDetailV1))
	mux.HandleFunc("PUT /api/admin/v1/catalog/{type}/{id}/draft", protect(a.handleAdminCatalogDraftV1))
	mux.HandleFunc("POST /api/admin/v1/catalog/{type}/{id}/{action}", protect(a.handleAdminCatalogActionV1))
	mux.HandleFunc("GET /api/admin/v1/catalog/styles/{id}/preview", protect(a.handleAdminStylePreviewV1))
	mux.HandleFunc("GET /api/admin/v1/releases", protect(a.handleAdminReleasesV1))
	mux.HandleFunc("POST /api/admin/v1/releases", protect(a.handleAdminReleaseCreateV1))
	mux.HandleFunc("GET /api/admin/v1/releases/{id}/history", protect(a.handleAdminReleaseHistoryV1))
	mux.HandleFunc("POST /api/admin/v1/releases/{id}/{action}", protect(a.handleAdminReleaseActionV1))
	mux.HandleFunc("GET /api/admin/v1/audit/admin", protect(a.handleAdminAuditV1))
	mux.HandleFunc("GET /api/admin/v1/audit/access", protect(a.handleAdminAccessAuditV1))
	mux.HandleFunc("GET /api/admin/v1/audit/admin/export", protect(a.handleAdminAuditExportV1))
	mux.HandleFunc("GET /api/admin/v1/audit/access/export", protect(a.handleAdminAccessAuditExportV1))
	mux.HandleFunc("GET /api/admin/v1/system/settings", protect(a.handleAdminSettingsGetV1))
	mux.HandleFunc("PUT /api/admin/v1/system/settings", protect(a.handleAdminSettingsPutV1))
	mux.HandleFunc("POST /api/admin/v1/system/settings/smtp-test", protect(a.handleAdminSettingsSMTPTestV1))
	mux.HandleFunc("GET /api/admin/v1/system/maintenance/jobs", protect(a.handleAdminMaintenanceJobsV1))
	mux.HandleFunc("POST /api/admin/v1/system/maintenance/jobs", protect(a.handleAdminMaintenanceCreateV1))
	mux.HandleFunc("GET /api/admin/v1/system/backups", protect(a.handleAdminBackupsV1))
	mux.HandleFunc("POST /api/admin/v1/system/backups", protect(a.handleAdminBackupCreateV1))
	mux.HandleFunc("POST /api/admin/v1/system/backups/{id}/restore", protect(a.handleAdminBackupRestoreV1))
	mux.HandleFunc("GET /api/admin/v1/system/health", protect(a.handleAdminSystemHealthV1))
}

func (a *App) ensureAdminV1Schema(ctx context.Context) error {
	tx, err := a.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS maintenance_jobs (
			id TEXT PRIMARY KEY, kind TEXT NOT NULL, status TEXT NOT NULL,
			detail TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL, started_at TEXT NOT NULL DEFAULT '', finished_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_maintenance_jobs_created ON maintenance_jobs(created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS backup_records (
			id TEXT PRIMARY KEY, kind TEXT NOT NULL, status TEXT NOT NULL,
			database_path TEXT NOT NULL, manifest_path TEXT NOT NULL,
			checksum TEXT NOT NULL, size_bytes INTEGER NOT NULL,
			created_at TEXT NOT NULL, restored_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_backup_records_created ON backup_records(created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS catalog_revisions (
			id TEXT PRIMARY KEY, item_type TEXT NOT NULL, item_id TEXT NOT NULL,
			revision INTEGER NOT NULL, status TEXT NOT NULL, visibility TEXT NOT NULL,
			fields_json TEXT NOT NULL, validation_errors_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL, published_at TEXT NOT NULL DEFAULT '',
			UNIQUE(item_type, item_id, revision)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_catalog_latest ON catalog_revisions(item_type, item_id, revision DESC)`,
		`CREATE TABLE IF NOT EXISTS release_drafts (
			id TEXT PRIMARY KEY, version TEXT NOT NULL, channel TEXT NOT NULL,
			notes TEXT NOT NULL, download_url TEXT NOT NULL, minimum_version TEXT NOT NULL,
			status TEXT NOT NULL, created_at TEXT NOT NULL,
			UNIQUE(channel, version)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_release_drafts_created ON release_drafts(created_at DESC)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO app_releases(id,version,channel,notes,download_url,minimum_version,schema_version,status,created_at,updated_at,published_at,disabled_at)
		SELECT id,version,channel,notes,download_url,minimum_version,2,status,created_at,created_at,
			CASE WHEN status='published' THEN created_at ELSE '' END,
			CASE WHEN status='disabled' THEN created_at ELSE '' END
		FROM release_drafts`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO release_events(id,release_id,action,from_status,to_status,admin_id,request_id,created_at)
		SELECT 'migrated_' || d.id,d.id,'migrate','',d.status,'','migration',d.created_at
		FROM release_drafts d WHERE EXISTS (SELECT 1 FROM app_releases r WHERE r.id=d.id)`); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *App) handleAdminOverviewV1(w http.ResponseWriter, r *http.Request) {
	cutoff := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339Nano)
	var attempts, successes, conflicts, unauthorized, throttled, serverErrors int
	if err := a.store.db.QueryRowContext(r.Context(), `SELECT
		COUNT(*),
		COALESCE(SUM(CASE WHEN status BETWEEN 200 AND 299 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = 401 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = 429 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status >= 500 THEN 1 ELSE 0 END), 0)
		FROM sync_attempts WHERE created_at >= ?`, cutoff).Scan(&attempts, &successes, &unauthorized, &throttled, &serverErrors); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "OVERVIEW_UNAVAILABLE", "无法读取同步概览")
		return
	}
	if err := a.store.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM sync_conflicts WHERE status = 'open'`).Scan(&conflicts); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "OVERVIEW_UNAVAILABLE", "无法读取冲突概览")
		return
	}
	successRate := 0.0
	if attempts > 0 {
		successRate = float64(successes) * 100 / float64(attempts)
	}
	attention := []map[string]any{}
	if conflicts > 0 {
		attention = append(attention, map[string]any{
			"id": "open-sync-conflicts", "kind": "conflict", "severity": "warning",
			"title": "存在待处理同步冲突", "detail": conflicts, "href": "/admin/sync/conflicts?status=open",
		})
	}
	backupStatus, backupDetail := "unknown", "尚无可用备份"
	var latestBackupStatus, latestBackupAt string
	if err := a.store.db.QueryRowContext(r.Context(), `SELECT status,created_at FROM backup_records ORDER BY created_at DESC LIMIT 1`).Scan(&latestBackupStatus, &latestBackupAt); err == nil {
		backupStatus, backupDetail = map[bool]string{true: "healthy", false: "warning"}[latestBackupStatus == "ready"], latestBackupStatus+" · "+latestBackupAt
	}
	databasePath, _ := adminV1DatabasePath(r.Context(), a.store.db)
	var databaseBytes int64
	if info, err := os.Stat(databasePath); err == nil {
		databaseBytes = info.Size()
	}
	storageLimit, _ := a.store.PersistedLimit(r.Context(), "storageBytes", 1<<30)
	storageStatus := "healthy"
	if databaseBytes >= int64(storageLimit) {
		storageStatus = "critical"
	} else if databaseBytes*100 >= int64(storageLimit)*80 {
		storageStatus = "warning"
	}
	writeAPIData(w, http.StatusOK, map[string]any{
		"health": []map[string]any{
			{"id": "api", "label": "API", "status": "healthy", "detail": "服务可用"},
			{"id": "sqlite", "label": "SQLite", "status": "healthy", "detail": "数据库可查询"},
			{"id": "smtp", "label": "邮件", "status": map[bool]string{true: "healthy", false: "unknown"}[a.runtimeMailer() != nil]},
			{"id": "backup", "label": "最近备份", "status": backupStatus, "detail": backupDetail},
			{"id": "storage", "label": "存储水位", "status": storageStatus, "detail": fmt.Sprintf("%d / %d bytes", databaseBytes, storageLimit)},
		},
		"attention": attention,
		"sync24h": map[string]any{
			"successRate": successRate, "conflicts": conflicts, "unauthorized": unauthorized,
			"throttled": throttled, "serverErrors": serverErrors, "idempotentReplays": 0,
		},
		"recent": []map[string]any{},
	})
}

func (a *App) handleAdminUsersV1(w http.ResponseWriter, r *http.Request) {
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	where := []string{"u.role = 'user'"}
	args := []any{}
	if query != "" {
		where = append(where, `LOWER(u.email) LIKE ?`)
		args = append(args, "%"+query+"%")
	}
	if status != "" {
		if !oneOf(status, "active", "pending_verification", "legacy_unverified", "suspended", "banned") {
			writeAPIError(w, http.StatusBadRequest, "INVALID_FILTER", "用户状态筛选无效")
			return
		}
		where = append(where, `u.status = ?`)
		args = append(args, status)
	}
	order := "u.last_login_at DESC, u.created_at DESC"
	switch r.URL.Query().Get("sort") {
	case "email":
		order = "u.email ASC"
	case "-createdAt":
		order = "u.created_at DESC"
	case "", "-lastActivityAt":
	default:
		writeAPIError(w, http.StatusBadRequest, "INVALID_SORT", "用户排序参数无效")
		return
	}
	rows, err := a.store.db.QueryContext(r.Context(), `SELECT
		u.id,u.email,u.status,u.email_verified_at,u.created_at,u.last_login_at,
		(SELECT COUNT(*) FROM devices d WHERE d.user_id=u.id AND d.revoked_at='')
		FROM users u WHERE `+strings.Join(where, " AND ")+` ORDER BY `+order+` LIMIT 100`, args...)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "USERS_UNAVAILABLE", "无法读取用户列表")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, email, userStatus, verifiedAt, createdAt, lastLoginAt string
		var deviceCount int
		if err := rows.Scan(&id, &email, &userStatus, &verifiedAt, &createdAt, &lastLoginAt, &deviceCount); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "USERS_UNAVAILABLE", "无法读取用户列表")
			return
		}
		verification := userStatus
		if verifiedAt != "" {
			verification = "verified"
		}
		items = append(items, map[string]any{
			"id": id, "email": email, "status": userStatus, "verificationStatus": verification,
			"lastActivityAt": lastLoginAt, "deviceCount": deviceCount, "createdAt": createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "USERS_UNAVAILABLE", "无法读取用户列表")
		return
	}
	writeAPIData(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) handleAdminUserDetailV1(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	if !validAdminV1Identifier(userID) {
		writeAPIError(w, http.StatusBadRequest, "INVALID_USER_ID", "用户 ID 无效")
		return
	}
	var email, status, verifiedAt, createdAt, lastLoginAt string
	if err := a.store.db.QueryRowContext(r.Context(), `SELECT email,status,email_verified_at,created_at,last_login_at FROM users WHERE id=? AND role='user'`, userID).
		Scan(&email, &status, &verifiedAt, &createdAt, &lastLoginAt); errorsIsNoRows(err) {
		writeAPIError(w, http.StatusNotFound, "USER_NOT_FOUND", "用户不存在")
		return
	} else if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "USER_UNAVAILABLE", "无法读取用户")
		return
	}
	verification := status
	if verifiedAt != "" {
		verification = "verified"
	}
	sessions, err := a.adminV1UserSessions(r.Context(), userID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "USER_UNAVAILABLE", "无法读取用户会话")
		return
	}
	attempts, err := a.adminV1UserAttempts(r.Context(), userID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "USER_UNAVAILABLE", "无法读取同步记录")
		return
	}
	versions, err := a.adminV1UserVersions(r.Context(), userID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "USER_UNAVAILABLE", "无法读取配置版本")
		return
	}
	profile := any(nil)
	var profileRaw, profileHash, updatedAt string
	var version, schema int
	err = a.store.db.QueryRowContext(r.Context(), `SELECT profile_json,version,schema_version,profile_hash,updated_at FROM sync_profiles WHERE user_id=?`, userID).
		Scan(&profileRaw, &version, &schema, &profileHash, &updatedAt)
	if err == nil {
		groups, shortcuts := profileCollectionCounts([]byte(profileRaw))
		profile = map[string]any{"version": version, "schemaVersion": schema, "bytes": len(profileRaw), "groups": groups, "shortcuts": shortcuts, "profileHash": profileHash, "updatedAt": updatedAt}
	} else if !errorsIsNoRows(err) {
		writeAPIError(w, http.StatusInternalServerError, "USER_UNAVAILABLE", "无法读取用户配置")
		return
	}
	writeAPIData(w, http.StatusOK, map[string]any{
		"user":     map[string]any{"id": userID, "email": email, "status": status, "verificationStatus": verification, "createdAt": createdAt, "lastActivityAt": lastLoginAt},
		"sessions": sessions, "profile": profile, "attempts": attempts, "versions": versions,
	})
}

func (a *App) handleAdminUserStatusV1(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	if !validAdminV1Identifier(userID) {
		writeAPIError(w, http.StatusBadRequest, "INVALID_USER_ID", "用户 ID 无效")
		return
	}
	var input struct {
		Status string `json:"status"`
	}
	if !a.decodeJSON(w, r, &input) {
		return
	}
	input.Status = strings.TrimSpace(input.Status)
	if !oneOf(input.Status, "active", "suspended", "banned") {
		writeAPIError(w, http.StatusBadRequest, "INVALID_USER_STATUS", "用户状态无效")
		return
	}
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "USER_UPDATE_FAILED", "无法更新用户状态")
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(r.Context(), `UPDATE users SET status=?,updated_at=? WHERE id=? AND role='user'`, input.Status, nowString(), userID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "USER_UPDATE_FAILED", "无法更新用户状态")
		return
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		writeAPIError(w, http.StatusNotFound, "USER_NOT_FOUND", "用户不存在")
		return
	}
	if input.Status != "active" {
		now := nowString()
		if _, err = tx.ExecContext(r.Context(), `UPDATE refresh_token_families SET revoked_at=? WHERE user_id=? AND revoked_at=''`, now, userID); err == nil {
			_, err = tx.ExecContext(r.Context(), `UPDATE access_tokens SET revoked_at=? WHERE user_id=? AND revoked_at=''`, now, userID)
		}
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `DELETE FROM sessions WHERE user_id=?`, userID)
		}
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "USER_UPDATE_FAILED", "无法撤销用户会话")
			return
		}
	}
	requestID := newID("req_")
	admin, _ := adminFromContext(r.Context())
	ip, _ := a.clientIP(r)
	if err := insertAdminAuditTx(r.Context(), tx, admin.ID, "user.status.update", "user", userID, requestID, ip.String(), map[string]any{"status": input.Status}); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "AUDIT_FAILED", "无法记录管理员审计")
		return
	}
	if err := tx.Commit(); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "USER_UPDATE_FAILED", "无法更新用户状态")
		return
	}
	writeAdminV1Data(w, http.StatusOK, map[string]any{"id": userID, "status": input.Status}, requestID)
}

func (a *App) handleAdminUserSessionRevokeV1(w http.ResponseWriter, r *http.Request) {
	userID, familyID := r.PathValue("id"), r.PathValue("sessionId")
	if !validAdminV1Identifier(userID) || !validAdminV1Identifier(familyID) {
		writeAPIError(w, http.StatusBadRequest, "INVALID_SESSION_ID", "用户或会话 ID 无效")
		return
	}
	var input struct{}
	if !a.decodeJSON(w, r, &input) {
		return
	}
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "SESSION_REVOKE_FAILED", "无法撤销用户会话")
		return
	}
	defer tx.Rollback()
	now := nowString()
	result, err := tx.ExecContext(r.Context(), `UPDATE refresh_token_families SET revoked_at=? WHERE id=? AND user_id=? AND revoked_at=''`, now, familyID, userID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "SESSION_REVOKE_FAILED", "无法撤销用户会话")
		return
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		writeAPIError(w, http.StatusNotFound, "SESSION_NOT_FOUND", "用户会话不存在")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE access_tokens SET revoked_at=? WHERE family_id=? AND user_id=? AND revoked_at=''`, now, familyID, userID); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "SESSION_REVOKE_FAILED", "无法撤销用户会话")
		return
	}
	requestID := newID("req_")
	admin, _ := adminFromContext(r.Context())
	ip, _ := a.clientIP(r)
	if err := insertAdminAuditTx(r.Context(), tx, admin.ID, "user.session.revoke", "refresh_family", familyID, requestID, ip.String(), map[string]any{"userId": userID}); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "AUDIT_FAILED", "无法记录管理员审计")
		return
	}
	if err := tx.Commit(); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "SESSION_REVOKE_FAILED", "无法撤销用户会话")
		return
	}
	writeAdminV1Data(w, http.StatusOK, map[string]any{"id": familyID, "revoked": true}, requestID)
}

func (a *App) handleAdminUserVersionRestoreV1(w http.ResponseWriter, r *http.Request) {
	userID, versionID := r.PathValue("id"), r.PathValue("versionId")
	if !validAdminV1Identifier(userID) || !validAdminV1Identifier(versionID) {
		writeAPIError(w, http.StatusBadRequest, "INVALID_VERSION_ID", "用户或版本 ID 无效")
		return
	}
	var input struct{}
	if !a.decodeJSON(w, r, &input) {
		return
	}
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "VERSION_RESTORE_FAILED", "无法恢复配置版本")
		return
	}
	defer tx.Rollback()
	var profileJSON, profileHash string
	var schemaVersion int
	err = tx.QueryRowContext(r.Context(), `SELECT profile_json,schema_version,profile_hash FROM sync_profile_versions WHERE id=? AND user_id=?`, versionID, userID).
		Scan(&profileJSON, &schemaVersion, &profileHash)
	if err == sql.ErrNoRows {
		writeAPIError(w, http.StatusNotFound, "VERSION_NOT_FOUND", "配置版本不存在")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "VERSION_RESTORE_FAILED", "无法恢复配置版本")
		return
	}
	currentVersion := 0
	err = tx.QueryRowContext(r.Context(), `SELECT version FROM sync_profiles WHERE user_id=?`, userID).Scan(&currentVersion)
	if err != nil && err != sql.ErrNoRows {
		writeAPIError(w, http.StatusInternalServerError, "VERSION_RESTORE_FAILED", "无法恢复配置版本")
		return
	}
	nextVersion := currentVersion + 1
	now := nowString()
	mutationID := newID("admin_restore_")
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO sync_profiles(user_id,profile_json,version,schema_version,profile_hash,updated_at) VALUES(?,?,?,?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET profile_json=excluded.profile_json,version=excluded.version,schema_version=excluded.schema_version,profile_hash=excluded.profile_hash,updated_at=excluded.updated_at`,
		userID, profileJSON, nextVersion, schemaVersion, profileHash, now); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "VERSION_RESTORE_FAILED", "无法恢复配置版本")
		return
	}
	newVersionID := newID("pver_")
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO sync_profile_versions(id,user_id,version,schema_version,profile_json,profile_hash,mutation_id,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		newVersionID, userID, nextVersion, schemaVersion, profileJSON, profileHash, mutationID, now); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "VERSION_RESTORE_FAILED", "无法恢复配置版本")
		return
	}
	requestID := newID("req_")
	admin, _ := adminFromContext(r.Context())
	ip, _ := a.clientIP(r)
	if err := insertAdminAuditTx(r.Context(), tx, admin.ID, "user.version.restore", "profile_version", versionID, requestID, ip.String(), map[string]any{"userId": userID, "newVersion": nextVersion}); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "AUDIT_FAILED", "无法记录管理员审计")
		return
	}
	if err := tx.Commit(); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "VERSION_RESTORE_FAILED", "无法恢复配置版本")
		return
	}
	writeAdminV1Data(w, http.StatusOK, map[string]any{"id": newVersionID, "version": nextVersion, "profileHash": profileHash}, requestID)
}

func (a *App) handleAdminSyncAttemptsV1(w http.ResponseWriter, r *http.Request) {
	rows, err := a.store.db.QueryContext(r.Context(), `SELECT a.id,a.user_id,u.email,a.device_id,a.mutation_id,a.status,a.error_code,a.created_at
		FROM sync_attempts a LEFT JOIN users u ON u.id=a.user_id ORDER BY a.created_at DESC LIMIT 200`)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "SYNC_ATTEMPTS_UNAVAILABLE", "无法读取同步尝试")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, userID, email, deviceID, mutationID, code, createdAt string
		var status int
		if err := rows.Scan(&id, &userID, &email, &deviceID, &mutationID, &status, &code, &createdAt); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "SYNC_ATTEMPTS_UNAVAILABLE", "无法读取同步尝试")
			return
		}
		outcome := "failed"
		if status >= 200 && status < 300 {
			outcome = "success"
		}
		items = append(items, map[string]any{"id": id, "userId": userID, "userEmail": email, "deviceId": deviceID, "requestId": mutationID, "status": outcome, "statusCode": status, "code": code, "createdAt": createdAt})
	}
	writeAPIData(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) handleAdminSyncConflictsV1(w http.ResponseWriter, r *http.Request) {
	rows, err := a.store.db.QueryContext(r.Context(), `SELECT c.id,c.user_id,u.email,c.mutation_id,c.device_id,c.base_version,c.current_version,c.status,c.created_at,c.resolved_at
		FROM sync_conflicts c LEFT JOIN users u ON u.id=c.user_id ORDER BY c.created_at DESC LIMIT 200`)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "SYNC_CONFLICTS_UNAVAILABLE", "无法读取同步冲突")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, userID, email, mutationID, deviceID, status, createdAt, resolvedAt string
		var baseVersion, currentVersion int
		if err := rows.Scan(&id, &userID, &email, &mutationID, &deviceID, &baseVersion, &currentVersion, &status, &createdAt, &resolvedAt); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "SYNC_CONFLICTS_UNAVAILABLE", "无法读取同步冲突")
			return
		}
		items = append(items, map[string]any{"id": id, "userId": userID, "userEmail": email, "requestId": mutationID, "deviceId": deviceID, "baseVersion": baseVersion, "currentVersion": currentVersion, "status": status, "createdAt": createdAt, "resolvedAt": resolvedAt})
	}
	writeAPIData(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) handleAdminCatalogListV1(w http.ResponseWriter, r *http.Request) {
	itemType := r.PathValue("type")
	if !oneOf(itemType, "official", "web", "styles") {
		writeAPIError(w, http.StatusNotFound, "CATALOG_NOT_FOUND", "目录类型不存在")
		return
	}
	items, err := a.adminV1CatalogItems(r.Context(), itemType)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "CATALOG_UNAVAILABLE", "无法读取内容目录")
		return
	}
	writeAPIData(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) handleAdminCatalogCreateV1(w http.ResponseWriter, r *http.Request) {
	itemType := r.PathValue("type")
	if !validCatalogType(itemType) {
		writeAPIError(w, http.StatusNotFound, "CATALOG_NOT_FOUND", "目录类型不存在")
		return
	}
	fields, ok := a.decodeAdminCatalogFields(w, r, itemType, true)
	if !ok {
		return
	}
	itemID, _ := fields["id"].(string)
	itemID = strings.TrimSpace(itemID)
	if !validAdminV1Identifier(itemID) {
		writeAPIError(w, http.StatusBadRequest, "INVALID_CATALOG_ID", "资源 ID 无效")
		return
	}
	if strings.TrimSpace(catalogName(fields)) == "" {
		writeAPIError(w, http.StatusBadRequest, "INVALID_CATALOG_ITEM", "资源名称不能为空")
		return
	}
	encoded, _ := json.Marshal(fields)
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "CATALOG_WRITE_FAILED", "无法创建内容草稿")
		return
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM catalog_revisions WHERE item_type=? AND item_id=?`, itemType, itemID).Scan(&count); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "CATALOG_WRITE_FAILED", "无法创建内容草稿")
		return
	}
	if count != 0 {
		writeAPIError(w, http.StatusConflict, "CATALOG_ITEM_EXISTS", "资源 ID 已存在")
		return
	}
	now := nowString()
	revisionID := newID("crev_")
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO catalog_revisions(id,item_type,item_id,revision,status,visibility,fields_json,validation_errors_json,created_at,updated_at,published_at)
		VALUES(?,?,?,1,'draft','enabled',?,'{}',?,?, '')`, revisionID, itemType, itemID, string(encoded), now, now); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "CATALOG_WRITE_FAILED", "无法创建内容草稿")
		return
	}
	requestID := newID("req_")
	admin, _ := adminFromContext(r.Context())
	ip, _ := a.clientIP(r)
	if err := insertAdminAuditTx(r.Context(), tx, admin.ID, "catalog.create", itemType, itemID, requestID, ip.String(), map[string]any{"revision": 1}); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "AUDIT_FAILED", "无法记录管理员审计")
		return
	}
	if err := tx.Commit(); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "CATALOG_WRITE_FAILED", "无法创建内容草稿")
		return
	}
	writeAdminV1Data(w, http.StatusCreated, map[string]any{"item": catalogItemDTO(itemType, itemID, 1, "draft", "enabled", fields, now)}, requestID)
}

func (a *App) handleAdminCatalogDetailV1(w http.ResponseWriter, r *http.Request) {
	itemType, itemID := r.PathValue("type"), r.PathValue("id")
	if !validCatalogType(itemType) || !validAdminV1Identifier(itemID) {
		writeAPIError(w, http.StatusBadRequest, "INVALID_CATALOG_ID", "目录类型或资源 ID 无效")
		return
	}
	var revision, status string
	var revisionNumber int
	var visibility, fieldsRaw, updatedAt string
	load := func() error {
		return a.store.db.QueryRowContext(r.Context(), `SELECT id,revision,status,visibility,fields_json,updated_at FROM catalog_revisions WHERE item_type=? AND item_id=? ORDER BY revision DESC LIMIT 1`, itemType, itemID).
			Scan(&revision, &revisionNumber, &status, &visibility, &fieldsRaw, &updatedAt)
	}
	err := load()
	if err == sql.ErrNoRows {
		migrated, migrateErr := a.migrateLegacyCatalogRevision(r.Context(), itemType, itemID)
		if migrateErr != nil {
			writeAPIError(w, http.StatusInternalServerError, "CATALOG_UNAVAILABLE", "无法迁移旧资源详情")
			return
		}
		if !migrated {
			writeAPIError(w, http.StatusNotFound, "CATALOG_ITEM_NOT_FOUND", "资源不存在")
			return
		}
		err = load()
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "CATALOG_UNAVAILABLE", "无法读取资源详情")
		return
	}
	var fields map[string]any
	if json.Unmarshal([]byte(fieldsRaw), &fields) != nil {
		writeAPIError(w, http.StatusInternalServerError, "CATALOG_UNAVAILABLE", "资源数据无效")
		return
	}
	revisions, err := a.adminV1CatalogRevisions(r.Context(), itemType, itemID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "CATALOG_UNAVAILABLE", "无法读取 revision 历史")
		return
	}
	item := catalogItemDTO(itemType, itemID, revisionNumber, status, visibility, fields, updatedAt)
	item["revisionId"] = revision
	writeAPIData(w, http.StatusOK, map[string]any{"item": item, "revisions": revisions})
}

func (a *App) migrateLegacyCatalogRevision(ctx context.Context, itemType, itemID string) (bool, error) {
	var fields map[string]any
	visibility, createdAt, updatedAt := "enabled", nowString(), nowString()
	switch itemType {
	case "official":
		records, err := a.store.ListAdminOfficialWallpapers(ctx)
		if err != nil {
			return false, err
		}
		for _, record := range records {
			if record.ID != itemID {
				continue
			}
			fields = map[string]any{"id": record.ID, "name": record.Title, "title": record.Title, "category": record.Category, "tags": record.Tags, "previewUrl": record.PreviewURL, "variants": record.Variants, "sortIndex": record.SortIndex}
			visibility, createdAt, updatedAt = visibilityFor(record.Enabled), firstNonEmpty(record.CreatedAt, createdAt), firstNonEmpty(record.UpdatedAt, updatedAt)
			break
		}
	case "web":
		records, err := a.store.ListAdminWebWallpapers(ctx)
		if err != nil {
			return false, err
		}
		for _, record := range records {
			if record.ID != itemID {
				continue
			}
			fields = map[string]any{"id": record.ID, "name": record.Title, "title": record.Title, "provider": record.Provider, "sourcePageUrl": record.SourcePageURL, "category": record.Category, "tags": record.Tags, "previewUrl": record.PreviewURL, "variants": record.Variants}
			visibility, createdAt, updatedAt = visibilityFor(record.Enabled), firstNonEmpty(record.CachedAt, createdAt), firstNonEmpty(record.UpdatedAt, updatedAt)
			break
		}
	case "styles":
		records, err := a.store.ListAdminStylePackages(ctx)
		if err != nil {
			return false, err
		}
		for _, record := range records {
			if record.ID != itemID {
				continue
			}
			var config any = map[string]any{}
			_ = json.Unmarshal(record.ConfigJSON, &config)
			fields = map[string]any{"id": record.ID, "name": record.Name, "version": record.Version, "schemaVersion": 2, "description": record.Description, "previewUrl": record.PreviewURL, "css": record.CSS, "config": config, "sortIndex": record.SortIndex}
			visibility, createdAt, updatedAt = visibilityFor(record.Enabled), firstNonEmpty(record.CreatedAt, createdAt), firstNonEmpty(record.UpdatedAt, updatedAt)
			break
		}
	}
	if fields == nil {
		return false, nil
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return false, err
	}
	_, err = a.store.db.ExecContext(ctx, `INSERT OR IGNORE INTO catalog_revisions(id,item_type,item_id,revision,status,visibility,fields_json,validation_errors_json,created_at,updated_at,published_at) VALUES(?,?,?,1,'published',?,?,'{}',?,?,?)`, newID("crev_"), itemType, itemID, visibility, string(encoded), createdAt, updatedAt, updatedAt)
	return err == nil, err
}

func firstNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func (a *App) handleAdminCatalogDraftV1(w http.ResponseWriter, r *http.Request) {
	itemType, itemID := r.PathValue("type"), r.PathValue("id")
	if !validCatalogType(itemType) || !validAdminV1Identifier(itemID) {
		writeAPIError(w, http.StatusBadRequest, "INVALID_CATALOG_ID", "目录类型或资源 ID 无效")
		return
	}
	fields, ok := a.decodeAdminCatalogFields(w, r, itemType, false)
	if !ok {
		return
	}
	if payloadID, found := fields["id"].(string); found && strings.TrimSpace(payloadID) != "" && strings.TrimSpace(payloadID) != itemID {
		writeAPIError(w, http.StatusConflict, "CATALOG_ID_MISMATCH", "请求体资源 ID 与路径不一致")
		return
	}
	fields["id"] = itemID
	if strings.TrimSpace(catalogName(fields)) == "" {
		writeAPIError(w, http.StatusBadRequest, "INVALID_CATALOG_ITEM", "资源名称不能为空")
		return
	}
	encoded, _ := json.Marshal(fields)
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "CATALOG_WRITE_FAILED", "无法保存内容草稿")
		return
	}
	defer tx.Rollback()
	var revisionID, status, visibility string
	var revision int
	err = tx.QueryRowContext(r.Context(), `SELECT id,revision,status,visibility FROM catalog_revisions WHERE item_type=? AND item_id=? ORDER BY revision DESC LIMIT 1`, itemType, itemID).
		Scan(&revisionID, &revision, &status, &visibility)
	if err == sql.ErrNoRows {
		writeAPIError(w, http.StatusNotFound, "CATALOG_ITEM_NOT_FOUND", "资源不存在")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "CATALOG_WRITE_FAILED", "无法保存内容草稿")
		return
	}
	now := nowString()
	if status == "draft" {
		_, err = tx.ExecContext(r.Context(), `UPDATE catalog_revisions SET fields_json=?,validation_errors_json='{}',updated_at=? WHERE id=?`, string(encoded), now, revisionID)
	} else {
		revision++
		revisionID = newID("crev_")
		_, err = tx.ExecContext(r.Context(), `INSERT INTO catalog_revisions(id,item_type,item_id,revision,status,visibility,fields_json,validation_errors_json,created_at,updated_at,published_at)
			VALUES(?,?,?,?,'draft',?,?,'{}',?,?, '')`, revisionID, itemType, itemID, revision, visibility, string(encoded), now, now)
		status = "draft"
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "CATALOG_WRITE_FAILED", "无法保存内容草稿")
		return
	}
	requestID := newID("req_")
	admin, _ := adminFromContext(r.Context())
	ip, _ := a.clientIP(r)
	if err := insertAdminAuditTx(r.Context(), tx, admin.ID, "catalog.draft.update", itemType, itemID, requestID, ip.String(), map[string]any{"revision": revision}); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "AUDIT_FAILED", "无法记录管理员审计")
		return
	}
	if err := tx.Commit(); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "CATALOG_WRITE_FAILED", "无法保存内容草稿")
		return
	}
	writeAdminV1Data(w, http.StatusOK, map[string]any{"item": catalogItemDTO(itemType, itemID, revision, status, visibility, fields, now)}, requestID)
}

func (a *App) handleAdminCatalogActionV1(w http.ResponseWriter, r *http.Request) {
	itemType, itemID, action := r.PathValue("type"), r.PathValue("id"), r.PathValue("action")
	if !validCatalogType(itemType) || !validAdminV1Identifier(itemID) || !oneOf(action, "validate", "publish", "disable", "rollback", "archive") {
		writeAPIError(w, http.StatusNotFound, "CATALOG_ACTION_NOT_FOUND", "内容操作不存在")
		return
	}
	var input struct{}
	if !a.decodeJSON(w, r, &input) {
		return
	}
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "CATALOG_ACTION_FAILED", "内容操作失败")
		return
	}
	defer tx.Rollback()
	var revisionID, status, visibility, fieldsRaw, updatedAt string
	var revision int
	err = tx.QueryRowContext(r.Context(), `SELECT id,revision,status,visibility,fields_json,updated_at FROM catalog_revisions WHERE item_type=? AND item_id=? ORDER BY revision DESC LIMIT 1`, itemType, itemID).
		Scan(&revisionID, &revision, &status, &visibility, &fieldsRaw, &updatedAt)
	if err == sql.ErrNoRows {
		writeAPIError(w, http.StatusNotFound, "CATALOG_ITEM_NOT_FOUND", "资源不存在")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "CATALOG_ACTION_FAILED", "内容操作失败")
		return
	}
	var fields map[string]any
	if json.Unmarshal([]byte(fieldsRaw), &fields) != nil {
		writeAPIError(w, http.StatusInternalServerError, "CATALOG_ACTION_FAILED", "内容数据无效")
		return
	}
	now := nowString()
	switch action {
	case "validate":
		validationErrors := validateCatalogFields(itemType, itemID, fields)
		if len(validationErrors) > 0 {
			errorsJSON, _ := json.Marshal(validationErrors)
			_, _ = tx.ExecContext(r.Context(), `UPDATE catalog_revisions SET status='draft',validation_errors_json=?,updated_at=? WHERE id=?`, string(errorsJSON), now, revisionID)
			writeAPIErrorDetails(w, http.StatusUnprocessableEntity, "CATALOG_VALIDATION_FAILED", "内容校验失败", validationErrors)
			return
		}
		status = "ready"
		if _, err := tx.ExecContext(r.Context(), `UPDATE catalog_revisions SET status='ready',validation_errors_json='{}',updated_at=? WHERE id=?`, now, revisionID); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "CATALOG_ACTION_FAILED", "内容校验失败")
			return
		}
	case "publish":
		if status != "ready" {
			writeAPIError(w, http.StatusConflict, "CATALOG_NOT_READY", "只有 ready revision 可以发布")
			return
		}
		if _, err := tx.ExecContext(r.Context(), `UPDATE catalog_revisions SET status='superseded',updated_at=? WHERE item_type=? AND item_id=? AND status='published'`, now, itemType, itemID); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "CATALOG_ACTION_FAILED", "内容发布失败")
			return
		}
		status, visibility = "published", "enabled"
		if _, err := tx.ExecContext(r.Context(), `UPDATE catalog_revisions SET status='published',visibility='enabled',published_at=?,updated_at=? WHERE id=?`, now, now, revisionID); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "CATALOG_ACTION_FAILED", "内容发布失败")
			return
		}
		if err := upsertPublishedCatalogTx(r.Context(), tx, itemType, itemID, fields, true); err != nil {
			writeAPIError(w, http.StatusUnprocessableEntity, "CATALOG_PUBLISH_FAILED", "发布数据不符合扩展目录要求")
			return
		}
	case "disable":
		visibility = "disabled"
		if _, err := tx.ExecContext(r.Context(), `UPDATE catalog_revisions SET visibility='disabled',updated_at=? WHERE item_type=? AND item_id=?`, now, itemType, itemID); err != nil || setPublishedCatalogEnabledTx(r.Context(), tx, itemType, itemID, false) != nil {
			writeAPIError(w, http.StatusInternalServerError, "CATALOG_ACTION_FAILED", "停用内容失败")
			return
		}
	case "archive":
		if visibility != "disabled" {
			writeAPIError(w, http.StatusConflict, "CATALOG_MUST_BE_DISABLED", "归档前必须先停用内容")
			return
		}
		visibility = "archived"
		if _, err := tx.ExecContext(r.Context(), `UPDATE catalog_revisions SET visibility='archived',updated_at=? WHERE item_type=? AND item_id=?`, now, itemType, itemID); err != nil || deletePublishedCatalogTx(r.Context(), tx, itemType, itemID) != nil {
			writeAPIError(w, http.StatusInternalServerError, "CATALOG_ACTION_FAILED", "归档内容失败")
			return
		}
	case "rollback":
		var sourceFields string
		err := tx.QueryRowContext(r.Context(), `SELECT fields_json FROM catalog_revisions WHERE item_type=? AND item_id=? AND revision<? AND status IN ('published','superseded') ORDER BY revision DESC LIMIT 1`, itemType, itemID, revision).Scan(&sourceFields)
		if err == sql.ErrNoRows {
			writeAPIError(w, http.StatusConflict, "ROLLBACK_REVISION_UNAVAILABLE", "没有可回滚的历史 revision")
			return
		}
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "CATALOG_ACTION_FAILED", "创建回滚草稿失败")
			return
		}
		revision++
		revisionID = newID("crev_")
		status = "draft"
		fieldsRaw = sourceFields
		_ = json.Unmarshal([]byte(fieldsRaw), &fields)
		if _, err := tx.ExecContext(r.Context(), `INSERT INTO catalog_revisions(id,item_type,item_id,revision,status,visibility,fields_json,validation_errors_json,created_at,updated_at,published_at)
			VALUES(?,?,?,?,'draft',?,?,'{}',?,?, '')`, revisionID, itemType, itemID, revision, visibility, sourceFields, now, now); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "CATALOG_ACTION_FAILED", "创建回滚草稿失败")
			return
		}
	}
	requestID := newID("req_")
	admin, _ := adminFromContext(r.Context())
	ip, _ := a.clientIP(r)
	if err := insertAdminAuditTx(r.Context(), tx, admin.ID, "catalog."+action, itemType, itemID, requestID, ip.String(), map[string]any{"revision": revision}); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "AUDIT_FAILED", "无法记录管理员审计")
		return
	}
	if err := tx.Commit(); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "CATALOG_ACTION_FAILED", "内容操作失败")
		return
	}
	writeAdminV1Data(w, http.StatusOK, map[string]any{"item": catalogItemDTO(itemType, itemID, revision, status, visibility, fields, now), "status": status, "visibility": visibility}, requestID)
}

func (a *App) handleAdminStylePreviewV1(w http.ResponseWriter, r *http.Request) {
	itemID := r.PathValue("id")
	if !validAdminV1Identifier(itemID) {
		writeAPIError(w, http.StatusBadRequest, "INVALID_CATALOG_ID", "风格 ID 无效")
		return
	}
	var fieldsRaw string
	if err := a.store.db.QueryRowContext(r.Context(), `SELECT fields_json FROM catalog_revisions WHERE item_type='styles' AND item_id=? ORDER BY revision DESC LIMIT 1`, itemID).Scan(&fieldsRaw); err == sql.ErrNoRows {
		writeAPIError(w, http.StatusNotFound, "CATALOG_ITEM_NOT_FOUND", "风格不存在")
		return
	} else if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "CATALOG_UNAVAILABLE", "无法读取风格预览")
		return
	}
	var fields map[string]any
	if json.Unmarshal([]byte(fieldsRaw), &fields) != nil {
		writeAPIError(w, http.StatusInternalServerError, "CATALOG_UNAVAILABLE", "风格数据无效")
		return
	}
	if validationErrors := validateCatalogFields("styles", itemID, fields); len(validationErrors) > 0 {
		writeAPIErrorDetails(w, http.StatusUnprocessableEntity, "STYLE_PREVIEW_UNSAFE", "风格未通过安全校验", validationErrors)
		return
	}
	css, _ := fields["css"].(string)
	name := html.EscapeString(catalogName(fields))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; script-src 'none'; connect-src 'none'; frame-ancestors 'self'; form-action 'none'; base-uri 'none'")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><style>%s</style></head><body><main class="newtab-root" data-style-id="%s"><h1>%s</h1><section class="shortcut-card">示例快捷方式</section></main></body></html>`, css, html.EscapeString(itemID), name)
}

func (a *App) handleAdminReleasesV1(w http.ResponseWriter, r *http.Request) {
	releases, err := a.store.ListReleases(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "RELEASES_UNAVAILABLE", "无法读取版本记录")
		return
	}
	items := make([]map[string]any, 0, len(releases))
	for _, release := range releases {
		items = append(items, releaseAdminDTO(release))
	}
	writeAPIData(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) handleAdminReleaseCreateV1(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Version        string `json:"version"`
		Channel        string `json:"channel"`
		Notes          string `json:"notes"`
		DownloadURL    string `json:"downloadUrl"`
		MinimumVersion string `json:"minimumVersion"`
		Status         string `json:"status"`
	}
	if !a.decodeJSON(w, r, &input) {
		return
	}
	input.Version = strings.TrimSpace(input.Version)
	input.Channel = strings.TrimSpace(input.Channel)
	input.DownloadURL = strings.TrimSpace(input.DownloadURL)
	input.MinimumVersion = strings.TrimSpace(input.MinimumVersion)
	if !validSimpleSemver(input.Version) || !oneOf(input.Channel, "stable", "beta") || (input.MinimumVersion != "" && !validSimpleSemver(input.MinimumVersion)) || (input.DownloadURL != "" && !safeHTTPSURL(input.DownloadURL)) || (input.Status != "" && input.Status != "draft") {
		writeAPIError(w, http.StatusBadRequest, "INVALID_RELEASE", "版本草稿字段无效")
		return
	}
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "RELEASE_CREATE_FAILED", "无法创建版本草稿")
		return
	}
	defer tx.Rollback()
	id, createdAt := newID("reldraft_"), nowString()
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO app_releases(id,version,channel,notes,download_url,minimum_version,schema_version,status,created_at,updated_at,published_at,disabled_at)
		VALUES(?,?,?,?,?,?,2,'draft',?,?,'','')`, id, input.Version, input.Channel, input.Notes, input.DownloadURL, input.MinimumVersion, createdAt, createdAt); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeAPIError(w, http.StatusConflict, "RELEASE_EXISTS", "该通道版本草稿已存在")
		} else {
			writeAPIError(w, http.StatusInternalServerError, "RELEASE_CREATE_FAILED", "无法创建版本草稿")
		}
		return
	}
	requestID := newID("req_")
	admin, _ := adminFromContext(r.Context())
	ip, _ := a.clientIP(r)
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO release_events(id,release_id,action,from_status,to_status,admin_id,request_id,created_at) VALUES(?,?,'create','','draft',?,?,?)`, newID("relevt_"), id, admin.ID, requestID, createdAt); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "RELEASE_CREATE_FAILED", "无法记录版本历史")
		return
	}
	if err := insertAdminAuditTx(r.Context(), tx, admin.ID, "release.create", "release", id, requestID, ip.String(), map[string]any{"version": input.Version, "channel": input.Channel}); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "AUDIT_FAILED", "无法记录管理员审计")
		return
	}
	if err := tx.Commit(); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "RELEASE_CREATE_FAILED", "无法创建版本草稿")
		return
	}
	writeAdminV1Data(w, http.StatusCreated, map[string]any{"item": releaseAdminDTO(ReleaseRecord{ID: id, Version: input.Version, Channel: input.Channel, Notes: input.Notes, DownloadURL: input.DownloadURL, MinimumVersion: input.MinimumVersion, SchemaVersion: 2, Status: "draft", CreatedAt: createdAt, UpdatedAt: createdAt}), "status": "draft"}, requestID)
}

func (a *App) handleAdminReleaseActionV1(w http.ResponseWriter, r *http.Request) {
	releaseID, action := r.PathValue("id"), r.PathValue("action")
	if !oneOf(action, "publish", "disable") {
		writeAPIError(w, http.StatusNotFound, "RELEASE_ACTION_NOT_FOUND", "版本操作不存在")
		return
	}
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "RELEASE_UPDATE_FAILED", "无法更新版本")
		return
	}
	defer tx.Rollback()
	var release ReleaseRecord
	err = tx.QueryRowContext(r.Context(), `SELECT id,version,channel,notes,download_url,minimum_version,schema_version,status,created_at,updated_at,published_at,disabled_at FROM app_releases WHERE id=?`, releaseID).
		Scan(&release.ID, &release.Version, &release.Channel, &release.Notes, &release.DownloadURL, &release.MinimumVersion, &release.SchemaVersion, &release.Status, &release.CreatedAt, &release.UpdatedAt, &release.PublishedAt, &release.DisabledAt)
	if errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, http.StatusNotFound, "RELEASE_NOT_FOUND", "版本不存在")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "RELEASE_UPDATE_FAILED", "无法读取版本")
		return
	}
	expectedStatus, nextStatus := "draft", "published"
	if action == "disable" {
		expectedStatus, nextStatus = "published", "disabled"
	}
	if release.Status != expectedStatus {
		writeAPIError(w, http.StatusConflict, "INVALID_RELEASE_TRANSITION", fmt.Sprintf("版本不能从 %s 执行 %s", release.Status, action))
		return
	}
	now := nowString()
	release.Status, release.UpdatedAt = nextStatus, now
	if action == "publish" {
		release.PublishedAt, release.DisabledAt = now, ""
	} else {
		release.DisabledAt = now
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE app_releases SET status=?,updated_at=?,published_at=?,disabled_at=? WHERE id=? AND status=?`, release.Status, release.UpdatedAt, release.PublishedAt, release.DisabledAt, release.ID, expectedStatus); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "RELEASE_UPDATE_FAILED", "无法更新版本")
		return
	}
	requestID := newID("req_")
	admin, _ := adminFromContext(r.Context())
	ip, _ := a.clientIP(r)
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO release_events(id,release_id,action,from_status,to_status,admin_id,request_id,created_at) VALUES(?,?,?,?,?,?,?,?)`, newID("relevt_"), release.ID, action, expectedStatus, nextStatus, admin.ID, requestID, now); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "RELEASE_UPDATE_FAILED", "无法记录版本历史")
		return
	}
	if err := insertAdminAuditTx(r.Context(), tx, admin.ID, "release."+action, "release", release.ID, requestID, ip.String(), map[string]any{"version": release.Version, "channel": release.Channel, "fromStatus": expectedStatus, "toStatus": nextStatus}); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "AUDIT_FAILED", "无法记录管理员审计")
		return
	}
	if err := tx.Commit(); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "RELEASE_UPDATE_FAILED", "无法更新版本")
		return
	}
	writeAdminV1Data(w, http.StatusOK, map[string]any{"item": releaseAdminDTO(release), "status": release.Status}, requestID)
}

func (a *App) handleAdminReleaseHistoryV1(w http.ResponseWriter, r *http.Request) {
	releaseID := r.PathValue("id")
	var exists int
	if err := a.store.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM app_releases WHERE id=?`, releaseID).Scan(&exists); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "RELEASE_HISTORY_UNAVAILABLE", "无法读取版本历史")
		return
	}
	if exists == 0 {
		writeAPIError(w, http.StatusNotFound, "RELEASE_NOT_FOUND", "版本不存在")
		return
	}
	rows, err := a.store.db.QueryContext(r.Context(), `SELECT e.id,e.action,e.from_status,e.to_status,e.admin_id,COALESCE(a.email,''),e.request_id,e.created_at
		FROM release_events e LEFT JOIN admin_users a ON a.id=e.admin_id WHERE e.release_id=? ORDER BY e.created_at,e.id`, releaseID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "RELEASE_HISTORY_UNAVAILABLE", "无法读取版本历史")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, action, fromStatus, toStatus, adminID, adminEmail, requestID, createdAt string
		if err := rows.Scan(&id, &action, &fromStatus, &toStatus, &adminID, &adminEmail, &requestID, &createdAt); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "RELEASE_HISTORY_UNAVAILABLE", "无法读取版本历史")
			return
		}
		items = append(items, map[string]any{"id": id, "action": action, "fromStatus": fromStatus, "toStatus": toStatus, "adminId": adminID, "adminEmail": adminEmail, "requestId": requestID, "createdAt": createdAt})
	}
	if err := rows.Err(); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "RELEASE_HISTORY_UNAVAILABLE", "无法读取版本历史")
		return
	}
	writeAPIData(w, http.StatusOK, map[string]any{"items": items})
}

func releaseAdminDTO(release ReleaseRecord) map[string]any {
	return map[string]any{
		"id": release.ID, "version": release.Version, "channel": release.Channel,
		"notes": release.Notes, "downloadUrl": release.DownloadURL,
		"minimumVersion": release.MinimumVersion, "schemaVersion": release.SchemaVersion,
		"status": release.Status, "createdAt": release.CreatedAt, "updatedAt": release.UpdatedAt,
		"publishedAt": release.PublishedAt, "disabledAt": release.DisabledAt,
	}
}

func (a *App) handleAdminAuditV1(w http.ResponseWriter, r *http.Request) {
	rows, err := a.store.db.QueryContext(r.Context(), `SELECT l.id,l.created_at,l.admin_id,COALESCE(a.email,''),l.action,l.target_type,l.target_id,l.request_id,l.ip,l.details_json
		FROM admin_audit_logs l LEFT JOIN admin_users a ON a.id=l.admin_id ORDER BY l.created_at DESC LIMIT 500`)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "AUDIT_UNAVAILABLE", "无法读取管理员审计")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, createdAt, adminID, adminEmail, action, targetType, targetID, requestID, ip, details string
		if err := rows.Scan(&id, &createdAt, &adminID, &adminEmail, &action, &targetType, &targetID, &requestID, &ip, &details); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "AUDIT_UNAVAILABLE", "无法读取管理员审计")
			return
		}
		items = append(items, map[string]any{"id": id, "createdAt": createdAt, "adminId": adminID, "adminEmail": adminEmail, "action": action, "targetType": targetType, "targetId": targetID, "requestId": requestID, "ip": ip, "details": json.RawMessage(details), "status": "success"})
	}
	writeAPIData(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) handleAdminAccessAuditV1(w http.ResponseWriter, r *http.Request) {
	logs, err := a.store.ListAPILogs(r.Context(), APILogFilter{Limit: 500})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "ACCESS_LOGS_UNAVAILABLE", "无法读取访问日志")
		return
	}
	items := make([]map[string]any, 0, len(logs))
	for _, log := range logs {
		path := log.Path
		if index := strings.IndexByte(path, '?'); index >= 0 {
			path = path[:index]
		}
		items = append(items, map[string]any{"id": log.ID, "createdAt": log.CreatedAt, "userEmail": log.UserEmail, "ip": log.IP, "method": log.Method, "path": path, "routeGroup": log.RouteGroup, "status": log.Status, "durationMs": log.DurationMS, "requestId": log.ID})
	}
	writeAPIData(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) handleAdminAuditExportV1(w http.ResponseWriter, r *http.Request) {
	rows, err := a.store.db.QueryContext(r.Context(), `SELECT l.created_at,COALESCE(a.email,''),l.action,l.target_type,l.target_id,l.request_id,l.ip FROM admin_audit_logs l LEFT JOIN admin_users a ON a.id=l.admin_id ORDER BY l.created_at DESC LIMIT 5000`)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "AUDIT_EXPORT_FAILED", "无法导出管理员审计")
		return
	}
	defer rows.Close()
	records := [][]string{{"createdAt", "adminEmail", "action", "targetType", "targetId", "requestId", "ip"}}
	for rows.Next() {
		record := make([]string, 7)
		if err := rows.Scan(&record[0], &record[1], &record[2], &record[3], &record[4], &record[5], &record[6]); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "AUDIT_EXPORT_FAILED", "无法导出管理员审计")
			return
		}
		records = append(records, record)
	}
	writeAdminV1CSV(w, "admin-audit.csv", records)
}

func (a *App) handleAdminAccessAuditExportV1(w http.ResponseWriter, r *http.Request) {
	logs, err := a.store.ListAPILogs(r.Context(), APILogFilter{Limit: 500})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "AUDIT_EXPORT_FAILED", "无法导出访问日志")
		return
	}
	records := [][]string{{"createdAt", "userEmail", "ip", "method", "path", "routeGroup", "status", "durationMs"}}
	for _, log := range logs {
		path := log.Path
		if index := strings.IndexByte(path, '?'); index >= 0 {
			path = path[:index]
		}
		records = append(records, []string{log.CreatedAt, log.UserEmail, log.IP, log.Method, path, log.RouteGroup, strconv.Itoa(log.Status), strconv.FormatInt(log.DurationMS, 10)})
	}
	writeAdminV1CSV(w, "access-audit.csv", records)
}

type adminV1SMTPSettingsInput struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	TLS       string `json:"tls"`
	From      string `json:"from"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	Recipient string `json:"recipient,omitempty"`
}

func normalizeAdminV1SMTP(input adminV1SMTPSettingsInput) (SMTPSettings, error) {
	settings := SMTPSettings{
		Host: strings.TrimSpace(input.Host), Port: input.Port, TLS: strings.ToLower(strings.TrimSpace(input.TLS)),
		From: normalizeEmail(input.From), Username: strings.TrimSpace(input.Username),
	}
	if settings.Host == "" || settings.Port < 1 || settings.Port > 65535 || !oneOf(settings.TLS, "none", "starttls", "tls") || !validEmail(settings.From) {
		return SMTPSettings{}, fmt.Errorf("invalid SMTP settings")
	}
	return settings, nil
}

func adminV1SMTPFingerprint(key []byte, settings SMTPSettings, password string) string {
	payload, _ := json.Marshal(struct {
		Settings SMTPSettings `json:"settings"`
		Password string       `json:"password"`
	}{Settings: settings, Password: password})
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *App) adminV1SMTPSecret() (Secrets, error) {
	if strings.TrimSpace(a.config.SecretsPath) == "" {
		return Secrets{}, fmt.Errorf("secrets path is not configured")
	}
	secrets, _, err := LoadOrCreateSecrets(a.config.SecretsPath)
	return secrets, err
}

func (a *App) adminV1SMTPVerification(ctx context.Context) (fingerprint, verifiedAt string) {
	rows, err := a.store.db.QueryContext(ctx, `SELECT key,value FROM settings WHERE key IN ('smtp_verified_fingerprint','smtp_verified_at')`)
	if err != nil {
		return "", ""
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if rows.Scan(&key, &value) != nil {
			return "", ""
		}
		if key == "smtp_verified_fingerprint" {
			fingerprint = value
		} else {
			verifiedAt = value
		}
	}
	return fingerprint, verifiedAt
}

func (a *App) adminV1SettingsDTO(ctx context.Context, settings RuntimeSettings, secrets Secrets) map[string]any {
	result := adminV1SettingsDTO(settings)
	if settings.SMTP == nil {
		result["smtp"] = nil
		return result
	}
	fingerprint, verifiedAt := a.adminV1SMTPVerification(ctx)
	verified := len(secrets.TokenDerivationKey) == 32 && fingerprint == adminV1SMTPFingerprint(secrets.TokenDerivationKey, *settings.SMTP, secrets.SMTPPassword)
	if !verified && settings.RegistrationOpen {
		if current, ok := a.runtimeMailer().(SMTPMailer); ok {
			verified = current.Settings == *settings.SMTP && current.Password == secrets.SMTPPassword
		}
	}
	result["smtp"] = map[string]any{
		"host": settings.SMTP.Host, "port": settings.SMTP.Port, "tls": settings.SMTP.TLS, "from": settings.SMTP.From,
		"username": settings.SMTP.Username, "passwordConfigured": secrets.SMTPPassword != "", "verified": verified, "verifiedAt": verifiedAt,
	}
	return result
}

func (a *App) handleAdminSettingsGetV1(w http.ResponseWriter, r *http.Request) {
	settings, err := a.store.LoadRuntimeSettings(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "SETTINGS_UNAVAILABLE", "无法读取系统设置")
		return
	}
	secrets, _ := a.adminV1SMTPSecret()
	writeAPIData(w, http.StatusOK, map[string]any{"settings": a.adminV1SettingsDTO(r.Context(), settings, secrets)})
}

func (a *App) handleAdminSettingsSMTPTestV1(w http.ResponseWriter, r *http.Request) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	var input adminV1SMTPSettingsInput
	if !a.decodeJSON(w, r, &input) {
		return
	}
	settings, err := normalizeAdminV1SMTP(input)
	if err != nil || !validEmail(normalizeEmail(input.Recipient)) {
		writeAPIError(w, http.StatusBadRequest, "SMTP_INVALID", "SMTP 配置或测试收件人无效")
		return
	}
	secrets, secretErr := a.adminV1SMTPSecret()
	if secretErr != nil {
		writeAPIError(w, http.StatusInternalServerError, "SMTP_TEST_FAILED", "无法读取 SMTP secret")
		return
	}
	password := input.Password
	if password == "" {
		password = secrets.SMTPPassword
	}
	if settings.Username != "" && password == "" {
		writeAPIError(w, http.StatusPreconditionFailed, "SMTP_PASSWORD_REQUIRED", "请先输入或保存 SMTP 密码")
		return
	}
	tester := a.config.SMTPTester
	if tester == nil {
		tester = TestSMTP
	}
	if err := tester(r.Context(), SMTPTestInput{Host: settings.Host, Port: settings.Port, TLS: settings.TLS, From: settings.From, Username: settings.Username, Password: password, Recipient: normalizeEmail(input.Recipient)}); err != nil {
		writeAPIError(w, http.StatusBadGateway, "SMTP_TEST_FAILED", "SMTP 测试失败")
		return
	}
	now := nowString()
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "SMTP_TEST_RECORD_FAILED", "测试成功但无法保存验证状态")
		return
	}
	defer tx.Rollback()
	for key, value := range map[string]string{"smtp_verified_fingerprint": adminV1SMTPFingerprint(secrets.TokenDerivationKey, settings, password), "smtp_verified_at": now} {
		if _, err := tx.ExecContext(r.Context(), `INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, value, now); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "SMTP_TEST_RECORD_FAILED", "测试成功但无法保存验证状态")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "SMTP_TEST_RECORD_FAILED", "测试成功但无法保存验证状态")
		return
	}
	writeAPIData(w, http.StatusOK, map[string]any{"verified": true, "verifiedAt": now})
}

func (a *App) handleAdminSettingsPutV1(w http.ResponseWriter, r *http.Request) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	var raw map[string]json.RawMessage
	if !a.decodeJSON(w, r, &raw) {
		return
	}
	allowed := map[string]bool{"registrationEnabled": true, "publicBaseUrl": true, "webOrigins": true, "maxUsers": true, "profileKiB": true, "storageGiB": true, "versionsPerUser": true, "accessLogDays": true, "auditLogDays": true, "smtp": true}
	for key := range raw {
		if !allowed[key] {
			writeAPIError(w, http.StatusBadRequest, "SETTING_NOT_EDITABLE", "包含不可编辑或未知的系统设置")
			return
		}
	}
	settings, err := a.store.LoadRuntimeSettings(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "SETTINGS_UPDATE_FAILED", "无法读取系统设置")
		return
	}
	if settings.Limits == nil {
		settings.Limits = map[string]any{}
	}
	originalRegistrationOpen := settings.RegistrationOpen
	var originalSecrets Secrets
	smtpPassword := ""
	secretsLoaded := false
	if settings.SMTP != nil {
		if originalSecrets, err = a.adminV1SMTPSecret(); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "SETTINGS_UPDATE_FAILED", "无法读取 SMTP secret")
			return
		}
		secretsLoaded = true
		smtpPassword = originalSecrets.SMTPPassword
	}
	smtpProvided := false
	secretsChanged := false
	if value, found := raw["smtp"]; found {
		var input adminV1SMTPSettingsInput
		if json.Unmarshal(value, &input) != nil {
			writeAPIError(w, http.StatusBadRequest, "INVALID_SETTING", "smtp 必须是有效对象")
			return
		}
		nextSMTP, smtpErr := normalizeAdminV1SMTP(input)
		if smtpErr != nil {
			writeAPIError(w, http.StatusBadRequest, "INVALID_SETTING", "SMTP 配置无效")
			return
		}
		if !secretsLoaded {
			if originalSecrets, err = a.adminV1SMTPSecret(); err != nil {
				writeAPIError(w, http.StatusInternalServerError, "SETTINGS_UPDATE_FAILED", "无法读取 SMTP secret")
				return
			}
			secretsLoaded = true
			smtpPassword = originalSecrets.SMTPPassword
		}
		if input.Password != "" {
			smtpPassword = input.Password
			secretsChanged = input.Password != originalSecrets.SMTPPassword
		}
		if nextSMTP.Username != "" && smtpPassword == "" {
			writeAPIError(w, http.StatusPreconditionFailed, "SMTP_PASSWORD_REQUIRED", "SMTP 用户名需要已保存或新输入的密码")
			return
		}
		settings.SMTP = &nextSMTP
		smtpProvided = true
	}
	if value, found := raw["registrationEnabled"]; found {
		if json.Unmarshal(value, &settings.RegistrationOpen) != nil {
			writeAPIError(w, http.StatusBadRequest, "INVALID_SETTING", "registrationEnabled 必须是布尔值")
			return
		}
	}
	if settings.RegistrationOpen {
		if settings.SMTP == nil || (settings.SMTP.Username != "" && smtpPassword == "") {
			writeAPIError(w, http.StatusPreconditionFailed, "SMTP_REQUIRED", "开放注册前必须配置可用 SMTP")
			return
		}
		candidateFingerprint := adminV1SMTPFingerprint(originalSecrets.TokenDerivationKey, *settings.SMTP, smtpPassword)
		verifiedFingerprint, _ := a.adminV1SMTPVerification(r.Context())
		verified := verifiedFingerprint == candidateFingerprint
		if !verified && originalRegistrationOpen {
			if current, ok := a.runtimeMailer().(SMTPMailer); ok {
				verified = current.Settings == *settings.SMTP && current.Password == smtpPassword
			}
		}
		if !verified && (!originalRegistrationOpen || smtpProvided) {
			writeAPIError(w, http.StatusPreconditionFailed, "SMTP_TEST_REQUIRED", "开放注册前必须使用当前 SMTP 配置成功发送测试邮件")
			return
		}
	}
	if value, found := raw["publicBaseUrl"]; found {
		if json.Unmarshal(value, &settings.PublicBaseURL) != nil {
			writeAPIError(w, http.StatusBadRequest, "INVALID_SETTING", "publicBaseUrl 必须是字符串")
			return
		}
		parsed, parseErr := url.Parse(strings.TrimSpace(settings.PublicBaseURL))
		if parseErr != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
			writeAPIError(w, http.StatusBadRequest, "INVALID_SETTING", "publicBaseUrl 必须是绝对 HTTPS URL")
			return
		}
		settings.PublicBaseURL = strings.TrimRight(settings.PublicBaseURL, "/")
	}
	if value, found := raw["webOrigins"]; found {
		origins, parseErr := parseAdminV1Origins(value)
		if parseErr != nil {
			writeAPIError(w, http.StatusBadRequest, "INVALID_SETTING", "webOrigins 格式无效")
			return
		}
		for _, origin := range origins {
			if !validAllowedOrigin(origin) {
				writeAPIError(w, http.StatusBadRequest, "INVALID_SETTING", "webOrigins 包含无效来源")
				return
			}
		}
		settings.AllowedOrigins = origins
	}
	for _, limit := range []struct {
		key, stored string
		minimum     int
		maximum     int
		multiplier  int
	}{
		{"maxUsers", "maxUsers", 1, 1_000_000, 1},
		{"profileKiB", "profileBytes", 1, 1024, 1024},
		{"storageGiB", "storageBytes", 1, 1024, 1 << 30},
		{"versionsPerUser", "versionsPerUser", 1, 1000, 1},
		{"accessLogDays", "accessLogDays", 1, 365, 1},
		{"auditLogDays", "auditLogDays", 1, 3650, 1},
	} {
		value, found := raw[limit.key]
		if !found {
			continue
		}
		parsed, parseErr := parseAdminV1Integer(value)
		if parseErr != nil || parsed < limit.minimum || parsed > limit.maximum {
			writeAPIError(w, http.StatusBadRequest, "INVALID_SETTING", limit.key+" 超出允许范围")
			return
		}
		settings.Limits[limit.stored] = parsed * limit.multiplier
	}
	limitsJSON, _ := json.Marshal(settings.Limits)
	smtpJSON, _ := json.Marshal(settings.SMTP)
	settingsCommitted := false
	if secretsChanged {
		updatedSecrets := originalSecrets
		updatedSecrets.SMTPPassword = smtpPassword
		if err := SaveSecrets(a.config.SecretsPath, updatedSecrets); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "SETTINGS_UPDATE_FAILED", "无法保存 SMTP secret")
			return
		}
		defer func() {
			if !settingsCommitted {
				_ = SaveSecrets(a.config.SecretsPath, originalSecrets)
			}
		}()
	}
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "SETTINGS_UPDATE_FAILED", "无法保存系统设置")
		return
	}
	defer tx.Rollback()
	for key, value := range map[string]string{
		"external_base_url": settings.PublicBaseURL, "allowed_origins": strings.Join(settings.AllowedOrigins, "\n"),
		"registration_enabled": strconv.FormatBool(settings.RegistrationOpen), "limits": string(limitsJSON), "smtp_config": string(smtpJSON),
	} {
		if _, err := tx.ExecContext(r.Context(), `INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, value, nowString()); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "SETTINGS_UPDATE_FAILED", "无法保存系统设置")
			return
		}
	}
	requestID := newID("req_")
	admin, _ := adminFromContext(r.Context())
	ip, _ := a.clientIP(r)
	if err := insertAdminAuditTx(r.Context(), tx, admin.ID, "system.settings.update", "system_settings", "runtime", requestID, ip.String(), map[string]any{"keys": sortedAdminV1Keys(raw)}); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "AUDIT_FAILED", "无法记录管理员审计")
		return
	}
	if err := tx.Commit(); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "SETTINGS_UPDATE_FAILED", "无法保存系统设置")
		return
	}
	settingsCommitted = true
	a.applyRuntimeSettings(settings, smtpPassword)
	responseSecrets := originalSecrets
	responseSecrets.SMTPPassword = smtpPassword
	writeAdminV1Data(w, http.StatusOK, map[string]any{"settings": a.adminV1SettingsDTO(r.Context(), settings, responseSecrets), "restartRequired": false}, requestID)
}

func (a *App) handleAdminMaintenanceJobsV1(w http.ResponseWriter, r *http.Request) {
	rows, err := a.store.db.QueryContext(r.Context(), `SELECT id,kind,status,detail,error,created_at,started_at,finished_at FROM maintenance_jobs ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "MAINTENANCE_UNAVAILABLE", "无法读取维护任务")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, kind, status, detail, jobError, createdAt, startedAt, finishedAt string
		if err := rows.Scan(&id, &kind, &status, &detail, &jobError, &createdAt, &startedAt, &finishedAt); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "MAINTENANCE_UNAVAILABLE", "无法读取维护任务")
			return
		}
		items = append(items, map[string]any{"id": id, "kind": kind, "status": status, "detail": detail, "error": jobError, "createdAt": createdAt, "startedAt": startedAt, "finishedAt": finishedAt})
	}
	writeAPIData(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) handleAdminMaintenanceCreateV1(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Kind string `json:"kind"`
	}
	if !a.decodeJSON(w, r, &input) {
		return
	}
	input.Kind = strings.TrimSpace(input.Kind)
	if !oneOf(input.Kind, "cleanup", "checkpoint", "retention") {
		writeAPIError(w, http.StatusBadRequest, "INVALID_MAINTENANCE_JOB", "维护任务类型无效")
		return
	}
	jobID, startedAt := newID("job_"), nowString()
	claimed, err := a.store.claimMaintenanceJob(r.Context(), jobID, input.Kind, startedAt)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "MAINTENANCE_FAILED", "无法创建维护任务")
		return
	}
	if !claimed {
		writeAPIError(w, http.StatusConflict, "MAINTENANCE_CONFLICT", "已有维护或恢复任务正在运行")
		return
	}
	if a.beforeMaintenanceRun != nil {
		a.beforeMaintenanceRun()
	}
	detail, runErr := a.runAdminV1Maintenance(r.Context(), input.Kind, jobID)
	finishedAt := nowString()
	status, errorText := "completed", ""
	if runErr != nil {
		status, errorText = "failed", runErr.Error()
	}
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "MAINTENANCE_FAILED", "无法完成维护任务")
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), `UPDATE maintenance_jobs SET status=?,detail=?,error=?,finished_at=? WHERE id=?`, status, detail, errorText, finishedAt, jobID); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "MAINTENANCE_FAILED", "无法完成维护任务")
		return
	}
	requestID := newID("req_")
	admin, _ := adminFromContext(r.Context())
	ip, _ := a.clientIP(r)
	if err := insertAdminAuditTx(r.Context(), tx, admin.ID, "system.maintenance."+input.Kind, "maintenance_job", jobID, requestID, ip.String(), map[string]any{"status": status}); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "AUDIT_FAILED", "无法记录管理员审计")
		return
	}
	if err := tx.Commit(); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "MAINTENANCE_FAILED", "无法完成维护任务")
		return
	}
	if runErr != nil {
		if errors.Is(runErr, errAdminV1MaintenanceConflict) {
			writeAPIError(w, http.StatusConflict, "MAINTENANCE_CONFLICT", "维护任务因并发维护或恢复状态而未执行")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "MAINTENANCE_FAILED", "维护任务执行失败")
		return
	}
	writeAdminV1Data(w, http.StatusCreated, map[string]any{"item": map[string]any{"id": jobID, "kind": input.Kind, "status": status, "detail": detail, "createdAt": startedAt, "startedAt": startedAt, "finishedAt": finishedAt}, "status": status}, requestID)
}

var errAdminV1MaintenanceConflict = errors.New("maintenance execution skipped because another operation is active")

func (a *App) runAdminV1Maintenance(ctx context.Context, kind, jobID string) (string, error) {
	switch kind {
	case "checkpoint":
		if _, err := a.store.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			return "", err
		}
		return "SQLite checkpoint 已完成", nil
	case "cleanup":
		if err := a.applyAdminV1Retention(ctx, jobID); err != nil {
			return "", err
		}
		return "过期会话、token 与保留期数据已清理", nil
	case "retention":
		if err := a.applyAdminV1Retention(ctx, jobID); err != nil {
			return "", err
		}
		return "访问日志、审计与同步证据保留策略已执行", nil
	default:
		return "", fmt.Errorf("unsupported maintenance job")
	}
}

func (a *App) applyAdminV1Retention(ctx context.Context, jobID string) error {
	result, err := a.store.runRetention(ctx, time.Now().UTC(), jobID)
	if err == nil && result.Skipped {
		return errAdminV1MaintenanceConflict
	}
	return err
}

func (a *App) handleAdminBackupsV1(w http.ResponseWriter, r *http.Request) {
	rows, err := a.store.db.QueryContext(r.Context(), `SELECT id,kind,status,checksum,size_bytes,created_at,restored_at FROM backup_records ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "BACKUPS_UNAVAILABLE", "无法读取备份")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, kind, status, checksum, createdAt, restoredAt string
		var size int64
		if err := rows.Scan(&id, &kind, &status, &checksum, &size, &createdAt, &restoredAt); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "BACKUPS_UNAVAILABLE", "无法读取备份")
			return
		}
		items = append(items, map[string]any{"id": id, "kind": kind, "status": status, "checksum": checksum, "sizeBytes": size, "createdAt": createdAt, "restoredAt": restoredAt})
	}
	writeAPIData(w, http.StatusOK, map[string]any{"items": items})
}

const (
	adminV1BackupManifestFormat  = "fullpro-backup"
	adminV1BackupManifestVersion = 1
)

type adminV1BackupManifest struct {
	Format        string `json:"format"`
	FormatVersion int    `json:"formatVersion"`
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	CreatedAt     string `json:"createdAt"`
	SchemaVersion int    `json:"schemaVersion"`
	DatabaseFile  string `json:"databaseFile"`
	SizeBytes     int64  `json:"sizeBytes"`
	SHA256        string `json:"sha256"`
	SecretsFile   string `json:"secretsFile,omitempty"`
	SecretsSize   int64  `json:"secretsSizeBytes,omitempty"`
	SecretsSHA256 string `json:"secretsSha256,omitempty"`
	KDF           string `json:"kdf,omitempty"`
	Cipher        string `json:"cipher,omitempty"`
}

func decodeAdminV1BackupManifest(raw []byte) (adminV1BackupManifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest adminV1BackupManifest
	if err := decoder.Decode(&manifest); err != nil {
		return adminV1BackupManifest{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return adminV1BackupManifest{}, fmt.Errorf("backup manifest contains multiple JSON values")
		}
		return adminV1BackupManifest{}, fmt.Errorf("backup manifest contains trailing data: %w", err)
	}
	if manifest.Format != adminV1BackupManifestFormat || manifest.FormatVersion != adminV1BackupManifestVersion {
		return adminV1BackupManifest{}, fmt.Errorf("unsupported backup manifest format")
	}
	return manifest, nil
}

func (a *App) handleAdminBackupCreateV1(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Kind       string `json:"kind"`
		Passphrase string `json:"passphrase,omitempty"`
	}
	if !a.decodeJSON(w, r, &input) {
		return
	}
	if !oneOf(input.Kind, "full", "data-only") {
		writeAPIError(w, http.StatusBadRequest, "INVALID_BACKUP_KIND", "备份类型无效")
		return
	}
	if input.Kind == "full" && len(input.Passphrase) < 12 {
		writeAPIError(w, http.StatusBadRequest, "BACKUP_PASSPHRASE_REQUIRED", "完整备份需要至少 12 个字符的恢复口令")
		return
	}
	a.store.backupMu.Lock()
	defer a.store.backupMu.Unlock()
	if input.Kind == "full" {
		a.settingsMu.Lock()
		defer a.settingsMu.Unlock()
	}
	livePath, err := adminV1DatabasePath(r.Context(), a.store.db)
	if err != nil || livePath == "" {
		writeAPIError(w, http.StatusInternalServerError, "BACKUP_FAILED", "无法定位 SQLite 数据库")
		return
	}
	backupDir, err := a.adminV1BackupDirectory(r.Context(), livePath)
	if err != nil || os.MkdirAll(backupDir, 0o700) != nil {
		writeAPIError(w, http.StatusInternalServerError, "BACKUP_FAILED", "无法创建备份目录")
		return
	}
	id, createdAt := newID("backup_"), nowString()
	databasePath := filepath.Join(backupDir, id+".sqlite")
	manifestPath := filepath.Join(backupDir, id+".manifest.json")
	encryptedSecretsPath := filepath.Join(backupDir, id+".secrets.enc")
	if err := createSQLiteOnlineBackup(r.Context(), a.store.db, databasePath); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "BACKUP_FAILED", "无法创建 SQLite 一致性快照")
		return
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(databasePath)
			_ = os.Remove(manifestPath)
			_ = os.Remove(encryptedSecretsPath)
		}
	}()
	if err := os.Chmod(databasePath, 0o600); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "BACKUP_FAILED", "无法保护备份文件")
		return
	}
	checksum, sizeBytes, err := adminV1FileSHA256(databasePath)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "BACKUP_FAILED", "无法校验备份文件")
		return
	}
	manifest := adminV1BackupManifest{Format: adminV1BackupManifestFormat, FormatVersion: adminV1BackupManifestVersion, ID: id, Kind: input.Kind, CreatedAt: createdAt, SchemaVersion: schemaVersion, DatabaseFile: filepath.Base(databasePath), SizeBytes: sizeBytes, SHA256: checksum}
	if input.Kind == "full" {
		if a.config.SecretsPath == "" {
			writeAPIError(w, http.StatusInternalServerError, "BACKUP_FAILED", "服务未配置 secrets 文件路径")
			return
		}
		secretsRaw, readErr := os.ReadFile(a.config.SecretsPath)
		if readErr != nil || adminV1ValidateSecretsJSON(secretsRaw) != nil {
			writeAPIError(w, http.StatusInternalServerError, "BACKUP_FAILED", "无法读取有效 secrets 文件")
			return
		}
		encrypted, encryptErr := adminV1EncryptSecrets(secretsRaw, input.Passphrase)
		if encryptErr != nil || adminV1WriteFileAtomic(encryptedSecretsPath, encrypted, 0o600) != nil {
			writeAPIError(w, http.StatusInternalServerError, "BACKUP_FAILED", "无法加密 secrets 文件")
			return
		}
		secretsChecksum, secretsSize, hashErr := adminV1FileSHA256(encryptedSecretsPath)
		if hashErr != nil {
			writeAPIError(w, http.StatusInternalServerError, "BACKUP_FAILED", "无法校验加密 secrets 文件")
			return
		}
		manifest.SecretsFile = filepath.Base(encryptedSecretsPath)
		manifest.SecretsSize = secretsSize
		manifest.SecretsSHA256 = secretsChecksum
		manifest.KDF = "Argon2id"
		manifest.Cipher = "AES-256-GCM"
	}
	manifestJSON, _ := json.MarshalIndent(manifest, "", "  ")
	if err := adminV1WriteFileAtomic(manifestPath, append(manifestJSON, '\n'), 0o600); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "BACKUP_FAILED", "无法写入备份 manifest")
		return
	}
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "BACKUP_FAILED", "无法登记备份")
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO backup_records(id,kind,status,database_path,manifest_path,checksum,size_bytes,created_at,restored_at) VALUES(?,?,'ready',?,?,?,?,?,'')`, id, input.Kind, databasePath, manifestPath, checksum, sizeBytes, createdAt); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "BACKUP_FAILED", "无法登记备份")
		return
	}
	requestID := newID("req_")
	admin, _ := adminFromContext(r.Context())
	ip, _ := a.clientIP(r)
	if err := insertAdminAuditTx(r.Context(), tx, admin.ID, "system.backup.create", "backup", id, requestID, ip.String(), map[string]any{"kind": input.Kind, "checksum": checksum, "sizeBytes": sizeBytes}); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "AUDIT_FAILED", "无法记录管理员审计")
		return
	}
	if err := tx.Commit(); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "BACKUP_FAILED", "无法登记备份")
		return
	}
	cleanup = false
	writeAdminV1Data(w, http.StatusCreated, map[string]any{"item": map[string]any{"id": id, "kind": input.Kind, "status": "ready", "checksum": checksum, "sizeBytes": sizeBytes, "createdAt": createdAt}}, requestID)
}

func (a *App) handleAdminBackupRestoreV1(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validAdminV1Identifier(id) {
		writeAPIError(w, http.StatusBadRequest, "INVALID_BACKUP_ID", "备份 ID 无效")
		return
	}
	var input struct {
		Passphrase string `json:"passphrase,omitempty"`
	}
	if !a.decodeJSON(w, r, &input) {
		return
	}
	a.store.backupMu.Lock()
	defer a.store.backupMu.Unlock()
	var kind, status, databasePath, manifestPath, checksum, createdAt string
	var sizeBytes int64
	err := a.store.db.QueryRowContext(r.Context(), `SELECT kind,status,database_path,manifest_path,checksum,size_bytes,created_at FROM backup_records WHERE id=?`, id).
		Scan(&kind, &status, &databasePath, &manifestPath, &checksum, &sizeBytes, &createdAt)
	if err == sql.ErrNoRows {
		writeAPIError(w, http.StatusNotFound, "BACKUP_NOT_FOUND", "备份不存在")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "RESTORE_FAILED", "无法读取备份记录")
		return
	}
	if status != "ready" {
		writeAPIError(w, http.StatusConflict, "BACKUP_NOT_READY", "备份当前不可恢复")
		return
	}
	livePath, err := adminV1DatabasePath(r.Context(), a.store.db)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "RESTORE_FAILED", "无法定位 SQLite 数据库")
		return
	}
	backupDir, err := a.adminV1BackupDirectory(r.Context(), livePath)
	if err != nil || !adminV1PathWithin(backupDir, databasePath) || !adminV1PathWithin(backupDir, manifestPath) {
		writeAPIError(w, http.StatusConflict, "BACKUP_PATH_REJECTED", "备份路径不在登记目录内")
		return
	}
	actualChecksum, actualSize, err := adminV1FileSHA256(databasePath)
	if err != nil || actualChecksum != checksum || actualSize != sizeBytes {
		writeAPIError(w, http.StatusConflict, "BACKUP_CHECKSUM_MISMATCH", "备份文件校验失败")
		return
	}
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		writeAPIError(w, http.StatusConflict, "BACKUP_MANIFEST_INVALID", "备份 manifest 不可读")
		return
	}
	manifest, manifestErr := decodeAdminV1BackupManifest(manifestRaw)
	if manifestErr != nil || manifest.ID != id || manifest.Kind != kind || manifest.DatabaseFile != filepath.Base(databasePath) || manifest.SHA256 != checksum || manifest.SizeBytes != sizeBytes || manifest.SchemaVersion < 1 || manifest.SchemaVersion > schemaVersion {
		writeAPIError(w, http.StatusConflict, "BACKUP_MANIFEST_INVALID", "备份 manifest 校验失败")
		return
	}
	liveSecretsPath, stagedSecretsPath := a.config.SecretsPath, ""
	if kind == "full" {
		if len(input.Passphrase) < 12 || liveSecretsPath == "" || manifest.SecretsFile == "" || manifest.SecretsSHA256 == "" || manifest.SecretsSize <= 0 || manifest.KDF != "Argon2id" || manifest.Cipher != "AES-256-GCM" {
			writeAPIError(w, http.StatusBadRequest, "BACKUP_PASSPHRASE_REQUIRED", "完整备份需要正确的恢复口令和完整 secrets manifest")
			return
		}
		encryptedSecretsPath := filepath.Join(backupDir, manifest.SecretsFile)
		if !adminV1PathWithin(backupDir, encryptedSecretsPath) {
			writeAPIError(w, http.StatusConflict, "BACKUP_PATH_REJECTED", "加密 secrets 路径不在登记目录内")
			return
		}
		secretsChecksum, secretsSize, hashErr := adminV1FileSHA256(encryptedSecretsPath)
		if hashErr != nil || secretsChecksum != manifest.SecretsSHA256 || secretsSize != manifest.SecretsSize {
			writeAPIError(w, http.StatusConflict, "BACKUP_CHECKSUM_MISMATCH", "加密 secrets 文件校验失败")
			return
		}
		encrypted, readErr := os.ReadFile(encryptedSecretsPath)
		decrypted, decryptErr := adminV1DecryptSecrets(encrypted, input.Passphrase)
		if readErr != nil || decryptErr != nil || adminV1ValidateSecretsJSON(decrypted) != nil {
			writeAPIError(w, http.StatusConflict, "BACKUP_PASSPHRASE_INVALID", "恢复口令错误或 secrets 文件无效")
			return
		}
		stagedSecretsPath = filepath.Join(filepath.Dir(liveSecretsPath), "."+id+"-restore-secrets.json")
		if err := adminV1WriteFileAtomic(stagedSecretsPath, decrypted, 0o600); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "RESTORE_FAILED", "无法准备 secrets 恢复文件")
			return
		}
	}
	if err := adminV1CheckSQLiteSnapshotCompatible(databasePath, manifest.SchemaVersion); err != nil {
		_ = os.Remove(stagedSecretsPath)
		writeAPIError(w, http.StatusConflict, "BACKUP_DATABASE_INVALID", "备份 SQLite 完整性校验失败")
		return
	}
	stagedPath := filepath.Join(filepath.Dir(livePath), "."+id+"-restore.sqlite")
	if err := adminV1CopyFileAtomic(databasePath, stagedPath, 0o600); err != nil {
		_ = os.Remove(stagedSecretsPath)
		writeAPIError(w, http.StatusInternalServerError, "RESTORE_FAILED", "无法准备恢复文件")
		return
	}
	if err := adminV1MigrateRestoreSnapshot(stagedPath); err != nil {
		_ = os.Remove(stagedPath)
		_ = os.Remove(stagedSecretsPath)
		writeAPIError(w, http.StatusConflict, "BACKUP_DATABASE_INVALID", "备份 schema 无法安全迁移到当前版本")
		return
	}
	if err := adminV1PrepareDataOnlyRestore(stagedPath); err != nil {
		_ = os.Remove(stagedPath)
		_ = os.Remove(stagedSecretsPath)
		writeAPIError(w, http.StatusConflict, "BACKUP_DATABASE_INVALID", "无法撤销恢复快照中的会话与 token")
		return
	}
	if err := adminV1CheckSQLiteSnapshot(stagedPath); err != nil {
		_ = os.Remove(stagedPath)
		_ = os.Remove(stagedSecretsPath)
		writeAPIError(w, http.StatusConflict, "BACKUP_DATABASE_INVALID", "迁移后的备份 SQLite 校验失败")
		return
	}
	if kind == "data-only" {
		if liveSecretsPath != "" {
			stagedSecretsPath = filepath.Join(filepath.Dir(liveSecretsPath), "."+id+"-restore-secrets.json")
			_ = os.Remove(stagedSecretsPath)
			if _, _, err := LoadOrCreateSecrets(stagedSecretsPath); err != nil {
				_ = os.Remove(stagedPath)
				writeAPIError(w, http.StatusInternalServerError, "RESTORE_FAILED", "无法生成 data-only 恢复的新 secrets")
				return
			}
		}
	}
	restoreIntent, err := newAdminV1RestoreIntent(id, livePath, stagedPath, liveSecretsPath, stagedSecretsPath)
	if err != nil {
		_ = os.Remove(stagedPath)
		_ = os.Remove(stagedSecretsPath)
		writeAPIError(w, http.StatusInternalServerError, "RESTORE_FAILED", "无法创建持久恢复意图")
		return
	}
	if err := writeAdminV1RestoreIntent(restoreIntent); err != nil {
		_ = os.Remove(stagedPath)
		_ = os.Remove(stagedSecretsPath)
		writeAPIError(w, http.StatusConflict, "RESTORE_ALREADY_RUNNING", "已有持久恢复意图尚未完成")
		return
	}
	restoreScheduled := false
	defer func() {
		if !restoreScheduled {
			_ = discardAdminV1PreparedRestore(restoreIntent)
		}
	}()
	a.store.maintenanceMu.Lock()
	maintenanceLocked := true
	defer func() {
		if maintenanceLocked {
			a.store.maintenanceMu.Unlock()
		}
	}()
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		_ = os.Remove(stagedPath)
		_ = os.Remove(stagedSecretsPath)
		writeAPIError(w, http.StatusInternalServerError, "RESTORE_FAILED", "无法登记恢复任务")
		return
	}
	defer tx.Rollback()
	activeOperation, err := persistedMaintenanceActiveExceptWithQuerier(r.Context(), tx, "")
	if err != nil {
		_ = os.Remove(stagedPath)
		_ = os.Remove(stagedSecretsPath)
		writeAPIError(w, http.StatusInternalServerError, "RESTORE_FAILED", "无法检查恢复维护状态")
		return
	}
	if activeOperation {
		_ = os.Remove(stagedPath)
		_ = os.Remove(stagedSecretsPath)
		writeAPIError(w, http.StatusConflict, "RESTORE_ALREADY_RUNNING", "已有维护或备份恢复任务正在运行")
		return
	}
	restoredAt := nowString()
	result, err := tx.ExecContext(r.Context(), `UPDATE backup_records SET status='restoring',restored_at=? WHERE id=? AND status='ready'`, restoredAt, id)
	if err != nil {
		_ = os.Remove(stagedPath)
		_ = os.Remove(stagedSecretsPath)
		writeAPIError(w, http.StatusInternalServerError, "RESTORE_FAILED", "无法登记恢复任务")
		return
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
		_ = os.Remove(stagedPath)
		_ = os.Remove(stagedSecretsPath)
		writeAPIError(w, http.StatusConflict, "BACKUP_NOT_READY", "备份已由另一个恢复任务占用")
		return
	}
	requestID := newID("req_")
	admin, _ := adminFromContext(r.Context())
	ip, _ := a.clientIP(r)
	if err := insertAdminAuditTx(r.Context(), tx, admin.ID, "system.backup.restore", "backup", id, requestID, ip.String(), map[string]any{"checksum": checksum, "createdAt": createdAt}); err != nil {
		_ = os.Remove(stagedPath)
		_ = os.Remove(stagedSecretsPath)
		writeAPIError(w, http.StatusInternalServerError, "AUDIT_FAILED", "无法记录管理员审计")
		return
	}
	if err := tx.Commit(); err != nil {
		_ = os.Remove(stagedPath)
		_ = os.Remove(stagedSecretsPath)
		writeAPIError(w, http.StatusInternalServerError, "RESTORE_FAILED", "无法登记恢复任务")
		return
	}
	a.store.maintenanceMu.Unlock()
	maintenanceLocked = false
	restoreScheduled = true
	a.maintenanceMode.Store(true)
	writeAdminV1Data(w, http.StatusAccepted, map[string]any{"id": id, "status": "restoring", "restartRequired": true}, requestID)
	adminV1ScheduleRestore(a, livePath, stagedPath, liveSecretsPath, stagedSecretsPath)
}

func (a *App) handleAdminSystemHealthV1(w http.ResponseWriter, r *http.Request) {
	databasePath, _ := adminV1DatabasePath(r.Context(), a.store.db)
	var databaseBytes int64
	if info, err := os.Stat(databasePath); err == nil {
		databaseBytes = info.Size()
	}
	softLimitBytes, _ := a.store.PersistedLimit(r.Context(), "storageBytes", 1<<30)
	remainingBytes := int64(softLimitBytes) - databaseBytes
	if remainingBytes < 0 {
		remainingBytes = 0
	}
	writeAPIData(w, http.StatusOK, map[string]any{
		"health":  []map[string]any{{"id": "api", "label": "API", "status": "healthy", "detail": "ready"}, {"id": "sqlite", "label": "SQLite", "status": "healthy", "detail": "queryable"}},
		"startup": map[string]any{"listenAddress": a.config.Addr, "dataDirectory": filepath.Dir(databasePath), "adminCidrs": a.config.AdminAllowedCIDRs, "trustedProxies": a.config.TrustedProxyCIDRs},
		"storage": map[string]any{"databaseBytes": databaseBytes, "freeBytes": remainingBytes, "softLimitBytes": softLimitBytes},
	})
}

func (a *App) adminV1UserSessions(ctx context.Context, userID string) ([]map[string]any, error) {
	rows, err := a.store.db.QueryContext(ctx, `SELECT id,device_id,created_at,created_at FROM refresh_token_families WHERE user_id=? AND revoked_at='' AND expires_at>? ORDER BY created_at DESC LIMIT 50`, userID, nowString())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, deviceID, createdAt, lastUsedAt string
		if err := rows.Scan(&id, &deviceID, &createdAt, &lastUsedAt); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": id, "deviceName": deviceID, "createdAt": createdAt, "lastUsedAt": lastUsedAt})
	}
	return items, rows.Err()
}

func (a *App) adminV1UserAttempts(ctx context.Context, userID string) ([]map[string]any, error) {
	rows, err := a.store.db.QueryContext(ctx, `SELECT id,status,error_code,mutation_id,created_at FROM sync_attempts WHERE user_id=? ORDER BY created_at DESC LIMIT 20`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, code, requestID, createdAt string
		var statusCode int
		if err := rows.Scan(&id, &statusCode, &code, &requestID, &createdAt); err != nil {
			return nil, err
		}
		status := "failed"
		if statusCode >= 200 && statusCode < 300 {
			status = "success"
		}
		items = append(items, map[string]any{"id": id, "status": status, "code": code, "requestId": requestID, "createdAt": createdAt})
	}
	return items, rows.Err()
}

func (a *App) adminV1UserVersions(ctx context.Context, userID string) ([]map[string]any, error) {
	var currentVersion int
	var currentProfileJSON string
	if err := a.store.db.QueryRowContext(ctx, `SELECT version,profile_json FROM sync_profiles WHERE user_id=?`, userID).Scan(&currentVersion, &currentProfileJSON); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	currentSummary := summarizeAdminV1ProfileStructure([]byte(currentProfileJSON))
	rows, err := a.store.db.QueryContext(ctx, `SELECT id,version,created_at,profile_json FROM sync_profile_versions WHERE user_id=? ORDER BY version DESC LIMIT 21`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type versionRow struct {
		id, createdAt, profileJSON string
		version                    int
		summary                    adminV1ProfileStructureSummary
	}
	versions := []versionRow{}
	for rows.Next() {
		var item versionRow
		if err := rows.Scan(&item.id, &item.version, &item.createdAt, &item.profileJSON); err != nil {
			return nil, err
		}
		item.summary = summarizeAdminV1ProfileStructure([]byte(item.profileJSON))
		versions = append(versions, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	limit := len(versions)
	if limit > 20 {
		limit = 20
	}
	items := make([]map[string]any, 0, limit)
	for index := 0; index < limit; index++ {
		item := versions[index]
		changes := adminV1ProfileStructureChanges{
			CurrentVersion:   currentVersion,
			GroupsDelta:      item.summary.Groups - currentSummary.Groups,
			ShortcutsDelta:   item.summary.Shortcuts - currentSummary.Shortcuts,
			WallpaperChanged: item.summary.wallpaperFingerprint != currentSummary.wallpaperFingerprint,
			StyleChanged:     item.summary.StyleID != currentSummary.StyleID,
		}
		items = append(items, map[string]any{"id": item.id, "version": item.version, "createdAt": item.createdAt, "summary": item.summary, "changes": changes})
	}
	return items, nil
}

type adminV1ProfileStructureSummary struct {
	Groups               int    `json:"groups"`
	Shortcuts            int    `json:"shortcuts"`
	Wallpaper            string `json:"wallpaper"`
	StyleID              string `json:"styleId"`
	wallpaperFingerprint string
}

type adminV1ProfileStructureChanges struct {
	CurrentVersion   int  `json:"currentVersion"`
	GroupsDelta      int  `json:"groupsDelta"`
	ShortcutsDelta   int  `json:"shortcutsDelta"`
	WallpaperChanged bool `json:"wallpaperChanged"`
	StyleChanged     bool `json:"styleChanged"`
}

func summarizeAdminV1ProfileStructure(raw []byte) adminV1ProfileStructureSummary {
	var profile struct {
		Groups    []json.RawMessage `json:"groups"`
		Shortcuts []json.RawMessage `json:"shortcuts"`
		Wallpaper struct {
			Selected         json.RawMessage `json:"selected"`
			PortableSelected json.RawMessage `json:"portableSelected"`
		} `json:"wallpaper"`
		Theme struct {
			StyleID string `json:"styleId"`
		} `json:"theme"`
	}
	if json.Unmarshal(raw, &profile) != nil {
		return adminV1ProfileStructureSummary{}
	}
	wallpaper, wallpaperFingerprint := adminV1WallpaperSummary(profile.Wallpaper.PortableSelected, profile.Wallpaper.Selected)
	return adminV1ProfileStructureSummary{
		Groups: len(profile.Groups), Shortcuts: len(profile.Shortcuts), Wallpaper: wallpaper, StyleID: profile.Theme.StyleID,
		wallpaperFingerprint: wallpaperFingerprint,
	}
}

func adminV1WallpaperSummary(preferred, fallback json.RawMessage) (string, string) {
	for _, raw := range []json.RawMessage{preferred, fallback} {
		if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			continue
		}
		object, ok := value.(map[string]any)
		if !ok {
			continue
		}
		kind, _ := object["kind"].(string)
		if kind == "" {
			continue
		}
		canonical, err := json.Marshal(value)
		if err != nil {
			continue
		}
		digest := sha256.Sum256(append([]byte("fullpro-wallpaper-selection-v1\x00"), canonical...))
		return kind, hex.EncodeToString(digest[:])
	}
	return "", ""
}

func (a *App) adminV1CatalogItems(ctx context.Context, itemType string) ([]map[string]any, error) {
	items := []map[string]any{}
	rows, err := a.store.db.QueryContext(ctx, `SELECT r.item_id,r.revision,r.status,r.visibility,r.fields_json,r.updated_at
		FROM catalog_revisions r WHERE r.item_type=? AND r.revision=(SELECT MAX(x.revision) FROM catalog_revisions x WHERE x.item_type=r.item_type AND x.item_id=r.item_id)
		ORDER BY r.updated_at DESC`, itemType)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for rows.Next() {
		var id, status, visibility, raw, updatedAt string
		var revision int
		if err := rows.Scan(&id, &revision, &status, &visibility, &raw, &updatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		item := map[string]any{"id": id, "revision": revision, "status": status, "visibility": visibility, "updatedAt": updatedAt}
		var fields map[string]any
		_ = json.Unmarshal([]byte(raw), &fields)
		copyCatalogSummary(item, fields)
		items = append(items, item)
		seen[id] = true
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	switch itemType {
	case "official":
		records, err := a.store.ListAdminOfficialWallpapers(ctx)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			if !seen[record.ID] {
				items = append(items, map[string]any{"id": record.ID, "title": record.Title, "name": record.Title, "previewUrl": record.PreviewURL, "status": "published", "visibility": visibilityFor(record.Enabled), "revision": 1, "updatedAt": record.UpdatedAt})
			}
		}
	case "web":
		records, err := a.store.ListAdminWebWallpapers(ctx)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			if !seen[record.ID] {
				items = append(items, map[string]any{"id": record.ID, "title": record.Title, "name": record.Title, "previewUrl": record.PreviewURL, "status": "published", "visibility": visibilityFor(record.Enabled), "revision": 1, "updatedAt": record.UpdatedAt})
			}
		}
	case "styles":
		records, err := a.store.ListAdminStylePackages(ctx)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			if !seen[record.ID] {
				items = append(items, map[string]any{"id": record.ID, "name": record.Name, "description": record.Description, "previewUrl": record.PreviewURL, "status": "published", "visibility": visibilityFor(record.Enabled), "revision": 1, "updatedAt": record.UpdatedAt})
			}
		}
	}
	return items, nil
}

func validCatalogType(itemType string) bool {
	return oneOf(itemType, "official", "web", "styles")
}

func (a *App) decodeAdminCatalogFields(w http.ResponseWriter, r *http.Request, itemType string, requireID bool) (map[string]any, bool) {
	raw, ok := a.readRequestBody(w, r)
	if !ok {
		return nil, false
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_CATALOG_ITEM", "内容草稿必须是 JSON 对象")
		return nil, false
	}
	allowed := map[string]bool{
		"id": true, "name": true, "title": true, "description": true, "previewUrl": true,
		"category": true, "tags": true, "variants": true, "sortIndex": true,
	}
	if itemType == "web" {
		allowed["provider"] = true
		allowed["sourcePageUrl"] = true
	}
	if itemType == "styles" {
		allowed["version"] = true
		allowed["schemaVersion"] = true
		allowed["minExtensionVersion"] = true
		allowed["css"] = true
		allowed["config"] = true
	}
	for key := range fields {
		if !allowed[key] {
			writeAPIError(w, http.StatusBadRequest, "INVALID_CATALOG_ITEM", "内容草稿包含不支持的字段: "+key)
			return nil, false
		}
	}
	if requireID {
		id, _ := fields["id"].(string)
		if strings.TrimSpace(id) == "" {
			writeAPIError(w, http.StatusBadRequest, "INVALID_CATALOG_ID", "资源 ID 不能为空")
			return nil, false
		}
	}
	for _, key := range []string{"id", "name", "title", "description", "previewUrl", "category", "provider", "sourcePageUrl", "version", "minExtensionVersion", "css"} {
		if value, found := fields[key]; found {
			text, isString := value.(string)
			if !isString {
				writeAPIError(w, http.StatusBadRequest, "INVALID_CATALOG_ITEM", key+" 必须是字符串")
				return nil, false
			}
			fields[key] = strings.TrimSpace(text)
		}
	}
	return fields, true
}

func catalogName(fields map[string]any) string {
	if name, ok := fields["name"].(string); ok && strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	if title, ok := fields["title"].(string); ok {
		return strings.TrimSpace(title)
	}
	return ""
}

func catalogItemDTO(itemType, itemID string, revision int, status, visibility string, fields map[string]any, updatedAt string) map[string]any {
	item := map[string]any{
		"id": itemID, "revision": revision, "status": status, "visibility": visibility,
		"updatedAt": updatedAt, "fields": fields,
	}
	name := catalogName(fields)
	if itemType == "styles" {
		item["name"] = name
	} else {
		item["title"] = name
		item["name"] = name
	}
	copyCatalogSummary(item, fields)
	return item
}

func (a *App) adminV1CatalogRevisions(ctx context.Context, itemType, itemID string) ([]map[string]any, error) {
	rows, err := a.store.db.QueryContext(ctx, `SELECT id,revision,status,visibility,created_at,updated_at,published_at
		FROM catalog_revisions WHERE item_type=? AND item_id=? ORDER BY revision DESC`, itemType, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, status, visibility, createdAt, updatedAt, publishedAt string
		var revision int
		if err := rows.Scan(&id, &revision, &status, &visibility, &createdAt, &updatedAt, &publishedAt); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "revision": revision, "status": status, "visibility": visibility,
			"createdAt": createdAt, "updatedAt": updatedAt, "publishedAt": publishedAt,
		})
	}
	return items, rows.Err()
}

func validateCatalogFields(itemType, itemID string, fields map[string]any) map[string]string {
	errorsByField := map[string]string{}
	if catalogName(fields) == "" {
		errorsByField["name"] = "名称不能为空"
	}
	if preview, _ := fields["previewUrl"].(string); preview != "" && !safeHTTPSURL(preview) {
		errorsByField["previewUrl"] = "预览地址必须是绝对 HTTPS URL"
	}
	if itemType == "styles" {
		version, _ := fields["version"].(string)
		if !validSimpleSemver(version) {
			errorsByField["version"] = "风格版本必须是 semver"
		}
		schemaVersion := intField(fields["schemaVersion"], 0)
		if schemaVersion < 1 || schemaVersion > 100 {
			errorsByField["schemaVersion"] = "Style schema 版本无效"
		}
		css, _ := fields["css"].(string)
		if css == "" || len(css) > 64<<10 {
			errorsByField["css"] = "CSS 不能为空且不得超过 64 KiB"
		} else if err := validateCatalogStyleCSS(itemID, css); err != nil {
			errorsByField["css"] = err.Error()
		}
		if config, found := fields["config"]; found {
			if _, ok := config.(map[string]any); !ok {
				errorsByField["config"] = "结构化配置必须是 JSON 对象"
			}
		}
		return errorsByField
	}
	category, _ := fields["category"].(string)
	if strings.TrimSpace(category) == "" {
		errorsByField["category"] = "分类不能为空"
	}
	variants, ok := fields["variants"].([]any)
	if !ok || len(variants) == 0 {
		errorsByField["variants"] = "至少需要一个图片变体"
	} else {
		for _, raw := range variants {
			variant, ok := raw.(map[string]any)
			if !ok {
				errorsByField["variants"] = "图片变体格式无效"
				break
			}
			id, _ := variant["id"].(string)
			label, _ := variant["label"].(string)
			value, _ := variant["url"].(string)
			if strings.TrimSpace(id) == "" || strings.TrimSpace(label) == "" || !safeHTTPSURL(value) {
				errorsByField["variants"] = "每个图片变体都需要 id、label 和绝对 HTTPS URL"
				break
			}
		}
	}
	if itemType == "web" {
		provider, _ := fields["provider"].(string)
		if provider != "uhdpaper" {
			errorsByField["provider"] = "当前仅允许受支持的 UHD Paper provider"
		}
		source, _ := fields["sourcePageUrl"].(string)
		if !safeHTTPSURL(source) {
			errorsByField["sourcePageUrl"] = "来源页面必须是绝对 HTTPS URL"
		}
	}
	return errorsByField
}

// validateCatalogStyleCSS accepts a deliberately small, flat subset of CSS.
// Remote styles cannot use at-rules or resource-producing functions, and each
// selector must remain rooted at the exact style package container.
func validateCatalogStyleCSS(itemID, source string) error {
	if catalogCSSContainsHTMLDelimiter(source) {
		return fmt.Errorf("CSS 包含 HTML 分隔符")
	}
	css, err := normalizeCatalogCSS(source)
	if err != nil {
		return fmt.Errorf("CSS 转义、注释或字符串语法无效")
	}
	if catalogCSSContainsHTMLDelimiter(css) {
		return fmt.Errorf("CSS 包含 HTML 分隔符")
	}
	code, hasAtRule, err := catalogCSSCodeProjection(css)
	if err != nil {
		return fmt.Errorf("CSS 字符串语法无效")
	}
	if hasAtRule {
		return fmt.Errorf("CSS 不允许 at-rule")
	}
	forbidden := []string{
		"url(", "var(", "attr(", "expression(",
		"image(", "image-set(", "-webkit-image-set(", "cross-fade(", "-webkit-cross-fade(",
		"element(", "-moz-element(", "paint(", "src(", "-webkit-canvas(", "-webkit-named-image(",
		"javascript:", "vbscript:", "behavior:", "-moz-binding", "progid:",
	}
	for _, construct := range forbidden {
		if strings.Contains(code, construct) {
			return fmt.Errorf("CSS 包含被禁止的外链、变量或可执行规则")
		}
	}

	prefix := `.newtab-root[data-style-id="` + itemID + `"]`
	if err := validateCatalogCSSRules(css, prefix); err != nil {
		return err
	}
	return nil
}

func catalogCSSContainsHTMLDelimiter(css string) bool {
	lowerCSS := strings.ToLower(css)
	return strings.Contains(lowerCSS, "</style") || strings.Contains(lowerCSS, "<script") || strings.Contains(lowerCSS, "<!--") || strings.Contains(lowerCSS, "-->") || strings.Contains(css, "<")
}

// normalizeCatalogCSS removes comments and resolves every CSS escape according
// to the CSS Syntax hex-escape rules (1-6 hex digits plus optional whitespace).
// Escapes that would turn into parser punctuation outside strings are rejected
// conservatively so the validation parser cannot reinterpret token boundaries.
func normalizeCatalogCSS(source string) (string, error) {
	if !utf8.ValidString(source) {
		return "", fmt.Errorf("invalid UTF-8")
	}
	var normalized strings.Builder
	normalized.Grow(len(source))
	var quote byte
	for i := 0; i < len(source); {
		if quote == 0 && i+1 < len(source) && source[i] == '/' && source[i+1] == '*' {
			end := strings.Index(source[i+2:], "*/")
			if end < 0 {
				return "", fmt.Errorf("unterminated comment")
			}
			i += end + 4
			continue
		}
		if source[i] == '\\' {
			decoded, next, continuation, err := decodeCatalogCSSEscape(source, i, quote != 0)
			if err != nil {
				return "", err
			}
			i = next
			if continuation {
				continue
			}
			if quote == 0 {
				if !catalogCSSIdentifierEscapeRune(decoded) {
					return "", fmt.Errorf("escaped parser punctuation")
				}
				normalized.WriteRune(decoded)
				continue
			}
			if decoded < 0x20 || decoded == 0x7f {
				return "", fmt.Errorf("escaped control in string")
			}
			if decoded == rune(quote) || decoded == '\\' {
				normalized.WriteByte('\\')
			}
			normalized.WriteRune(decoded)
			continue
		}

		if source[i] < utf8.RuneSelf {
			current := source[i]
			if quote != 0 {
				if current == '\n' || current == '\r' || current == '\f' {
					return "", fmt.Errorf("newline in string")
				}
				normalized.WriteByte(current)
				i++
				if current == quote {
					quote = 0
				}
				continue
			}
			if current == '\'' || current == '"' {
				quote = current
			}
			normalized.WriteByte(current)
			i++
			continue
		}

		decoded, size := utf8.DecodeRuneInString(source[i:])
		if decoded == utf8.RuneError && size == 1 {
			return "", fmt.Errorf("invalid UTF-8")
		}
		normalized.WriteRune(decoded)
		i += size
	}
	if quote != 0 {
		return "", fmt.Errorf("unterminated string")
	}
	return normalized.String(), nil
}

func decodeCatalogCSSEscape(source string, slash int, inString bool) (rune, int, bool, error) {
	i := slash + 1
	if i >= len(source) {
		return 0, i, false, fmt.Errorf("trailing escape")
	}
	if source[i] == '\n' || source[i] == '\r' || source[i] == '\f' {
		if !inString {
			return 0, i, false, fmt.Errorf("escaped newline outside string")
		}
		if source[i] == '\r' && i+1 < len(source) && source[i+1] == '\n' {
			i++
		}
		return 0, i + 1, true, nil
	}
	if isCatalogCSSHex(source[i]) {
		value := uint32(0)
		digits := 0
		for i < len(source) && digits < 6 && isCatalogCSSHex(source[i]) {
			value = value*16 + uint32(catalogCSSHexValue(source[i]))
			i++
			digits++
		}
		if i < len(source) && isCatalogCSSWhitespace(source[i]) {
			if source[i] == '\r' && i+1 < len(source) && source[i+1] == '\n' {
				i++
			}
			i++
		}
		if value == 0 || value > utf8.MaxRune || value >= 0xd800 && value <= 0xdfff {
			value = utf8.RuneError
		}
		return rune(value), i, false, nil
	}
	decoded, size := utf8.DecodeRuneInString(source[i:])
	if decoded == utf8.RuneError && size == 1 {
		return 0, i, false, fmt.Errorf("invalid escaped UTF-8")
	}
	return decoded, i + size, false, nil
}

func catalogCSSIdentifierEscapeRune(value rune) bool {
	if value >= utf8.RuneSelf {
		return value != utf8.RuneError
	}
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '-' || value == '_'
}

func isCatalogCSSHex(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
}

func catalogCSSHexValue(value byte) byte {
	switch {
	case value >= '0' && value <= '9':
		return value - '0'
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10
	default:
		return value - 'A' + 10
	}
}

func isCatalogCSSWhitespace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\n' || value == '\r' || value == '\f'
}

// catalogCSSCodeProjection removes strings and whitespace, leaving a lowercase
// token projection suitable for conservative forbidden-construct checks.
func catalogCSSCodeProjection(css string) (string, bool, error) {
	var code strings.Builder
	var quote byte
	hasAtRule := false
	for i := 0; i < len(css); i++ {
		current := css[i]
		if quote != 0 {
			if current == '\\' {
				if i+1 >= len(css) {
					return "", false, fmt.Errorf("trailing string escape")
				}
				i++
				continue
			}
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' {
			quote = current
			continue
		}
		if current == '@' {
			hasAtRule = true
		}
		if !isCatalogCSSWhitespace(current) {
			code.WriteByte(current)
		}
	}
	if quote != 0 {
		return "", false, fmt.Errorf("unterminated string")
	}
	return strings.ToLower(code.String()), hasAtRule, nil
}

func validateCatalogCSSRules(css, prefix string) error {
	rules := 0
	for offset := 0; ; {
		offset = skipCatalogCSSWhitespace(css, offset)
		if offset == len(css) {
			break
		}
		selectorStart := offset
		blockStart, err := findCatalogCSSBlockStart(css, offset)
		if err != nil {
			return fmt.Errorf("CSS 规则语法无效")
		}
		selectors := strings.TrimSpace(css[selectorStart:blockStart])
		if err := validateCatalogCSSSelectorList(selectors, prefix); err != nil {
			return err
		}
		blockEnd, err := findCatalogCSSBlockEnd(css, blockStart+1)
		if err != nil || strings.TrimSpace(css[blockStart+1:blockEnd]) == "" {
			return fmt.Errorf("CSS 声明块语法无效")
		}
		rules++
		offset = blockEnd + 1
	}
	if rules == 0 {
		return fmt.Errorf("CSS 至少需要一条规则")
	}
	return nil
}

func skipCatalogCSSWhitespace(css string, offset int) int {
	for offset < len(css) && isCatalogCSSWhitespace(css[offset]) {
		offset++
	}
	return offset
}

func findCatalogCSSBlockStart(css string, offset int) (int, error) {
	var quote byte
	brackets, parentheses := 0, 0
	for i := offset; i < len(css); i++ {
		current := css[i]
		if quote != 0 {
			if current == '\\' {
				i++
				if i >= len(css) {
					return 0, fmt.Errorf("trailing string escape")
				}
				continue
			}
			if current == quote {
				quote = 0
			}
			continue
		}
		switch current {
		case '\'', '"':
			quote = current
		case '[':
			brackets++
		case ']':
			brackets--
			if brackets < 0 {
				return 0, fmt.Errorf("unmatched bracket")
			}
		case '(':
			parentheses++
		case ')':
			parentheses--
			if parentheses < 0 {
				return 0, fmt.Errorf("unmatched parenthesis")
			}
		case '{':
			if brackets == 0 && parentheses == 0 {
				if quote != 0 {
					return 0, fmt.Errorf("unterminated string")
				}
				return i, nil
			}
		case '}', ';':
			if brackets == 0 && parentheses == 0 {
				return 0, fmt.Errorf("unexpected top-level delimiter")
			}
		}
	}
	return 0, fmt.Errorf("missing declaration block")
}

func findCatalogCSSBlockEnd(css string, offset int) (int, error) {
	var quote byte
	brackets, parentheses := 0, 0
	for i := offset; i < len(css); i++ {
		current := css[i]
		if quote != 0 {
			if current == '\\' {
				i++
				if i >= len(css) {
					return 0, fmt.Errorf("trailing string escape")
				}
				continue
			}
			if current == quote {
				quote = 0
			}
			continue
		}
		switch current {
		case '\'', '"':
			quote = current
		case '[':
			brackets++
		case ']':
			brackets--
			if brackets < 0 {
				return 0, fmt.Errorf("unmatched bracket")
			}
		case '(':
			parentheses++
		case ')':
			parentheses--
			if parentheses < 0 {
				return 0, fmt.Errorf("unmatched parenthesis")
			}
		case '{':
			return 0, fmt.Errorf("nested rule")
		case '}':
			if brackets == 0 && parentheses == 0 {
				return i, nil
			}
		}
	}
	return 0, fmt.Errorf("missing closing brace")
}

func validateCatalogCSSSelectorList(selectors, prefix string) error {
	if selectors == "" {
		return fmt.Errorf("CSS 选择器不能为空")
	}
	start := 0
	var quote byte
	brackets, parentheses := 0, 0
	validateOne := func(raw string) error {
		selector := strings.TrimSpace(raw)
		if !strings.HasPrefix(selector, prefix) {
			return fmt.Errorf("所有选择器必须位于当前 style-id 作用域")
		}
		tail := selector[len(prefix):]
		if tail != "" && !isCatalogCSSScopedSelectorBoundary(tail[0]) {
			return fmt.Errorf("所有选择器必须位于当前 style-id 作用域")
		}
		if catalogCSSSelectorEscapesScope(tail) {
			return fmt.Errorf("CSS 选择器不得离开当前 style-id 作用域")
		}
		return nil
	}
	for i := 0; i < len(selectors); i++ {
		current := selectors[i]
		if quote != 0 {
			if current == '\\' {
				i++
				if i >= len(selectors) {
					return fmt.Errorf("CSS 选择器字符串无效")
				}
				continue
			}
			if current == quote {
				quote = 0
			}
			continue
		}
		switch current {
		case '\'', '"':
			quote = current
		case '[':
			brackets++
		case ']':
			brackets--
		case '(':
			parentheses++
		case ')':
			parentheses--
		case ',':
			if brackets == 0 && parentheses == 0 {
				if err := validateOne(selectors[start:i]); err != nil {
					return err
				}
				start = i + 1
			}
		}
	}
	if quote != 0 || brackets != 0 || parentheses != 0 {
		return fmt.Errorf("CSS 选择器语法无效")
	}
	return validateOne(selectors[start:])
}

func isCatalogCSSScopedSelectorBoundary(value byte) bool {
	return isCatalogCSSWhitespace(value) || value == '.' || value == '#' || value == '[' || value == ':' || value == '>'
}

func catalogCSSSelectorEscapesScope(selectorTail string) bool {
	var quote byte
	brackets, parentheses := 0, 0
	for i := 0; i < len(selectorTail); i++ {
		current := selectorTail[i]
		if quote != 0 {
			if current == '\\' {
				i++
				continue
			}
			if current == quote {
				quote = 0
			}
			continue
		}
		switch current {
		case '\'', '"':
			quote = current
		case '[':
			brackets++
		case ']':
			brackets--
		case '(':
			parentheses++
		case ')':
			parentheses--
		case '+', '~', '|', '/':
			if brackets == 0 && parentheses == 0 {
				return true
			}
		}
	}
	return false
}

func safeHTTPSURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}

func validSimpleSemver(value string) bool {
	value = strings.TrimSpace(value)
	parts := strings.SplitN(value, "-", 2)
	core := strings.Split(parts[0], ".")
	if len(core) != 3 {
		return false
	}
	for _, part := range core {
		if part == "" {
			return false
		}
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return true
}

func intField(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		if parsed, err := strconv.Atoi(typed.String()); err == nil {
			return parsed
		}
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil {
			return parsed
		}
	}
	return fallback
}

func stringSliceField(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return []string{}
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			result = append(result, strings.TrimSpace(text))
		}
	}
	return result
}

func upsertPublishedCatalogTx(ctx context.Context, tx *sql.Tx, itemType, itemID string, fields map[string]any, enabled bool) error {
	now := nowString()
	name := catalogName(fields)
	preview, _ := fields["previewUrl"].(string)
	sortIndex := intField(fields["sortIndex"], 0)
	switch itemType {
	case "official":
		category, _ := fields["category"].(string)
		tags, _ := json.Marshal(stringSliceField(fields["tags"]))
		variants, _ := json.Marshal(fields["variants"])
		_, err := tx.ExecContext(ctx, `INSERT INTO official_wallpapers(id,title,category,tags_json,preview_url,variants_json,enabled,sort_index,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET title=excluded.title,category=excluded.category,tags_json=excluded.tags_json,preview_url=excluded.preview_url,variants_json=excluded.variants_json,enabled=excluded.enabled,sort_index=excluded.sort_index,updated_at=excluded.updated_at`,
			itemID, name, category, string(tags), preview, string(variants), boolInt(enabled), sortIndex, now, now)
		return err
	case "web":
		provider, _ := fields["provider"].(string)
		source, _ := fields["sourcePageUrl"].(string)
		category, _ := fields["category"].(string)
		tags, _ := json.Marshal(stringSliceField(fields["tags"]))
		variants, _ := json.Marshal(fields["variants"])
		_, err := tx.ExecContext(ctx, `INSERT INTO web_wallpaper_cache(id,provider,source_page_url,title,category,tags_json,preview_url,variants_json,enabled,cached_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET provider=excluded.provider,source_page_url=excluded.source_page_url,title=excluded.title,category=excluded.category,tags_json=excluded.tags_json,preview_url=excluded.preview_url,variants_json=excluded.variants_json,enabled=excluded.enabled,cached_at=excluded.cached_at,updated_at=excluded.updated_at`,
			itemID, provider, source, name, category, string(tags), preview, string(variants), boolInt(enabled), now, now)
		return err
	case "styles":
		version, _ := fields["version"].(string)
		description, _ := fields["description"].(string)
		css, _ := fields["css"].(string)
		config, _ := json.Marshal(fields["config"])
		if len(config) == 0 || string(config) == "null" {
			config = []byte("{}")
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO style_packages(id,name,version,description,preview_url,css,config_json,enabled,sort_index,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,version=excluded.version,description=excluded.description,preview_url=excluded.preview_url,css=excluded.css,config_json=excluded.config_json,enabled=excluded.enabled,sort_index=excluded.sort_index,updated_at=excluded.updated_at`,
			itemID, name, version, description, preview, css, string(config), boolInt(enabled), sortIndex, now, now)
		return err
	default:
		return fmt.Errorf("unsupported catalog type %q", itemType)
	}
}

func setPublishedCatalogEnabledTx(ctx context.Context, tx *sql.Tx, itemType, itemID string, enabled bool) error {
	table := map[string]string{"official": "official_wallpapers", "web": "web_wallpaper_cache", "styles": "style_packages"}[itemType]
	if table == "" {
		return fmt.Errorf("unsupported catalog type %q", itemType)
	}
	_, err := tx.ExecContext(ctx, `UPDATE `+table+` SET enabled=?,updated_at=? WHERE id=?`, boolInt(enabled), nowString(), itemID)
	return err
}

func deletePublishedCatalogTx(ctx context.Context, tx *sql.Tx, itemType, itemID string) error {
	table := map[string]string{"official": "official_wallpapers", "web": "web_wallpaper_cache", "styles": "style_packages"}[itemType]
	if table == "" {
		return fmt.Errorf("unsupported catalog type %q", itemType)
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE id=?`, itemID)
	return err
}

func copyCatalogSummary(target, fields map[string]any) {
	for _, key := range []string{"name", "title", "description", "previewUrl"} {
		if value, found := fields[key]; found {
			target[key] = value
		}
	}
}

func visibilityFor(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func profileCollectionCounts(raw []byte) (int, int) {
	var value struct {
		Groups    []json.RawMessage `json:"groups"`
		Shortcuts []json.RawMessage `json:"shortcuts"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return 0, 0
	}
	return len(value.Groups), len(value.Shortcuts)
}

func validAdminV1Identifier(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("._:-", char) {
			continue
		}
		return false
	}
	return true
}

func errorsIsNoRows(err error) bool { return err == sql.ErrNoRows }

func insertAdminAuditTx(ctx context.Context, tx *sql.Tx, adminID, action, targetType, targetID, requestID, ip string, details any) error {
	detailsJSON, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO admin_audit_logs (id,created_at,admin_id,action,target_type,target_id,request_id,ip,details_json) VALUES (?,?,?,?,?,?,?,?,?)`,
		newID("audit_"), nowString(), adminID, action, targetType, targetID, requestID, ip, string(detailsJSON))
	return err
}

func writeAdminV1Data(w http.ResponseWriter, status int, data any, requestID string) {
	writeJSON(w, status, apiEnvelope{Data: data, RequestID: requestID})
}

func adminV1DatabasePath(ctx context.Context, db *sql.DB) (string, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA database_list`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var sequence int
		var name, path string
		if err := rows.Scan(&sequence, &name, &path); err != nil {
			return "", err
		}
		if name == "main" {
			return path, nil
		}
	}
	return "", fmt.Errorf("main database path is unavailable")
}

func parseAdminV1Limit(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		if parsed, err := strconv.Atoi(typed.String()); err == nil {
			return parsed
		}
	case string:
		if parsed, err := strconv.Atoi(typed); err == nil {
			return parsed
		}
	}
	return fallback
}

func adminV1SettingsDTO(settings RuntimeSettings) map[string]any {
	result := map[string]any{"registrationEnabled": settings.RegistrationOpen, "publicBaseUrl": settings.PublicBaseURL, "webOrigins": settings.AllowedOrigins}
	result["maxUsers"] = parseAdminV1Limit(settings.Limits["maxUsers"], 100)
	result["profileKiB"] = parseAdminV1Limit(settings.Limits["profileBytes"], 512<<10) / 1024
	result["storageGiB"] = parseAdminV1Limit(settings.Limits["storageBytes"], 1<<30) / (1 << 30)
	result["versionsPerUser"] = parseAdminV1Limit(settings.Limits["versionsPerUser"], 50)
	result["accessLogDays"] = parseAdminV1Limit(settings.Limits["accessLogDays"], 30)
	result["auditLogDays"] = parseAdminV1Limit(settings.Limits["auditLogDays"], 180)
	return result
}

func parseAdminV1Origins(raw json.RawMessage) ([]string, error) {
	var lines string
	if json.Unmarshal(raw, &lines) == nil {
		return normalizeAdminV1Origins(strings.FieldsFunc(lines, func(char rune) bool { return char == '\n' || char == '\r' || char == ',' })), nil
	}
	var values []string
	if json.Unmarshal(raw, &values) != nil {
		return nil, fmt.Errorf("origins must be a string or string array")
	}
	return normalizeAdminV1Origins(values), nil
}

func normalizeAdminV1Origins(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func parseAdminV1Integer(raw json.RawMessage) (int, error) {
	var number int
	if json.Unmarshal(raw, &number) == nil {
		return number, nil
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return 0, fmt.Errorf("integer is required")
	}
	return strconv.Atoi(strings.TrimSpace(text))
}

func sortedAdminV1Keys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func writeAdminV1CSV(w http.ResponseWriter, filename string, records [][]string) {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	for _, record := range records {
		cleaned := append([]string(nil), record...)
		for index, value := range cleaned {
			if value != "" && strings.ContainsRune("=+-@", rune(value[0])) {
				cleaned[index] = "'" + value
			}
		}
		_ = writer.Write(cleaned)
	}
	writer.Flush()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buffer.Bytes())
}

func (a *App) adminV1BackupDirectory(ctx context.Context, livePath string) (string, error) {
	return a.store.backupDirectory(ctx, livePath)
}

func adminV1FileSHA256(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func adminV1WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	return writeFileAtomicDurable(path, ".admin-v1-write-*", mode, func(writer io.Writer) error {
		_, err := writer.Write(data)
		return err
	})
}

func adminV1CopyFileAtomic(sourcePath, destinationPath string, mode os.FileMode) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	return writeFileAtomicDurable(destinationPath, ".admin-v1-restore-*", mode, func(writer io.Writer) error {
		_, err := io.Copy(writer, source)
		return err
	})
}

func adminV1PathWithin(basePath, candidatePath string) bool {
	basePath, baseErr := filepath.Abs(filepath.Clean(basePath))
	candidatePath, candidateErr := filepath.Abs(filepath.Clean(candidatePath))
	if baseErr != nil || candidateErr != nil {
		return false
	}
	relative, err := filepath.Rel(basePath, candidatePath)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func adminV1CheckSQLiteSnapshot(path string) error {
	if err := adminV1CheckSQLiteQuickCheck(path); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=query_only(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer db.Close()
	foreignKeys, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer foreignKeys.Close()
	if foreignKeys.Next() {
		return fmt.Errorf("sqlite foreign_key_check failed")
	}
	var count, minimum, maximum int
	if err := db.QueryRow(`SELECT COUNT(*),COALESCE(MIN(version),0),COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&count, &minimum, &maximum); err != nil {
		return err
	}
	if count != schemaVersion || minimum != 1 || maximum != schemaVersion {
		return fmt.Errorf("snapshot schema version is incomplete or unsupported")
	}
	return nil
}

func adminV1CheckSQLiteSnapshotCompatible(path string, declaredSchemaVersion int) error {
	if declaredSchemaVersion < 1 || declaredSchemaVersion > schemaVersion {
		return fmt.Errorf("snapshot schema version is unsupported")
	}
	if err := adminV1CheckSQLiteQuickCheck(path); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=query_only(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer db.Close()
	foreignKeys, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	if foreignKeys.Next() {
		_ = foreignKeys.Close()
		return fmt.Errorf("sqlite foreign_key_check failed")
	}
	if err := foreignKeys.Close(); err != nil {
		return err
	}
	rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return err
		}
		count++
		if version != count || version > declaredSchemaVersion {
			return fmt.Errorf("snapshot schema migration history is not contiguous")
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count != declaredSchemaVersion {
		return fmt.Errorf("snapshot schema manifest does not match migration history")
	}
	return nil
}

func adminV1MigrateRestoreSnapshot(path string) error {
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	needsMigration, err := inspectSchemaMigrationState(context.Background(), db)
	if err == nil && needsMigration {
		err = store.migrate(context.Background())
	}
	if err != nil {
		return errors.Join(err, db.Close())
	}
	return adminV1FinalizeStagedSQLite(context.Background(), db, path)
}

func adminV1CheckSQLiteQuickCheck(path string) error {
	db, err := sql.Open("sqlite", path+"?_pragma=query_only(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer db.Close()
	var result string
	if err := db.QueryRow(`PRAGMA quick_check`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("sqlite quick_check: %s", result)
	}
	return nil
}

func applyAdminV1Restore(app *App, livePath, stagedPath, liveSecretsPath, stagedSecretsPath string) error {
	if app == nil || app.store == nil || livePath == "" || stagedPath == "" {
		return fmt.Errorf("restore paths are required")
	}
	intent, err := readAdminV1RestoreIntent(livePath, liveSecretsPath)
	if err != nil {
		return fmt.Errorf("read restore intent: %w", err)
	}
	if filepath.Clean(intent.StagedDatabase) != filepath.Clean(stagedPath) || filepath.Clean(intent.StagedSecrets) != filepath.Clean(stagedSecretsPath) {
		return fmt.Errorf("scheduled restore paths do not match durable intent")
	}
	fail := func(cause error) error {
		if errors.Is(cause, errAdminV1RestoreCrashInjected) {
			return cause
		}
		if rollbackErr := rollbackAdminV1RestoreIntent(intent); rollbackErr != nil {
			return errors.Join(cause, fmt.Errorf("restore rollback failed: %w", rollbackErr))
		}
		return cause
	}
	if err := adminV1CheckpointSQLite(context.Background(), app.store.db); err != nil {
		return fail(fmt.Errorf("checkpoint live SQLite before restore: %w", err))
	}
	if err := app.store.db.Close(); err != nil {
		return fail(err)
	}
	for _, sidecar := range []string{livePath + "-wal", livePath + "-shm"} {
		if err := removeAdminV1File(sidecar); err != nil {
			return fail(err)
		}
	}
	if err := renameAdminV1File(intent.LiveDatabase, intent.RollbackDatabase); err != nil {
		return fail(err)
	}
	if err := recordAdminV1RestorePhase(intent, restorePhaseDatabaseBackedUp); err != nil {
		return fail(err)
	}
	if err := maybeCrashAdminV1Restore(restorePhaseDatabaseBackedUp); err != nil {
		return err
	}
	if intent.LiveSecretsExisted {
		if err := renameAdminV1File(intent.LiveSecrets, intent.RollbackSecrets); err != nil {
			return fail(err)
		}
	}
	if intent.LiveSecrets != "" {
		if err := recordAdminV1RestorePhase(intent, restorePhaseSecretsBackedUp); err != nil {
			return fail(err)
		}
		if err := maybeCrashAdminV1Restore(restorePhaseSecretsBackedUp); err != nil {
			return err
		}
	}
	if err := renameAdminV1File(intent.StagedDatabase, intent.LiveDatabase); err != nil {
		return fail(err)
	}
	if err := recordAdminV1RestorePhase(intent, restorePhaseDatabaseInstalled); err != nil {
		return fail(err)
	}
	if err := maybeCrashAdminV1Restore(restorePhaseDatabaseInstalled); err != nil {
		return err
	}
	if intent.StagedSecrets != "" {
		if err := renameAdminV1File(intent.StagedSecrets, intent.LiveSecrets); err != nil {
			return fail(err)
		}
		if err := recordAdminV1RestorePhase(intent, restorePhaseSecretsInstalled); err != nil {
			return fail(err)
		}
		if err := maybeCrashAdminV1Restore(restorePhaseSecretsInstalled); err != nil {
			return err
		}
	}
	if err := os.Chmod(intent.LiveDatabase, 0o600); err != nil {
		return fail(err)
	}
	if intent.LiveSecrets != "" {
		if err := os.Chmod(intent.LiveSecrets, 0o600); err != nil {
			return fail(err)
		}
	}
	if err := validateAdminV1InstalledRestore(intent); err != nil {
		return fail(err)
	}
	if err := recordAdminV1RestorePhase(intent, restorePhaseCompleted); err != nil {
		return fail(err)
	}
	if err := maybeCrashAdminV1Restore(restorePhaseCompleted); err != nil {
		return err
	}
	return cleanupAdminV1CompletedRestore(intent)
}
