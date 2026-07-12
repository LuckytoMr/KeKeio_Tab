package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sharedProfileFixture = `{
	"schemaVersion":2,
	"profileId":"profile-a",
	"updatedAt":"2026-01-01T00:00:00Z",
	"groups":[{"id":"g1","title":"Main","sortIndex":0,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}],
	"shortcuts":[{"id":"s1","groupId":"g1","title":"A","url":"https://example.com","icon":{"kind":"text","text":"A"},"sortIndex":0,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}],
	"search":{"mode":"browser-default","disposition":"NEW_TAB","selectedEngineId":"google","engines":[{"id":"google","title":"Google","template":"https://www.google.com/search?q={query}"}]},
	"wallpaper":{"selected":{"kind":"builtin","id":"mist"},"selectedIds":["mist"],"rotationMode":"manual","rotationSource":"selected","rotationIntervalSeconds":60,"overlayOpacity":0.2,"blur":0},
	"theme":{"styleId":"quark-flow","density":"comfortable","sidebarSide":"left","showBrand":true,"columns":6,"rows":3,"iconSize":"medium","iconShape":"rounded"}
}`

func verifiedAccessToken(t *testing.T, handler http.Handler, mailer *captureMailer, email, deviceID string) string {
	t.Helper()
	register := postV1(t, handler, "/api/v1/auth/register", `{"email":"`+email+`","password":"safe-password-123"}`)
	if register.Code != http.StatusCreated {
		t.Fatalf("register = %d %s", register.Code, register.Body.String())
	}
	token := mailer.token("verify_email")
	if verify := postV1(t, handler, "/api/v1/auth/verify-email", `{"token":"`+token+`"}`); verify.Code != http.StatusOK {
		t.Fatalf("verify = %d %s", verify.Code, verify.Body.String())
	}
	login := postV1(t, handler, "/api/v1/auth/login", `{"email":"`+email+`","password":"safe-password-123","deviceId":"`+deviceID+`"}`)
	if login.Code != http.StatusOK {
		t.Fatalf("login = %d %s", login.Code, login.Body.String())
	}
	access, _ := decodeV1Data(t, login)["accessToken"].(string)
	return access
}

func putSync(t *testing.T, handler http.Handler, accessToken, mutationID, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/sync/profile", strings.NewReader(body))
	request.RemoteAddr = "198.51.100.10:1234"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Idempotency-Key", mutationID)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestSyncV1CASAtomicIdempotencyAndConflictResolution(t *testing.T) {
	handler, store, mailer := newV1AuthApp(t)
	access := verifiedAccessToken(t, handler, mailer, "sync@example.com", "dev_a")
	profileA := sharedProfileFixture
	bodyA := `{"baseVersion":0,"mutationId":"mut_a","deviceId":"dev_a","schemaVersion":2,"profile":` + profileA + `}`
	first := putSync(t, handler, access, "mut_a", bodyA)
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), `"version":1`) || !strings.Contains(first.Body.String(), `"profileHash":"`) || !strings.Contains(first.Body.String(), `"mutationId":"mut_a"`) {
		t.Fatalf("first sync = %d %s", first.Code, first.Body.String())
	}

	replay := putSync(t, handler, access, "mut_a", bodyA)
	if replay.Code != http.StatusOK || !strings.Contains(replay.Body.String(), `"idempotentReplay":true`) {
		t.Fatalf("idempotent replay = %d %s", replay.Code, replay.Body.String())
	}
	var versions int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sync_profile_versions`).Scan(&versions); err != nil || versions != 1 {
		t.Fatalf("profile versions after replay = %d err=%v", versions, err)
	}

	mismatchBody := strings.Replace(bodyA, `"title":"A"`, `"title":"different"`, 1)
	mismatch := putSync(t, handler, access, "mut_a", mismatchBody)
	if mismatch.Code != http.StatusConflict || !strings.Contains(mismatch.Body.String(), "IDEMPOTENCY_MISMATCH") {
		t.Fatalf("idempotency mismatch = %d %s", mismatch.Code, mismatch.Body.String())
	}

	profileB := strings.Replace(profileA, `"title":"A"`, `"title":"B"`, 1)
	conflictBody := `{"baseVersion":0,"mutationId":"mut_b","deviceId":"dev_b","schemaVersion":2,"profile":` + profileB + `}`
	conflict := putSync(t, handler, access, "mut_b", conflictBody)
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "PROFILE_CONFLICT") || !strings.Contains(conflict.Body.String(), `"baseVersion":0`) || !strings.Contains(conflict.Body.String(), `"currentVersion":1`) || !strings.Contains(conflict.Body.String(), `"currentHash":"`) {
		t.Fatalf("profile conflict = %d %s", conflict.Code, conflict.Body.String())
	}
	var conflictEnvelope struct {
		Error struct {
			Details struct {
				ConflictID string `json:"conflictId"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(conflict.Body.Bytes(), &conflictEnvelope); err != nil || conflictEnvelope.Error.Details.ConflictID == "" {
		t.Fatalf("conflict id missing: err=%v body=%s", err, conflict.Body.String())
	}

	resolvedBody := `{"baseVersion":1,"mutationId":"mut_resolve","deviceId":"dev_b","schemaVersion":2,"resolvesConflictId":"` + conflictEnvelope.Error.Details.ConflictID + `","profile":` + profileB + `}`
	resolved := putSync(t, handler, access, "mut_resolve", resolvedBody)
	if resolved.Code != http.StatusOK || !strings.Contains(resolved.Body.String(), `"version":2`) {
		t.Fatalf("resolved sync = %d %s", resolved.Code, resolved.Body.String())
	}
	var conflictStatus string
	if err := store.db.QueryRow(`SELECT status FROM sync_conflicts WHERE id = ?`, conflictEnvelope.Error.Details.ConflictID).Scan(&conflictStatus); err != nil || conflictStatus != "resolved" {
		t.Fatalf("conflict status=%q err=%v", conflictStatus, err)
	}

	current := getV1Bearer(t, handler, "/api/v1/sync/profile", access)
	if current.Code != http.StatusOK || !strings.Contains(current.Body.String(), `"title":"B"`) || !strings.Contains(current.Body.String(), `"version":2`) {
		t.Fatalf("current profile = %d %s", current.Code, current.Body.String())
	}
}

func TestSyncV1RejectsLocalOnlyFieldsAndLegacyWrites(t *testing.T) {
	handler, store, mailer := newV1AuthApp(t)
	access := verifiedAccessToken(t, handler, mailer, "boundary@example.com", "dev_boundary")
	empty := getV1Bearer(t, handler, "/api/v1/sync/profile", access)
	if empty.Code != http.StatusOK || !strings.Contains(empty.Body.String(), `"profile":null`) || !strings.Contains(empty.Body.String(), `"version":0`) || !strings.Contains(empty.Body.String(), `"profileHash":null`) || !strings.Contains(empty.Body.String(), `"schemaVersion":2`) {
		t.Fatalf("empty profile contract = %d %s", empty.Code, empty.Body.String())
	}
	var emptyEnvelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(empty.Body.Bytes(), &emptyEnvelope); err != nil {
		t.Fatalf("decode empty sync profile: %v", err)
	}
	if len(emptyEnvelope.Data) != 4 {
		t.Fatalf("empty sync profile must be strict four-field record, got %#v", emptyEnvelope.Data)
	}
	invalid := `{"baseVersion":0,"mutationId":"mut_local","deviceId":"dev_boundary","schemaVersion":2,"profile":{"schemaVersion":2,"groups":[],"shortcuts":[],"rotationHistory":[],"deviceId":"must-not-upload"}}`
	response := putSync(t, handler, access, "mut_local", invalid)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "PROFILE_INVALID") {
		t.Fatalf("local-only profile = %d %s", response.Code, response.Body.String())
	}
	var profiles int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sync_profiles`).Scan(&profiles); err != nil || profiles != 0 {
		t.Fatalf("invalid profile was persisted: count=%d err=%v", profiles, err)
	}

	legacy := httptest.NewRequest(http.MethodPut, "/api/profile", strings.NewReader(`{"profile":{"schemaVersion":1}}`))
	legacy.RemoteAddr = "198.51.100.10:1234"
	legacy.Header.Set("Content-Type", "application/json")
	legacy.Header.Set("Authorization", "Bearer "+access)
	legacyResponse := httptest.NewRecorder()
	handler.ServeHTTP(legacyResponse, legacy)
	if legacyResponse.Code != http.StatusUpgradeRequired || !strings.Contains(legacyResponse.Body.String(), "UPGRADE_REQUIRED") {
		t.Fatalf("legacy profile write = %d %s", legacyResponse.Code, legacyResponse.Body.String())
	}

	trailing := putSync(t, handler, access, "mut_trailing", `{"baseVersion":0,"mutationId":"mut_trailing","deviceId":"dev_boundary","schemaVersion":2,"profile":{"schemaVersion":2}} {}`)
	if trailing.Code != http.StatusBadRequest {
		t.Fatalf("trailing JSON request accepted: %d %s", trailing.Code, trailing.Body.String())
	}
}

func TestSyncV1RejectsUnsupportedSchemaWithStableErrorCode(t *testing.T) {
	handler, _, mailer := newV1AuthApp(t)
	access := verifiedAccessToken(t, handler, mailer, "schema@example.test", "dev_schema")
	request := httptest.NewRequest(http.MethodPut, "/api/v1/sync/profile", strings.NewReader(`{"baseVersion":0,"mutationId":"mut_schema","deviceId":"dev_schema","schemaVersion":3,"profile":{}}`))
	request.Header.Set("Authorization", "Bearer "+access)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "mut_schema")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), `"code":"SCHEMA_UNSUPPORTED"`) {
		t.Fatalf("unsupported schema = %d %s", response.Code, response.Body.String())
	}
}

func TestSharedProfileV2ValidatorRejectsIncompleteAndUnknownSchema(t *testing.T) {
	raw := json.RawMessage(`{"schemaVersion":2,"groups":[],"shortcuts":[],"unexpected":true}`)
	if err := ValidateSharedProfileV2(raw, 512<<10); err == nil {
		t.Fatal("validator accepted an incomplete profile with an unknown field")
	}
}

func TestSharedProfileV2ValidatorRequiresEveryWireField(t *testing.T) {
	if err := ValidateSharedProfileV2(json.RawMessage(sharedProfileFixture), 512<<10); err != nil {
		t.Fatalf("real extension fixture rejected: %v", err)
	}
	missingShowBrand := strings.Replace(sharedProfileFixture, `,"showBrand":true`, "", 1)
	if err := ValidateSharedProfileV2(json.RawMessage(missingShowBrand), 512<<10); err == nil {
		t.Fatal("validator accepted profile missing required theme.showBrand")
	}
}

func TestSharedProfileV2ValidatorRejectsInsecureRemoteIconURL(t *testing.T) {
	insecure := strings.Replace(sharedProfileFixture, `"icon":{"kind":"text","text":"A"}`, `"icon":{"kind":"url","url":"http://icons.example.test/a.png","fallbackText":"A"}`, 1)
	if err := ValidateSharedProfileV2(json.RawMessage(insecure), 512<<10); err == nil {
		t.Fatal("validator accepted an insecure HTTP icon URL")
	}
}

func TestCanonicalProfileHashMatchesExtensionCanonicalJSON(t *testing.T) {
	hash, err := canonicalProfileHash(json.RawMessage(`{"b":"R&D <x>","a":"line\u2028separator"}`))
	if err != nil {
		t.Fatalf("canonical profile hash: %v", err)
	}
	const extensionHash = "4151168a3679329aeab2bacad5b1b43a2c48c3b9a4f8bb99cdb88025a0090327"
	if hash != extensionHash {
		t.Fatalf("canonical profile hash = %s, want extension JSON.stringify hash %s", hash, extensionHash)
	}
}
