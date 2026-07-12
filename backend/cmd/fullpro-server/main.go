package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"fullpro-backend/internal/server"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "admin-reset":
			if err := runAdminReset(); err != nil {
				log.Fatalf("admin reset: %v", err)
			}
		case "dev":
			if err := runDevelopment(); err != nil {
				log.Fatalf("local development server: %v", err)
			}
		default:
			log.Fatalf("unknown command %q; supported commands: admin-reset, dev", os.Args[1])
		}
		return
	}
	addr := env("FULLPRO_ADDR", ":9009")
	dbPath := env("FULLPRO_DB", "data/fullpro.db")
	dataDir := filepath.Dir(dbPath)
	secretsPath := env("FULLPRO_SECRETS_FILE", filepath.Join(dataDir, "secrets.json"))
	cookieSecure, err := envBool("FULLPRO_COOKIE_SECURE", false)
	if err != nil {
		log.Fatalf("parse FULLPRO_COOKIE_SECURE: %v", err)
	}

	store, err := openRecoveredStore(dbPath, secretsPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer store.Close()
	if err := store.SetBackupDirectoryOverride(os.Getenv("FULLPRO_BACKUP_DIRECTORY")); err != nil {
		log.Fatalf("configure backup directory: %v", err)
	}
	state, err := store.InstallationState(context.Background())
	if err != nil {
		log.Fatalf("read installation state: %v", err)
	}
	secrets, _, err := server.LoadOrCreateSecrets(secretsPath)
	if err != nil {
		log.Fatalf("load secrets: %v", err)
	}
	runtimeSettings, err := store.LoadRuntimeSettings(context.Background())
	if err != nil {
		log.Fatalf("load runtime settings: %v", err)
	}
	installCodePath := env("FULLPRO_INSTALL_CODE_FILE", filepath.Join(dataDir, "install-code"))
	installCode, installCodeCreated, err := server.EnsureInstallCode(installCodePath, os.Getenv("FULLPRO_INSTALL_CODE"), state)
	if err != nil {
		log.Fatalf("prepare install code: %v", err)
	}
	if installCodeCreated {
		log.Printf("one-time installation code: %s (also stored at %s)", installCode, installCodePath)
	}

	baseConfig := server.Config{
		Addr:                    addr,
		CookieName:              env("FULLPRO_COOKIE_NAME", "fullpro_session"),
		InstallCookieName:       env("FULLPRO_INSTALL_COOKIE_NAME", "fullpro_install"),
		InstallCode:             installCode,
		InstallCodePath:         installCodePath,
		CookieSecure:            cookieSecure,
		TokenDerivationKey:      secrets.TokenDerivationKey,
		SecretsPath:             secretsPath,
		MaxBodyBytes:            1 << 20,
		AdminAllowedCIDRs:       envList("FULLPRO_ADMIN_ALLOWED_CIDRS"),
		TrustedProxyCIDRs:       envList("FULLPRO_TRUSTED_PROXIES"),
		PasswordHashConcurrency: envInt("FULLPRO_PASSWORD_HASH_CONCURRENCY", 2),
		AuthRateLimit: server.RateLimitConfig{
			Limit:  envInt("FULLPRO_AUTH_RATE_LIMIT", 20),
			Window: time.Duration(envInt("FULLPRO_AUTH_RATE_WINDOW_SECONDS", 60)) * time.Second,
		},
	}
	overrides := server.RuntimeOverrides{}
	if value, ok := os.LookupEnv("FULLPRO_PUBLIC_BASE_URL"); ok {
		value = strings.TrimSpace(value)
		overrides.PublicBaseURL = &value
	}
	if raw, ok := os.LookupEnv("FULLPRO_REGISTRATION_OPEN"); ok {
		value, parseErr := strconv.ParseBool(strings.TrimSpace(raw))
		if parseErr != nil {
			log.Fatalf("parse FULLPRO_REGISTRATION_OPEN: %v", parseErr)
		}
		overrides.RegistrationOpen = &value
	}
	if raw, ok := os.LookupEnv("FULLPRO_API_ALLOWED_ORIGINS"); ok {
		values := parseEnvList(raw)
		overrides.AllowedOrigins = &values
	} else if raw, ok := os.LookupEnv("FULLPRO_ALLOWED_ORIGIN"); ok {
		values := parseEnvList(raw)
		overrides.AllowedOrigins = &values
	}
	config := server.ApplyRuntimeSettings(baseConfig, runtimeSettings, secrets, overrides)
	if err := server.ValidateConfig(config); err != nil {
		log.Fatalf("invalid startup configuration: %v", err)
	}
	app := server.NewApp(store, config)
	handler := app.Routes()
	maintenanceContext, stopMaintenance := context.WithCancel(context.Background())
	var maintenanceWorkers sync.WaitGroup
	maintenanceWorkers.Add(2)
	go func() {
		defer maintenanceWorkers.Done()
		store.RunRetentionScheduler(maintenanceContext, 24*time.Hour, func(result server.RetentionResult, err error) {
			if err != nil {
				log.Printf("scheduled retention failed: %v", err)
				return
			}
			log.Printf("scheduled retention completed: profile_versions=%d access_logs=%d audit_logs=%d sync_attempts=%d sync_mutations=%d conflicts=%d idempotency=%d email_tokens=%d reset_tokens=%d plugin_sessions=%d admin_sessions=%d admin_login_sessions=%d install_sessions=%d access_tokens=%d refresh_tokens=%d refresh_families=%d devices=%d",
				result.ProfileVersionsDeleted, result.AccessLogsDeleted, result.AdminAuditLogsDeleted,
				result.SyncAttemptsDeleted, result.SyncMutationsDeleted, result.ResolvedConflictsDeleted,
				result.IdempotencyResponsesDeleted, result.EmailVerificationTokensDeleted, result.PasswordResetTokensDeleted,
				result.PluginSessionsDeleted, result.AdminSessionsDeleted, result.AdminLoginSessionsDeleted,
				result.InstallSessionsDeleted, result.AccessTokensDeleted, result.RefreshTokensDeleted,
				result.RefreshFamiliesDeleted, result.DevicesDeleted)
		})
	}()
	go func() {
		defer maintenanceWorkers.Done()
		store.RunBackupScheduler(maintenanceContext, 24*time.Hour, func(result server.AutomaticBackupResult, err error) {
			if err != nil {
				log.Printf("scheduled data-only backup failed: %v", err)
				return
			}
			if !result.Skipped {
				log.Printf("scheduled data-only backup completed: backup_id=%s pruned=%d", result.BackupID, result.Deleted)
			}
		})
	}()

	httpServer := server.NewHTTPServer(addr, handler)
	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("KeKeIO Tab backend listening on %s, db=%s", addr, dbPath)
		serverErrors <- httpServer.ListenAndServe()
	}()

	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var restoreAction func() error
	shutdownRequested := false
	select {
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serve: %v", err)
		}
	case <-signalContext.Done():
		shutdownRequested = true
	case restoreAction = <-app.ShutdownRequests():
		shutdownRequested = true
	}
	if shutdownRequested {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownContext); err != nil {
			log.Printf("graceful shutdown failed: %v", err)
			_ = httpServer.Close()
		}
	}
	stopMaintenance()
	maintenanceWorkers.Wait()
	if restoreAction != nil {
		if err := restoreAction(); err != nil {
			log.Fatalf("apply scheduled restore: %v", err)
		}
	}
}

type developmentCredentials struct {
	AdminEmail  string `json:"adminEmail"`
	PluginEmail string `json:"pluginEmail"`
	Password    string `json:"password"`
}

const localDevelopmentPassword = "2231"

func runDevelopment() error {
	dataDirectory := env("FULLPRO_DEV_DATA_DIR", ".dev-data")
	store, config, credentials, err := prepareDevelopment(dataDirectory)
	if err != nil {
		return err
	}
	defer store.Close()

	log.Printf("local development mode: http://127.0.0.1:8787/admin")
	log.Printf("local admin account: %s / %s", credentials.AdminEmail, credentials.Password)
	log.Printf("local plugin account: %s / %s", credentials.PluginEmail, credentials.Password)
	log.Printf("local data directory: %s", filepath.Clean(dataDirectory))
	return serveDevelopment(store, config)
}

func prepareDevelopment(dataDirectory string) (*server.Store, server.Config, developmentCredentials, error) {
	dataDirectory = filepath.Clean(strings.TrimSpace(dataDirectory))
	if dataDirectory == "" || dataDirectory == "." {
		return nil, server.Config{}, developmentCredentials{}, fmt.Errorf("local development data directory is required")
	}
	if err := os.MkdirAll(dataDirectory, 0o700); err != nil {
		return nil, server.Config{}, developmentCredentials{}, fmt.Errorf("create local development data directory: %w", err)
	}
	credentialsPath := filepath.Join(dataDirectory, "dev-credentials.json")
	credentials, err := loadOrCreateDevelopmentCredentials(credentialsPath)
	if err != nil {
		return nil, server.Config{}, developmentCredentials{}, err
	}
	databasePath := filepath.Join(dataDirectory, "fullpro.db")
	secretsPath := filepath.Join(dataDirectory, "secrets.json")
	store, err := openRecoveredStore(databasePath, secretsPath)
	if err != nil {
		return nil, server.Config{}, developmentCredentials{}, fmt.Errorf("open local development database: %w", err)
	}
	if err := store.EnsureDevelopmentInstallation(context.Background(), server.DevelopmentAccounts{
		AdminEmail: credentials.AdminEmail, PluginEmail: credentials.PluginEmail, Password: credentials.Password, AllowWeakPassword: true,
	}); err != nil {
		_ = store.Close()
		return nil, server.Config{}, developmentCredentials{}, fmt.Errorf("initialize local development database: %w", err)
	}
	secrets, _, err := server.LoadOrCreateSecrets(secretsPath)
	if err != nil {
		_ = store.Close()
		return nil, server.Config{}, developmentCredentials{}, fmt.Errorf("load local development secrets: %w", err)
	}
	config := server.Config{
		Addr:                    "127.0.0.1:8787",
		CookieName:              "fullpro_dev_session",
		InstallCookieName:       "fullpro_dev_install",
		CookieSecure:            false,
		MaxBodyBytes:            1 << 20,
		AdminAllowedCIDRs:       []string{"127.0.0.1/32", "::1/128"},
		PasswordHashConcurrency: 2,
		AuthRateLimit:           server.RateLimitConfig{Limit: 20, Window: time.Minute},
		PublicBaseURL:           "http://127.0.0.1:8787",
		TokenDerivationKey:      secrets.TokenDerivationKey,
		AllowInsecureAdminHTTP:  true,
		DevelopmentMode:         true,
		SecretsPath:             secretsPath,
	}
	if err := server.ValidateConfig(config); err != nil {
		_ = store.Close()
		return nil, server.Config{}, developmentCredentials{}, fmt.Errorf("validate local development configuration: %w", err)
	}
	return store, config, credentials, nil
}

func loadOrCreateDevelopmentCredentials(path string) (developmentCredentials, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		var credentials developmentCredentials
		if err := json.Unmarshal(data, &credentials); err != nil {
			return developmentCredentials{}, fmt.Errorf("read local development credentials: %w", err)
		}
		if credentials.AdminEmail == "" || credentials.PluginEmail == "" {
			return developmentCredentials{}, fmt.Errorf("local development credentials are invalid; delete %s to recreate the local test data", path)
		}
		if credentials.Password != localDevelopmentPassword {
			credentials.Password = localDevelopmentPassword
			data, err := json.MarshalIndent(credentials, "", "  ")
			if err != nil {
				return developmentCredentials{}, err
			}
			if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
				return developmentCredentials{}, fmt.Errorf("update local development credentials: %w", err)
			}
		}
		return credentials, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return developmentCredentials{}, fmt.Errorf("read local development credentials: %w", err)
	}

	credentials := developmentCredentials{
		AdminEmail:  "admin@local.test",
		PluginEmail: "user@local.test",
		Password:    localDevelopmentPassword,
	}
	data, err = json.MarshalIndent(credentials, "", "  ")
	if err != nil {
		return developmentCredentials{}, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return loadOrCreateDevelopmentCredentials(path)
	}
	if err != nil {
		return developmentCredentials{}, fmt.Errorf("create local development credentials: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return developmentCredentials{}, fmt.Errorf("write local development credentials: %w", err)
	}
	return credentials, nil
}

func serveDevelopment(store *server.Store, config server.Config) error {
	app := server.NewApp(store, config)
	httpServer := server.NewHTTPServer(config.Addr, app.Routes())
	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("KeKeIO Tab local development server listening on %s", config.Addr)
		serverErrors <- httpServer.ListenAndServe()
	}()

	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var restoreAction func() error
	select {
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
	case <-signalContext.Done():
	case restoreAction = <-app.ShutdownRequests():
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownContext); err != nil {
		_ = httpServer.Close()
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if restoreAction != nil {
		if err := restoreAction(); err != nil {
			return fmt.Errorf("apply scheduled restore: %w", err)
		}
	}
	return nil
}

func runAdminReset() error {
	dbPath := env("FULLPRO_DB", "data/fullpro.db")
	secretsPath := env("FULLPRO_SECRETS_FILE", filepath.Join(filepath.Dir(dbPath), "secrets.json"))
	store, err := openRecoveredStore(dbPath, secretsPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.Close()
	if err := store.RequireAdminReset(context.Background()); err != nil {
		return err
	}
	codePath := env("FULLPRO_INSTALL_CODE_FILE", filepath.Join(filepath.Dir(dbPath), "install-code"))
	code, _, err := server.EnsureInstallCode(codePath, os.Getenv("FULLPRO_INSTALL_CODE"), "requires_admin_reset")
	if err != nil {
		return fmt.Errorf("prepare one-time reset code: %w", err)
	}
	log.Printf("administrator recovery enabled; one-time reset code: %s (also stored at %s)", code, codePath)
	return nil
}

func openRecoveredStore(databasePath, secretsPath string) (*server.Store, error) {
	if err := server.RecoverPendingRestore(databasePath, secretsPath); err != nil {
		return nil, fmt.Errorf("recover pending restore: %w", err)
	}
	return server.OpenStore(databasePath)
}

func env(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) (bool, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, err
	}
	return parsed, nil
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envList(key string) []string {
	return parseEnvList(os.Getenv(key))
}

func parseEnvList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}
