package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fullpro-backend/internal/server"
)

func TestOpenRecoveredStoreRejectsPendingRestoreBeforeOpeningDatabase(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "missing-live.db")
	secretsPath := filepath.Join(directory, "secrets.json")
	if err := os.WriteFile(databasePath+".restore-intent.json", []byte(`{"version":1,"liveDatabase":"wrong.db"}`), 0o600); err != nil {
		t.Fatalf("write invalid pending restore: %v", err)
	}
	store, err := openRecoveredStore(databasePath, secretsPath)
	if store != nil {
		_ = store.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "pending restore") {
		t.Fatalf("open recovered store err=%v", err)
	}
	if _, statErr := os.Stat(databasePath); !os.IsNotExist(statErr) {
		t.Fatalf("database was opened before restore recovery: %v", statErr)
	}
}

func TestPrepareDevelopmentUsesIsolatedLoopbackData(t *testing.T) {
	directory := t.TempDir()
	store, config, credentials, err := prepareDevelopment(directory)
	if err != nil {
		t.Fatalf("prepare local development service: %v", err)
	}
	defer store.Close()

	if config.Addr != "127.0.0.1:8787" || !config.DevelopmentMode || config.CookieSecure || !config.AllowInsecureAdminHTTP {
		t.Fatalf("local development config = %#v", config)
	}
	if credentials.Password != "2231" {
		t.Fatalf("local development password = %q, want fixed test password", credentials.Password)
	}
	if _, err := os.Stat(filepath.Join(directory, "dev-credentials.json")); err != nil {
		t.Fatalf("development credentials missing: %v", err)
	}
	if _, err := store.AuthenticateAdmin(t.Context(), credentials.AdminEmail, credentials.Password); err != nil {
		t.Fatalf("authenticate prepared local admin: %v", err)
	}
	if _, err := store.AuthenticatePlugin(t.Context(), credentials.PluginEmail, credentials.Password); err != nil {
		t.Fatalf("authenticate prepared local plugin user: %v", err)
	}
}

func TestPrepareDevelopmentMigratesExistingLocalAccountsToFixedPassword(t *testing.T) {
	directory := t.TempDir()
	const oldPassword = "old-local-development-password-2026"
	store, err := server.OpenStore(filepath.Join(directory, "fullpro.db"))
	if err != nil {
		t.Fatalf("create legacy local development store: %v", err)
	}
	if err := store.EnsureDevelopmentInstallation(t.Context(), server.DevelopmentAccounts{
		AdminEmail: "admin@local.test", PluginEmail: "user@local.test", Password: oldPassword,
	}); err != nil {
		_ = store.Close()
		t.Fatalf("seed legacy local development store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close legacy local development store: %v", err)
	}
	legacyCredentials := []byte("{\n  \"adminEmail\": \"admin@local.test\",\n  \"pluginEmail\": \"user@local.test\",\n  \"password\": \"" + oldPassword + "\"\n}\n")
	if err := os.WriteFile(filepath.Join(directory, "dev-credentials.json"), legacyCredentials, 0o600); err != nil {
		t.Fatalf("write legacy local credentials: %v", err)
	}

	prepared, _, credentials, err := prepareDevelopment(directory)
	if err != nil {
		t.Fatalf("migrate local development service: %v", err)
	}
	defer prepared.Close()
	if credentials.Password != "2231" {
		t.Fatalf("migrated local development password = %q", credentials.Password)
	}
	if _, err := prepared.AuthenticateAdmin(t.Context(), credentials.AdminEmail, credentials.Password); err != nil {
		t.Fatalf("authenticate migrated local admin: %v", err)
	}
	if _, err := prepared.AuthenticatePlugin(t.Context(), credentials.PluginEmail, credentials.Password); err != nil {
		t.Fatalf("authenticate migrated local plugin user: %v", err)
	}
	if _, err := prepared.AuthenticateAdmin(t.Context(), credentials.AdminEmail, oldPassword); err == nil {
		t.Fatal("legacy local admin password still works")
	}
}

func TestEnvBoolRejectsInvalidSecurityConfiguration(t *testing.T) {
	t.Setenv("FULLPRO_COOKIE_SECURE", "sometimes")
	if _, err := envBool("FULLPRO_COOKIE_SECURE", false); err == nil {
		t.Fatal("invalid cookie security boolean was silently accepted")
	}
}

func TestRunAdminResetRevokesAccessAndCreatesOneTimeCode(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "fullpro.db")
	codePath := filepath.Join(directory, "admin-reset-code")
	store, err := server.OpenStore(databasePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := store.BeginInstallation(t.Context(), server.InstallationInput{Email: "owner@example.test", DisplayName: "Owner", Password: "strong-admin-password-2026", ExternalBaseURL: "https://sync.example.test"}); err != nil {
		t.Fatalf("install test service: %v", err)
	}
	if err := store.FinishInstallation(t.Context()); err != nil {
		t.Fatalf("finish installation: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	t.Setenv("FULLPRO_DB", databasePath)
	t.Setenv("FULLPRO_INSTALL_CODE_FILE", codePath)
	t.Setenv("FULLPRO_INSTALL_CODE", "")
	if err := runAdminReset(); err != nil {
		t.Fatalf("run admin reset: %v", err)
	}
	code, err := os.ReadFile(codePath)
	if err != nil || len(string(code)) < 32 {
		t.Fatalf("admin reset code file invalid: len=%d err=%v", len(string(code)), err)
	}
	reopened, err := server.OpenStore(databasePath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()
	if state, err := reopened.InstallationState(t.Context()); err != nil || state != "requires_admin_reset" {
		t.Fatalf("reset state=%q err=%v", state, err)
	}
}
