package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestFirstV2SyncArchivesLegacyProfileAndOldReadProjectsCurrentV2(t *testing.T) {
	handler, store, mailer := newV1AuthApp(t)
	access := verifiedAccessToken(t, handler, mailer, "legacy-profile@example.com", "legacy-profile-device")
	user, err := store.UserByAccessToken(t.Context(), access)
	if err != nil {
		t.Fatalf("resolve access user: %v", err)
	}
	legacy := json.RawMessage(`{"schemaVersion":1,"profileId":"legacy","shortcuts":[{"title":"Legacy"}]}`)
	if _, err := store.SaveProfile(t.Context(), user.ID, legacy); err != nil {
		t.Fatalf("seed legacy profile: %v", err)
	}
	body := `{"baseVersion":0,"mutationId":"legacy_to_v2","deviceId":"legacy-profile-device","schemaVersion":2,"profile":` + sharedProfileFixture + `}`
	response := putSync(t, handler, access, "legacy_to_v2", body)
	if response.Code != http.StatusOK {
		t.Fatalf("first v2 sync = %d %s", response.Code, response.Body.String())
	}
	var activeLegacy, backups int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM profiles WHERE user_id=?`, user.ID).Scan(&activeLegacy); err != nil {
		t.Fatalf("count active legacy profiles: %v", err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM legacy_profile_backups WHERE user_id=? AND profile_json=?`, user.ID, string(legacy)).Scan(&backups); err != nil {
		t.Fatalf("count legacy backups: %v", err)
	}
	if activeLegacy != 0 || backups != 1 {
		t.Fatalf("legacy migration active=%d backups=%d", activeLegacy, backups)
	}
	projected, err := store.GetProfile(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("read compatibility projection: %v", err)
	}
	if projected.Version != 1 || !strings.Contains(string(projected.ProfileJSON), `"schemaVersion":2`) {
		t.Fatalf("legacy read projection = %#v", projected)
	}
}

func TestVersionThreeMigrationArchivesAlreadySupersededLegacyProfile(t *testing.T) {
	path := t.TempDir() + "/legacy-upgrade.db"
	store, err := OpenStore(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	user, err := store.CreateUser(t.Context(), "already-synced@example.com", "safe-password-123")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	legacy := json.RawMessage(`{"schemaVersion":1,"profileId":"legacy"}`)
	if _, err := store.SaveProfile(t.Context(), user.ID, legacy); err != nil {
		t.Fatalf("seed legacy profile: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO sync_profiles(user_id,profile_json,version,schema_version,profile_hash,updated_at) VALUES(?,?,?,?,?,?)`,
		user.ID, sharedProfileFixture, 3, 2, "existing-v2-hash", nowString()); err != nil {
		t.Fatalf("seed v2 profile: %v", err)
	}
	if _, err := store.db.Exec(`DELETE FROM schema_migrations WHERE version>=3`); err != nil {
		t.Fatalf("simulate pre-v3 database: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close pre-v3 database: %v", err)
	}
	reopened, err := OpenStore(path)
	if err != nil {
		t.Fatalf("apply v3 migration: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	var activeLegacy, backups int
	if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM profiles WHERE user_id=?`, user.ID).Scan(&activeLegacy); err != nil {
		t.Fatalf("count legacy profile after migration: %v", err)
	}
	if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM legacy_profile_backups WHERE user_id=?`, user.ID).Scan(&backups); err != nil {
		t.Fatalf("count backups after migration: %v", err)
	}
	if activeLegacy != 0 || backups != 1 {
		t.Fatalf("pre-existing split brain remained after migration: active=%d backups=%d", activeLegacy, backups)
	}
	projected, err := reopened.GetProfile(t.Context(), user.ID)
	if err != nil || projected.Version != 3 {
		t.Fatalf("compatibility projection after migration = %#v err=%v", projected, err)
	}
}
