package server

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPluginRegistrationEnforcesPersistedMaxUsers(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.BeginInstallation(t.Context(), InstallationInput{
		Mode: "fresh_install", Email: "owner@example.com", DisplayName: "Owner",
		Password: "correct horse battery staple", ExternalBaseURL: "https://fullpro.example",
		Limits: map[string]any{"maxUsers": 1},
	}); err != nil {
		t.Fatalf("begin installation: %v", err)
	}
	if err := store.FinishInstallation(t.Context()); err != nil {
		t.Fatalf("finish installation: %v", err)
	}
	if _, _, err := store.CreatePendingPluginUser(t.Context(), "first@example.com", "safe-password-123"); err != nil {
		t.Fatalf("create first plugin user: %v", err)
	}
	if _, _, err := store.CreatePendingPluginUser(t.Context(), "second@example.com", "safe-password-123"); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("second plugin user error = %v, want ErrQuotaExceeded", err)
	}
	var users int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role='user'`).Scan(&users); err != nil || users != 1 {
		t.Fatalf("plugin user count = %d err=%v", users, err)
	}
}

func TestExpiredPendingRegistrationDoesNotPermanentlyConsumeUserQuota(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.BeginInstallation(t.Context(), InstallationInput{
		Mode: "fresh_install", Email: "owner@example.com", DisplayName: "Owner",
		Password: "correct horse battery staple", ExternalBaseURL: "https://fullpro.example",
		Limits: map[string]any{"maxUsers": 1},
	}); err != nil {
		t.Fatalf("begin installation: %v", err)
	}
	if err := store.FinishInstallation(t.Context()); err != nil {
		t.Fatalf("finish installation: %v", err)
	}
	stale, _, err := store.CreatePendingPluginUser(t.Context(), "stale@example.com", "safe-password-123")
	if err != nil {
		t.Fatalf("create stale pending user: %v", err)
	}
	staleAt := time.Now().UTC().Add(-25 * time.Hour).Format(time.RFC3339Nano)
	if _, err := store.db.Exec(`UPDATE users SET created_at=?, updated_at=? WHERE id=?`, staleAt, staleAt, stale.ID); err != nil {
		t.Fatalf("age pending registration: %v", err)
	}
	staleExpiry := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := store.db.Exec(`UPDATE email_verification_tokens SET created_at=?, expires_at=? WHERE user_id=?`, staleAt, staleExpiry, stale.ID); err != nil {
		t.Fatalf("expire stale verification token: %v", err)
	}

	created, _, err := store.CreatePendingPluginUser(t.Context(), "fresh@example.com", "safe-password-456")
	if err != nil {
		t.Fatalf("expired pending registration still consumed quota: %v", err)
	}
	if created.Email != "fresh@example.com" {
		t.Fatalf("created email = %q", created.Email)
	}
	var staleUsers, staleTokens int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM users WHERE id=?`, stale.ID).Scan(&staleUsers); err != nil {
		t.Fatalf("count stale users: %v", err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM email_verification_tokens WHERE user_id=?`, stale.ID).Scan(&staleTokens); err != nil {
		t.Fatalf("count stale verification tokens: %v", err)
	}
	if staleUsers != 0 || staleTokens != 0 {
		t.Fatalf("expired pending registration not cleaned up: users=%d tokens=%d", staleUsers, staleTokens)
	}
}

func TestSyncProfileEnforcesPersistedProfileByteLimit(t *testing.T) {
	handler, store, mailer := newV1AuthApp(t)
	access := verifiedAccessToken(t, handler, mailer, "quota@example.com", "quota-device")
	if _, err := store.db.Exec(`UPDATE settings SET value='{"profileBytes":128}', updated_at=? WHERE key='limits'`, nowString()); err != nil {
		t.Fatalf("set profile quota: %v", err)
	}
	body := `{"baseVersion":0,"mutationId":"quota_mutation","deviceId":"quota-device","schemaVersion":2,"profile":` + sharedProfileFixture + `}`
	response := putSync(t, handler, access, "quota_mutation", body)
	if response.Code != http.StatusRequestEntityTooLarge || !strings.Contains(response.Body.String(), `"code":"PROFILE_QUOTA_EXCEEDED"`) {
		t.Fatalf("oversized profile = %d %s", response.Code, response.Body.String())
	}
	var profiles, mutations int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sync_profiles`).Scan(&profiles); err != nil {
		t.Fatalf("count profiles: %v", err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sync_mutations`).Scan(&mutations); err != nil {
		t.Fatalf("count mutations: %v", err)
	}
	if profiles != 0 || mutations != 0 {
		t.Fatalf("quota rejection persisted profile/mutation: profiles=%d mutations=%d", profiles, mutations)
	}
}
