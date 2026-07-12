package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func getV1(t *testing.T, handler http.Handler, path, accessToken string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.RemoteAddr = "198.51.100.20:1234"
	if accessToken != "" {
		request.Header.Set("Authorization", "Bearer "+accessToken)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestCatalogV1UsesAccessTokensEnvelopeAndClientDTOs(t *testing.T) {
	handler, store, mailer := newV1AuthApp(t)
	access := verifiedAccessToken(t, handler, mailer, "catalog@example.com", "catalog-device")
	variant := WallpaperVariantRecord{ID: "4k", Label: "3840x2160", URL: "https://cdn.example.test/wallpaper.jpg"}
	if err := store.UpsertWebWallpaper(t.Context(), WebWallpaperRecord{
		ID: "web:one", Provider: "uhdpaper", SourcePageURL: "https://www.uhdpaper.com/one.html",
		Title: "Web One", Category: "nature", Variants: []WallpaperVariantRecord{variant}, Enabled: true,
	}); err != nil {
		t.Fatalf("seed web wallpaper: %v", err)
	}
	if err := store.UpsertOfficialWallpaper(t.Context(), OfficialWallpaperRecord{
		ID: "official:one", Title: "Official One", Category: "nature",
		Variants: []WallpaperVariantRecord{variant}, Enabled: true,
	}); err != nil {
		t.Fatalf("seed official wallpaper: %v", err)
	}
	css := `.newtab-root[data-style-id="style:glass"] .app-shell{color:#fff}`
	if err := store.UpsertStylePackage(t.Context(), StylePackageRecord{
		ID: "style:glass", Name: "Glass", Version: "1.0.0", CSS: css,
		ConfigJSON: []byte(`{"theme":"glass"}`), Enabled: true,
	}); err != nil {
		t.Fatalf("seed style: %v", err)
	}
	if err := store.UpsertStylePackage(t.Context(), StylePackageRecord{
		ID: "style:draft", Name: "Draft", Version: "1.0.0", CSS: css,
		ConfigJSON: []byte(`{}`), Enabled: false,
	}); err != nil {
		t.Fatalf("seed disabled style: %v", err)
	}

	for _, path := range []string{
		"/api/v1/catalog/wallpapers/official",
		"/api/v1/catalog/wallpapers/web?page=1&pageSize=20",
		"/api/v1/catalog/styles",
	} {
		anonymous := getV1(t, handler, path, "")
		if anonymous.Code != http.StatusUnauthorized || !strings.Contains(anonymous.Body.String(), `"code":"UNAUTHORIZED"`) {
			t.Fatalf("anonymous %s = %d %s, want v1 401 envelope", path, anonymous.Code, anonymous.Body.String())
		}
	}

	web := getV1(t, handler, "/api/v1/catalog/wallpapers/web?page=1&pageSize=20", access)
	if web.Code != http.StatusOK || !strings.Contains(web.Body.String(), `"data":{"items"`) || !strings.Contains(web.Body.String(), variant.URL) {
		t.Fatalf("v1 web catalog = %d %s", web.Code, web.Body.String())
	}
	official := getV1(t, handler, "/api/v1/catalog/wallpapers/official", access)
	if official.Code != http.StatusOK || !strings.Contains(official.Body.String(), `"provider":"official"`) || !strings.Contains(official.Body.String(), variant.URL) {
		t.Fatalf("v1 official catalog = %d %s", official.Code, official.Body.String())
	}
	styles := getV1(t, handler, "/api/v1/catalog/styles", access)
	wantHashBytes := sha256.Sum256([]byte(css))
	wantHash := hex.EncodeToString(wantHashBytes[:])
	for _, fragment := range []string{
		`"id":"style:glass"`, `"status":"published"`, `"sha256":"` + wantHash + `"`,
		`"styleSchemaVersion":1`, `"minExtensionVersion":`,
	} {
		if !strings.Contains(styles.Body.String(), fragment) {
			t.Fatalf("v1 style catalog missing %s: %d %s", fragment, styles.Code, styles.Body.String())
		}
	}
	if strings.Contains(styles.Body.String(), "style:draft") {
		t.Fatalf("disabled/draft style leaked to client: %s", styles.Body.String())
	}
}

func TestBootstrapV1IsPublicEnvelopeAndOnlyExposesPublishedReleaseDTO(t *testing.T) {
	handler, store, _ := newV1AuthApp(t)
	if _, err := store.AddRelease(t.Context(), ReleaseRecord{
		Version: "0.2.0", Channel: "stable", Notes: "Ready", DownloadURL: "https://example.test/fullpro",
	}); err != nil {
		t.Fatalf("seed release: %v", err)
	}
	response := getV1(t, handler, "/api/v1/app/bootstrap", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"data":`) || !strings.Contains(response.Body.String(), `"version":"0.2.0"`) || !strings.Contains(response.Body.String(), `"status":"published"`) {
		t.Fatalf("v1 bootstrap = %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "password") || strings.Contains(response.Body.String(), "token") {
		t.Fatalf("bootstrap leaked secret-shaped fields: %s", response.Body.String())
	}
}
