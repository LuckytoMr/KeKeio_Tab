package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()

	store, err := OpenStore(t.TempDir() + "/fullpro-test.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	})
	return store
}

func TestCreateUserMakesPluginUserAndNormalizesEmail(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	owner, err := store.CreateUser(ctx, " Root@Example.COM ", "safe-password-123")
	if err != nil {
		t.Fatalf("create plugin user: %v", err)
	}
	if owner.Role != RoleUser {
		t.Fatalf("registered user role = %q, want user", owner.Role)
	}
	if owner.Email != "root@example.com" {
		t.Fatalf("normalized email = %q", owner.Email)
	}
	if !store.CheckPassword(ctx, owner.Email, "safe-password-123") {
		t.Fatalf("owner password did not verify")
	}

	user, err := store.CreateUser(ctx, "user@example.com", "safe-password-123")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if user.Role != RoleUser {
		t.Fatalf("second user role = %q, want user", user.Role)
	}

	if _, err := store.CreateUser(ctx, "USER@example.com", "safe-password-123"); !errors.Is(err, ErrDuplicateEmail) {
		t.Fatalf("duplicate email err = %v, want ErrDuplicateEmail", err)
	}
}

func TestFixedAdminLoginIsNotSeeded(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	if _, err := store.AuthenticateUser(ctx, "lucky", "2231"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("fixed admin login err = %v, want ErrUnauthorized", err)
	}
}

func TestCreateUserUsesFourUnicodeCharacterMinimum(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	if _, err := store.CreateUser(ctx, "too-short@example.com", "密密密"); err == nil {
		t.Fatal("create user accepted fewer than four Unicode characters")
	}

	user, err := store.CreateUser(ctx, "short@example.com", "密码四位")
	if err != nil {
		t.Fatalf("create user with four Unicode character password: %v", err)
	}
	if !store.CheckPassword(ctx, user.Email, "密码四位") {
		t.Fatalf("four Unicode character password did not verify")
	}
}

func TestCreateUserKeepsOnlyFixedAdminUnderConcurrentRegistration(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	const total = 6
	var wg sync.WaitGroup
	users := make(chan User, total)
	errs := make(chan error, total)
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			user, err := store.CreateUser(ctx, "concurrent-"+string(rune('a'+index))+"@example.com", "safe-password-123")
			if err != nil {
				errs <- err
				return
			}
			users <- user
		}(i)
	}
	wg.Wait()
	close(users)
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent create user failed: %v", err)
	}

	registeredAdmins := 0
	for user := range users {
		if user.Role == RoleAdmin {
			registeredAdmins++
		}
	}
	if registeredAdmins != 0 {
		t.Fatalf("registered admin count = %d, want 0", registeredAdmins)
	}

	var adminRows int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin'`).Scan(&adminRows); err != nil {
		t.Fatalf("count plugin administrators: %v", err)
	}
	if adminRows != 0 {
		t.Fatalf("plugin administrator rows = %d, want 0", adminRows)
	}
}

func TestUsersTableRejectsSecondAdminRole(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	plugin, err := store.CreateUser(ctx, "plugin@example.com", "safe-password-123")
	if err != nil {
		t.Fatalf("create plugin user: %v", err)
	}

	if _, err := store.db.ExecContext(ctx, `UPDATE users SET role = ? WHERE id = ?`, string(RoleAdmin), plugin.ID); err == nil {
		t.Fatalf("database should reject a second admin role")
	}
}

func TestListUsersAndAdminSummaryOnlyCountPluginUsers(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	first, err := store.CreateUser(ctx, "first-plugin@example.com", "safe-password-123")
	if err != nil {
		t.Fatalf("create first plugin user: %v", err)
	}
	second, err := store.CreateUser(ctx, "second-plugin@example.com", "safe-password-123")
	if err != nil {
		t.Fatalf("create second plugin user: %v", err)
	}

	users, err := store.ListUsers(ctx)
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("users length = %d, want 2 plugin users: %#v", len(users), users)
	}
	for _, user := range users {
		if user.Email == "lucky" || user.Role == RoleAdmin {
			t.Fatalf("admin leaked into plugin users list: %#v", user)
		}
	}
	if users[0].ID != second.ID || users[1].ID != first.ID {
		t.Fatalf("users order = %#v, want newest plugin users first", users)
	}

	summary, err := store.AdminSummary(ctx)
	if err != nil {
		t.Fatalf("admin summary: %v", err)
	}
	if summary.Users != 2 {
		t.Fatalf("summary users = %d, want 2 plugin users", summary.Users)
	}
}

func TestProfilesAreVersionedAndRestorable(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	user, err := store.CreateUser(ctx, "profile@example.com", "safe-password-123")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	first := json.RawMessage(`{"schemaVersion":1,"shortcuts":[{"title":"A"}]}`)
	second := json.RawMessage(`{"schemaVersion":1,"shortcuts":[{"title":"B"}]}`)

	profile, err := store.SaveProfile(ctx, user.ID, first)
	if err != nil {
		t.Fatalf("save first profile: %v", err)
	}
	if profile.Version != 1 {
		t.Fatalf("first version = %d, want 1", profile.Version)
	}

	profile, err = store.SaveProfile(ctx, user.ID, second)
	if err != nil {
		t.Fatalf("save second profile: %v", err)
	}
	if profile.Version != 2 {
		t.Fatalf("second version = %d, want 2", profile.Version)
	}

	versions, err := store.ListProfileVersions(ctx, user.ID, 10)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 2 || versions[0].Version != 2 || versions[1].Version != 1 {
		t.Fatalf("versions = %#v, want newest-first 2,1", versions)
	}

	restored, err := store.RestoreProfileVersion(ctx, user.ID, versions[1].ID)
	if err != nil {
		t.Fatalf("restore profile: %v", err)
	}
	if restored.Version != 3 {
		t.Fatalf("restored version = %d, want 3", restored.Version)
	}
	if !strings.Contains(string(restored.ProfileJSON), `"A"`) {
		t.Fatalf("restored profile = %s, want first profile", restored.ProfileJSON)
	}
}

func TestPublicWebWallpapersHideSourceURLs(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	record := WebWallpaperRecord{
		ID:            "uhdpaper:354@5@d",
		Provider:      "uhdpaper",
		SourcePageURL: "https://www.uhdpaper.com/2025/03/3545d-anime.html",
		Title:         "Bunny Ears Katana",
		Category:      "anime",
		Tags:          []string{"Anime", "4K"},
		PreviewURL:    "https://img.uhdpaper.com/wallpaper/preview.jpg",
		Variants: []WallpaperVariantRecord{
			{ID: "4k", Label: "3840x2160", URL: "https://image-5.uhdpaper.com/wallpaper/full-4k.jpg"},
			{ID: "2k", Label: "2560x1440", URL: "https://image-5.uhdpaper.com/wallpaper/full-2k.jpg"},
		},
		Enabled: true,
	}
	if err := store.UpsertWebWallpaper(ctx, record); err != nil {
		t.Fatalf("upsert web wallpaper: %v", err)
	}

	adminRecords, err := store.ListAdminWebWallpapers(ctx)
	if err != nil {
		t.Fatalf("list admin wallpapers: %v", err)
	}
	if len(adminRecords) != 1 || adminRecords[0].Variants[0].URL == "" {
		t.Fatalf("admin record lost private URL: %#v", adminRecords)
	}

	publicRecords, err := store.ListPublicWebWallpapers(ctx, WallpaperListFilter{Category: "anime", Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("list public wallpapers: %v", err)
	}
	encoded, err := json.Marshal(publicRecords)
	if err != nil {
		t.Fatalf("marshal public records: %v", err)
	}
	if strings.Contains(string(encoded), "uhdpaper.com") || strings.Contains(string(encoded), "image-5") {
		t.Fatalf("public wallpaper payload leaked source URL: %s", encoded)
	}
	if len(publicRecords.Items) != 1 || publicRecords.Items[0].Variants[0].Label != "3840x2160" {
		t.Fatalf("public variants missing labels: %#v", publicRecords.Items)
	}
}

func TestPublicWebWallpapersReturnEmptyItemsArray(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	publicRecords, err := store.ListPublicWebWallpapers(ctx, WallpaperListFilter{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("list public wallpapers: %v", err)
	}
	encoded, err := json.Marshal(publicRecords)
	if err != nil {
		t.Fatalf("marshal public records: %v", err)
	}
	if strings.Contains(string(encoded), `"items":null`) {
		t.Fatalf("empty list should encode as an empty array: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"items":[]`) {
		t.Fatalf("empty list should expose items array: %s", encoded)
	}
}

func TestAPILogsAreStoredNewestFirst(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	first := APILogRecord{
		UserID:        "user_1",
		UserEmail:     "a@example.com",
		Role:          string(RoleUser),
		IP:            "127.0.0.1",
		Method:        http.MethodGet,
		Path:          "/api/me",
		RouteGroup:    "/api/me",
		Status:        200,
		DurationMS:    12,
		RequestBytes:  0,
		ResponseBytes: 24,
		UserAgent:     "test-agent",
	}
	second := first
	second.Path = "/api/profile"
	second.RouteGroup = "/api/profile"
	second.Status = 401
	if err := store.InsertAPILog(ctx, first); err != nil {
		t.Fatalf("insert first log: %v", err)
	}
	if err := store.InsertAPILog(ctx, second); err != nil {
		t.Fatalf("insert second log: %v", err)
	}
	logs, err := store.ListAPILogs(ctx, APILogFilter{Limit: 20})
	if err != nil {
		t.Fatalf("list logs: %v", err)
	}
	if len(logs) != 2 || logs[0].RouteGroup != "/api/profile" || logs[1].RouteGroup != "/api/me" {
		t.Fatalf("logs = %#v", logs)
	}
}

func TestOpenStoreEnablesWALAndConnectionSafetyPragmas(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "wal.db"))
	if err != nil {
		t.Fatalf("open WAL store: %v", err)
	}
	defer store.Close()
	var journalMode string
	var synchronous, foreignKeys, busyTimeout int
	if err := store.db.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("read journal mode: %v", err)
	}
	if err := store.db.QueryRow(`PRAGMA synchronous`).Scan(&synchronous); err != nil {
		t.Fatalf("read synchronous: %v", err)
	}
	if err := store.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("read foreign keys: %v", err)
	}
	if err := store.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("read busy timeout: %v", err)
	}
	if strings.ToLower(journalMode) != "wal" || synchronous != 1 || foreignKeys != 1 || busyTimeout < 5000 {
		t.Fatalf("SQLite pragmas journal=%q synchronous=%d foreign_keys=%d busy_timeout=%d", journalMode, synchronous, foreignKeys, busyTimeout)
	}
}
