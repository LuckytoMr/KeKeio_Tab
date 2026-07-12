package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type syncVersionsV1TestEnvelope struct {
	Data struct {
		Items []struct {
			ID            string `json:"id"`
			Version       int    `json:"version"`
			SchemaVersion int    `json:"schemaVersion"`
			ProfileHash   string `json:"profileHash"`
			MutationID    string `json:"mutationId"`
			CreatedAt     string `json:"createdAt"`
		} `json:"items"`
		NextCursor string `json:"nextCursor"`
		Limit      int    `json:"limit"`
		Version    int    `json:"version"`
	} `json:"data"`
	Error struct {
		Code    string `json:"code"`
		Details struct {
			ConflictID string `json:"conflictId"`
		} `json:"details"`
	} `json:"error"`
}

func newSyncVersionsV1TestHandlers(t *testing.T) (http.Handler, http.Handler, *Store, *captureMailer) {
	t.Helper()
	base, store, mailer := newV1AuthApp(t)
	app := NewApp(store, Config{})
	mux := http.NewServeMux()
	app.registerSyncVersionV1Routes(mux)
	return base, mux, store, mailer
}

func syncVersionsV1Request(t *testing.T, handler http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = "198.51.100.30:1234"
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeSyncVersionsV1TestEnvelope(t *testing.T, response *httptest.ResponseRecorder) syncVersionsV1TestEnvelope {
	t.Helper()
	var envelope syncVersionsV1TestEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, response.Body.String())
	}
	return envelope
}

func mustPutSyncVersionV1(t *testing.T, handler http.Handler, token, mutationID string, baseVersion int, profile string) {
	t.Helper()
	body := fmt.Sprintf(`{"baseVersion":%d,"mutationId":%q,"deviceId":"versions-device","schemaVersion":2,"profile":%s}`, baseVersion, mutationID, profile)
	response := putSync(t, handler, token, mutationID, body)
	if response.Code != http.StatusOK {
		t.Fatalf("seed sync version: %d %s", response.Code, response.Body.String())
	}
}

func TestSyncVersionsV1ListRequiresBearerScopesMetadataAndCapsLimit(t *testing.T) {
	base, versions, _, mailer := newSyncVersionsV1TestHandlers(t)
	userAToken := verifiedAccessToken(t, base, mailer, "versions-a@example.com", "versions-a")
	userBToken := verifiedAccessToken(t, base, mailer, "versions-b@example.com", "versions-b")
	profileB := strings.Replace(sharedProfileFixture, `"title":"A"`, `"title":"B"`, 1)
	mustPutSyncVersionV1(t, base, userAToken, "versions_a_1", 0, sharedProfileFixture)
	mustPutSyncVersionV1(t, base, userAToken, "versions_a_2", 1, profileB)
	mustPutSyncVersionV1(t, base, userBToken, "versions_b_1", 0, sharedProfileFixture)

	missing := syncVersionsV1Request(t, versions, http.MethodGet, "/api/v1/sync/profile/versions", "", "")
	if missing.Code != http.StatusUnauthorized || decodeSyncVersionsV1TestEnvelope(t, missing).Error.Code != "UNAUTHORIZED" {
		t.Fatalf("missing bearer = %d %s", missing.Code, missing.Body.String())
	}

	listed := syncVersionsV1Request(t, versions, http.MethodGet, "/api/v1/sync/profile/versions?limit=1000", userAToken, "")
	if listed.Code != http.StatusOK {
		t.Fatalf("list versions = %d %s", listed.Code, listed.Body.String())
	}
	if strings.Contains(listed.Body.String(), `"profile":`) || strings.Contains(listed.Body.String(), `"userId":`) {
		t.Fatalf("version list leaked profile or user metadata: %s", listed.Body.String())
	}
	envelope := decodeSyncVersionsV1TestEnvelope(t, listed)
	if envelope.Data.Limit != 100 || len(envelope.Data.Items) != 2 {
		t.Fatalf("list pagination = %#v", envelope.Data)
	}
	for _, item := range envelope.Data.Items {
		if item.ID == "" || item.Version < 1 || item.SchemaVersion != 2 || item.ProfileHash == "" || item.MutationID == "" || item.CreatedAt == "" {
			t.Fatalf("incomplete version metadata: %#v", item)
		}
	}

	firstPage := syncVersionsV1Request(t, versions, http.MethodGet, "/api/v1/sync/profile/versions?limit=1", userAToken, "")
	firstEnvelope := decodeSyncVersionsV1TestEnvelope(t, firstPage)
	if firstPage.Code != http.StatusOK || len(firstEnvelope.Data.Items) != 1 || firstEnvelope.Data.Items[0].Version != 2 || firstEnvelope.Data.NextCursor == "" {
		t.Fatalf("first cursor page = %d %s", firstPage.Code, firstPage.Body.String())
	}
	secondPage := syncVersionsV1Request(t, versions, http.MethodGet, "/api/v1/sync/profile/versions?limit=1&cursor="+url.QueryEscape(firstEnvelope.Data.NextCursor), userAToken, "")
	secondEnvelope := decodeSyncVersionsV1TestEnvelope(t, secondPage)
	if secondPage.Code != http.StatusOK || len(secondEnvelope.Data.Items) != 1 || secondEnvelope.Data.Items[0].Version != 1 {
		t.Fatalf("second cursor page = %d %s", secondPage.Code, secondPage.Body.String())
	}

	otherUser := syncVersionsV1Request(t, versions, http.MethodGet, "/api/v1/sync/profile/versions", userBToken, "")
	otherEnvelope := decodeSyncVersionsV1TestEnvelope(t, otherUser)
	if otherUser.Code != http.StatusOK || len(otherEnvelope.Data.Items) != 1 || otherEnvelope.Data.Items[0].MutationID != "versions_b_1" {
		t.Fatalf("cross-user version list = %d %s", otherUser.Code, otherUser.Body.String())
	}
}

func TestSyncVersionsV1RestoreUsesCASResolutionAndIdempotentMutation(t *testing.T) {
	base, versions, store, mailer := newSyncVersionsV1TestHandlers(t)
	ownerToken := verifiedAccessToken(t, base, mailer, "restore-owner@example.com", "restore-owner")
	otherToken := verifiedAccessToken(t, base, mailer, "restore-other@example.com", "restore-other")
	profileB := strings.Replace(sharedProfileFixture, `"title":"A"`, `"title":"B"`, 1)
	mustPutSyncVersionV1(t, base, ownerToken, "restore_seed_1", 0, sharedProfileFixture)
	mustPutSyncVersionV1(t, base, ownerToken, "restore_seed_2", 1, profileB)

	listed := syncVersionsV1Request(t, versions, http.MethodGet, "/api/v1/sync/profile/versions", ownerToken, "")
	listEnvelope := decodeSyncVersionsV1TestEnvelope(t, listed)
	var versionOneID string
	for _, item := range listEnvelope.Data.Items {
		if item.Version == 1 {
			versionOneID = item.ID
		}
	}
	if versionOneID == "" {
		t.Fatalf("version one id missing: %s", listed.Body.String())
	}
	restorePath := "/api/v1/sync/profile/versions/" + versionOneID + "/restore"

	missing := syncVersionsV1Request(t, versions, http.MethodPost, restorePath, "", `{"baseVersion":2,"mutationId":"restore_missing","deviceId":"restore-device"}`)
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("restore without token = %d %s", missing.Code, missing.Body.String())
	}
	foreign := syncVersionsV1Request(t, versions, http.MethodPost, restorePath, otherToken, `{"baseVersion":0,"mutationId":"restore_foreign","deviceId":"restore-device"}`)
	if foreign.Code != http.StatusNotFound || decodeSyncVersionsV1TestEnvelope(t, foreign).Error.Code != "PROFILE_VERSION_NOT_FOUND" {
		t.Fatalf("cross-user restore = %d %s", foreign.Code, foreign.Body.String())
	}
	unknownField := syncVersionsV1Request(t, versions, http.MethodPost, restorePath, ownerToken, `{"baseVersion":2,"mutationId":"restore_bad","deviceId":"restore-device","profile":{}}`)
	if unknownField.Code != http.StatusBadRequest || decodeSyncVersionsV1TestEnvelope(t, unknownField).Error.Code != "INVALID_REQUEST" {
		t.Fatalf("unknown restore field = %d %s", unknownField.Code, unknownField.Body.String())
	}
	missingDevice := syncVersionsV1Request(t, versions, http.MethodPost, restorePath, ownerToken, `{"baseVersion":2,"mutationId":"restore_bad_2"}`)
	if missingDevice.Code != http.StatusBadRequest {
		t.Fatalf("missing restore field = %d %s", missingDevice.Code, missingDevice.Body.String())
	}

	conflict := syncVersionsV1Request(t, versions, http.MethodPost, restorePath, ownerToken, `{"baseVersion":1,"mutationId":"restore_conflict","deviceId":"restore-device"}`)
	conflictEnvelope := decodeSyncVersionsV1TestEnvelope(t, conflict)
	if conflict.Code != http.StatusConflict || conflictEnvelope.Error.Code != "PROFILE_CONFLICT" || conflictEnvelope.Error.Details.ConflictID == "" {
		t.Fatalf("restore CAS conflict = %d %s", conflict.Code, conflict.Body.String())
	}

	successBody := fmt.Sprintf(`{"baseVersion":2,"mutationId":"restore_success","deviceId":"restore-device","resolvesConflictId":%q}`, conflictEnvelope.Error.Details.ConflictID)
	success := syncVersionsV1Request(t, versions, http.MethodPost, restorePath, ownerToken, successBody)
	if success.Code != http.StatusOK || !strings.Contains(success.Body.String(), `"version":3`) || !strings.Contains(success.Body.String(), `"title":"A"`) || !strings.Contains(success.Body.String(), `"idempotentReplay":false`) {
		t.Fatalf("restore success = %d %s", success.Code, success.Body.String())
	}
	replay := syncVersionsV1Request(t, versions, http.MethodPost, restorePath, ownerToken, successBody)
	if replay.Code != http.StatusOK || !strings.Contains(replay.Body.String(), `"version":3`) || !strings.Contains(replay.Body.String(), `"idempotentReplay":true`) {
		t.Fatalf("restore replay = %d %s", replay.Code, replay.Body.String())
	}

	var versionCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sync_profile_versions`).Scan(&versionCount); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	if versionCount != 3 {
		t.Fatalf("restore replay created extra version: %d", versionCount)
	}
	var conflictStatus string
	if err := store.db.QueryRow(`SELECT status FROM sync_conflicts WHERE id=?`, conflictEnvelope.Error.Details.ConflictID).Scan(&conflictStatus); err != nil || conflictStatus != "resolved" {
		t.Fatalf("restore did not resolve conflict: status=%q err=%v", conflictStatus, err)
	}
	current := getV1Bearer(t, base, "/api/v1/sync/profile", ownerToken)
	if current.Code != http.StatusOK || !strings.Contains(current.Body.String(), `"version":3`) || !strings.Contains(current.Body.String(), `"title":"A"`) {
		t.Fatalf("restored profile is not current: %d %s", current.Code, current.Body.String())
	}
}
