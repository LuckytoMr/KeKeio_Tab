package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAdminV1SMTPSettingsCanBeTestedSavedAndEnabledWithoutExposingPassword(t *testing.T) {
	app, store, handler, adminCookie := newAdminV1TestHandler(t)
	csrf := adminV1CSRFToken(t, app, adminCookie)
	secretsPath := t.TempDir() + "/secrets.json"
	if _, _, err := LoadOrCreateSecrets(secretsPath); err != nil {
		t.Fatalf("create secrets: %v", err)
	}
	app.config.SecretsPath = secretsPath
	var tested SMTPTestInput
	app.config.SMTPTester = func(_ context.Context, input SMTPTestInput) error {
		tested = input
		return nil
	}

	testResponse := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/system/settings/smtp-test", `{
		"host":"smtp.example.test","port":587,"tls":"starttls","from":"fullpro@example.test",
		"username":"mailer","password":"smtp-secret-123","recipient":"owner@example.com"
	}`, adminCookie, csrf)
	if testResponse.Code != http.StatusOK {
		t.Fatalf("smtp test = %d %s", testResponse.Code, testResponse.Body.String())
	}
	if tested.Host != "smtp.example.test" || tested.Password != "smtp-secret-123" || tested.Recipient != "owner@example.com" {
		t.Fatalf("tested SMTP input = %#v", tested)
	}
	var storedFingerprint string
	if err := store.db.QueryRow(`SELECT value FROM settings WHERE key='smtp_verified_fingerprint'`).Scan(&storedFingerprint); err != nil {
		t.Fatalf("read SMTP fingerprint: %v", err)
	}
	settingsForFingerprint := SMTPSettings{Host: "smtp.example.test", Port: 587, TLS: "starttls", From: "fullpro@example.test", Username: "mailer"}
	payload, _ := json.Marshal(struct {
		Settings SMTPSettings `json:"settings"`
		Password string       `json:"password"`
	}{Settings: settingsForFingerprint, Password: "smtp-secret-123"})
	legacyDigest := sha256.Sum256(payload)
	if hmac.Equal([]byte(storedFingerprint), []byte(hex.EncodeToString(legacyDigest[:]))) {
		t.Fatal("SMTP verification fingerprint must not be an offline password verifier")
	}
	secretsForFingerprint, _, err := LoadOrCreateSecrets(secretsPath)
	if err != nil {
		t.Fatalf("reload fingerprint key: %v", err)
	}
	mac := hmac.New(sha256.New, secretsForFingerprint.TokenDerivationKey)
	_, _ = mac.Write(payload)
	if !hmac.Equal([]byte(storedFingerprint), []byte(hex.EncodeToString(mac.Sum(nil)))) {
		t.Fatal("SMTP verification fingerprint is not bound to the server derivation key")
	}

	saved := adminV1Request(t, handler, http.MethodPut, "/api/admin/v1/system/settings", `{
		"registrationEnabled":true,
		"smtp":{"host":"smtp.example.test","port":587,"tls":"starttls","from":"fullpro@example.test","username":"mailer","password":"smtp-secret-123"}
	}`, adminCookie, csrf)
	if saved.Code != http.StatusOK {
		t.Fatalf("save SMTP settings = %d %s", saved.Code, saved.Body.String())
	}
	if strings.Contains(saved.Body.String(), "smtp-secret-123") || !strings.Contains(saved.Body.String(), `"passwordConfigured":true`) {
		t.Fatalf("SMTP response leaked password or omitted password state: %s", saved.Body.String())
	}
	settings, err := store.LoadRuntimeSettings(t.Context())
	if err != nil || settings.SMTP == nil || settings.SMTP.Host != "smtp.example.test" || !settings.RegistrationOpen {
		t.Fatalf("stored runtime settings = %#v err=%v", settings, err)
	}
	secrets, _, err := LoadOrCreateSecrets(secretsPath)
	if err != nil || secrets.SMTPPassword != "smtp-secret-123" {
		t.Fatalf("stored SMTP secret=%q err=%v", secrets.SMTPPassword, err)
	}
	mailer, ok := app.runtimeMailer().(SMTPMailer)
	if !ok || mailer.Settings.Host != "smtp.example.test" || mailer.Password != "smtp-secret-123" {
		t.Fatalf("runtime mailer = %#v", app.runtimeMailer())
	}

	retained := adminV1Request(t, handler, http.MethodPut, "/api/admin/v1/system/settings", `{
		"smtp":{"host":"smtp.example.test","port":587,"tls":"starttls","from":"fullpro@example.test","username":"mailer","password":""}
	}`, adminCookie, csrf)
	if retained.Code != http.StatusOK {
		t.Fatalf("retain SMTP password = %d %s", retained.Code, retained.Body.String())
	}
	secrets, _, err = LoadOrCreateSecrets(secretsPath)
	if err != nil || secrets.SMTPPassword != "smtp-secret-123" {
		t.Fatalf("retained SMTP secret=%q err=%v", secrets.SMTPPassword, err)
	}
}

func TestAdminV1SMTPTestAndSettingsSaveAreSerialized(t *testing.T) {
	app, _, handler, adminCookie := newAdminV1TestHandler(t)
	csrf := adminV1CSRFToken(t, app, adminCookie)
	secretsPath := t.TempDir() + "/secrets.json"
	if _, _, err := LoadOrCreateSecrets(secretsPath); err != nil {
		t.Fatalf("create secrets: %v", err)
	}
	app.config.SecretsPath = secretsPath
	testStarted := make(chan struct{})
	releaseTest := make(chan struct{})
	app.config.SMTPTester = func(_ context.Context, _ SMTPTestInput) error {
		close(testStarted)
		<-releaseTest
		return nil
	}
	testDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		testDone <- adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/system/settings/smtp-test", `{"host":"smtp.local","port":25,"tls":"none","from":"fullpro@example.test","recipient":"owner@example.com"}`, adminCookie, csrf)
	}()
	<-testStarted
	saveDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		saveDone <- adminV1Request(t, handler, http.MethodPut, "/api/admin/v1/system/settings", `{"maxUsers":"101"}`, adminCookie, csrf)
	}()
	select {
	case response := <-saveDone:
		t.Fatalf("settings save overtook SMTP test: %d %s", response.Code, response.Body.String())
	case <-time.After(75 * time.Millisecond):
	}
	close(releaseTest)
	if response := <-testDone; response.Code != http.StatusOK {
		t.Fatalf("SMTP test = %d %s", response.Code, response.Body.String())
	}
	if response := <-saveDone; response.Code != http.StatusOK {
		t.Fatalf("settings save = %d %s", response.Code, response.Body.String())
	}
}

func TestAdminV1UnauthenticatedSMTPCanRemainPasswordlessAndVerified(t *testing.T) {
	app, _, handler, adminCookie := newAdminV1TestHandler(t)
	csrf := adminV1CSRFToken(t, app, adminCookie)
	secretsPath := t.TempDir() + "/secrets.json"
	if _, _, err := LoadOrCreateSecrets(secretsPath); err != nil {
		t.Fatalf("create secrets: %v", err)
	}
	app.config.SecretsPath = secretsPath
	app.config.SMTPTester = func(_ context.Context, input SMTPTestInput) error {
		if input.Username != "" || input.Password != "" {
			t.Fatalf("passwordless SMTP input = %#v", input)
		}
		return nil
	}
	body := `{"host":"smtp.local","port":25,"tls":"none","from":"fullpro@example.test","username":"","password":"","recipient":"owner@example.com"}`
	if response := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/system/settings/smtp-test", body, adminCookie, csrf); response.Code != http.StatusOK {
		t.Fatalf("passwordless SMTP test = %d %s", response.Code, response.Body.String())
	}
	if response := adminV1Request(t, handler, http.MethodPut, "/api/admin/v1/system/settings", `{"smtp":{"host":"smtp.local","port":25,"tls":"none","from":"fullpro@example.test","username":"","password":""}}`, adminCookie, csrf); response.Code != http.StatusOK {
		t.Fatalf("passwordless SMTP save = %d %s", response.Code, response.Body.String())
	}
	getResponse := adminV1Request(t, handler, http.MethodGet, "/api/admin/v1/system/settings", "", adminCookie, "")
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Body.String(), `"passwordConfigured":false`) || !strings.Contains(getResponse.Body.String(), `"verified":true`) {
		t.Fatalf("passwordless SMTP DTO = %d %s", getResponse.Code, getResponse.Body.String())
	}
}

func newAdminV1TestHandler(t *testing.T) (*App, *Store, http.Handler, *http.Cookie) {
	t.Helper()
	app, store := newTestApp(t)
	authHandler := app.Routes()
	adminCookie := fixedAdminCookie(t, authHandler)
	mux := http.NewServeMux()
	app.registerAdminV1Routes(mux)
	return app, store, mux, adminCookie
}

func adminV1CSRFToken(t *testing.T, app *App, adminCookie *http.Cookie) string {
	t.Helper()
	response := get(t, app.Routes(), "/api/admin/v1/auth/session", adminCookie)
	var body struct {
		Data struct {
			CSRFToken string `json:"csrfToken"`
		} `json:"data"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Data.CSRFToken == "" {
		t.Fatalf("admin session probe = %d %s", response.Code, response.Body.String())
	}
	return body.Data.CSRFToken
}

func adminV1Request(t *testing.T, handler http.Handler, method, path, body string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.RemoteAddr = "127.0.0.1:1234"
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		request.Header.Set("Origin", "http://localhost:5173")
		request.Header.Set("X-CSRF-Token", csrf)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestAuthenticatedAdminSessionProbeIsStableAcrossTabs(t *testing.T) {
	app, store, adminHandler, adminCookie := newAdminV1TestHandler(t)
	sessionHandler := app.Routes()
	type probeResult struct {
		code int
		body []byte
	}
	results := make(chan probeResult, 2)
	for range 2 {
		go func() {
			request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/auth/session", nil)
			request.RemoteAddr = "127.0.0.1:1234"
			request.AddCookie(adminCookie)
			response := httptest.NewRecorder()
			sessionHandler.ServeHTTP(response, request)
			results <- probeResult{code: response.Code, body: append([]byte(nil), response.Body.Bytes()...)}
		}()
	}
	csrfTokens := make([]string, 0, 2)
	for range 2 {
		result := <-results
		var envelope struct {
			Data struct {
				Authenticated bool   `json:"authenticated"`
				CSRFToken     string `json:"csrfToken"`
			} `json:"data"`
		}
		if result.code != http.StatusOK || json.Unmarshal(result.body, &envelope) != nil || !envelope.Data.Authenticated || envelope.Data.CSRFToken == "" {
			t.Fatalf("authenticated session probe = %d %s", result.code, result.body)
		}
		csrfTokens = append(csrfTokens, envelope.Data.CSRFToken)
	}
	if csrfTokens[0] != csrfTokens[1] {
		t.Fatalf("two tabs received mutually invalidating CSRF tokens: %q != %q", csrfTokens[0], csrfTokens[1])
	}
	user, err := store.CreateUser(t.Context(), "csrf-tabs@example.test", "safe-password-123")
	if err != nil {
		t.Fatalf("create CSRF test user: %v", err)
	}
	for index, status := range []string{"suspended", "active"} {
		response := adminV1Request(t, adminHandler, http.MethodPost, "/api/admin/v1/users/"+user.ID+"/status", `{"status":"`+status+`"}`, adminCookie, csrfTokens[index])
		if response.Code != http.StatusOK {
			t.Fatalf("tab %d CSRF rejected = %d %s", index+1, response.Code, response.Body.String())
		}
	}
	forged := adminV1Request(t, adminHandler, http.MethodPost, "/api/admin/v1/users/"+user.ID+"/status", `{"status":"suspended"}`, adminCookie, "csrf_forged")
	if forged.Code != http.StatusForbidden || !strings.Contains(forged.Body.String(), "CSRF_REJECTED") {
		t.Fatalf("forged CSRF accepted = %d %s", forged.Code, forged.Body.String())
	}
}

func TestAuthenticatedAdminSessionProbeUpgradesLegacyRandomCSRF(t *testing.T) {
	app, store, adminHandler, _ := newAdminV1TestHandler(t)
	var adminID string
	if err := store.db.QueryRow(`SELECT id FROM admin_users WHERE status='active' LIMIT 1`).Scan(&adminID); err != nil {
		t.Fatalf("read admin id: %v", err)
	}
	legacyToken, legacyCSRF, err := store.CreateAdminSession(t.Context(), adminID, time.Hour)
	if err != nil {
		t.Fatalf("create legacy random-CSRF session: %v", err)
	}
	adminCookie := &http.Cookie{Name: app.config.CookieName, Value: legacyToken, Path: "/"}
	probe := get(t, app.Routes(), "/api/admin/v1/auth/session", adminCookie)
	var envelope struct {
		Data struct {
			Authenticated bool   `json:"authenticated"`
			CSRFToken     string `json:"csrfToken"`
		} `json:"data"`
	}
	if probe.Code != http.StatusOK || json.Unmarshal(probe.Body.Bytes(), &envelope) != nil || !envelope.Data.Authenticated || envelope.Data.CSRFToken == "" || envelope.Data.CSRFToken == legacyCSRF {
		t.Fatalf("legacy session upgrade = %d %s", probe.Code, probe.Body.String())
	}
	secondProbe := get(t, app.Routes(), "/api/admin/v1/auth/session", adminCookie)
	var secondEnvelope struct {
		Data struct {
			CSRFToken string `json:"csrfToken"`
		} `json:"data"`
	}
	if secondProbe.Code != http.StatusOK || json.Unmarshal(secondProbe.Body.Bytes(), &secondEnvelope) != nil || secondEnvelope.Data.CSRFToken != envelope.Data.CSRFToken {
		t.Fatalf("upgraded session was not stable: first=%q second=%d %s", envelope.Data.CSRFToken, secondProbe.Code, secondProbe.Body.String())
	}
	var storedHash string
	if err := store.db.QueryRow(`SELECT csrf_hash FROM admin_sessions WHERE token_hash=?`, tokenHash(legacyToken)).Scan(&storedHash); err != nil || storedHash != tokenHash(envelope.Data.CSRFToken) {
		t.Fatalf("legacy CSRF hash was not upgraded atomically: hash=%q err=%v", storedHash, err)
	}
	user, err := store.CreateUser(t.Context(), "legacy-csrf-upgrade@example.test", "safe-password-123")
	if err != nil {
		t.Fatalf("create legacy CSRF test user: %v", err)
	}
	oldToken := adminV1Request(t, adminHandler, http.MethodPost, "/api/admin/v1/users/"+user.ID+"/status", `{"status":"suspended"}`, adminCookie, legacyCSRF)
	if oldToken.Code != http.StatusForbidden {
		t.Fatalf("legacy random CSRF remained valid after upgrade: %d %s", oldToken.Code, oldToken.Body.String())
	}
	upgradedToken := adminV1Request(t, adminHandler, http.MethodPost, "/api/admin/v1/users/"+user.ID+"/status", `{"status":"suspended"}`, adminCookie, envelope.Data.CSRFToken)
	if upgradedToken.Code != http.StatusOK {
		t.Fatalf("upgraded deterministic CSRF rejected: %d %s", upgradedToken.Code, upgradedToken.Body.String())
	}
}

func TestAdminV1OverviewRequiresAuthenticationAndUsesCanonicalEnvelope(t *testing.T) {
	_, _, handler, adminCookie := newAdminV1TestHandler(t)

	unauthenticated := httptest.NewRequest(http.MethodGet, "/api/admin/v1/overview", nil)
	unauthenticated.RemoteAddr = "127.0.0.1:1234"
	unauthenticatedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticatedResponse, unauthenticated)
	if unauthenticatedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated overview = %d %s", unauthenticatedResponse.Code, unauthenticatedResponse.Body.String())
	}

	authenticated := httptest.NewRequest(http.MethodGet, "/api/admin/v1/overview", nil)
	authenticated.RemoteAddr = "127.0.0.1:1234"
	authenticated.AddCookie(adminCookie)
	authenticatedResponse := httptest.NewRecorder()
	handler.ServeHTTP(authenticatedResponse, authenticated)
	if authenticatedResponse.Code != http.StatusOK {
		t.Fatalf("authenticated overview = %d %s", authenticatedResponse.Code, authenticatedResponse.Body.String())
	}
	var body struct {
		Data struct {
			Health    []map[string]any `json:"health"`
			Attention []map[string]any `json:"attention"`
			Sync24H   map[string]any   `json:"sync24h"`
			Recent    []map[string]any `json:"recent"`
		} `json:"data"`
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(authenticatedResponse.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	if body.RequestID == "" || len(body.Data.Health) == 0 || body.Data.Attention == nil || body.Data.Sync24H == nil || body.Data.Recent == nil {
		t.Fatalf("overview DTO shape = %#v", body)
	}
}

func TestAdminV1UIReadRoutesAndStatusWriteGate(t *testing.T) {
	app, store, handler, adminCookie := newAdminV1TestHandler(t)
	csrf := adminV1CSRFToken(t, app, adminCookie)
	user, err := store.CreateUser(t.Context(), "member@example.com", "safe-password-123")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := store.db.ExecContext(t.Context(), `UPDATE users SET status='active', email_verified_at=?, updated_at=? WHERE id=?`, nowString(), nowString(), user.ID); err != nil {
		t.Fatalf("activate user: %v", err)
	}

	paths := []string{
		"/api/admin/v1/users", "/api/admin/v1/users/" + user.ID,
		"/api/admin/v1/sync/attempts", "/api/admin/v1/sync/conflicts",
		"/api/admin/v1/catalog/official", "/api/admin/v1/catalog/web", "/api/admin/v1/catalog/styles",
		"/api/admin/v1/releases", "/api/admin/v1/audit/admin", "/api/admin/v1/audit/access",
		"/api/admin/v1/system/settings", "/api/admin/v1/system/maintenance/jobs",
		"/api/admin/v1/system/backups", "/api/admin/v1/system/health",
	}
	for _, path := range paths {
		response := adminV1Request(t, handler, http.MethodGet, path, "", adminCookie, "")
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"requestId":"req_`) {
			t.Fatalf("GET %s = %d %s", path, response.Code, response.Body.String())
		}
	}

	missingJSON := httptest.NewRequest(http.MethodPost, "/api/admin/v1/users/"+user.ID+"/status", strings.NewReader(`{"status":"suspended"}`))
	missingJSON.RemoteAddr = "127.0.0.1:1234"
	missingJSON.Header.Set("Origin", "http://localhost:5173")
	missingJSON.Header.Set("X-CSRF-Token", csrf)
	missingJSON.AddCookie(adminCookie)
	missingJSONResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingJSONResponse, missingJSON)
	if missingJSONResponse.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status without JSON = %d %s", missingJSONResponse.Code, missingJSONResponse.Body.String())
	}

	missingCSRF := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/users/"+user.ID+"/status", `{"status":"suspended"}`, adminCookie, "")
	if missingCSRF.Code != http.StatusForbidden {
		t.Fatalf("status without CSRF = %d %s", missingCSRF.Code, missingCSRF.Body.String())
	}

	updated := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/users/"+user.ID+"/status", `{"status":"suspended"}`, adminCookie, csrf)
	if updated.Code != http.StatusOK {
		t.Fatalf("status update = %d %s", updated.Code, updated.Body.String())
	}
	var status, auditAction string
	if err := store.db.QueryRowContext(t.Context(), `SELECT status FROM users WHERE id=?`, user.ID).Scan(&status); err != nil || status != "suspended" {
		t.Fatalf("stored status=%q err=%v", status, err)
	}
	if err := store.db.QueryRowContext(t.Context(), `SELECT action FROM admin_audit_logs ORDER BY created_at DESC LIMIT 1`).Scan(&auditAction); err != nil || auditAction != "user.status.update" {
		t.Fatalf("audit action=%q err=%v", auditAction, err)
	}
}

func TestAdminV1UserSessionRevokeAndVersionRestoreAreScopedAndAudited(t *testing.T) {
	app, store, handler, adminCookie := newAdminV1TestHandler(t)
	csrf := adminV1CSRFToken(t, app, adminCookie)
	user, err := store.CreateUser(t.Context(), "sync-owner@example.com", "safe-password-123")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	other, err := store.CreateUser(t.Context(), "other@example.com", "safe-password-123")
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}
	if _, err := store.db.ExecContext(t.Context(), `UPDATE users SET status='active',email_verified_at=?,updated_at=? WHERE id IN (?,?)`, nowString(), nowString(), user.ID, other.ID); err != nil {
		t.Fatalf("activate users: %v", err)
	}
	pair, err := store.CreateTokenFamily(t.Context(), user.ID, "device-admin-test")
	if err != nil {
		t.Fatalf("create token family: %v", err)
	}
	var familyID string
	if err := store.db.QueryRowContext(t.Context(), `SELECT family_id FROM refresh_tokens WHERE token_hash=?`, tokenHash(pair.RefreshToken)).Scan(&familyID); err != nil {
		t.Fatalf("read family id: %v", err)
	}
	request := SyncMutationRequest{BaseVersion: 0, MutationID: "admin-v1-seed", DeviceID: "device-admin-test", SchemaVersion: 2, Profile: json.RawMessage(sharedProfileFixture)}
	if _, _, _, err := store.ApplySyncMutation(t.Context(), user.ID, request, "seed-hash"); err != nil {
		t.Fatalf("seed sync profile: %v", err)
	}
	var versionID string
	if err := store.db.QueryRowContext(t.Context(), `SELECT id FROM sync_profile_versions WHERE user_id=? AND version=1`, user.ID).Scan(&versionID); err != nil {
		t.Fatalf("read version id: %v", err)
	}

	crossAccount := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/users/"+other.ID+"/sessions/"+familyID+"/revoke", `{}`, adminCookie, csrf)
	if crossAccount.Code != http.StatusNotFound {
		t.Fatalf("cross-account revoke = %d %s", crossAccount.Code, crossAccount.Body.String())
	}
	validRevoke := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/users/"+user.ID+"/sessions/"+familyID+"/revoke", `{}`, adminCookie, csrf)
	if validRevoke.Code != http.StatusOK {
		t.Fatalf("valid revoke = %d %s", validRevoke.Code, validRevoke.Body.String())
	}
	validRestore := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/users/"+user.ID+"/versions/"+versionID+"/restore", `{}`, adminCookie, csrf)
	if validRestore.Code != http.StatusOK {
		t.Fatalf("valid restore = %d %s", validRestore.Code, validRestore.Body.String())
	}
	var version int
	if err := store.db.QueryRowContext(t.Context(), `SELECT version FROM sync_profiles WHERE user_id=?`, user.ID).Scan(&version); err != nil || version != 2 {
		t.Fatalf("restored profile version=%d err=%v", version, err)
	}
	var auditRows int
	if err := store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM admin_audit_logs WHERE action IN ('user.session.revoke','user.version.restore')`).Scan(&auditRows); err != nil || auditRows != 2 {
		t.Fatalf("action audit rows=%d err=%v", auditRows, err)
	}
}

func TestAdminV1UsersExposeActiveBrowserCountAndNormalizedSessions(t *testing.T) {
	_, store, handler, adminCookie := newAdminV1TestHandler(t)
	user, err := store.CreateUser(t.Context(), "browser-owner@example.com", "safe-password-123")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := store.db.ExecContext(t.Context(), `UPDATE users SET status='active',email_verified_at=?,updated_at=? WHERE id=?`, nowString(), nowString(), user.ID); err != nil {
		t.Fatalf("activate user: %v", err)
	}
	if _, err := store.createTokenFamilyForBrowser(t.Context(), user.ID, "device-chrome", clientBrowser{Family: "chrome", Version: "126.0.0.0"}); err != nil {
		t.Fatalf("create Chrome family: %v", err)
	}
	if _, err := store.createTokenFamilyForBrowser(t.Context(), user.ID, "device-chrome", clientBrowser{Family: "chrome", Version: "126.0.0.1"}); err != nil {
		t.Fatalf("create duplicate Chrome family: %v", err)
	}
	edgePair, err := store.createTokenFamilyForBrowser(t.Context(), user.ID, "device-edge", clientBrowser{Family: "edge", Version: "127.0.2651.74"})
	if err != nil {
		t.Fatalf("create Edge family: %v", err)
	}
	if _, err := store.createTokenFamilyForBrowser(t.Context(), user.ID, "device-revoked", clientBrowser{Family: "safari", Version: "17.6"}); err != nil {
		t.Fatalf("create revoked family: %v", err)
	}
	if _, err := store.db.ExecContext(t.Context(), `UPDATE refresh_token_families SET revoked_at=? WHERE user_id=? AND device_id='device-revoked'`, nowString(), user.ID); err != nil {
		t.Fatalf("revoke family: %v", err)
	}
	if _, err := store.createTokenFamilyForBrowser(t.Context(), user.ID, "device-expired", clientBrowser{Family: "firefox", Version: "128.0"}); err != nil {
		t.Fatalf("create expired family: %v", err)
	}
	if _, err := store.db.ExecContext(t.Context(), `UPDATE refresh_token_families SET expires_at='2000-01-01T00:00:00Z' WHERE user_id=? AND device_id='device-expired'`, user.ID); err != nil {
		t.Fatalf("expire family: %v", err)
	}
	latestUse := "2099-01-02T03:04:05Z"
	if _, err := store.db.ExecContext(t.Context(), `UPDATE access_tokens SET created_at=? WHERE token_hash=?`, latestUse, tokenHash(edgePair.AccessToken)); err != nil {
		t.Fatalf("update Edge last use: %v", err)
	}
	seenAt := nowString()
	if _, err := store.db.ExecContext(t.Context(), `INSERT INTO devices(user_id,device_id,first_seen_at,last_seen_at,last_version,revoked_at) VALUES(?,?,?,?,1,'')`, user.ID, "device-chrome", seenAt, seenAt); err != nil {
		t.Fatalf("seed sync-writing device: %v", err)
	}

	listResponse := adminV1Request(t, handler, http.MethodGet, "/api/admin/v1/users", "", adminCookie, "")
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list users = %d %s", listResponse.Code, listResponse.Body.String())
	}
	var listEnvelope struct {
		Data struct {
			Items []struct {
				DeviceCount     int      `json:"deviceCount"`
				BrowserCount    int      `json:"browserCount"`
				BrowserFamilies []string `json:"browserFamilies"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listResponse.Body.Bytes(), &listEnvelope); err != nil {
		t.Fatalf("decode user list: %v", err)
	}
	if len(listEnvelope.Data.Items) != 1 {
		t.Fatalf("user list items = %#v", listEnvelope.Data.Items)
	}
	item := listEnvelope.Data.Items[0]
	if item.DeviceCount != 1 || item.BrowserCount != 2 || strings.Join(item.BrowserFamilies, ",") != "chrome,edge" {
		t.Fatalf("user browser summary = %#v", item)
	}

	detailResponse := adminV1Request(t, handler, http.MethodGet, "/api/admin/v1/users/"+user.ID, "", adminCookie, "")
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("user detail = %d %s", detailResponse.Code, detailResponse.Body.String())
	}
	var detailEnvelope struct {
		Data struct {
			User struct {
				BrowserCount    int      `json:"browserCount"`
				BrowserFamilies []string `json:"browserFamilies"`
			} `json:"user"`
			Sessions []struct {
				DeviceID       string `json:"deviceId"`
				BrowserFamily  string `json:"browserFamily"`
				BrowserVersion string `json:"browserVersion"`
				LastUsedAt     string `json:"lastUsedAt"`
			} `json:"sessions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(detailResponse.Body.Bytes(), &detailEnvelope); err != nil {
		t.Fatalf("decode user detail: %v", err)
	}
	if detailEnvelope.Data.User.BrowserCount != 2 || strings.Join(detailEnvelope.Data.User.BrowserFamilies, ",") != "chrome,edge" {
		t.Fatalf("detail browser summary = %#v", detailEnvelope.Data.User)
	}
	foundEdge := false
	for _, session := range detailEnvelope.Data.Sessions {
		if session.DeviceID == "device-revoked" || session.DeviceID == "device-expired" {
			t.Fatalf("inactive session exposed: %#v", session)
		}
		if session.DeviceID != "device-edge" {
			continue
		}
		foundEdge = true
		if session.BrowserFamily != "edge" || session.BrowserVersion != "127.0.2651.74" || session.LastUsedAt != latestUse {
			t.Fatalf("Edge session = %#v", session)
		}
	}
	if !foundEdge {
		t.Fatalf("Edge session missing: %#v", detailEnvelope.Data.Sessions)
	}
}

func TestAdminV1UserVersionsExposeOnlyStructuralAndChangeSummaries(t *testing.T) {
	_, store, handler, adminCookie := newAdminV1TestHandler(t)
	user, err := store.CreateUser(t.Context(), "version-summary@example.com", "safe-password-123")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := store.db.ExecContext(t.Context(), `UPDATE users SET status='active',email_verified_at=?,updated_at=? WHERE id=?`, nowString(), nowString(), user.ID); err != nil {
		t.Fatalf("activate user: %v", err)
	}
	first := SyncMutationRequest{BaseVersion: 0, MutationID: "summary-v1", DeviceID: "summary-device", SchemaVersion: 2, Profile: json.RawMessage(sharedProfileFixture)}
	if _, _, _, err := store.ApplySyncMutation(t.Context(), user.ID, first, "summary-hash-v1"); err != nil {
		t.Fatalf("seed version 1: %v", err)
	}
	var profile map[string]any
	if err := json.Unmarshal([]byte(sharedProfileFixture), &profile); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	shortcuts := profile["shortcuts"].([]any)
	secondShortcut := map[string]any{}
	for key, value := range shortcuts[0].(map[string]any) {
		secondShortcut[key] = value
	}
	secondShortcut["id"] = "s2"
	secondShortcut["title"] = "Private second shortcut"
	secondShortcut["url"] = "https://private.example.test/secret"
	secondShortcut["sortIndex"] = float64(1)
	profile["shortcuts"] = append(shortcuts, secondShortcut)
	secondRaw, _ := json.Marshal(profile)
	second := SyncMutationRequest{BaseVersion: 1, MutationID: "summary-v2", DeviceID: "summary-device", SchemaVersion: 2, Profile: secondRaw}
	if _, _, _, err := store.ApplySyncMutation(t.Context(), user.ID, second, "summary-hash-v2"); err != nil {
		t.Fatalf("seed version 2: %v", err)
	}

	response := adminV1Request(t, handler, http.MethodGet, "/api/admin/v1/users/"+user.ID, "", adminCookie, "")
	if response.Code != http.StatusOK {
		t.Fatalf("user detail = %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "private.example.test") || strings.Contains(response.Body.String(), "https://example.com") {
		t.Fatalf("version DTO leaked private URL: %s", response.Body.String())
	}
	var body struct {
		Data struct {
			Versions []struct {
				Version int `json:"version"`
				Summary struct {
					Groups    int    `json:"groups"`
					Shortcuts int    `json:"shortcuts"`
					Wallpaper string `json:"wallpaper"`
					StyleID   string `json:"styleId"`
				} `json:"summary"`
				Changes struct {
					CurrentVersion   int  `json:"currentVersion"`
					GroupsDelta      int  `json:"groupsDelta"`
					ShortcutsDelta   int  `json:"shortcutsDelta"`
					WallpaperChanged bool `json:"wallpaperChanged"`
					StyleChanged     bool `json:"styleChanged"`
				} `json:"changes"`
			} `json:"versions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode version summaries: %v", err)
	}
	if len(body.Data.Versions) < 2 || body.Data.Versions[0].Version != 2 || body.Data.Versions[0].Summary.Groups != 1 || body.Data.Versions[0].Summary.Shortcuts != 2 {
		t.Fatalf("version summaries = %#v", body.Data.Versions)
	}
	if body.Data.Versions[0].Changes.CurrentVersion != 2 || body.Data.Versions[0].Changes.ShortcutsDelta != 0 {
		t.Fatalf("current version comparison = %#v", body.Data.Versions[0].Changes)
	}
	if body.Data.Versions[1].Version != 1 || body.Data.Versions[1].Changes.CurrentVersion != 2 || body.Data.Versions[1].Changes.ShortcutsDelta != -1 {
		t.Fatalf("restore target comparison = %#v", body.Data.Versions[1])
	}
}

func TestAdminV1VersionSummaryDetectsDifferentWallpaperWithSameKind(t *testing.T) {
	_, store, handler, adminCookie := newAdminV1TestHandler(t)
	user, err := store.CreateUser(t.Context(), "wallpaper-summary@example.test", "safe-password-123")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := store.db.ExecContext(t.Context(), `UPDATE users SET status='active',email_verified_at=?,updated_at=? WHERE id=?`, nowString(), nowString(), user.ID); err != nil {
		t.Fatalf("activate user: %v", err)
	}
	first := SyncMutationRequest{BaseVersion: 0, MutationID: "wallpaper-summary-v1", DeviceID: "wallpaper-summary-device", SchemaVersion: 2, Profile: json.RawMessage(sharedProfileFixture)}
	if _, _, _, err := store.ApplySyncMutation(t.Context(), user.ID, first, "wallpaper-summary-hash-v1"); err != nil {
		t.Fatalf("seed wallpaper version 1: %v", err)
	}
	secondRaw := strings.Replace(sharedProfileFixture, `"id":"mist"`, `"id":"dawn"`, 1)
	second := SyncMutationRequest{BaseVersion: 1, MutationID: "wallpaper-summary-v2", DeviceID: "wallpaper-summary-device", SchemaVersion: 2, Profile: json.RawMessage(secondRaw)}
	if _, _, _, err := store.ApplySyncMutation(t.Context(), user.ID, second, "wallpaper-summary-hash-v2"); err != nil {
		t.Fatalf("seed wallpaper version 2: %v", err)
	}
	response := adminV1Request(t, handler, http.MethodGet, "/api/admin/v1/users/"+user.ID, "", adminCookie, "")
	if response.Code != http.StatusOK {
		t.Fatalf("user detail = %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), `"mist"`) || strings.Contains(response.Body.String(), `"dawn"`) || strings.Contains(response.Body.String(), "https://") {
		t.Fatalf("wallpaper summary leaked identifier or URL: %s", response.Body.String())
	}
	var envelope struct {
		Data struct {
			Versions []struct {
				Version int `json:"version"`
				Summary struct {
					Wallpaper string `json:"wallpaper"`
				} `json:"summary"`
				Changes struct {
					WallpaperChanged bool `json:"wallpaperChanged"`
				} `json:"changes"`
			} `json:"versions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || len(envelope.Data.Versions) < 2 {
		t.Fatalf("decode wallpaper summaries: %v %s", err, response.Body.String())
	}
	if envelope.Data.Versions[0].Version != 2 || envelope.Data.Versions[0].Summary.Wallpaper != "builtin" || envelope.Data.Versions[0].Changes.WallpaperChanged {
		t.Fatalf("current wallpaper summary=%+v", envelope.Data.Versions[0])
	}
	if envelope.Data.Versions[1].Version != 1 || envelope.Data.Versions[1].Summary.Wallpaper != "builtin" || !envelope.Data.Versions[1].Changes.WallpaperChanged {
		t.Fatalf("same-kind historical wallpaper change was missed: %+v", envelope.Data.Versions[1])
	}
}

func TestAdminV1StyleDraftValidatePreviewPublishDisableArchive(t *testing.T) {
	app, store, handler, adminCookie := newAdminV1TestHandler(t)
	csrf := adminV1CSRFToken(t, app, adminCookie)
	payload := `{
		"id":"style:quiet","name":"Quiet","description":"A quiet style","previewUrl":"https://cdn.example.test/quiet.png",
		"version":"1.0.0","schemaVersion":2,
		"css":".newtab-root[data-style-id=\"style:quiet\"] .shortcut-card { color: #223344; }",
		"config":{"density":"comfortable"}
	}`
	created := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/catalog/styles", payload, adminCookie, csrf)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"status":"draft"`) {
		t.Fatalf("create style draft = %d %s", created.Code, created.Body.String())
	}
	detail := adminV1Request(t, handler, http.MethodGet, "/api/admin/v1/catalog/styles/style:quiet", "", adminCookie, "")
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `"revisions"`) {
		t.Fatalf("style detail = %d %s", detail.Code, detail.Body.String())
	}
	validated := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/catalog/styles/style:quiet/validate", `{}`, adminCookie, csrf)
	if validated.Code != http.StatusOK || !strings.Contains(validated.Body.String(), `"status":"ready"`) {
		t.Fatalf("validate style = %d %s", validated.Code, validated.Body.String())
	}
	preview := adminV1Request(t, handler, http.MethodGet, "/api/admin/v1/catalog/styles/style:quiet/preview", "", adminCookie, "")
	if preview.Code != http.StatusOK || !strings.Contains(preview.Header().Get("Content-Security-Policy"), "sandbox") || !strings.Contains(preview.Body.String(), "shortcut-card") {
		t.Fatalf("style preview = %d CSP=%q body=%s", preview.Code, preview.Header().Get("Content-Security-Policy"), preview.Body.String())
	}
	published := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/catalog/styles/style:quiet/publish", `{}`, adminCookie, csrf)
	if published.Code != http.StatusOK || !strings.Contains(published.Body.String(), `"status":"published"`) {
		t.Fatalf("publish style = %d %s", published.Code, published.Body.String())
	}
	var enabled int
	if err := store.db.QueryRowContext(t.Context(), `SELECT enabled FROM style_packages WHERE id='style:quiet'`).Scan(&enabled); err != nil || enabled != 1 {
		t.Fatalf("published style enabled=%d err=%v", enabled, err)
	}
	disabled := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/catalog/styles/style:quiet/disable", `{}`, adminCookie, csrf)
	if disabled.Code != http.StatusOK {
		t.Fatalf("disable style = %d %s", disabled.Code, disabled.Body.String())
	}
	archived := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/catalog/styles/style:quiet/archive", `{}`, adminCookie, csrf)
	if archived.Code != http.StatusOK || !strings.Contains(archived.Body.String(), `"visibility":"archived"`) {
		t.Fatalf("archive style = %d %s", archived.Code, archived.Body.String())
	}
	var auditRows int
	if err := store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM admin_audit_logs WHERE target_id='style:quiet'`).Scan(&auditRows); err != nil || auditRows != 5 {
		t.Fatalf("style audit rows=%d err=%v", auditRows, err)
	}
}

func TestAdminV1StylePreviewRejectsStoredHTMLDelimiter(t *testing.T) {
	app, _, handler, adminCookie := newAdminV1TestHandler(t)
	csrf := adminV1CSRFToken(t, app, adminCookie)
	payload, _ := json.Marshal(map[string]any{
		"id": "style:xss", "name": "Unsafe", "version": "1.0.0", "schemaVersion": 2,
		"css":    `.newtab-root[data-style-id="style:xss"] .shortcut-card { color: red; }</style><script>alert(1)</script><style>`,
		"config": map[string]any{},
	})
	created := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/catalog/styles", string(payload), adminCookie, csrf)
	if created.Code != http.StatusCreated {
		t.Fatalf("create unsafe style = %d %s", created.Code, created.Body.String())
	}
	validated := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/catalog/styles/style:xss/validate", `{}`, adminCookie, csrf)
	if validated.Code != http.StatusUnprocessableEntity {
		t.Fatalf("validate unsafe style = %d %s", validated.Code, validated.Body.String())
	}
	preview := adminV1Request(t, handler, http.MethodGet, "/api/admin/v1/catalog/styles/style:xss/preview", "", adminCookie, "")
	if preview.Code != http.StatusUnprocessableEntity || strings.Contains(strings.ToLower(preview.Body.String()), "<script") {
		t.Fatalf("unsafe preview = %d %s", preview.Code, preview.Body.String())
	}
}

func TestValidateCatalogStyleCSSRejectsEscapedDangerousConstructsAndUnscopedSelectors(t *testing.T) {
	const itemID = "style:quiet"
	prefix := `.newtab-root[data-style-id="` + itemID + `"]`
	tests := map[string]string{
		"escaped url function":         prefix + ` .shortcut-card { background-image: u\72l(https://evil.example/beacon); }`,
		"padded escaped url function":  prefix + ` .shortcut-card { background-image: \000075\000072\00006c(https://evil.example/beacon); }`,
		"comment split url function":   prefix + ` .shortcut-card { background-image: u/**/rl(https://evil.example/beacon); }`,
		"escaped var function":         prefix + ` .shortcut-card { color: v\61r(--full-pro-color); }`,
		"escaped expression function":  prefix + ` .shortcut-card { width: e\78pression(alert(1)); }`,
		"escaped import at-rule":       `@\69mport "https://evil.example/style.css"; ` + prefix + ` .shortcut-card { color: red; }`,
		"escaped at sign":              `\40 import "https://evil.example/style.css"; ` + prefix + ` .shortcut-card { color: red; }`,
		"other at-rule":                `@media (min-width: 1px) { ` + prefix + ` .shortcut-card { color: red; } }`,
		"HTML delimiter in comment":    `/* </style><script>alert(1)</script> */ ` + prefix + ` .shortcut-card { color: red; }`,
		"unscoped selector list item":  prefix + ` .shortcut-card, body { color: red; }`,
		"unscoped separate rule":       prefix + ` { color: red; } body { color: black; }`,
		"sibling scope escape":         prefix + `.active ~ body { color: red; }`,
		"scope prefix only in comment": `/* ` + prefix + ` */ body { color: red; }`,
	}

	for name, css := range tests {
		t.Run(name, func(t *testing.T) {
			errorsByField := validateCatalogFields("styles", itemID, map[string]any{
				"name":          "Quiet",
				"version":       "1.0.0",
				"schemaVersion": 2,
				"css":           css,
				"config":        map[string]any{},
			})
			if errorsByField["css"] == "" {
				t.Fatalf("unsafe CSS was accepted: %s", css)
			}
		})
	}
}

func TestValidateCatalogStyleCSSAcceptsEveryScopedSelectorAfterEscapeNormalization(t *testing.T) {
	const itemID = "style:quiet"
	prefix := `.newtab-root[data-style-id="` + itemID + `"]`
	tests := map[string]string{
		"selector list":    prefix + ` .shortcut-card, ` + prefix + ` .app-shell { background: linear-gradient(#fff, #eee); color: #223344; }`,
		"child combinator": prefix + ` > .shortcut-card { color: #223344; }`,
		"escaped scope":    `.newtab-r\6f ot[data-style-id="style:quiet"] .shortcut-card { color: #223344; }`,
	}

	for name, css := range tests {
		t.Run(name, func(t *testing.T) {
			errorsByField := validateCatalogFields("styles", itemID, map[string]any{
				"name":          "Quiet",
				"version":       "1.0.0",
				"schemaVersion": 2,
				"css":           css,
				"config":        map[string]any{},
			})
			if message := errorsByField["css"]; message != "" {
				t.Fatalf("safe CSS was rejected: %s (css=%s)", message, css)
			}
		})
	}
}

func TestAdminV1OperationsWritesExportsAndBackupIntegrity(t *testing.T) {
	app, store, handler, adminCookie := newAdminV1TestHandler(t)
	csrf := adminV1CSRFToken(t, app, adminCookie)

	unknownSetting := adminV1Request(t, handler, http.MethodPut, "/api/admin/v1/system/settings", `{"listenAddress":"0.0.0.0:80"}`, adminCookie, csrf)
	if unknownSetting.Code != http.StatusBadRequest {
		t.Fatalf("startup setting accepted = %d %s", unknownSetting.Code, unknownSetting.Body.String())
	}
	settings := adminV1Request(t, handler, http.MethodPut, "/api/admin/v1/system/settings", `{
		"registrationEnabled":false,"publicBaseUrl":"https://fullpro.example","webOrigins":"https://app.example\nchrome-extension://abcdefghijklmnopabcdefghijklmnop",
		"maxUsers":"100","profileKiB":"512","accessLogDays":"1","auditLogDays":"180"
	}`, adminCookie, csrf)
	if settings.Code != http.StatusOK {
		t.Fatalf("settings update = %d %s", settings.Code, settings.Body.String())
	}

	release := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/releases", `{"version":"0.2.0","channel":"stable","notes":"Canonical admin","downloadUrl":"https://example.test/store","minimumVersion":"0.1.0","status":"draft"}`, adminCookie, csrf)
	if release.Code != http.StatusCreated || !strings.Contains(release.Body.String(), `"status":"draft"`) {
		t.Fatalf("release draft = %d %s", release.Code, release.Body.String())
	}
	releases := adminV1Request(t, handler, http.MethodGet, "/api/admin/v1/releases", "", adminCookie, "")
	if releases.Code != http.StatusOK || !strings.Contains(releases.Body.String(), `"version":"0.2.0"`) {
		t.Fatalf("release list = %d %s", releases.Code, releases.Body.String())
	}

	old := time.Now().UTC().Add(-10 * 24 * time.Hour).Format(time.RFC3339Nano)
	if err := store.InsertAPILog(t.Context(), APILogRecord{ID: "log_old", CreatedAt: old, Method: "GET", Path: "/api/v1/sync/profile?token=secret", RouteGroup: "/api/v1/sync", Status: 200}); err != nil {
		t.Fatalf("seed access log: %v", err)
	}
	for _, kind := range []string{"cleanup", "checkpoint", "retention"} {
		response := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/system/maintenance/jobs", `{"kind":"`+kind+`"}`, adminCookie, csrf)
		if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"status":"completed"`) {
			t.Fatalf("maintenance %s = %d %s", kind, response.Code, response.Body.String())
		}
	}
	var oldLogs int
	if err := store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM api_logs WHERE id='log_old'`).Scan(&oldLogs); err != nil || oldLogs != 0 {
		t.Fatalf("retention old logs=%d err=%v", oldLogs, err)
	}
	if err := store.InsertAPILog(t.Context(), APILogRecord{ID: "log_export", Method: "GET", Path: "/api/v1/sync/profile?token=secret", RouteGroup: "/api/v1/sync", Status: 200}); err != nil {
		t.Fatalf("seed export log: %v", err)
	}
	accessExport := adminV1Request(t, handler, http.MethodGet, "/api/admin/v1/audit/access/export", "", adminCookie, "")
	if accessExport.Code != http.StatusOK || !strings.Contains(accessExport.Header().Get("Content-Type"), "text/csv") || strings.Contains(accessExport.Body.String(), "secret") {
		t.Fatalf("access export = %d type=%q body=%s", accessExport.Code, accessExport.Header().Get("Content-Type"), accessExport.Body.String())
	}
	adminExport := adminV1Request(t, handler, http.MethodGet, "/api/admin/v1/audit/admin/export", "", adminCookie, "")
	if adminExport.Code != http.StatusOK || !strings.Contains(adminExport.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("admin export = %d type=%q body=%s", adminExport.Code, adminExport.Header().Get("Content-Type"), adminExport.Body.String())
	}

	createBackup := func() (string, string) {
		response := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/system/backups", `{"kind":"data-only"}`, adminCookie, csrf)
		if response.Code != http.StatusCreated {
			t.Fatalf("create backup = %d %s", response.Code, response.Body.String())
		}
		var body struct {
			Data struct {
				Item struct {
					ID       string `json:"id"`
					Checksum string `json:"checksum"`
				} `json:"item"`
			} `json:"data"`
		}
		if json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Data.Item.ID == "" || body.Data.Item.Checksum == "" {
			t.Fatalf("backup DTO = %s", response.Body.String())
		}
		var databasePath, manifestPath string
		if err := store.db.QueryRowContext(t.Context(), `SELECT database_path,manifest_path FROM backup_records WHERE id=?`, body.Data.Item.ID).Scan(&databasePath, &manifestPath); err != nil {
			t.Fatalf("read backup record: %v", err)
		}
		if _, err := os.Stat(databasePath); err != nil {
			t.Fatalf("snapshot missing: %v", err)
		}
		if manifest, err := os.ReadFile(manifestPath); err != nil || !strings.Contains(string(manifest), body.Data.Item.Checksum) {
			t.Fatalf("manifest invalid: %v %s", err, manifest)
		}
		return body.Data.Item.ID, databasePath
	}

	tamperedID, tamperedPath := createBackup()
	file, err := os.OpenFile(tamperedPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open snapshot for tamper: %v", err)
	}
	_, _ = file.WriteString("tampered")
	_ = file.Close()
	tamperedRestore := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/system/backups/"+tamperedID+"/restore", `{}`, adminCookie, csrf)
	if tamperedRestore.Code != http.StatusConflict {
		t.Fatalf("tampered restore = %d %s", tamperedRestore.Code, tamperedRestore.Body.String())
	}

	validID, _ := createBackup()
	called := false
	previousScheduler := adminV1ScheduleRestore
	adminV1ScheduleRestore = func(_ *App, livePath, stagedPath, _, _ string) {
		called = livePath != "" && stagedPath != ""
	}
	t.Cleanup(func() { adminV1ScheduleRestore = previousScheduler })
	validRestore := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/system/backups/"+validID+"/restore", `{}`, adminCookie, csrf)
	if validRestore.Code != http.StatusAccepted || !called {
		t.Fatalf("valid restore = %d called=%t body=%s", validRestore.Code, called, validRestore.Body.String())
	}
}

func TestConcurrentMaintenanceJobsOnlyOneClaimsExecution(t *testing.T) {
	app, store, handler, adminCookie := newAdminV1TestHandler(t)
	csrf := adminV1CSRFToken(t, app, adminCookie)
	now := time.Now().UTC()
	if err := store.InsertAPILog(t.Context(), APILogRecord{ID: "maintenance-claim-old-log", CreatedAt: now.Add(-365 * 24 * time.Hour).Format(time.RFC3339Nano), Method: "GET", Path: "/old", RouteGroup: "/old", Status: 200}); err != nil {
		t.Fatalf("seed old log: %v", err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	var pauseOnce sync.Once
	app.beforeMaintenanceRun = func() {
		pauseOnce.Do(func() {
			close(entered)
			<-release
		})
	}
	makeRequest := func() (*httptest.ResponseRecorder, *http.Request) {
		request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/system/maintenance/jobs", strings.NewReader(`{"kind":"cleanup"}`))
		request.RemoteAddr = "127.0.0.1:1234"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "http://localhost:5173")
		request.Header.Set("X-CSRF-Token", csrf)
		request.AddCookie(adminCookie)
		return httptest.NewRecorder(), request
	}
	firstResponse, firstRequest := makeRequest()
	firstDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(firstResponse, firstRequest)
		close(firstDone)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first maintenance job did not reach claimed execution state")
	}

	secondResponse, secondRequest := makeRequest()
	secondDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(secondResponse, secondRequest)
		close(secondDone)
	}()
	select {
	case <-secondDone:
		if secondResponse.Code != http.StatusConflict || !strings.Contains(secondResponse.Body.String(), "MAINTENANCE_CONFLICT") {
			close(release)
			t.Fatalf("second concurrent maintenance = %d %s", secondResponse.Code, secondResponse.Body.String())
		}
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("second maintenance request waited instead of failing its claim")
	}
	var beforeRelease int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM api_logs WHERE id='maintenance-claim-old-log'`).Scan(&beforeRelease); err != nil || beforeRelease != 1 {
		close(release)
		t.Fatalf("rejected maintenance performed business writes: rows=%d err=%v", beforeRelease, err)
	}
	close(release)
	select {
	case <-firstDone:
		if firstResponse.Code != http.StatusCreated {
			t.Fatalf("claimed maintenance = %d %s", firstResponse.Code, firstResponse.Body.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("claimed maintenance did not finish")
	}
	var afterRun, jobRows int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM api_logs WHERE id='maintenance-claim-old-log'`).Scan(&afterRun); err != nil || afterRun != 0 {
		t.Fatalf("claimed cleanup did not run retention: rows=%d err=%v", afterRun, err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM maintenance_jobs`).Scan(&jobRows); err != nil || jobRows != 1 {
		t.Fatalf("rejected claim wrote a maintenance row: rows=%d err=%v", jobRows, err)
	}
}

func TestAdminV1SettingsUseExplicitStorageGiBAndVersionRetentionFields(t *testing.T) {
	app, store, handler, adminCookie := newAdminV1TestHandler(t)
	csrf := adminV1CSRFToken(t, app, adminCookie)
	for name, body := range map[string]string{
		"raw storage bytes":    `{"storageBytes":2147483648}`,
		"storage below range":  `{"storageGiB":"0"}`,
		"versions above range": `{"versionsPerUser":"1001"}`,
	} {
		t.Run(name, func(t *testing.T) {
			response := adminV1Request(t, handler, http.MethodPut, "/api/admin/v1/system/settings", body, adminCookie, csrf)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("invalid storage setting = %d %s", response.Code, response.Body.String())
			}
		})
	}
	updated := adminV1Request(t, handler, http.MethodPut, "/api/admin/v1/system/settings", `{"storageGiB":"2","versionsPerUser":"75"}`, adminCookie, csrf)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"storageGiB":2`) || !strings.Contains(updated.Body.String(), `"versionsPerUser":75`) {
		t.Fatalf("explicit storage settings = %d %s", updated.Code, updated.Body.String())
	}
	settings, err := store.LoadRuntimeSettings(t.Context())
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if parseAdminV1Limit(settings.Limits["storageBytes"], 0) != 2<<30 || parseAdminV1Limit(settings.Limits["versionsPerUser"], 0) != 75 {
		t.Fatalf("stored limits = %#v", settings.Limits)
	}
	settings.Limits["profileBytes"] = float64(512 << 10)
	settings.Limits["backupDirectory"] = "/private/backups"
	rawLimits, err := json.Marshal(settings.Limits)
	if err != nil {
		t.Fatalf("marshal raw limits: %v", err)
	}
	if _, err := store.db.ExecContext(t.Context(), `UPDATE settings SET value=?,updated_at=? WHERE key='limits'`, string(rawLimits), nowString()); err != nil {
		t.Fatalf("seed raw limits: %v", err)
	}
	read := adminV1Request(t, handler, http.MethodGet, "/api/admin/v1/system/settings", "", adminCookie, "")
	if read.Code != http.StatusOK {
		t.Fatalf("read settings = %d %s", read.Code, read.Body.String())
	}
	for _, forbidden := range []string{`"profileBytes"`, `"storageBytes"`, `"backupDirectory"`} {
		if strings.Contains(read.Body.String(), forbidden) {
			t.Fatalf("settings DTO exposed internal limit %s: %s", forbidden, read.Body.String())
		}
	}
	for _, expected := range []string{`"profileKiB":512`, `"storageGiB":2`, `"versionsPerUser":75`} {
		if !strings.Contains(read.Body.String(), expected) {
			t.Fatalf("settings DTO missing %s: %s", expected, read.Body.String())
		}
	}
}

func TestAdminV1ReleaseLifecyclePublishesDisablesAndRecordsHistory(t *testing.T) {
	app, _, handler, adminCookie := newAdminV1TestHandler(t)
	csrf := adminV1CSRFToken(t, app, adminCookie)
	publicHandler := app.Routes()

	created := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/releases", `{"version":"0.2.0","channel":"stable","notes":"Lifecycle release","downloadUrl":"https://example.test/store","minimumVersion":"0.1.0","status":"draft"}`, adminCookie, csrf)
	if created.Code != http.StatusCreated {
		t.Fatalf("create release draft = %d %s", created.Code, created.Body.String())
	}
	var createdBody struct {
		Data struct {
			Item struct {
				ID string `json:"id"`
			} `json:"item"`
		} `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdBody); err != nil || createdBody.Data.Item.ID == "" {
		t.Fatalf("decode release draft: err=%v body=%s", err, created.Body.String())
	}
	releaseID := createdBody.Data.Item.ID

	draftBootstrap := getV1(t, publicHandler, "/api/v1/app/bootstrap", "")
	if draftBootstrap.Code != http.StatusOK || !strings.Contains(draftBootstrap.Body.String(), `"latestRelease":null`) || strings.Contains(draftBootstrap.Body.String(), "Lifecycle release") {
		t.Fatalf("draft leaked through bootstrap = %d %s", draftBootstrap.Code, draftBootstrap.Body.String())
	}
	draftLegacyBootstrap := getV1(t, publicHandler, "/api/app/bootstrap", "")
	if draftLegacyBootstrap.Code != http.StatusOK || strings.Contains(draftLegacyBootstrap.Body.String(), "Lifecycle release") {
		t.Fatalf("draft leaked through legacy bootstrap = %d %s", draftLegacyBootstrap.Code, draftLegacyBootstrap.Body.String())
	}

	published := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/releases/"+releaseID+"/publish", `{}`, adminCookie, csrf)
	if published.Code != http.StatusOK || !strings.Contains(published.Body.String(), `"status":"published"`) || !strings.Contains(published.Body.String(), `"publishedAt":`) {
		t.Fatalf("publish release = %d %s", published.Code, published.Body.String())
	}

	bootstrap := getV1(t, publicHandler, "/api/v1/app/bootstrap", "")
	for _, fragment := range []string{`"version":"0.2.0"`, `"channel":"stable"`, `"minimumVersion":"0.1.0"`, `"schemaVersion":2`, `"status":"published"`} {
		if bootstrap.Code != http.StatusOK || !strings.Contains(bootstrap.Body.String(), fragment) {
			t.Fatalf("published bootstrap missing %s = %d %s", fragment, bootstrap.Code, bootstrap.Body.String())
		}
	}

	republish := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/releases/"+releaseID+"/publish", `{}`, adminCookie, csrf)
	if republish.Code != http.StatusConflict || !strings.Contains(republish.Body.String(), `"code":"INVALID_RELEASE_TRANSITION"`) {
		t.Fatalf("illegal republish = %d %s", republish.Code, republish.Body.String())
	}

	disabled := adminV1Request(t, handler, http.MethodPost, "/api/admin/v1/releases/"+releaseID+"/disable", `{}`, adminCookie, csrf)
	if disabled.Code != http.StatusOK || !strings.Contains(disabled.Body.String(), `"status":"disabled"`) || !strings.Contains(disabled.Body.String(), `"disabledAt":`) {
		t.Fatalf("disable release = %d %s", disabled.Code, disabled.Body.String())
	}
	disabledBootstrap := getV1(t, publicHandler, "/api/v1/app/bootstrap", "")
	if disabledBootstrap.Code != http.StatusOK || !strings.Contains(disabledBootstrap.Body.String(), `"latestRelease":null`) || strings.Contains(disabledBootstrap.Body.String(), "Lifecycle release") {
		t.Fatalf("disabled release leaked through bootstrap = %d %s", disabledBootstrap.Code, disabledBootstrap.Body.String())
	}
	disabledLegacyBootstrap := getV1(t, publicHandler, "/api/app/bootstrap", "")
	if disabledLegacyBootstrap.Code != http.StatusOK || strings.Contains(disabledLegacyBootstrap.Body.String(), "Lifecycle release") {
		t.Fatalf("disabled release leaked through legacy bootstrap = %d %s", disabledLegacyBootstrap.Code, disabledLegacyBootstrap.Body.String())
	}

	history := adminV1Request(t, handler, http.MethodGet, "/api/admin/v1/releases/"+releaseID+"/history", "", adminCookie, "")
	for _, fragment := range []string{`"action":"create"`, `"action":"publish"`, `"fromStatus":"draft"`, `"toStatus":"published"`, `"action":"disable"`, `"fromStatus":"published"`, `"toStatus":"disabled"`} {
		if history.Code != http.StatusOK || !strings.Contains(history.Body.String(), fragment) {
			t.Fatalf("release history missing %s = %d %s", fragment, history.Code, history.Body.String())
		}
	}

	releases := adminV1Request(t, handler, http.MethodGet, "/api/admin/v1/releases", "", adminCookie, "")
	for _, fragment := range []string{`"status":"disabled"`, `"minimumVersion":"0.1.0"`, `"schemaVersion":2`, `"publishedAt":`, `"disabledAt":`} {
		if releases.Code != http.StatusOK || !strings.Contains(releases.Body.String(), fragment) {
			t.Fatalf("release list missing %s = %d %s", fragment, releases.Code, releases.Body.String())
		}
	}

	audit := adminV1Request(t, handler, http.MethodGet, "/api/admin/v1/audit/admin", "", adminCookie, "")
	if audit.Code != http.StatusOK || !strings.Contains(audit.Body.String(), `"action":"release.publish"`) || !strings.Contains(audit.Body.String(), `"action":"release.disable"`) {
		t.Fatalf("release audit = %d %s", audit.Code, audit.Body.String())
	}
}

func TestAdminV1ReleaseMigrationIgnoresDuplicateLegacyDraftWithoutOrphanHistory(t *testing.T) {
	app, store := newTestApp(t)
	if _, err := store.AddRelease(t.Context(), ReleaseRecord{Version: "0.2.0", Channel: "stable", Notes: "Already published"}); err != nil {
		t.Fatalf("seed canonical release: %v", err)
	}
	if _, err := store.db.ExecContext(t.Context(), `CREATE TABLE release_drafts (
		id TEXT PRIMARY KEY, version TEXT NOT NULL, channel TEXT NOT NULL,
		notes TEXT NOT NULL, download_url TEXT NOT NULL, minimum_version TEXT NOT NULL,
		status TEXT NOT NULL, created_at TEXT NOT NULL, UNIQUE(channel, version)
	)`); err != nil {
		t.Fatalf("create legacy release table: %v", err)
	}
	if _, err := store.db.ExecContext(t.Context(), `INSERT INTO release_drafts(id,version,channel,notes,download_url,minimum_version,status,created_at) VALUES('legacy-draft','0.2.0','stable','Duplicate','','0.1.0','draft',?)`, nowString()); err != nil {
		t.Fatalf("seed duplicate legacy draft: %v", err)
	}

	if err := app.ensureAdminV1Schema(t.Context()); err != nil {
		t.Fatalf("migrate duplicate legacy draft: %v", err)
	}
	var orphanEvents int
	if err := store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM release_events WHERE release_id='legacy-draft'`).Scan(&orphanEvents); err != nil || orphanEvents != 0 {
		t.Fatalf("orphan release history count=%d err=%v", orphanEvents, err)
	}
}

func TestReleaseLifecycleMigrationBackfillsHistoryForPublishedLegacyRows(t *testing.T) {
	store := newTestStore(t)
	release, err := store.AddRelease(t.Context(), ReleaseRecord{Version: "0.1.0", Channel: "stable", Notes: "Legacy release"})
	if err != nil {
		t.Fatalf("seed legacy release: %v", err)
	}
	tx, err := store.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin migration: %v", err)
	}
	defer tx.Rollback()
	if err := addReleaseLifecycle(t.Context(), tx); err != nil {
		t.Fatalf("run release migration: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit release migration: %v", err)
	}
	var action, toStatus string
	if err := store.db.QueryRowContext(t.Context(), `SELECT action,to_status FROM release_events WHERE release_id=?`, release.ID).Scan(&action, &toStatus); err != nil {
		t.Fatalf("read migrated release history: %v", err)
	}
	if action != "migrate" || toStatus != "published" {
		t.Fatalf("migrated release history = action %q status %q", action, toStatus)
	}
}

func TestBootstrapSelectsOnlyTheRequestedPublishedReleaseChannel(t *testing.T) {
	app, store := newTestApp(t)
	publicHandler := app.Routes()
	if _, err := store.AddRelease(t.Context(), ReleaseRecord{Version: "0.2.0", Channel: "stable", Notes: "Stable"}); err != nil {
		t.Fatalf("seed stable release: %v", err)
	}
	if _, err := store.AddRelease(t.Context(), ReleaseRecord{Version: "0.3.0-beta.1", Channel: "beta", Notes: "Beta"}); err != nil {
		t.Fatalf("seed beta release: %v", err)
	}

	stable := get(t, publicHandler, "/api/v1/app/bootstrap")
	if stable.Code != http.StatusOK || !strings.Contains(stable.Body.String(), `"version":"0.2.0"`) || strings.Contains(stable.Body.String(), "0.3.0-beta.1") {
		t.Fatalf("default bootstrap channel = %d %s", stable.Code, stable.Body.String())
	}
	beta := get(t, publicHandler, "/api/v1/app/bootstrap?channel=beta")
	if beta.Code != http.StatusOK || !strings.Contains(beta.Body.String(), `"version":"0.3.0-beta.1"`) || !strings.Contains(beta.Body.String(), `"channel":"beta"`) {
		t.Fatalf("beta bootstrap channel = %d %s", beta.Code, beta.Body.String())
	}
	legacyBeta := get(t, publicHandler, "/api/app/bootstrap?channel=beta")
	if legacyBeta.Code != http.StatusOK || !strings.Contains(legacyBeta.Body.String(), `"version":"0.3.0-beta.1"`) {
		t.Fatalf("legacy beta bootstrap channel = %d %s", legacyBeta.Code, legacyBeta.Body.String())
	}
	invalid := get(t, publicHandler, "/api/v1/app/bootstrap?channel=nightly")
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), `"code":"INVALID_RELEASE_CHANNEL"`) {
		t.Fatalf("invalid bootstrap channel = %d %s", invalid.Code, invalid.Body.String())
	}
}
