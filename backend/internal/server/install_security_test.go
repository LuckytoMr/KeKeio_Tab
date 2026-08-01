package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func postInstallSession(t *testing.T, handler http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/install/api/v1/session", strings.NewReader(`{}`))
	request.Host = "install.example.test"
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://install.example.test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestNumberedMigrationRunsOnceAndReopenKeepsPluginSession(t *testing.T) {
	path := t.TempDir() + "/reopen.db"
	store, err := OpenStore(path)
	if err != nil {
		t.Fatalf("open initial store: %v", err)
	}
	user, err := store.CreateUser(context.Background(), "persist@example.com", "safe-password-123")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	token, err := store.CreateSession(context.Background(), user.ID, time.Hour)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}

	reopened, err := OpenStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if _, err := reopened.UserBySession(context.Background(), token); err != nil {
		t.Fatalf("session was revoked by repeat migration: %v", err)
	}
	var applied int
	if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 1`).Scan(&applied); err != nil {
		t.Fatalf("read migration marker: %v", err)
	}
	if applied != 1 {
		t.Fatalf("migration version 1 rows = %d, want 1", applied)
	}
}

func TestFreshStoreExposesUninitializedInstallStatusAndRejectsFixedAdmin(t *testing.T) {
	store := newTestStore(t)
	app := NewApp(store, Config{
		CookieName:        "fullpro_test_session",
		InstallCookieName: "fullpro_test_install",
		AllowedOrigins:    []string{"http://localhost:5173"},
	})
	handler := app.Routes()

	status := get(t, handler, "/install/api/v1/status")
	if status.Code != http.StatusOK {
		t.Fatalf("install status = %d %s, want 200", status.Code, status.Body.String())
	}
	if !strings.Contains(status.Body.String(), `"state":"uninitialized"`) {
		t.Fatalf("install state response = %s", status.Body.String())
	}

	legacyAdmin := postJSON(t, handler, "/api/auth/login", `{"email":"lucky","password":"2231"}`)
	if legacyAdmin.Code != http.StatusUpgradeRequired {
		t.Fatalf("legacy fixed admin login = %d %s, want 426", legacyAdmin.Code, legacyAdmin.Body.String())
	}
}

func TestLegacyUnsafeAuthAndAdminWritesRequireProtocolUpgrade(t *testing.T) {
	app, _ := newTestApp(t)
	handler := app.Routes()
	for _, request := range []struct {
		path string
		body string
	}{
		{"/api/auth/register", `{"email":"legacy@example.com","password":"unsafe-password"}`},
		{"/api/auth/login", `{"email":"legacy@example.com","password":"unsafe-password"}`},
	} {
		response := postJSON(t, handler, request.path, request.body)
		if response.Code != http.StatusUpgradeRequired || !strings.Contains(response.Body.String(), `"code":"SYNC_PROTOCOL_UPGRADE_REQUIRED"`) {
			t.Fatalf("legacy auth %s = %d %s", request.path, response.Code, response.Body.String())
		}
	}
	adminCookie := fixedAdminCookie(t, handler)
	request := httptest.NewRequest(http.MethodPost, "/api/admin/styles", strings.NewReader(`{"id":"legacy-write"}`))
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("Content-Type", "text/plain")
	request.AddCookie(adminCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUpgradeRequired || !strings.Contains(response.Body.String(), `"code":"SYNC_PROTOCOL_UPGRADE_REQUIRED"`) {
		t.Fatalf("legacy admin write = %d %s, want 426", response.Code, response.Body.String())
	}
}

func TestInstallSessionAndWritesRequireSameOrigin(t *testing.T) {
	store := newTestStore(t)
	app := NewApp(store, Config{
		InstallCookieName: "fullpro_test_install",
	})
	handler := app.Routes()

	remoteRequest := httptest.NewRequest(http.MethodPost, "/install/api/v1/session", strings.NewReader(`{}`))
	remoteRequest.Host = "install.example.test"
	remoteRequest.RemoteAddr = "203.0.113.8:1234"
	remoteRequest.Header.Set("Content-Type", "application/json")
	remoteRequest.Header.Set("Origin", "http://install.example.test")
	remoteResponse := httptest.NewRecorder()
	handler.ServeHTTP(remoteResponse, remoteRequest)
	if remoteResponse.Code != http.StatusNotFound {
		t.Fatalf("remote install session = %d %s, want 404", remoteResponse.Code, remoteResponse.Body.String())
	}

	for name, origin := range map[string]string{
		"missing":      "",
		"cross-origin": "http://evil.example.test",
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/install/api/v1/session", strings.NewReader(`{}`))
			request.Host = "install.example.test"
			request.RemoteAddr = "127.0.0.1:1234"
			request.Header.Set("Content-Type", "application/json")
			if origin != "" {
				request.Header.Set("Origin", origin)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"ORIGIN_REJECTED"`) {
				t.Fatalf("install session origin %q = %d %s, want 403 ORIGIN_REJECTED", origin, response.Code, response.Body.String())
			}
		})
	}

	sessionRequest := httptest.NewRequest(http.MethodPost, "/install/api/v1/session", strings.NewReader(`{}`))
	sessionRequest.Host = "install.example.test"
	sessionRequest.RemoteAddr = "127.0.0.1:1234"
	sessionRequest.Header.Set("Content-Type", "application/json")
	sessionRequest.Header.Set("Origin", "http://install.example.test")
	sessionResponse := httptest.NewRecorder()
	handler.ServeHTTP(sessionResponse, sessionRequest)
	if sessionResponse.Code != http.StatusCreated {
		t.Fatalf("install session = %d %s", sessionResponse.Code, sessionResponse.Body.String())
	}
	cookie := responseCookie(t, sessionResponse, "fullpro_test_install")
	var sessionBody struct {
		Data struct {
			CSRFToken string `json:"csrfToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(sessionResponse.Body.Bytes(), &sessionBody); err != nil {
		t.Fatalf("decode install session: %v", err)
	}
	preflight := func(origin string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/install/api/v1/preflight", strings.NewReader(`{}`))
		request.Host = "install.example.test"
		request.RemoteAddr = "127.0.0.1:1234"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", origin)
		request.Header.Set("X-CSRF-Token", sessionBody.Data.CSRFToken)
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	if sameOrigin := preflight("http://install.example.test"); sameOrigin.Code != http.StatusOK {
		t.Fatalf("same-origin install preflight = %d %s, want 200", sameOrigin.Code, sameOrigin.Body.String())
	}
	if evil := preflight("http://evil.example.test"); evil.Code != http.StatusForbidden {
		t.Fatalf("cross-origin install preflight = %d %s, want 403", evil.Code, evil.Body.String())
	}
}

func TestAdministratorPasswordMinimumCountsUnicodeCharacters(t *testing.T) {
	tests := []struct {
		name      string
		password  string
		wantError bool
	}{
		{name: "three Unicode characters", password: "甲乙丙", wantError: true},
		{name: "four Unicode characters", password: "甲乙丙丁", wantError: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t)
			_, err := store.BeginInstallation(t.Context(), InstallationInput{
				Mode:            "fresh_install",
				Email:           "owner@example.test",
				DisplayName:     "Owner",
				Password:        test.password,
				ExternalBaseURL: "https://fullpro.example.test",
			})
			if test.wantError && err == nil {
				t.Fatal("three-character administrator password was accepted")
			}
			if !test.wantError && err != nil {
				t.Fatalf("four-character administrator password was rejected: %v", err)
			}
		})
	}
}

func TestInstallCreatesIndependentAdminAndClosesInstallRoute(t *testing.T) {
	store := newTestStore(t)
	smtpTested := false
	app := NewApp(store, Config{
		CookieName:         "fullpro_test_session",
		InstallCookieName:  "fullpro_test_install",
		AllowedOrigins:     []string{"http://localhost:5173"},
		AdminAllowedCIDRs:  []string{"127.0.0.1/32", "::1/128"},
		TokenDerivationKey: []byte("0123456789abcdef0123456789abcdef"),
		SMTPTester: func(_ context.Context, input SMTPTestInput) error {
			smtpTested = input.Host == "smtp.example.com" && input.Recipient == "owner@example.com"
			return nil
		},
	})
	handler := app.Routes()

	session := postInstallSession(t, handler)
	if session.Code != http.StatusCreated {
		t.Fatalf("install session = %d %s", session.Code, session.Body.String())
	}
	installCookie := responseCookie(t, session, "fullpro_test_install")
	var sessionBody struct {
		Data struct {
			CSRFToken string `json:"csrfToken"`
			Mode      string `json:"mode"`
			ExpiresAt string `json:"expiresAt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(session.Body.Bytes(), &sessionBody); err != nil {
		t.Fatalf("decode install session: %v", err)
	}
	if sessionBody.Data.CSRFToken == "" {
		t.Fatalf("install session omitted csrf token: %s", session.Body.String())
	}
	if sessionBody.Data.Mode != "fresh_install" || sessionBody.Data.ExpiresAt == "" {
		t.Fatalf("install session contract = %#v", sessionBody.Data)
	}

	preflightReq := httptest.NewRequest(http.MethodPost, "/install/api/v1/preflight", strings.NewReader(`{}`))
	preflightReq.RemoteAddr = "127.0.0.1:1234"
	preflightReq.Header.Set("Content-Type", "application/json")
	preflightReq.Header.Set("Origin", "http://localhost:5173")
	preflightReq.Header.Set("X-CSRF-Token", sessionBody.Data.CSRFToken)
	preflightReq.AddCookie(installCookie)
	preflight := httptest.NewRecorder()
	handler.ServeHTTP(preflight, preflightReq)
	if preflight.Code != http.StatusOK || !strings.Contains(preflight.Body.String(), `"checks"`) || !strings.Contains(preflight.Body.String(), `"label":"database"`) {
		t.Fatalf("install preflight = %d %s", preflight.Code, preflight.Body.String())
	}
	smtpRequest := httptest.NewRequest(http.MethodPost, "/install/api/v1/smtp-test", strings.NewReader(`{"host":"smtp.example.com","port":587,"tls":"starttls","from":"fullpro@example.com","username":"mailer","password":"secret","recipient":"owner@example.com"}`))
	smtpRequest.RemoteAddr = "127.0.0.1:1234"
	smtpRequest.Header.Set("Content-Type", "application/json")
	smtpRequest.Header.Set("Origin", "http://localhost:5173")
	smtpRequest.Header.Set("X-CSRF-Token", sessionBody.Data.CSRFToken)
	smtpRequest.AddCookie(installCookie)
	smtpResponse := httptest.NewRecorder()
	handler.ServeHTTP(smtpResponse, smtpRequest)
	if smtpResponse.Code != http.StatusOK || !smtpTested {
		t.Fatalf("smtp test = %d %s tested=%v", smtpResponse.Code, smtpResponse.Body.String(), smtpTested)
	}

	completeReq := httptest.NewRequest(http.MethodPost, "/install/api/v1/complete", strings.NewReader(`{
		"mode":"fresh_install",
		"admin":{"email":"owner@example.com","displayName":"Owner","password":"correct horse battery staple"},
		"publicApi":{"baseUrl":"https://fullpro.example","extensionIds":["abcdefghijklmnopabcdefghijklmnop"],"webOrigins":["http://localhost:5173"]},
		"registration":{"enabled":false},
		"smtp":null,
		"limits":{"maxUsers":100,"profileBytes":524288,"storageBytes":536870912,"versionsPerUser":50,"accessLogDays":30,"auditLogDays":180,"backupDirectory":"/data/backups"}
	}`))
	completeReq.RemoteAddr = "127.0.0.1:1234"
	completeReq.Header.Set("Content-Type", "application/json")
	completeReq.Header.Set("Origin", "http://localhost:5173")
	completeReq.Header.Set("X-CSRF-Token", sessionBody.Data.CSRFToken)
	completeReq.AddCookie(installCookie)
	complete := httptest.NewRecorder()
	handler.ServeHTTP(complete, completeReq)
	if complete.Code != http.StatusCreated {
		t.Fatalf("install complete = %d %s", complete.Code, complete.Body.String())
	}
	pluginPreflightRequest := httptest.NewRequest(http.MethodOptions, "/api/v1/me", nil)
	pluginPreflightRequest.RemoteAddr = "127.0.0.1:1234"
	pluginPreflightRequest.Header.Set("Origin", "chrome-extension://abcdefghijklmnopabcdefghijklmnop")
	pluginPreflight := httptest.NewRecorder()
	handler.ServeHTTP(pluginPreflight, pluginPreflightRequest)
	if pluginPreflight.Code != http.StatusNoContent {
		t.Fatalf("installed extension CORS preflight = %d %s, want 204 without restart", pluginPreflight.Code, pluginPreflight.Body.String())
	}
	closed := get(t, handler, "/install/api/v1/status")
	if closed.Code != http.StatusNotFound {
		t.Fatalf("completed install route = %d %s, want 404", closed.Code, closed.Body.String())
	}
	closedSession := postInstallSession(t, handler)
	if closedSession.Code != http.StatusNotFound {
		t.Fatalf("completed install session = %d %s, want 404", closedSession.Code, closedSession.Body.String())
	}

	preauth := get(t, handler, "/api/admin/v1/auth/session")
	if preauth.Code != http.StatusOK {
		t.Fatalf("admin preauth session = %d %s", preauth.Code, preauth.Body.String())
	}
	preauthCookie := responseCookie(t, preauth, "fullpro_test_preauth")
	var preauthBody struct {
		Data struct {
			CSRFToken string `json:"csrfToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(preauth.Body.Bytes(), &preauthBody); err != nil {
		t.Fatalf("decode preauth session: %v", err)
	}
	if !strings.Contains(preauth.Body.String(), `"authenticated":false`) {
		t.Fatalf("preauth session omitted authenticated=false: %s", preauth.Body.String())
	}
	loginReq := httptest.NewRequest(http.MethodPost, "/api/admin/v1/auth/login", strings.NewReader(`{"email":"owner@example.com","password":"correct horse battery staple"}`))
	loginReq.RemoteAddr = "127.0.0.1:1234"
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("Origin", "http://localhost:5173")
	loginReq.Header.Set("X-CSRF-Token", preauthBody.Data.CSRFToken)
	loginReq.AddCookie(preauthCookie)
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, loginReq)
	if login.Code != http.StatusOK {
		t.Fatalf("admin login = %d %s", login.Code, login.Body.String())
	}
	if !strings.Contains(login.Body.String(), `"user":{"id":`) || strings.Contains(login.Body.String(), `"admin":`) {
		t.Fatalf("admin login contract = %s", login.Body.String())
	}
	if cookie := responseCookie(t, login, "fullpro_test_session"); !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("admin cookie flags = %#v", cookie)
	}
	adminCookie := responseCookie(t, login, "fullpro_test_session")
	authenticatedSession := get(t, handler, "/api/admin/v1/auth/session", adminCookie)
	if authenticatedSession.Code != http.StatusOK || !strings.Contains(authenticatedSession.Body.String(), `"user":{"id":`) || !strings.Contains(authenticatedSession.Body.String(), `"authenticated":true`) {
		t.Fatalf("authenticated admin session = %d %s", authenticatedSession.Code, authenticatedSession.Body.String())
	}
	var authenticatedBody struct {
		Data struct {
			CSRFToken string `json:"csrfToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(authenticatedSession.Body.Bytes(), &authenticatedBody); err != nil {
		t.Fatalf("decode authenticated session: %v", err)
	}
	missingCSRF := postJSON(t, handler, "/api/admin/v1/auth/logout", `{}`, adminCookie)
	if missingCSRF.Code != http.StatusForbidden {
		t.Fatalf("admin logout without origin/csrf = %d %s", missingCSRF.Code, missingCSRF.Body.String())
	}
	logoutRequest := httptest.NewRequest(http.MethodPost, "/api/admin/v1/auth/logout", strings.NewReader(`{}`))
	logoutRequest.RemoteAddr = "127.0.0.1:1234"
	logoutRequest.Header.Set("Content-Type", "application/json")
	logoutRequest.Header.Set("Origin", "http://localhost:5173")
	logoutRequest.Header.Set("X-CSRF-Token", authenticatedBody.Data.CSRFToken)
	logoutRequest.AddCookie(adminCookie)
	logout := httptest.NewRecorder()
	handler.ServeHTTP(logout, logoutRequest)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("admin logout = %d %s", logout.Code, logout.Body.String())
	}
	var auditRows int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM admin_audit_logs WHERE action = 'auth.logout'`).Scan(&auditRows); err != nil || auditRows != 1 {
		t.Fatalf("logout audit rows=%d err=%v", auditRows, err)
	}
}

func TestTrustedProxyWalksForwardedChainFromRightToLeft(t *testing.T) {
	store := newTestStore(t)
	app := NewApp(store, Config{
		TrustedProxyCIDRs: []string{"10.0.0.0/8", "192.0.2.0/24"},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/admin/v1/overview", nil)
	req.RemoteAddr = "10.0.0.5:443"
	req.Header.Set("X-Forwarded-For", "127.0.0.1, 198.51.100.7, 192.0.2.9")
	ip, ok := app.clientIP(req)
	if !ok {
		t.Fatal("clientIP did not parse a valid trusted proxy chain")
	}
	if got := ip.String(); got != "198.51.100.7" {
		t.Fatalf("client ip = %s, want first untrusted hop 198.51.100.7", got)
	}
}

func TestTrustedProxyFailsClosedOnMissingOrMalformedClientHeaders(t *testing.T) {
	store := newTestStore(t)
	app := NewApp(store, Config{
		TrustedProxyCIDRs: []string{"127.0.0.1/32"},
	})
	for name, forwardedFor := range map[string]string{
		"missing":   "",
		"malformed": "not-an-ip, 192.168.1.8",
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/admin", nil)
			request.RemoteAddr = "127.0.0.1:8080"
			request.Header.Set("X-Forwarded-For", forwardedFor)
			request.Header.Set("X-Forwarded-Proto", "https")
			if ip, ok := app.clientIP(request); ok {
				t.Fatalf("trusted proxy %s client header resolved to %v; want fail closed", name, ip)
			}
			response := httptest.NewRecorder()
			app.Routes().ServeHTTP(response, request)
			if response.Code != http.StatusNotFound {
				t.Fatalf("trusted proxy %s client header reached admin: %d %s", name, response.Code, response.Body.String())
			}
		})
	}
}

func TestDefaultProxyPolicyIgnoresForwardingHeadersAndAllowsPrivateAdminNetworks(t *testing.T) {
	store := newTestStore(t)
	app := NewApp(store, Config{})

	spoofed := httptest.NewRequest(http.MethodGet, "/api/admin/v1/overview", nil)
	spoofed.RemoteAddr = "127.0.0.1:8787"
	spoofed.Header.Set("X-Forwarded-For", "203.0.113.77")
	ip, ok := app.clientIP(spoofed)
	if !ok || ip.String() != "127.0.0.1" {
		t.Fatalf("default trusted proxy policy accepted XFF: ip=%v ok=%v", ip, ok)
	}

	for _, remote := range []string{
		"10.1.2.3:9000",
		"172.16.5.4:9000",
		"192.168.8.9:9000",
		"[fd00::8]:9000",
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/admin/v1/overview", nil)
		req.RemoteAddr = remote
		if !app.isAdminNetworkRequest(req) {
			t.Fatalf("default admin CIDRs rejected private address %s", remote)
		}
	}
}

func TestAdminTransportRequiresHTTPSOutsideLoopbackAndTrustsOnlyConfiguredProxy(t *testing.T) {
	store := newTestStore(t)
	app := NewApp(store, Config{
		AdminAllowedCIDRs: []string{"127.0.0.1/32", "192.168.0.0/16"},
		TrustedProxyCIDRs: []string{"10.0.0.0/8", "127.0.0.1/32"},
	})
	handler := app.Routes()

	lanHTTP := httptest.NewRequest(http.MethodGet, "/admin", nil)
	lanHTTP.RemoteAddr = "192.168.1.9:8787"
	lanResponse := httptest.NewRecorder()
	handler.ServeHTTP(lanResponse, lanHTTP)
	if lanResponse.Code != http.StatusUpgradeRequired {
		t.Fatalf("LAN HTTP admin = %d %s, want 426", lanResponse.Code, lanResponse.Body.String())
	}

	loopback := httptest.NewRequest(http.MethodGet, "/admin", nil)
	loopback.RemoteAddr = "127.0.0.1:8787"
	loopback.Header.Set("X-Forwarded-For", "127.0.0.1")
	loopbackResponse := httptest.NewRecorder()
	handler.ServeHTTP(loopbackResponse, loopback)
	if loopbackResponse.Code != http.StatusOK {
		t.Fatalf("loopback HTTP admin = %d %s", loopbackResponse.Code, loopbackResponse.Body.String())
	}

	proxiedHTTPS := httptest.NewRequest(http.MethodGet, "/admin", nil)
	proxiedHTTPS.RemoteAddr = "10.0.0.5:443"
	proxiedHTTPS.Header.Set("X-Forwarded-For", "192.168.1.9")
	proxiedHTTPS.Header.Set("X-Forwarded-Proto", "https")
	proxiedResponse := httptest.NewRecorder()
	handler.ServeHTTP(proxiedResponse, proxiedHTTPS)
	if proxiedResponse.Code != http.StatusOK {
		t.Fatalf("trusted proxy HTTPS admin = %d %s", proxiedResponse.Code, proxiedResponse.Body.String())
	}
	loopbackProxyHTTP := httptest.NewRequest(http.MethodGet, "/admin", nil)
	loopbackProxyHTTP.RemoteAddr = "127.0.0.1:8080"
	loopbackProxyHTTP.Header.Set("X-Forwarded-For", "192.168.1.9")
	loopbackProxyHTTP.Header.Set("X-Forwarded-Proto", "http")
	loopbackProxyResponse := httptest.NewRecorder()
	handler.ServeHTTP(loopbackProxyResponse, loopbackProxyHTTP)
	if loopbackProxyResponse.Code != http.StatusUpgradeRequired {
		t.Fatalf("loopback proxy HTTP for LAN client = %d %s, want 426", loopbackProxyResponse.Code, loopbackProxyResponse.Body.String())
	}
}

func TestTrustedProxyHTTPSAlwaysSetsSecureInstallCookie(t *testing.T) {
	store := newTestStore(t)
	app := NewApp(store, Config{
		InstallCookieName: "fullpro_test_install",
		CookieSecure:      false,
		AdminAllowedCIDRs: []string{"192.168.0.0/16"},
		TrustedProxyCIDRs: []string{"10.0.0.0/8"},
	})
	request := httptest.NewRequest(http.MethodPost, "/install/api/v1/session", strings.NewReader(`{}`))
	request.RemoteAddr = "10.0.0.5:443"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Forwarded-For", "192.168.1.9")
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("Origin", "https://example.com")
	response := httptest.NewRecorder()
	app.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("proxied HTTPS install session = %d %s", response.Code, response.Body.String())
	}
	if cookie := responseCookie(t, response, "fullpro_test_install"); !cookie.Secure {
		t.Fatalf("proxied HTTPS install cookie missing Secure: %#v", cookie)
	}
}

func TestAdminAssetsUseNetworkGateAndSPARoutesFallBackToIndex(t *testing.T) {
	app, _ := newTestApp(t)
	handler := app.Routes()

	remoteAsset := httptest.NewRequest(http.MethodGet, "/admin/assets/admin.js", nil)
	remoteAsset.RemoteAddr = "203.0.113.8:9000"
	remoteResponse := httptest.NewRecorder()
	handler.ServeHTTP(remoteResponse, remoteAsset)
	if remoteResponse.Code != http.StatusNotFound {
		t.Fatalf("remote admin asset = %d %s, want 404", remoteResponse.Code, remoteResponse.Body.String())
	}

	deepLink := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	deepLink.RemoteAddr = "192.168.1.8:9000"
	deepResponse := httptest.NewRecorder()
	handler.ServeHTTP(deepResponse, deepLink)
	if deepResponse.Code != http.StatusOK || !strings.Contains(deepResponse.Body.String(), "<!doctype html>") {
		t.Fatalf("admin SPA deep link = %d %s", deepResponse.Code, deepResponse.Body.String())
	}
	installedShell := get(t, handler, "/install")
	if installedShell.Code != http.StatusNotFound {
		t.Fatalf("installed install shell = %d %s, want 404", installedShell.Code, installedShell.Body.String())
	}
	uninstalledStore := newTestStore(t)
	uninstalledApp := NewApp(uninstalledStore, Config{AllowInsecureAdminHTTP: true})
	installShell := get(t, uninstalledApp.Routes(), "/install")
	if installShell.Code != http.StatusOK || !strings.Contains(installShell.Body.String(), `<div id="app"></div>`) {
		t.Fatalf("uninitialized install shell = %d %s", installShell.Code, installShell.Body.String())
	}
}

func TestAdminResetPreservesUsersContentAndSettingsButRevokesSessions(t *testing.T) {
	store := newTestStore(t)
	secretsPath := filepath.Join(t.TempDir(), "secrets.json")
	secrets, _, err := LoadOrCreateSecrets(secretsPath)
	if err != nil {
		t.Fatalf("create reset secrets: %v", err)
	}
	secrets.SMTPPassword = "preserved-smtp-password"
	if err := SaveSecrets(secretsPath, secrets); err != nil {
		t.Fatalf("save reset secrets: %v", err)
	}
	for key, value := range map[string]string{
		"external_base_url":    "https://preserved.example.test",
		"allowed_origins":      "http://localhost:5173\nhttps://preserved-web.example.test",
		"registration_enabled": "true",
		"smtp_config":          `{"host":"smtp.preserved.example.test","port":587,"tls":"starttls","from":"mail@example.test","username":"mailer"}`,
	} {
		if _, err := store.db.Exec(`INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, value, nowString()); err != nil {
			t.Fatalf("seed runtime setting %s: %v", key, err)
		}
	}
	user, err := store.CreateUser(t.Context(), "legacy@example.com", "safe-password-123")
	if err != nil {
		t.Fatalf("seed legacy user: %v", err)
	}
	legacySession, err := store.CreateSession(t.Context(), user.ID, time.Hour)
	if err != nil {
		t.Fatalf("seed legacy session: %v", err)
	}
	if err := store.UpsertOfficialWallpaper(t.Context(), OfficialWallpaperRecord{
		ID: "wallpaper-keep", Title: "Keep", Category: "test", Enabled: true,
		Tags: []string{}, Variants: []WallpaperVariantRecord{},
	}); err != nil {
		t.Fatalf("seed content: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('preserve_me', 'yes', ?)`, nowString()); err != nil {
		t.Fatalf("seed setting: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE installation_state SET state = 'requires_admin_reset', updated_at = ? WHERE id = 1`, nowString()); err != nil {
		t.Fatalf("set reset state: %v", err)
	}

	runtimeSettings, err := store.LoadRuntimeSettings(t.Context())
	if err != nil {
		t.Fatalf("load reset runtime settings: %v", err)
	}
	config := ApplyRuntimeSettings(Config{
		CookieName:        "fullpro_test_session",
		InstallCookieName: "fullpro_test_install",
		SecretsPath:       secretsPath,
	}, runtimeSettings, secrets, RuntimeOverrides{})
	app := NewApp(store, config)
	handler := app.Routes()
	session := postInstallSession(t, handler)
	if !strings.Contains(session.Body.String(), `"mode":"admin_reset"`) {
		t.Fatalf("admin reset session = %d %s", session.Code, session.Body.String())
	}
	installCookie := responseCookie(t, session, "fullpro_test_install")
	var body struct {
		Data struct {
			CSRFToken string `json:"csrfToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(session.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode reset session: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/install/api/v1/complete", strings.NewReader(`{
		"mode":"admin_reset",
		"admin":{"email":"new-owner@example.com","displayName":"New Owner","password":"correct horse battery staple"},
		"publicApi":{"baseUrl":"","extensionIds":[],"webOrigins":[]},
		"registration":{"enabled":false},"smtp":null,"limits":{}
	}`))
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://localhost:5173")
	request.Header.Set("X-CSRF-Token", body.Data.CSRFToken)
	request.AddCookie(installCookie)
	response := httptest.NewRecorder()
	app.settingsMu.Lock()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, request)
		close(done)
	}()
	select {
	case <-done:
		app.settingsMu.Unlock()
		t.Fatalf("admin reset completed while settings mutation lock was held: %d %s", response.Code, response.Body.String())
	case <-time.After(150 * time.Millisecond):
	}
	app.settingsMu.Unlock()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("admin reset did not resume after settings mutation lock release")
	}
	if response.Code != http.StatusCreated {
		t.Fatalf("admin reset complete = %d %s", response.Code, response.Body.String())
	}
	registrationOpen, publicBaseURL, mailer := app.runtimeAuthSettings()
	smtpMailer, smtpOK := mailer.(SMTPMailer)
	if !registrationOpen || publicBaseURL != "https://preserved.example.test" || !smtpOK || smtpMailer.Settings.Host != "smtp.preserved.example.test" || smtpMailer.Password != "preserved-smtp-password" {
		t.Fatalf("admin reset runtime was not preserved: registration=%t public=%q mailer=%#v", registrationOpen, publicBaseURL, mailer)
	}
	corsRequest := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/login", nil)
	corsRequest.RemoteAddr = "127.0.0.1:1234"
	corsRequest.Header.Set("Origin", "https://preserved-web.example.test")
	corsRequest.Header.Set("Access-Control-Request-Headers", "Content-Type")
	corsResponse := httptest.NewRecorder()
	handler.ServeHTTP(corsResponse, corsRequest)
	if corsResponse.Code != http.StatusNoContent || corsResponse.Header().Get("Access-Control-Allow-Origin") != "https://preserved-web.example.test" {
		t.Fatalf("admin reset did not preserve runtime CORS: %d %#v", corsResponse.Code, corsResponse.Header())
	}

	if _, err := store.AuthenticateUser(t.Context(), "legacy@example.com", "safe-password-123"); err != nil {
		t.Fatalf("admin reset removed legacy user: %v", err)
	}
	if _, err := store.UserBySession(t.Context(), legacySession); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("legacy session survived admin reset: %v", err)
	}
	var setting string
	if err := store.db.QueryRow(`SELECT value FROM settings WHERE key = 'preserve_me'`).Scan(&setting); err != nil || setting != "yes" {
		t.Fatalf("preserved setting = %q err=%v", setting, err)
	}
	wallpapers, err := store.ListAdminOfficialWallpapers(t.Context())
	if err != nil || len(wallpapers) != 1 || wallpapers[0].ID != "wallpaper-keep" {
		t.Fatalf("preserved content = %#v err=%v", wallpapers, err)
	}
}

func TestLegacyUserMigrationDoesNotFabricateEmailVerification(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE users (
		id TEXT PRIMARY KEY, email TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL,
		role TEXT NOT NULL, created_at TEXT NOT NULL, last_login_at TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("create legacy users: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id,email,password_hash,role,created_at,last_login_at)
		VALUES ('legacy-user','legacy@example.com','unused','user','2025-01-01T00:00:00Z','')`); err != nil {
		t.Fatalf("insert legacy user: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := OpenStore(path)
	if err != nil {
		t.Fatalf("migrate legacy db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var status, verifiedAt string
	if err := store.db.QueryRow(`SELECT status, email_verified_at FROM users WHERE id = 'legacy-user'`).Scan(&status, &verifiedAt); err != nil {
		t.Fatalf("read migrated legacy user: %v", err)
	}
	if status != "legacy_unverified" || verifiedAt != "" {
		t.Fatalf("legacy verification status=%q verifiedAt=%q", status, verifiedAt)
	}
}

func TestSecurityHeadersCoverAdminAssetsAndAPI(t *testing.T) {
	app, _ := newTestApp(t)
	handler := app.Routes()
	for _, path := range []string{
		"/admin",
		"/admin/assets/admin-PvWmwkFl.js",
		"/api/app/bootstrap",
	} {
		response := get(t, handler, path)
		if response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s missing nosniff", path)
		}
		if response.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatalf("%s missing no-referrer", path)
		}
		if response.Header().Get("X-Frame-Options") != "DENY" || !strings.Contains(response.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatalf("%s missing frame protection: %#v", path, response.Header())
		}
		if !strings.Contains(response.Header().Get("Content-Security-Policy"), "frame-src 'self'") {
			t.Fatalf("%s blocks the same-origin style preview frame: %q", path, response.Header().Get("Content-Security-Policy"))
		}
		if response.Header().Get("Permissions-Policy") == "" {
			t.Fatalf("%s missing permissions policy", path)
		}
	}
}

func TestInstallCompleteStoresSMTPPasswordOnlyInSecrets(t *testing.T) {
	store := newTestStore(t)
	secretsPath := filepath.Join(t.TempDir(), "secrets.json")
	if _, _, err := LoadOrCreateSecrets(secretsPath); err != nil {
		t.Fatalf("create secrets: %v", err)
	}
	app := NewApp(store, Config{
		CookieName: "fullpro_test_session", InstallCookieName: "fullpro_test_install",
		SecretsPath:    secretsPath,
		AllowedOrigins: []string{"http://localhost:5173"},
		SMTPTester: func(_ context.Context, input SMTPTestInput) error {
			if input.Recipient != "owner@example.com" {
				t.Fatalf("SMTP test recipient = %q", input.Recipient)
			}
			return nil
		},
	})
	handler := app.Routes()
	session := postInstallSession(t, handler)
	installCookie := responseCookie(t, session, "fullpro_test_install")
	var sessionBody struct {
		Data struct {
			CSRFToken string `json:"csrfToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(session.Body.Bytes(), &sessionBody); err != nil {
		t.Fatalf("decode install session: %v", err)
	}
	completePayload := `{
		"mode":"fresh_install",
		"admin":{"email":"owner@example.com","displayName":"Owner","password":"correct horse battery staple"},
		"publicApi":{"baseUrl":"https://fullpro.example","extensionIds":["abcdefghijklmnopabcdefghijklmnop"],"webOrigins":["http://localhost:5173"]},
		"registration":{"enabled":true},
		"smtp":{"host":"smtp.example.com","port":587,"tls":"starttls","from":"fullpro@example.com","username":"mailer","password":"smtp-secret"},
		"limits":{"maxUsers":100,"profileBytes":524288,"storageBytes":1073741824,"versionsPerUser":50,"accessLogDays":30,"auditLogDays":180,"backupDirectory":"/data/backups"}
	}`
	installPost := func(path, payload string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(payload))
		request.RemoteAddr = "127.0.0.1:1234"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "http://localhost:5173")
		request.Header.Set("X-CSRF-Token", sessionBody.Data.CSRFToken)
		request.AddCookie(installCookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	skipped := installPost("/install/api/v1/complete", completePayload)
	if skipped.Code != http.StatusPreconditionFailed || !strings.Contains(skipped.Body.String(), `"code":"SMTP_TEST_REQUIRED"`) {
		t.Fatalf("registration without SMTP test = %d %s, want 412 SMTP_TEST_REQUIRED", skipped.Code, skipped.Body.String())
	}

	smtpPayload := `{"host":"smtp.example.com","port":587,"tls":"starttls","from":"fullpro@example.com","username":"mailer","password":"smtp-secret","recipient":"owner@example.com"}`
	if tested := installPost("/install/api/v1/smtp-test", smtpPayload); tested.Code != http.StatusOK {
		t.Fatalf("SMTP test = %d %s", tested.Code, tested.Body.String())
	}
	changedHost := strings.Replace(completePayload, "smtp.example.com", "smtp-b.example.com", 1)
	if changed := installPost("/install/api/v1/complete", changedHost); changed.Code != http.StatusPreconditionFailed {
		t.Fatalf("complete with SMTP config B after testing A = %d %s, want 412", changed.Code, changed.Body.String())
	}
	changedPassword := strings.Replace(completePayload, "smtp-secret", "different-secret", 1)
	if changed := installPost("/install/api/v1/complete", changedPassword); changed.Code != http.StatusPreconditionFailed {
		t.Fatalf("complete with changed SMTP password = %d %s, want 412", changed.Code, changed.Body.String())
	}
	if _, err := store.db.Exec(`UPDATE install_sessions SET smtp_verified_at = ?`, time.Now().UTC().Add(-31*time.Minute).Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("expire SMTP verification: %v", err)
	}
	if expired := installPost("/install/api/v1/complete", completePayload); expired.Code != http.StatusPreconditionFailed {
		t.Fatalf("complete with expired SMTP verification = %d %s, want 412", expired.Code, expired.Body.String())
	}
	if tested := installPost("/install/api/v1/smtp-test", smtpPayload); tested.Code != http.StatusOK {
		t.Fatalf("repeat SMTP test = %d %s", tested.Code, tested.Body.String())
	}

	response := installPost("/install/api/v1/complete", completePayload)
	if response.Code != http.StatusCreated {
		t.Fatalf("install complete = %d %s", response.Code, response.Body.String())
	}
	secrets, _, err := LoadOrCreateSecrets(secretsPath)
	if err != nil || secrets.SMTPPassword != "smtp-secret" {
		t.Fatalf("SMTP secret = %q err=%v", secrets.SMTPPassword, err)
	}
	var smtpConfig string
	if err := store.db.QueryRow(`SELECT value FROM settings WHERE key = 'smtp_config'`).Scan(&smtpConfig); err != nil {
		t.Fatalf("read smtp config: %v", err)
	}
	if strings.Contains(smtpConfig, "smtp-secret") {
		t.Fatalf("SMTP password leaked into database settings: %s", smtpConfig)
	}
}

func TestInstallSecretFailureLeavesDatabaseRetryable(t *testing.T) {
	store := newTestStore(t)
	secretsPath := filepath.Join(t.TempDir(), "secrets.json")
	if err := os.Mkdir(secretsPath, 0o700); err != nil {
		t.Fatalf("create blocking secrets directory: %v", err)
	}
	app := NewApp(store, Config{
		InstallCookieName: "fullpro_test_install",
		SecretsPath:       secretsPath,
		AllowedOrigins:    []string{"http://localhost:5173"},
	})
	handler := app.Routes()
	session := postInstallSession(t, handler)
	cookie := responseCookie(t, session, "fullpro_test_install")
	var sessionBody struct {
		Data struct {
			CSRFToken string `json:"csrfToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(session.Body.Bytes(), &sessionBody); err != nil {
		t.Fatalf("decode install session: %v", err)
	}
	payload := `{
		"mode":"fresh_install",
		"admin":{"email":"owner@example.com","displayName":"Owner","password":"correct horse battery staple"},
		"publicApi":{"baseUrl":"https://fullpro.example","webOrigins":["http://localhost:5173"]},
		"registration":{"enabled":false},
		"smtp":{"host":"smtp.example.com","port":587,"tls":"starttls","from":"fullpro@example.com","username":"mailer","password":"smtp-secret"},
		"limits":{}
	}`
	complete := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/install/api/v1/complete", strings.NewReader(payload))
		request.RemoteAddr = "127.0.0.1:1234"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "http://localhost:5173")
		request.Header.Set("X-CSRF-Token", sessionBody.Data.CSRFToken)
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	failed := complete()
	if failed.Code != http.StatusInternalServerError {
		t.Fatalf("install with unwritable secrets = %d %s, want 500", failed.Code, failed.Body.String())
	}
	state, err := store.InstallationState(t.Context())
	if err != nil || state != "uninitialized" {
		t.Fatalf("state after secrets failure = %q err=%v, want uninitialized", state, err)
	}
	var adminCount, settingCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM admin_users`).Scan(&adminCount); err != nil {
		t.Fatalf("count admins: %v", err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM settings`).Scan(&settingCount); err != nil {
		t.Fatalf("count settings: %v", err)
	}
	if adminCount != 0 || settingCount != 0 {
		t.Fatalf("failed install persisted admin/settings: admins=%d settings=%d", adminCount, settingCount)
	}
	if err := os.Remove(secretsPath); err != nil {
		t.Fatalf("remove blocking secrets directory: %v", err)
	}
	if retried := complete(); retried.Code != http.StatusCreated {
		t.Fatalf("retry install = %d %s, want 201", retried.Code, retried.Body.String())
	}
}

func TestConcurrentInstallCompletionCannotRollbackWinningSMTPSecret(t *testing.T) {
	store := newTestStore(t)
	secretsPath := filepath.Join(t.TempDir(), "secrets.json")
	if _, _, err := LoadOrCreateSecrets(secretsPath); err != nil {
		t.Fatalf("create secrets: %v", err)
	}
	app := NewApp(store, Config{
		InstallCookieName: "fullpro_test_install",
		SecretsPath:       secretsPath,
		AllowedOrigins:    []string{"http://localhost:5173"},
	})
	firstAtCommit := make(chan struct{})
	releaseFirst := make(chan struct{})
	var hookCalls atomic.Int32
	app.beforeInstallCommit = func() {
		if hookCalls.Add(1) == 1 {
			close(firstAtCommit)
			<-releaseFirst
		}
	}
	handler := app.Routes()
	type auth struct {
		cookie *http.Cookie
		csrf   string
	}
	newSession := func() auth {
		response := postInstallSession(t, handler)
		var body struct {
			Data struct {
				CSRFToken string `json:"csrfToken"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode install session: %v", err)
		}
		return auth{cookie: responseCookie(t, response, "fullpro_test_install"), csrf: body.Data.CSRFToken}
	}
	firstAuth, secondAuth := newSession(), newSession()
	payload := func(host, password string) string {
		return `{"mode":"fresh_install","admin":{"email":"owner@example.com","displayName":"Owner","password":"correct horse battery staple"},` +
			`"publicApi":{"baseUrl":"https://fullpro.example","webOrigins":["http://localhost:5173"]},"registration":{"enabled":false},` +
			`"smtp":{"host":"` + host + `","port":587,"tls":"starttls","from":"fullpro@example.com","username":"mailer","password":"` + password + `"},"limits":{}}`
	}
	type result struct {
		name     string
		response *httptest.ResponseRecorder
	}
	results := make(chan result, 2)
	complete := func(name string, authentication auth, body string) {
		request := httptest.NewRequest(http.MethodPost, "/install/api/v1/complete", strings.NewReader(body))
		request.RemoteAddr = "127.0.0.1:1234"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "http://localhost:5173")
		request.Header.Set("X-CSRF-Token", authentication.csrf)
		request.AddCookie(authentication.cookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		results <- result{name: name, response: response}
	}
	go complete("first", firstAuth, payload("smtp-first.example.com", "secret-first"))
	<-firstAtCommit
	go complete("second", secondAuth, payload("smtp-second.example.com", "secret-second"))
	collected := make([]result, 0, 2)
	select {
	case early := <-results:
		// Without serialization the second request can commit while the first is paused.
		collected = append(collected, early)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)
	for len(collected) < 2 {
		collected = append(collected, <-results)
	}
	successes := 0
	for _, item := range collected {
		if item.response.Code == http.StatusCreated {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent install results = %#v, want exactly one success", collected)
	}
	var smtpRaw string
	if err := store.db.QueryRow(`SELECT value FROM settings WHERE key = 'smtp_config'`).Scan(&smtpRaw); err != nil {
		t.Fatalf("read winning SMTP config: %v", err)
	}
	secrets, _, err := LoadOrCreateSecrets(secretsPath)
	if err != nil {
		t.Fatalf("read winning SMTP secret: %v", err)
	}
	if strings.Contains(smtpRaw, "smtp-first.example.com") && secrets.SMTPPassword != "secret-first" {
		t.Fatalf("first DB config won but secret = %q", secrets.SMTPPassword)
	}
	if strings.Contains(smtpRaw, "smtp-second.example.com") && secrets.SMTPPassword != "secret-second" {
		t.Fatalf("second DB config won but secret = %q", secrets.SMTPPassword)
	}
}
