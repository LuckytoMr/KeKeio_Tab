package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackupSecretsEncryptionRequiresCorrectPassphrase(t *testing.T) {
	plaintext := []byte(`{"tokenDerivationKey":"secret-material","smtpPassword":"smtp-secret"}`)
	encrypted, err := adminV1EncryptSecrets(plaintext, "correct recovery passphrase")
	if err != nil {
		t.Fatalf("encrypt secrets: %v", err)
	}
	if bytes.Contains(encrypted, []byte("smtp-secret")) || bytes.Contains(encrypted, []byte("secret-material")) {
		t.Fatal("encrypted secrets contained plaintext material")
	}
	if _, err := adminV1DecryptSecrets(encrypted, "wrong recovery passphrase"); err == nil {
		t.Fatal("wrong recovery passphrase decrypted secrets")
	}
	decrypted, err := adminV1DecryptSecrets(encrypted, "correct recovery passphrase")
	if err != nil || !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypt secrets mismatch err=%v got=%q", err, decrypted)
	}
}

func TestFullBackupContainsEncryptedSecretsAndRequiresPassphrase(t *testing.T) {
	app, store, handler, adminCookie := newAdminV1TestHandler(t)
	csrf := adminV1CSRFToken(t, app, adminCookie)
	secretsPath := filepath.Join(t.TempDir(), "secrets.json")
	secrets, _, err := LoadOrCreateSecrets(secretsPath)
	if err != nil {
		t.Fatalf("create secrets: %v", err)
	}
	secrets.SMTPPassword = "backup-only-smtp-secret"
	if err := SaveSecrets(secretsPath, secrets); err != nil {
		t.Fatalf("save secrets: %v", err)
	}
	app.config.SecretsPath = secretsPath

	missing := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/system/backups", `{"kind":"full"}`, adminCookie, csrf)
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("full backup without passphrase = %d %s, want 400", missing.Code, missing.Body.String())
	}
	created := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/system/backups", `{"kind":"full","passphrase":"correct recovery passphrase"}`, adminCookie, csrf)
	if created.Code != http.StatusCreated {
		t.Fatalf("full backup = %d %s", created.Code, created.Body.String())
	}
	var response struct {
		Data struct {
			Item struct {
				ID string `json:"id"`
			} `json:"item"`
		} `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil || response.Data.Item.ID == "" {
		t.Fatalf("decode full backup response: %v %s", err, created.Body.String())
	}
	var manifestPath string
	if err := store.db.QueryRowContext(t.Context(), `SELECT manifest_path FROM backup_records WHERE id=?`, response.Data.Item.ID).Scan(&manifestPath); err != nil {
		t.Fatalf("read manifest path: %v", err)
	}
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest adminV1BackupManifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil || manifest.Format != "fullpro-backup" || manifest.FormatVersion != 1 || manifest.SecretsFile == "" || manifest.SecretsSHA256 == "" || manifest.Cipher != "AES-256-GCM" {
		t.Fatalf("full manifest missing encrypted secrets metadata: err=%v manifest=%+v", err, manifest)
	}
	encrypted, err := os.ReadFile(filepath.Join(filepath.Dir(manifestPath), manifest.SecretsFile))
	if err != nil {
		t.Fatalf("read encrypted secrets: %v", err)
	}
	if strings.Contains(string(encrypted), secrets.SMTPPassword) {
		t.Fatal("full backup stored SMTP password in plaintext")
	}
	decrypted, err := adminV1DecryptSecrets(encrypted, "correct recovery passphrase")
	if err != nil || !bytes.Contains(decrypted, []byte("backup-only-smtp-secret")) {
		t.Fatalf("decrypt backed-up secrets err=%v data=%q", err, decrypted)
	}
	wrongRestore := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/system/backups/"+response.Data.Item.ID+"/restore", `{"passphrase":"wrong recovery passphrase"}`, adminCookie, csrf)
	if wrongRestore.Code != http.StatusConflict {
		t.Fatalf("full restore with wrong passphrase = %d %s, want 409", wrongRestore.Code, wrongRestore.Body.String())
	}
	called := false
	previousScheduler := adminV1ScheduleRestore
	adminV1ScheduleRestore = func(_ *App, liveDatabase, stagedDatabase, liveSecrets, stagedSecrets string) {
		stagedRaw, readErr := os.ReadFile(stagedSecrets)
		called = liveDatabase != "" && stagedDatabase != "" && liveSecrets == secretsPath && readErr == nil && bytes.Contains(stagedRaw, []byte("backup-only-smtp-secret"))
	}
	t.Cleanup(func() { adminV1ScheduleRestore = previousScheduler })
	validRestore := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/system/backups/"+response.Data.Item.ID+"/restore", `{"passphrase":"correct recovery passphrase"}`, adminCookie, csrf)
	if validRestore.Code != http.StatusAccepted || !called {
		t.Fatalf("full restore = %d called=%t body=%s", validRestore.Code, called, validRestore.Body.String())
	}
}

func TestFullBackupSerializesSnapshotAndSecretsWithSettingsWrites(t *testing.T) {
	app, _, handler, adminCookie := newAdminV1TestHandler(t)
	csrf := adminV1CSRFToken(t, app, adminCookie)
	secretsPath := filepath.Join(t.TempDir(), "secrets.json")
	if _, _, err := LoadOrCreateSecrets(secretsPath); err != nil {
		t.Fatalf("create secrets: %v", err)
	}
	app.config.SecretsPath = secretsPath

	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/system/backups", strings.NewReader(`{"kind":"full","passphrase":"correct recovery passphrase"}`))
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://localhost:5173")
	request.Header.Set("X-CSRF-Token", csrf)
	request.AddCookie(adminCookie)
	response := httptest.NewRecorder()

	app.settingsMu.Lock()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, request)
		close(done)
	}()
	select {
	case <-done:
		app.settingsMu.Unlock()
		t.Fatalf("full backup completed while settings write lock was held: %d %s", response.Code, response.Body.String())
	case <-time.After(150 * time.Millisecond):
	}
	app.settingsMu.Unlock()
	select {
	case <-done:
		if response.Code != http.StatusCreated {
			t.Fatalf("full backup after settings lock release = %d %s", response.Code, response.Body.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("full backup did not resume after settings write lock release")
	}
}

func TestRestoreRejectsManifestTrailingJSON(t *testing.T) {
	app, store, handler, adminCookie := newAdminV1TestHandler(t)
	csrf := adminV1CSRFToken(t, app, adminCookie)
	created := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/system/backups", `{"kind":"data-only"}`, adminCookie, csrf)
	if created.Code != http.StatusCreated {
		t.Fatalf("create data-only backup = %d %s", created.Code, created.Body.String())
	}
	var response struct {
		Data struct {
			Item struct {
				ID string `json:"id"`
			} `json:"item"`
		} `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil || response.Data.Item.ID == "" {
		t.Fatalf("decode backup response: %v %s", err, created.Body.String())
	}
	var manifestPath string
	if err := store.db.QueryRow(`SELECT manifest_path FROM backup_records WHERE id=?`, response.Data.Item.ID).Scan(&manifestPath); err != nil {
		t.Fatalf("read manifest path: %v", err)
	}
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, append(manifestRaw, []byte("{}\n")...), 0o600); err != nil {
		t.Fatalf("append manifest trailing JSON: %v", err)
	}
	previousScheduler := adminV1ScheduleRestore
	called := false
	adminV1ScheduleRestore = func(_ *App, _, _, _, _ string) { called = true }
	t.Cleanup(func() { adminV1ScheduleRestore = previousScheduler })
	restored := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/system/backups/"+response.Data.Item.ID+"/restore", `{}`, adminCookie, csrf)
	if restored.Code != http.StatusConflict || called || !strings.Contains(restored.Body.String(), "BACKUP_MANIFEST_INVALID") {
		t.Fatalf("restore with trailing manifest = %d called=%t body=%s", restored.Code, called, restored.Body.String())
	}
}

func TestDataOnlyRestoreStagingRevokesAllCredentialTables(t *testing.T) {
	app, store := newTestApp(t)
	adminCookie := fixedAdminCookie(t, app.Routes())
	_ = adminCookie
	user, err := store.CreateUser(t.Context(), "restore-token-owner@example.test", "safe-password-123")
	if err != nil {
		t.Fatalf("create restore token owner: %v", err)
	}
	for _, statement := range []string{
		`INSERT INTO email_verification_tokens(token_hash,user_id,created_at,expires_at,consumed_at) VALUES('verify-restored',?,'2026-07-12T00:00:00Z','2099-07-12T00:00:00Z','')`,
		`INSERT INTO password_reset_tokens(token_hash,user_id,created_at,expires_at,consumed_at) VALUES('reset-restored',?,'2026-07-12T00:00:00Z','2099-07-12T00:00:00Z','')`,
	} {
		if _, err := store.db.ExecContext(t.Context(), statement, user.ID); err != nil {
			t.Fatalf("seed restored one-time token: %v", err)
		}
	}
	livePath, err := adminV1DatabasePath(t.Context(), store.db)
	if err != nil {
		t.Fatalf("database path: %v", err)
	}
	stagedPath := filepath.Join(t.TempDir(), "staged.sqlite")
	if _, err := store.db.ExecContext(t.Context(), `VACUUM INTO ?`, stagedPath); err != nil {
		t.Fatalf("snapshot staged database: %v", err)
	}
	if err := adminV1PrepareDataOnlyRestore(stagedPath); err != nil {
		t.Fatalf("prepare data-only restore: %v", err)
	}
	for _, sidecar := range []string{stagedPath + "-wal", stagedPath + "-shm"} {
		if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
			t.Fatalf("prepared restore retained SQLite sidecar %s: %v", sidecar, err)
		}
	}
	standalonePath := filepath.Join(t.TempDir(), "standalone-main.sqlite")
	if err := adminV1CopyFileAtomic(stagedPath, standalonePath, 0o600); err != nil {
		t.Fatalf("copy only staged main database: %v", err)
	}
	staged, err := sql.Open("sqlite", standalonePath)
	if err != nil {
		t.Fatalf("open staged database: %v", err)
	}
	defer staged.Close()
	for _, table := range []string{"admin_sessions", "admin_login_sessions", "sessions", "access_tokens", "refresh_tokens", "refresh_token_families", "install_sessions", "email_verification_tokens", "password_reset_tokens"} {
		var rows int
		if err := staged.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&rows); err != nil || rows != 0 {
			t.Fatalf("staged credential table %s rows=%d err=%v", table, rows, err)
		}
	}
	if _, err := os.Stat(livePath); err != nil {
		t.Fatalf("live database changed while preparing staging: %v", err)
	}
}

func TestPrepareDataOnlyRestoreRejectsBusyWALCheckpoint(t *testing.T) {
	app, store := newTestApp(t)
	_ = app.Routes()
	stagedPath := filepath.Join(t.TempDir(), "busy-staged.sqlite")
	if err := createSQLiteOnlineBackup(t.Context(), store.db, stagedPath); err != nil {
		t.Fatalf("create staged database: %v", err)
	}
	control, err := sql.Open("sqlite", stagedPath)
	if err != nil {
		t.Fatalf("open staged control connection: %v", err)
	}
	var mode string
	if err := control.QueryRow(`PRAGMA journal_mode=WAL`).Scan(&mode); err != nil || !strings.EqualFold(mode, "wal") {
		_ = control.Close()
		t.Fatalf("enable staged WAL mode=%q err=%v", mode, err)
	}
	if err := control.Close(); err != nil {
		t.Fatalf("close staged control connection: %v", err)
	}
	reader, err := sql.Open("sqlite", stagedPath)
	if err != nil {
		t.Fatalf("open staged reader: %v", err)
	}
	readerTx, err := reader.Begin()
	if err != nil {
		_ = reader.Close()
		t.Fatalf("begin staged read transaction: %v", err)
	}
	var rows int
	if err := readerTx.QueryRow(`SELECT COUNT(*) FROM settings`).Scan(&rows); err != nil {
		_ = readerTx.Rollback()
		_ = reader.Close()
		t.Fatalf("establish staged reader snapshot: %v", err)
	}

	prepareErr := adminV1PrepareDataOnlyRestore(stagedPath)
	if prepareErr == nil || !strings.Contains(strings.ToLower(prepareErr.Error()), "checkpoint") {
		_ = readerTx.Rollback()
		_ = reader.Close()
		t.Fatalf("busy staged WAL checkpoint was accepted: %v", prepareErr)
	}
	if err := readerTx.Rollback(); err != nil {
		t.Fatalf("release staged reader: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close staged reader: %v", err)
	}
}

func TestRestoreSnapshotMigratesSupportedOlderSchemaBeforeSwap(t *testing.T) {
	app, store := newTestApp(t)
	_ = app.Routes()
	livePath, err := adminV1DatabasePath(t.Context(), store.db)
	if err != nil {
		t.Fatalf("locate live database: %v", err)
	}
	olderPath := filepath.Join(t.TempDir(), "schema-v4.sqlite")
	if err := createSQLiteOnlineBackup(t.Context(), store.db, olderPath); err != nil {
		t.Fatalf("create older restore fixture: %v", err)
	}
	older, err := sql.Open("sqlite", olderPath)
	if err != nil {
		t.Fatalf("open older restore fixture: %v", err)
	}
	if _, err := older.Exec(`DELETE FROM schema_migrations WHERE version>=?`, schemaVersion); err != nil {
		_ = older.Close()
		t.Fatalf("downgrade restore migration marker: %v", err)
	}
	if err := older.Close(); err != nil {
		t.Fatalf("close older restore fixture: %v", err)
	}
	if err := adminV1CheckSQLiteSnapshotCompatible(olderPath, schemaVersion-1); err != nil {
		t.Fatalf("supported older snapshot rejected: %v", err)
	}
	if err := adminV1MigrateRestoreSnapshot(olderPath); err != nil {
		t.Fatalf("migrate older restore snapshot: %v", err)
	}
	for _, sidecar := range []string{olderPath + "-wal", olderPath + "-shm"} {
		if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
			t.Fatalf("migrated restore retained SQLite sidecar %s: %v", sidecar, err)
		}
	}
	if err := adminV1CheckSQLiteSnapshot(olderPath); err != nil {
		t.Fatalf("migrated restore snapshot is not current: %v", err)
	}
	if _, err := os.Stat(livePath); err != nil {
		t.Fatalf("offline restore migration changed live database: %v", err)
	}
}

func TestRestoreSnapshotRejectsFutureSchema(t *testing.T) {
	app, store := newTestApp(t)
	_ = app.Routes()
	futurePath := filepath.Join(t.TempDir(), "schema-future.sqlite")
	if err := createSQLiteOnlineBackup(t.Context(), store.db, futurePath); err != nil {
		t.Fatalf("create future restore fixture: %v", err)
	}
	future, err := sql.Open("sqlite", futurePath)
	if err != nil {
		t.Fatalf("open future restore fixture: %v", err)
	}
	if _, err := future.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, schemaVersion+1, nowString()); err != nil {
		_ = future.Close()
		t.Fatalf("seed future restore marker: %v", err)
	}
	if err := future.Close(); err != nil {
		t.Fatalf("close future restore fixture: %v", err)
	}
	if err := adminV1CheckSQLiteSnapshotCompatible(futurePath, schemaVersion+1); err == nil {
		t.Fatal("future restore snapshot was accepted")
	}
}
