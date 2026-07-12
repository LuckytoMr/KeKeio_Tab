package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnsureDevelopmentInstallationCreatesReadyLocalAccounts(t *testing.T) {
	store := newTestStore(t)
	accounts := DevelopmentAccounts{
		AdminEmail:  "admin@local.test",
		PluginEmail: "user@local.test",
		Password:    "local-development-password-2026",
	}

	if err := store.EnsureDevelopmentInstallation(t.Context(), accounts); err != nil {
		t.Fatalf("initialize local development data: %v", err)
	}
	if state, err := store.InstallationState(t.Context()); err != nil || state != "installed" {
		t.Fatalf("installation state = %q, err = %v", state, err)
	}
	if _, err := store.AuthenticateAdmin(t.Context(), accounts.AdminEmail, accounts.Password); err != nil {
		t.Fatalf("authenticate local administrator: %v", err)
	}
	plugin, err := store.AuthenticatePlugin(t.Context(), accounts.PluginEmail, accounts.Password)
	if err != nil {
		t.Fatalf("authenticate local plugin user: %v", err)
	}
	pair, err := store.CreateTokenFamily(t.Context(), plugin.ID, "local-development-device")
	if err != nil {
		t.Fatalf("create local plugin token pair: %v", err)
	}
	if pair.Scope != AccessScopeFull || pair.RefreshToken == "" {
		t.Fatalf("local plugin token pair = %#v", pair)
	}
	settings, err := store.LoadRuntimeSettings(t.Context())
	if err != nil {
		t.Fatalf("load local development settings: %v", err)
	}
	if settings.PublicBaseURL != "http://127.0.0.1:8787" || settings.RegistrationOpen {
		t.Fatalf("local runtime settings = %#v", settings)
	}
}

func TestEnsureDevelopmentInstallationRejectsWeakPasswordWithoutLocalDevOptIn(t *testing.T) {
	store := newTestStore(t)
	err := store.EnsureDevelopmentInstallation(t.Context(), DevelopmentAccounts{
		AdminEmail: "admin@local.test", PluginEmail: "user@local.test", Password: "2231",
	})
	if err == nil {
		t.Fatal("weak password was accepted without the local development opt-in")
	}
}

func TestDevelopmentModeAllowsOnlyValidChromeExtensionOrigins(t *testing.T) {
	validOrigin := "chrome-extension://abcdefghijklmnopabcdefghijklmnop"
	request := httptest.NewRequest(http.MethodOptions, "http://127.0.0.1:8787/api/v1/auth/login", nil)
	request.Header.Set("Origin", validOrigin)
	request.RemoteAddr = "127.0.0.1:12345"

	development := NewApp(newTestStore(t), Config{DevelopmentMode: true})
	if !development.originAllowed(request) {
		t.Fatal("development mode rejected a valid Chrome extension origin")
	}
	production := NewApp(newTestStore(t), Config{})
	if production.originAllowed(request) {
		t.Fatal("production mode accepted an unconfigured Chrome extension origin")
	}

	request.Header.Set("Origin", "chrome-extension://not-a-chrome-extension-id")
	if development.originAllowed(request) {
		t.Fatal("development mode accepted an invalid Chrome extension origin")
	}
}
