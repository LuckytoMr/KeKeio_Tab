package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestAdminV1LegacyCatalogDetailCreatesInitialPublishedRevision(t *testing.T) {
	app, store, handler, adminCookie := newAdminV1TestHandler(t)
	_ = app
	if err := store.UpsertOfficialWallpaper(t.Context(), OfficialWallpaperRecord{
		ID: "legacy:aurora", Title: "Legacy Aurora", Category: "nature", Tags: []string{"night"},
		PreviewURL: "https://cdn.example.test/aurora-preview.jpg",
		Variants:   []WallpaperVariantRecord{{ID: "4k", Label: "3840x2160", URL: "https://cdn.example.test/aurora-4k.jpg"}},
		Enabled:    true, SortIndex: 7,
	}); err != nil {
		t.Fatalf("seed legacy wallpaper: %v", err)
	}

	detail := adminV1Request(t, handler, http.MethodGet, "/api/admin/v1/catalog/official/legacy:aurora", "", adminCookie, "")
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `"name":"Legacy Aurora"`) || !strings.Contains(detail.Body.String(), `"status":"published"`) || !strings.Contains(detail.Body.String(), `"variants"`) {
		t.Fatalf("legacy catalog detail = %d %s", detail.Code, detail.Body.String())
	}
	var revisions int
	if err := store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM catalog_revisions WHERE item_type='official' AND item_id='legacy:aurora' AND status='published'`).Scan(&revisions); err != nil || revisions != 1 {
		t.Fatalf("legacy initial revision rows=%d err=%v", revisions, err)
	}
}
