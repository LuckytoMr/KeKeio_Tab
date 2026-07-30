package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestApp(t *testing.T) (*App, *Store) {
	t.Helper()

	store := newTestStore(t)
	if _, err := store.BeginInstallation(t.Context(), InstallationInput{
		Email:           "owner@example.com",
		DisplayName:     "Owner",
		Password:        "correct horse battery staple",
		ExternalBaseURL: "https://fullpro.example",
		AllowedOrigins:  []string{"http://localhost:5173"},
	}); err != nil {
		t.Fatalf("begin test installation: %v", err)
	}
	if err := store.FinishInstallation(t.Context()); err != nil {
		t.Fatalf("finish test installation: %v", err)
	}
	app := NewApp(store, Config{
		Addr:                   "127.0.0.1:0",
		CookieName:             "fullpro_test_session",
		CookieSecure:           false,
		MaxBodyBytes:           1 << 20,
		AllowedOrigin:          "http://localhost:5173",
		AllowInsecureAdminHTTP: true,
		TokenDerivationKey:     []byte("0123456789abcdef0123456789abcdef"),
	})
	return app, store
}

func postJSON(t *testing.T, handler http.Handler, path string, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func putJSON(t *testing.T, handler http.Handler, path string, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func get(t *testing.T, handler http.Handler, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = "127.0.0.1:1234"
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func getBearer(t *testing.T, handler http.Handler, path string, token string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func responseCookie(t *testing.T, res *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()

	for _, cookie := range res.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("cookie %q not found in response", name)
	return nil
}

func fixedAdminCookie(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()

	preauth := get(t, handler, "/api/admin/v1/auth/session")
	if preauth.Code != http.StatusOK {
		t.Fatalf("admin preauth status = %d, body = %s", preauth.Code, preauth.Body.String())
	}
	var preauthBody struct {
		Data struct {
			CSRFToken string `json:"csrfToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(preauth.Body.Bytes(), &preauthBody); err != nil {
		t.Fatalf("decode preauth: %v", err)
	}
	preauthCookie := responseCookie(t, preauth, "fullpro_test_preauth")
	req := httptest.NewRequest(http.MethodPost, "/api/admin/v1/auth/login", strings.NewReader(`{"email":"owner@example.com","password":"correct horse battery staple"}`))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("X-CSRF-Token", preauthBody.Data.CSRFToken)
	req.AddCookie(preauthCookie)
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, req)
	if login.Code != http.StatusOK {
		t.Fatalf("admin login status = %d, body = %s", login.Code, login.Body.String())
	}
	return responseCookie(t, login, "fullpro_test_session")
}

func legacyPluginCookie(t *testing.T, store *Store, email string) *http.Cookie {
	t.Helper()
	user, err := store.CreateUser(t.Context(), email, "safe-password-123")
	if err != nil {
		t.Fatalf("create legacy plugin user: %v", err)
	}
	token, err := store.CreateSession(t.Context(), user.ID, time.Hour)
	if err != nil {
		t.Fatalf("create legacy plugin session: %v", err)
	}
	return &http.Cookie{Name: "fullpro_test_session", Value: token, Path: "/"}
}

func TestAuthProfileAndVersionAPI(t *testing.T) {
	app, _ := newTestApp(t)
	handler := app.Routes()

	register := postJSON(t, handler, "/api/auth/register", `{"email":"Owner@Example.COM","password":"safe-password-123"}`)
	if register.Code != http.StatusUpgradeRequired || !strings.Contains(register.Body.String(), "SYNC_PROTOCOL_UPGRADE_REQUIRED") {
		t.Fatalf("legacy register status = %d, body = %s", register.Code, register.Body.String())
	}

	firstProfile := `{"profile":{"schemaVersion":1,"profileId":"p1","shortcuts":[{"title":"A"}]}}`
	save := putJSON(t, handler, "/api/profile", firstProfile)
	if save.Code != http.StatusUpgradeRequired || !strings.Contains(save.Body.String(), "UPGRADE_REQUIRED") {
		t.Fatalf("legacy save response = %d %s", save.Code, save.Body.String())
	}
}

func TestAdminPageUsesExplicitWallpaperFields(t *testing.T) {
	app, _ := newTestApp(t)
	handler := app.Routes()

	res := get(t, handler, "/admin")
	if res.Code != http.StatusOK {
		t.Fatalf("admin page status = %d", res.Code)
	}
	body := res.Body.String()
	for _, want := range []string{"<!doctype html>", `<div id="app"></div>`, "/admin/assets/admin-"} {
		if !strings.Contains(body, want) {
			t.Fatalf("admin page missing %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, `data-mode="register"`) {
		t.Fatalf("admin page should not expose backend admin registration")
	}
	if strings.Contains(body, "变体 JSON") {
		t.Fatalf("admin page should not ask admins to type variant JSON")
	}

}

func TestAdminRoutesRequireAllowedNetwork(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.BeginInstallation(t.Context(), InstallationInput{
		Email:           "owner@example.com",
		DisplayName:     "Owner",
		Password:        "correct horse battery staple",
		ExternalBaseURL: "https://fullpro.example",
		AllowedOrigins:  []string{"http://localhost:5173"},
	}); err != nil {
		t.Fatalf("begin test installation: %v", err)
	}
	if err := store.FinishInstallation(t.Context()); err != nil {
		t.Fatalf("finish test installation: %v", err)
	}
	app := NewApp(store, Config{
		CookieName:         "fullpro_test_session",
		MaxBodyBytes:       1 << 20,
		AllowedOrigins:     []string{"http://localhost:5173", "chrome-extension://abc"},
		AdminAllowedCIDRs:  []string{"127.0.0.1/32"},
		TokenDerivationKey: []byte("0123456789abcdef0123456789abcdef"),
	})
	handler := app.Routes()

	cookie := fixedAdminCookie(t, handler)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/summary", nil)
	req.RemoteAddr = "203.0.113.10:5555"
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("remote admin status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestCorsAllowsConfiguredPluginOriginsAndHeaders(t *testing.T) {
	app, _ := newTestApp(t)
	handler := app.Routes()

	req := httptest.NewRequest(http.MethodOptions, "/api/profile", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Headers", "Authorization, Idempotency-Key")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", res.Code)
	}
	if got := res.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("allow origin = %q", got)
	}
	headers := res.Header().Get("Access-Control-Allow-Headers")
	if !strings.Contains(headers, "Authorization") || !strings.Contains(headers, "Idempotency-Key") {
		t.Fatalf("allow headers missing auth/idempotency: %q", headers)
	}
}

func TestCorsRejectsLocalDevOriginByDefault(t *testing.T) {
	store := newTestStore(t)
	app := NewApp(store, Config{})
	handler := app.Routes()

	for _, origin := range []string{"http://localhost:5173", "http://127.0.0.1:5173"} {
		req := httptest.NewRequest(http.MethodOptions, "/api/auth/register", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Headers", "Content-Type")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)

		if res.Code != http.StatusForbidden {
			t.Fatalf("%s preflight status = %d, want 403", origin, res.Code)
		}
		if got := res.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("%s unexpected allow origin = %q", origin, got)
		}
	}
}

func TestCorsRejectsUnconfiguredChromeExtensionOrigins(t *testing.T) {
	app, _ := newTestApp(t)
	handler := app.Routes()

	req := httptest.NewRequest(http.MethodOptions, "/api/auth/login", nil)
	req.Header.Set("Origin", "chrome-extension://abcdefghijklmnop")
	req.Header.Set("Access-Control-Request-Headers", "Content-Type")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusForbidden {
		t.Fatalf("preflight status = %d, want 403", res.Code)
	}
	if got := res.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unexpected allow origin = %q", got)
	}
}

func TestAdminLogsEndpointDoesNotLogItself(t *testing.T) {
	app, store := newTestApp(t)
	handler := app.Routes()

	cookie := fixedAdminCookie(t, handler)
	before, err := store.ListAPILogs(t.Context(), APILogFilter{Limit: 100})
	if err != nil {
		t.Fatalf("list logs before: %v", err)
	}

	res := get(t, handler, "/api/admin/logs?limit=100", cookie)
	if res.Code != http.StatusOK {
		t.Fatalf("admin logs = %d %s", res.Code, res.Body.String())
	}
	after, err := store.ListAPILogs(t.Context(), APILogFilter{Limit: 100})
	if err != nil {
		t.Fatalf("list logs after: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("admin logs endpoint should not append log rows: before=%d after=%d", len(before), len(after))
	}
}

func TestAnonymousUnknownRoutesDoNotFillAccessLog(t *testing.T) {
	app, store := newTestApp(t)
	handler := app.Routes()

	for index := 0; index < 5; index++ {
		response := get(t, handler, fmt.Sprintf("/random-unknown-%d", index))
		if response.Code != http.StatusNotFound {
			t.Fatalf("unknown route %d = %d, want 404", index, response.Code)
		}
	}
	var rows int
	if err := store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM api_logs WHERE path LIKE '/random-unknown-%'`).Scan(&rows); err != nil {
		t.Fatalf("count unknown-route logs: %v", err)
	}
	if rows != 0 {
		t.Fatalf("anonymous unknown routes created %d access-log rows, want 0", rows)
	}
}

func TestAccessLogDropsQueryBeforePersistence(t *testing.T) {
	app, store := newTestApp(t)
	handler := app.Routes()

	response := get(t, handler, "/api/v1/app/bootstrap?token=secret&private=https%3A%2F%2Finternal.example")
	if response.Code != http.StatusOK {
		t.Fatalf("bootstrap = %d %s", response.Code, response.Body.String())
	}
	var path string
	if err := store.db.QueryRowContext(t.Context(), `SELECT path FROM api_logs WHERE route_group='/api/v1/app/bootstrap' ORDER BY created_at DESC LIMIT 1`).Scan(&path); err != nil {
		t.Fatalf("read persisted access-log path: %v", err)
	}
	if path != "/api/v1/app/bootstrap" {
		t.Fatalf("persisted access-log path = %q, want query-free path", path)
	}
}

func TestAccessLogAttributesV1BearerAccessToken(t *testing.T) {
	app, store := newTestApp(t)
	handler := app.Routes()

	user, err := store.CreateUser(t.Context(), "access-log-v1@example.com", "safe-password-123")
	if err != nil {
		t.Fatalf("create plugin user: %v", err)
	}
	tokens, err := store.CreateTokenFamily(t.Context(), user.ID, "access-log-device")
	if err != nil {
		t.Fatalf("create access token: %v", err)
	}

	response := getBearer(t, handler, "/api/v1/me", tokens.AccessToken)
	if response.Code != http.StatusOK {
		t.Fatalf("v1 me = %d %s", response.Code, response.Body.String())
	}

	var loggedUserID, loggedEmail, loggedRole string
	if err := store.db.QueryRowContext(
		t.Context(),
		`SELECT user_id,user_email,role FROM api_logs WHERE path='/api/v1/me' ORDER BY created_at DESC LIMIT 1`,
	).Scan(&loggedUserID, &loggedEmail, &loggedRole); err != nil {
		t.Fatalf("read v1 access log identity: %v", err)
	}
	if loggedUserID != user.ID || loggedEmail != user.Email || loggedRole != string(user.Role) {
		t.Fatalf(
			"v1 access log identity = (%q, %q, %q), want (%q, %q, %q)",
			loggedUserID,
			loggedEmail,
			loggedRole,
			user.ID,
			user.Email,
			user.Role,
		)
	}

	logoutRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", strings.NewReader(`{}`))
	logoutRequest.RemoteAddr = "127.0.0.1:1234"
	logoutRequest.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	logoutRequest.Header.Set("Content-Type", "application/json")
	logoutResponse := httptest.NewRecorder()
	handler.ServeHTTP(logoutResponse, logoutRequest)
	if logoutResponse.Code != http.StatusNoContent {
		t.Fatalf("v1 logout = %d %s", logoutResponse.Code, logoutResponse.Body.String())
	}

	if err := store.db.QueryRowContext(
		t.Context(),
		`SELECT user_id FROM api_logs WHERE path='/api/v1/auth/logout' ORDER BY created_at DESC LIMIT 1`,
	).Scan(&loggedUserID); err != nil {
		t.Fatalf("read v1 logout access log identity: %v", err)
	}
	if loggedUserID != user.ID {
		t.Fatalf("v1 logout access log user = %q, want %q", loggedUserID, user.ID)
	}
}

func TestAccessLogKeepsLegacyBearerSessionAttribution(t *testing.T) {
	app, store := newTestApp(t)
	handler := app.Routes()

	user, err := store.CreateUser(t.Context(), "access-log-legacy@example.com", "safe-password-123")
	if err != nil {
		t.Fatalf("create legacy plugin user: %v", err)
	}
	sessionToken, err := store.CreateSession(t.Context(), user.ID, time.Hour)
	if err != nil {
		t.Fatalf("create legacy session: %v", err)
	}

	response := getBearer(t, handler, "/api/me", sessionToken)
	if response.Code != http.StatusOK {
		t.Fatalf("legacy me = %d %s", response.Code, response.Body.String())
	}

	var loggedUserID string
	if err := store.db.QueryRowContext(
		t.Context(),
		`SELECT user_id FROM api_logs WHERE path='/api/me' ORDER BY created_at DESC LIMIT 1`,
	).Scan(&loggedUserID); err != nil {
		t.Fatalf("read legacy access log identity: %v", err)
	}
	if loggedUserID != user.ID {
		t.Fatalf("legacy access log user = %q, want %q", loggedUserID, user.ID)
	}
}

func TestDecodeJSONEnforcesWholeBodyLimitAndSingleValue(t *testing.T) {
	valid := `{"value":1}`
	tests := []struct {
		name       string
		body       string
		maxBytes   int64
		wantOK     bool
		wantStatus int
	}{
		{
			name:     "accepts body exactly at limit",
			body:     valid,
			maxBytes: int64(len(valid)),
			wantOK:   true,
		},
		{
			name:       "rejects bytes after valid prefix beyond limit",
			body:       valid + "x",
			maxBytes:   int64(len(valid)),
			wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name:       "rejects trailing data within limit",
			body:       valid + "x",
			maxBytes:   int64(len(valid) + 1),
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "rejects second JSON value within limit",
			body:       valid + `{}`,
			maxBytes:   int64(len(valid) + 2),
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := &App{config: Config{MaxBodyBytes: test.maxBytes}}
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			response := httptest.NewRecorder()
			var payload struct {
				Value int `json:"value"`
			}

			gotOK := app.decodeJSON(response, request, &payload)
			if gotOK != test.wantOK {
				t.Fatalf("decodeJSON() = %v, want %v; status=%d body=%s", gotOK, test.wantOK, response.Code, response.Body.String())
			}
			if test.wantOK {
				if payload.Value != 1 {
					t.Fatalf("decoded value = %d, want 1", payload.Value)
				}
				return
			}
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
		})
	}
}

func TestAdminUsersEndpointListsPluginUsersOnly(t *testing.T) {
	app, store := newTestApp(t)
	handler := app.Routes()

	adminCookie := fixedAdminCookie(t, handler)

	if _, err := store.CreateUser(t.Context(), "plugin@example.com", "safe-password-123"); err != nil {
		t.Fatalf("seed plugin user: %v", err)
	}

	users := get(t, handler, "/api/admin/users", adminCookie)
	if users.Code != http.StatusOK {
		t.Fatalf("admin users status = %d, body = %s", users.Code, users.Body.String())
	}
	if strings.Contains(users.Body.String(), "lucky") || strings.Contains(users.Body.String(), `"role":"admin"`) {
		t.Fatalf("admin account should not appear in plugin users endpoint: %s", users.Body.String())
	}
	if !strings.Contains(users.Body.String(), "plugin@example.com") || !strings.Contains(users.Body.String(), `"role":"user"`) {
		t.Fatalf("plugin user missing from admin users endpoint: %s", users.Body.String())
	}

	summary := get(t, handler, "/api/admin/summary", adminCookie)
	if summary.Code != http.StatusOK {
		t.Fatalf("summary status = %d, body = %s", summary.Code, summary.Body.String())
	}
	if !strings.Contains(summary.Body.String(), `"users":1`) {
		t.Fatalf("summary should count plugin users only: %s", summary.Body.String())
	}
}

func TestLegacyFixedAdminLoginIsRejectedAndRegisterCreatesOnlyPluginUsers(t *testing.T) {
	app, _ := newTestApp(t)
	handler := app.Routes()

	adminLogin := postJSON(t, handler, "/api/auth/login", `{"email":"lucky","password":"2231"}`)
	if adminLogin.Code != http.StatusUpgradeRequired {
		t.Fatalf("legacy fixed admin login = %d %s, want 426", adminLogin.Code, adminLogin.Body.String())
	}

	_ = fixedAdminCookie(t, handler)

	pluginRegister := postJSON(t, handler, "/api/auth/register", `{"email":"new@example.com","password":"2231"}`)
	if pluginRegister.Code != http.StatusUpgradeRequired || !strings.Contains(pluginRegister.Body.String(), "SYNC_PROTOCOL_UPGRADE_REQUIRED") {
		t.Fatalf("legacy plugin register = %d %s, want 426", pluginRegister.Code, pluginRegister.Body.String())
	}
}

func TestV1RegisterDuplicateEmailIsNonEnumerating(t *testing.T) {
	handler, _, _ := newV1AuthApp(t)

	first := postJSON(t, handler, "/api/v1/auth/register", `{"email":"duplicate@example.com","password":"safe-password-123"}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("first register status = %d, body = %s", first.Code, first.Body.String())
	}

	duplicate := postJSON(t, handler, "/api/v1/auth/register", `{"email":"DUPLICATE@example.com","password":"safe-password-123"}`)
	if duplicate.Code != http.StatusCreated {
		t.Fatalf("duplicate register status = %d, body = %s", duplicate.Code, duplicate.Body.String())
	}
	body := duplicate.Body.String()
	if !strings.Contains(body, `"status":"pending_verification"`) {
		t.Fatalf("duplicate register should return the same pending result: %s", body)
	}
	if strings.Contains(body, "constraint failed") || strings.Contains(body, "users.email") {
		t.Fatalf("duplicate register leaked database error: %s", body)
	}
}

func TestAuthRateLimitReturns429(t *testing.T) {
	store := newTestStore(t)
	app := NewApp(store, Config{
		CookieName:    "fullpro_test_session",
		MaxBodyBytes:  1 << 20,
		AuthRateLimit: RateLimitConfig{Limit: 2, Window: time.Minute},
	})
	handler := app.Routes()

	for i := 0; i < 2; i++ {
		res := postJSON(t, handler, "/api/v1/auth/login", `{"email":"missing@example.com","password":"1234","deviceId":"device-1"}`)
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, body = %s", i, res.Code, res.Body.String())
		}
	}
	res := postJSON(t, handler, "/api/v1/auth/login", `{"email":"missing@example.com","password":"1234","deviceId":"device-1"}`)
	if res.Code != http.StatusTooManyRequests {
		t.Fatalf("rate-limited status = %d, body = %s", res.Code, res.Body.String())
	}
	if retryAfter := res.Header().Get("Retry-After"); retryAfter == "" {
		t.Fatalf("rate-limited response omitted Retry-After: %#v", res.Header())
	}
}

func TestAuthRateLimitSeparatesAccountsAndNormalizesEmail(t *testing.T) {
	store := newTestStore(t)
	app := NewApp(store, Config{AuthRateLimit: RateLimitConfig{Limit: 1, Window: time.Minute}})
	handler := app.Routes()

	first := postJSON(t, handler, "/api/v1/auth/login", `{"email":"First@Example.com","password":"wrong","deviceId":"device-1"}`)
	if first.Code != http.StatusUnauthorized {
		t.Fatalf("first account attempt = %d %s", first.Code, first.Body.String())
	}
	secondAccount := postJSON(t, handler, "/api/v1/auth/login", `{"email":"second@example.com","password":"wrong","deviceId":"device-1"}`)
	if secondAccount.Code != http.StatusUnauthorized {
		t.Fatalf("different account attempt = %d %s, want independent bucket", secondAccount.Code, secondAccount.Body.String())
	}
	normalizedRepeat := postJSON(t, handler, "/api/v1/auth/login", `{"email":" first@example.COM ","password":"wrong","deviceId":"device-1"}`)
	if normalizedRepeat.Code != http.StatusTooManyRequests {
		t.Fatalf("normalized repeat = %d %s, want 429", normalizedRepeat.Code, normalizedRepeat.Body.String())
	}
}

func TestAuthRateLimiterBoundsHighCardinalityBuckets(t *testing.T) {
	store := newTestStore(t)
	app := NewApp(store, Config{AuthRateLimit: RateLimitConfig{Limit: 2, IPLimit: 1000, Window: time.Minute, MaxBuckets: 8}})
	handler := app.Routes()
	for index := 0; index < 100; index++ {
		payload := fmt.Sprintf(`{"email":"user-%d@example.com","password":"wrong","deviceId":"device-1"}`, index)
		response := postJSON(t, handler, "/api/v1/auth/login", payload)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("high-cardinality request %d = %d %s", index, response.Code, response.Body.String())
		}
	}
	app.authLimiter.mu.Lock()
	bucketCount := len(app.authLimiter.buckets)
	app.authLimiter.mu.Unlock()
	if bucketCount > 8 {
		t.Fatalf("auth limiter retained %d buckets, want <= 8", bucketCount)
	}
}

func TestAuthRateLimitHasPerIPRouteCeilingAcrossRandomTokens(t *testing.T) {
	store := newTestStore(t)
	app := NewApp(store, Config{AuthRateLimit: RateLimitConfig{Limit: 10, IPLimit: 2, Window: time.Minute}})
	handler := app.Routes()
	for attempt := 0; attempt < 2; attempt++ {
		response := postJSON(t, handler, "/api/v1/auth/reset-password", fmt.Sprintf(`{"token":"random-%d","password":"valid-password-123"}`, attempt))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("random token attempt %d = %d %s", attempt, response.Code, response.Body.String())
		}
	}
	limited := postJSON(t, handler, "/api/v1/auth/reset-password", `{"token":"random-3","password":"valid-password-123"}`)
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") == "" {
		t.Fatalf("per-IP random-token ceiling = %d %s headers=%#v", limited.Code, limited.Body.String(), limited.Header())
	}
}

func TestProfileSaveIsIdempotent(t *testing.T) {
	app, _ := newTestApp(t)
	handler := app.Routes()

	body := `{"profile":{"schemaVersion":1,"profileId":"p1","shortcuts":[{"title":"A"}]},"baseVersion":0,"clientMutationId":"m1","deviceId":"d1"}`
	req := httptest.NewRequest(http.MethodPut, "/api/profile", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "idem-1")
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, req)
	if first.Code != http.StatusUpgradeRequired || !strings.Contains(first.Body.String(), "UPGRADE_REQUIRED") {
		t.Fatalf("legacy write = %d %s", first.Code, first.Body.String())
	}
}

func TestAdminWallpaperAPIAndPublicSanitization(t *testing.T) {
	app, store := newTestApp(t)
	handler := app.Routes()

	cookie := fixedAdminCookie(t, handler)
	pluginCookie := legacyPluginCookie(t, store, "wallpaper-viewer@example.com")

	payload := `{
		"id":"uhdpaper:354@5@d",
		"provider":"uhdpaper",
		"sourcePageUrl":"https://www.uhdpaper.com/2025/03/3545d-anime.html",
		"title":"Bunny Ears Katana",
		"category":"anime",
		"tags":["Anime","4K"],
		"previewUrl":"https://img.uhdpaper.com/wallpaper/preview.jpg",
		"variants":[
			{"id":"4k","label":"3840x2160","url":"https://image-5.uhdpaper.com/wallpaper/full-4k.jpg"},
			{"id":"2k","label":"2560x1440","url":"https://image-5.uhdpaper.com/wallpaper/full-2k.jpg"}
		],
		"enabled":true
	}`
	create := postJSON(t, handler, "/api/admin/wallpapers/web", payload, cookie)
	if create.Code != http.StatusUpgradeRequired {
		t.Fatalf("legacy admin web wallpaper write = %d %s, want 426", create.Code, create.Body.String())
	}
	if err := store.UpsertWebWallpaper(t.Context(), WebWallpaperRecord{
		ID: "uhdpaper:354@5@d", Provider: "uhdpaper", SourcePageURL: "https://www.uhdpaper.com/2025/03/3545d-anime.html",
		Title: "Bunny Ears Katana", Category: "anime", Tags: []string{"Anime", "4K"}, PreviewURL: "https://img.uhdpaper.com/wallpaper/preview.jpg",
		Variants: []WallpaperVariantRecord{
			{ID: "4k", Label: "3840x2160", URL: "https://image-5.uhdpaper.com/wallpaper/full-4k.jpg"},
			{ID: "2k", Label: "2560x1440", URL: "https://image-5.uhdpaper.com/wallpaper/full-2k.jpg"},
		}, Enabled: true,
	}); err != nil {
		t.Fatalf("seed web wallpaper directly: %v", err)
	}

	anonymous := get(t, handler, "/api/wallpapers/web?category=anime&page=1&pageSize=20")
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous web wallpapers should require login: %d %s", anonymous.Code, anonymous.Body.String())
	}

	public := get(t, handler, "/api/wallpapers/web?category=anime&page=1&pageSize=20", pluginCookie)
	if public.Code != http.StatusOK {
		t.Fatalf("public wallpapers = %d %s", public.Code, public.Body.String())
	}
	if !strings.Contains(public.Body.String(), "https://image-5.uhdpaper.com") {
		t.Fatalf("authenticated web wallpaper response should include usable image URL: %s", public.Body.String())
	}
	if !strings.Contains(public.Body.String(), `"label":"3840x2160"`) {
		t.Fatalf("public response lost variant label: %s", public.Body.String())
	}

	admin := get(t, handler, "/api/admin/wallpapers/web", cookie)
	if admin.Code != http.StatusOK || !strings.Contains(admin.Body.String(), "https://image-5.uhdpaper.com") {
		t.Fatalf("admin response should include private URL: %d %s", admin.Code, admin.Body.String())
	}

	summary := get(t, handler, "/api/admin/summary", cookie)
	if summary.Code != http.StatusOK {
		t.Fatalf("summary = %d %s", summary.Code, summary.Body.String())
	}
	var decoded map[string]any
	if err := json.NewDecoder(bytes.NewReader(summary.Body.Bytes())).Decode(&decoded); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if decoded["users"].(float64) != 1 || decoded["webWallpapers"].(float64) != 1 {
		t.Fatalf("summary = %#v", decoded)
	}
}

func TestStylesRequireLoginAndCanBeManaged(t *testing.T) {
	app, store := newTestApp(t)
	handler := app.Routes()
	cookie := fixedAdminCookie(t, handler)
	pluginCookie := legacyPluginCookie(t, store, "style-viewer@example.com")

	payload := `{"id":"style:glass","name":"玻璃风","version":"1.0.0","description":"轻量玻璃效果","previewUrl":"https://example.com/style.jpg","css":":root{--remote-style-ready:1}","config":{"theme":"glass"},"enabled":true,"sortIndex":1}`
	create := postJSON(t, handler, "/api/admin/styles", payload, cookie)
	if create.Code != http.StatusUpgradeRequired {
		t.Fatalf("legacy admin style write = %d %s, want 426", create.Code, create.Body.String())
	}
	if err := store.UpsertStylePackage(t.Context(), StylePackageRecord{
		ID: "style:glass", Name: "玻璃风", Version: "1.0.0", Description: "轻量玻璃效果",
		PreviewURL: "https://example.com/style.jpg", CSS: ":root{--remote-style-ready:1}",
		ConfigJSON: json.RawMessage(`{"theme":"glass"}`), Enabled: true, SortIndex: 1,
	}); err != nil {
		t.Fatalf("seed style directly: %v", err)
	}

	anonymous := get(t, handler, "/api/styles")
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous styles should require login: %d %s", anonymous.Code, anonymous.Body.String())
	}
	styles := get(t, handler, "/api/styles", pluginCookie)
	if styles.Code != http.StatusOK || !strings.Contains(styles.Body.String(), `"id":"style:glass"`) {
		t.Fatalf("styles = %d %s", styles.Code, styles.Body.String())
	}
}
