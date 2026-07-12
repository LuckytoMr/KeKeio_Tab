package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func legacyUnverifiedAccessToken(t *testing.T, handler http.Handler, store *Store) (string, string) {
	t.Helper()
	user, err := store.CreateUser(t.Context(), "legacy-reader@example.com", "safe-password-123")
	if err != nil {
		t.Fatalf("create legacy user: %v", err)
	}
	if _, err := store.db.ExecContext(t.Context(),
		`UPDATE users SET status='legacy_unverified',email_verified_at='',updated_at=? WHERE id=?`, nowString(), user.ID,
	); err != nil {
		t.Fatalf("mark user legacy_unverified: %v", err)
	}
	if _, err := store.db.ExecContext(t.Context(),
		`INSERT INTO sync_profiles(user_id,profile_json,version,schema_version,profile_hash,updated_at) VALUES(?,?,?,?,?,?)`,
		user.ID, sharedProfileFixture, 1, 2, "legacy-profile-hash", nowString(),
	); err != nil {
		t.Fatalf("seed legacy sync profile: %v", err)
	}
	versionID := "legacy-version-1"
	if _, err := store.db.ExecContext(t.Context(),
		`INSERT INTO sync_profile_versions(id,user_id,version,schema_version,profile_json,profile_hash,mutation_id,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		versionID, user.ID, 1, 2, sharedProfileFixture, "legacy-profile-hash", "legacy-migration", nowString(),
	); err != nil {
		t.Fatalf("seed legacy sync version: %v", err)
	}
	login := postV1(t, handler, "/api/v1/auth/login", `{"email":"legacy-reader@example.com","password":"safe-password-123","deviceId":"legacy-device"}`)
	if login.Code != http.StatusOK {
		t.Fatalf("legacy login = %d %s", login.Code, login.Body.String())
	}
	data := decodeV1Data(t, login)
	access, _ := data["accessToken"].(string)
	if access == "" {
		t.Fatalf("legacy login did not issue read token: %#v", data)
	}
	if data["scope"] != "migration_read" {
		t.Fatalf("legacy access scope = %#v, want migration_read", data["scope"])
	}
	if _, issued := data["refreshToken"]; issued {
		t.Fatalf("legacy login issued refresh token: %#v", data)
	}
	return access, versionID
}

func TestLegacyUnverifiedAccessIsLimitedToOwnMigrationReads(t *testing.T) {
	handler, store, _ := newV1AuthApp(t)
	access, _ := legacyUnverifiedAccessToken(t, handler, store)

	for path, bodyFragment := range map[string]string{
		"/api/v1/me":                    "legacy-reader@example.com",
		"/api/v1/sync/profile":          `"version":1`,
		"/api/v1/sync/profile/versions": "legacy-version-1",
	} {
		response := getV1Bearer(t, handler, path, access)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), bodyFragment) {
			t.Fatalf("legacy migration read %s = %d %s", path, response.Code, response.Body.String())
		}
	}

	catalog := getV1Bearer(t, handler, "/api/v1/catalog/styles", access)
	if catalog.Code != http.StatusForbidden || !strings.Contains(catalog.Body.String(), "EMAIL_VERIFICATION_REQUIRED") {
		t.Fatalf("legacy token reached non-migration API = %d %s", catalog.Code, catalog.Body.String())
	}
}

func TestLegacyUnverifiedLogoutRevokesItsAccessOnlySession(t *testing.T) {
	handler, store, _ := newV1AuthApp(t)
	access, _ := legacyUnverifiedAccessToken(t, handler, store)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer "+access)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("legacy logout = %d %s", response.Code, response.Body.String())
	}
	if profile := getV1Bearer(t, handler, "/api/v1/sync/profile", access); profile.Code != http.StatusUnauthorized {
		t.Fatalf("legacy access survived logout = %d %s", profile.Code, profile.Body.String())
	}
}

func TestLegacyUnverifiedAccessCannotWriteOrRestoreSync(t *testing.T) {
	handler, store, _ := newV1AuthApp(t)
	access, versionID := legacyUnverifiedAccessToken(t, handler, store)

	body := `{"baseVersion":1,"mutationId":"legacy_write","deviceId":"legacy-device","schemaVersion":2,"profile":` + sharedProfileFixture + `}`
	write := putSync(t, handler, access, "legacy_write", body)
	if write.Code != http.StatusForbidden || !strings.Contains(write.Body.String(), "EMAIL_VERIFICATION_REQUIRED") {
		t.Fatalf("legacy sync write = %d %s", write.Code, write.Body.String())
	}

	restoreRequest := httptest.NewRequest(http.MethodPost, "/api/v1/sync/profile/versions/"+versionID+"/restore", strings.NewReader(
		`{"baseVersion":1,"mutationId":"legacy_restore","deviceId":"legacy-device"}`,
	))
	restoreRequest.SetPathValue("id", versionID)
	restoreRequest.Header.Set("Authorization", "Bearer "+access)
	restoreRequest.Header.Set("Content-Type", "application/json")
	restore := httptest.NewRecorder()
	handler.ServeHTTP(restore, restoreRequest)
	if restore.Code != http.StatusForbidden || !strings.Contains(restore.Body.String(), "EMAIL_VERIFICATION_REQUIRED") {
		t.Fatalf("legacy sync restore = %d %s", restore.Code, restore.Body.String())
	}

	for table, want := range map[string]int{"sync_profiles": 1, "sync_profile_versions": 1, "sync_mutations": 0, "sync_attempts": 0} {
		var count int
		if err := store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s rows after blocked legacy writes = %d, want %d (err=%v)", table, count, want, err)
		}
	}
}

func TestLegacyVerificationRequiresANewFullScopeLogin(t *testing.T) {
	handler, store, mailer := newV1AuthApp(t)
	legacyAccess, _ := legacyUnverifiedAccessToken(t, handler, store)
	if resend := postV1(t, handler, "/api/v1/auth/resend-verification", `{"email":"legacy-reader@example.com"}`); resend.Code != http.StatusAccepted {
		t.Fatalf("legacy verification resend = %d %s", resend.Code, resend.Body.String())
	}
	verificationToken := mailer.token("verify_email")
	if verificationToken == "" {
		t.Fatal("legacy verification did not issue a token")
	}
	if verify := postV1(t, handler, "/api/v1/auth/verify-email", `{"token":"`+verificationToken+`"}`); verify.Code != http.StatusOK {
		t.Fatalf("legacy verification = %d %s", verify.Code, verify.Body.String())
	}

	body := `{"baseVersion":1,"mutationId":"old_scope_write","deviceId":"legacy-device","schemaVersion":2,"profile":` + sharedProfileFixture + `}`
	if stale := putSync(t, handler, legacyAccess, "old_scope_write", body); stale.Code != http.StatusForbidden {
		t.Fatalf("pre-verification scope became writable = %d %s", stale.Code, stale.Body.String())
	}
	login := postV1(t, handler, "/api/v1/auth/login", `{"email":"legacy-reader@example.com","password":"safe-password-123","deviceId":"verified-device"}`)
	data := decodeV1Data(t, login)
	fullAccess, _ := data["accessToken"].(string)
	refresh, _ := data["refreshToken"].(string)
	if login.Code != http.StatusOK || data["scope"] != "full" || fullAccess == "" || refresh == "" {
		t.Fatalf("verified migration login did not issue full credentials: status=%d data=%#v", login.Code, data)
	}
	fullBody := strings.Replace(body, "old_scope_write", "verified_scope_write", 1)
	if write := putSync(t, handler, fullAccess, "verified_scope_write", fullBody); write.Code != http.StatusOK {
		t.Fatalf("verified migration user could not write = %d %s", write.Code, write.Body.String())
	}
}

func TestSyncWriteEnforcesPersistedStorageBytesQuotaAtomically(t *testing.T) {
	handler, store, mailer := newV1AuthApp(t)
	access := verifiedAccessToken(t, handler, mailer, "storage-quota@example.com", "storage-quota-device")
	if _, err := store.db.ExecContext(t.Context(),
		`UPDATE settings SET value='{"profileBytes":524288,"storageBytes":1}' WHERE key='limits'`,
	); err != nil {
		t.Fatalf("set storage quota: %v", err)
	}

	body := `{"baseVersion":0,"mutationId":"storage_quota","deviceId":"storage-quota-device","schemaVersion":2,"profile":` + sharedProfileFixture + `}`
	response := putSync(t, handler, access, "storage_quota", body)
	if response.Code != http.StatusInsufficientStorage || !strings.Contains(response.Body.String(), "STORAGE_QUOTA_EXCEEDED") {
		t.Fatalf("storage quota response = %d %s", response.Code, response.Body.String())
	}

	for _, table := range []string{"sync_profiles", "sync_profile_versions", "sync_mutations", "sync_attempts", "devices"} {
		var count int
		if err := store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s rows after storage rejection = %d (err=%v)", table, count, err)
		}
	}
}

func TestRetentionSchedulerRunsAtStartupAndStopsWithContext(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.db.ExecContext(t.Context(),
		`INSERT INTO settings(key,value,updated_at) VALUES('limits','{"versionsPerUser":50,"accessLogDays":1,"auditLogDays":1}',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		nowString(),
	); err != nil {
		t.Fatalf("set retention limits: %v", err)
	}
	if err := store.InsertAPILog(t.Context(), APILogRecord{
		ID: "scheduled-old-log", CreatedAt: time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339Nano),
		Method: http.MethodGet, Path: "/api/v1/sync/profile", RouteGroup: "/api/v1/sync", Status: http.StatusOK,
	}); err != nil {
		t.Fatalf("seed old access log: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	reports := make(chan RetentionResult, 1)
	errorsSeen := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		store.RunRetentionScheduler(ctx, time.Hour, func(result RetentionResult, err error) {
			if err != nil {
				errorsSeen <- err
				return
			}
			reports <- result
		})
	}()

	select {
	case err := <-errorsSeen:
		t.Fatalf("scheduled retention failed: %v", err)
	case result := <-reports:
		if result.AccessLogsDeleted != 1 {
			t.Fatalf("scheduled retention result = %#v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scheduled retention did not run at startup")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("retention scheduler did not stop after context cancellation")
	}
}
