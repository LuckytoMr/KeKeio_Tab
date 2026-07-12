package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInstallationRuntimeSettingsSurviveStoreRestart(t *testing.T) {
	path := t.TempDir() + "/runtime-settings.db"
	store, err := OpenStore(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	_, err = store.BeginInstallation(t.Context(), InstallationInput{
		Mode:                "fresh_install",
		Email:               "owner@example.com",
		DisplayName:         "Owner",
		Password:            "correct horse battery staple",
		ExternalBaseURL:     "https://fullpro.example",
		AllowedOrigins:      []string{"chrome-extension://abcdefghijklmnopabcdefghijklmnop", "https://dev.example"},
		ExtensionIDs:        []string{"abcdefghijklmnopabcdefghijklmnop"},
		RegistrationEnabled: true,
		SMTP:                &SMTPSettings{Host: "smtp.example.com", Port: 587, TLS: "starttls", From: "fullpro@example.com", Username: "mailer"},
		Limits:              map[string]any{"maxUsers": float64(100), "profileBytes": float64(524288), "backupDirectory": "/data/backups"},
	})
	if err != nil {
		t.Fatalf("begin installation: %v", err)
	}
	if err := store.FinishInstallation(t.Context()); err != nil {
		t.Fatalf("finish installation: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := OpenStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	settings, err := reopened.LoadRuntimeSettings(t.Context())
	if err != nil {
		t.Fatalf("load runtime settings: %v", err)
	}
	if settings.PublicBaseURL != "https://fullpro.example" || !settings.RegistrationOpen {
		t.Fatalf("runtime settings = %#v", settings)
	}
	if len(settings.AllowedOrigins) != 2 || settings.SMTP == nil || settings.SMTP.Host != "smtp.example.com" || settings.SMTP.Port != 587 {
		t.Fatalf("runtime origins/smtp = %#v", settings)
	}
	if settings.Limits["backupDirectory"] != "/data/backups" {
		t.Fatalf("runtime limits = %#v", settings.Limits)
	}

	secrets := Secrets{TokenDerivationKey: []byte("0123456789abcdef0123456789abcdef"), CookieKey: []byte("abcdef0123456789abcdef0123456789"), SMTPPassword: "smtp-secret"}
	config := ApplyRuntimeSettings(Config{}, settings, secrets, RuntimeOverrides{})
	if config.PublicBaseURL != "https://fullpro.example" || !config.RegistrationOpen {
		t.Fatalf("applied runtime config = %#v", config)
	}
	if len(config.AllowedOrigins) != 2 {
		t.Fatalf("applied origins = %#v", config.AllowedOrigins)
	}
	mailer, ok := config.Mailer.(SMTPMailer)
	if !ok || mailer.Settings.Host != "smtp.example.com" || mailer.Password != "smtp-secret" {
		t.Fatalf("applied SMTP mailer = %#v", config.Mailer)
	}
	app := NewApp(reopened, config)
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/login", nil)
	request.Header.Set("Origin", "https://dev.example")
	request.Header.Set("Access-Control-Request-Headers", "Content-Type")
	response := httptest.NewRecorder()
	app.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Origin") != "https://dev.example" {
		t.Fatalf("persisted origin after restart = %d %#v", response.Code, response.Header())
	}
}

func TestRuntimeOverridesWinOverPersistedSettings(t *testing.T) {
	publicURL := "https://override.example"
	registration := false
	origins := []string{"chrome-extension://override"}
	config := ApplyRuntimeSettings(Config{}, RuntimeSettings{
		PublicBaseURL: "https://persisted.example", RegistrationOpen: true,
		AllowedOrigins: []string{"https://persisted.example"},
	}, Secrets{}, RuntimeOverrides{
		PublicBaseURL: &publicURL, RegistrationOpen: &registration, AllowedOrigins: &origins,
	})
	if config.PublicBaseURL != publicURL || config.RegistrationOpen || len(config.AllowedOrigins) != 1 || config.AllowedOrigins[0] != origins[0] {
		t.Fatalf("runtime overrides were not applied: %#v", config)
	}
}

func TestPasswordlessSMTPIsConsistentAfterInstallAndRestart(t *testing.T) {
	smtp := &SMTPSettings{Host: "smtp.internal.example", Port: 25, TLS: "none", From: "fullpro@example.test"}

	restarted := ApplyRuntimeSettings(Config{}, RuntimeSettings{SMTP: smtp}, Secrets{}, RuntimeOverrides{})
	restartedMailer, ok := restarted.Mailer.(SMTPMailer)
	if !ok || restartedMailer.Settings != *smtp || restartedMailer.Password != "" {
		t.Fatalf("passwordless SMTP after restart = %#v", restarted.Mailer)
	}

	app := NewApp(nil, Config{})
	app.applyInstalledRuntime(InstallationInput{SMTP: smtp}, "")
	installedMailer, ok := app.runtimeMailer().(SMTPMailer)
	if !ok || installedMailer.Settings != *smtp || installedMailer.Password != "" {
		t.Fatalf("passwordless SMTP immediately after install = %#v", app.runtimeMailer())
	}

	requiringPassword := *smtp
	requiringPassword.Username = "mailer"
	app.applyInstalledRuntime(InstallationInput{SMTP: &requiringPassword}, "")
	if app.runtimeMailer() != nil {
		t.Fatalf("credentialed SMTP must not activate without its password: %#v", app.runtimeMailer())
	}
}
