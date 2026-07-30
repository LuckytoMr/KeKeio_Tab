package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed web/admin/*
var embeddedAdminFiles embed.FS

type Config struct {
	Addr                    string
	CookieName              string
	InstallCookieName       string
	InstallCode             string
	InstallCodePath         string
	CookieSecure            bool
	MaxBodyBytes            int64
	AllowedOrigin           string
	AllowedOrigins          []string
	AdminAllowedCIDRs       []string
	TrustedProxyCIDRs       []string
	AuthRateLimit           RateLimitConfig
	PasswordHashConcurrency int
	Mailer                  Mailer
	PublicBaseURL           string
	RegistrationOpen        bool
	TokenDerivationKey      []byte
	AllowInsecureAdminHTTP  bool
	DevelopmentMode         bool
	SecretsPath             string
	SMTPTester              func(context.Context, SMTPTestInput) error
}

type App struct {
	store                *Store
	config               Config
	runtimeMu            sync.RWMutex
	admin                http.Handler
	authLimiter          *rateLimiter
	authIPLimiter        *rateLimiter
	installMu            sync.Mutex
	settingsMu           sync.Mutex
	beforeInstallCommit  func()
	beforeMaintenanceRun func()
	shutdownRequests     chan func() error
	maintenanceMode      atomic.Bool
}

func (a *App) runtimeAuthSettings() (registrationOpen bool, publicBaseURL string, mailer Mailer) {
	a.runtimeMu.RLock()
	defer a.runtimeMu.RUnlock()
	return a.config.RegistrationOpen, a.config.PublicBaseURL, a.config.Mailer
}

func (a *App) runtimeMailer() Mailer {
	a.runtimeMu.RLock()
	defer a.runtimeMu.RUnlock()
	return a.config.Mailer
}

func (a *App) applyRuntimeSettings(settings RuntimeSettings, smtpPassword string) {
	a.runtimeMu.Lock()
	defer a.runtimeMu.Unlock()
	a.config.PublicBaseURL = settings.PublicBaseURL
	a.config.RegistrationOpen = settings.RegistrationOpen
	a.config.AllowedOrigins = append([]string(nil), settings.AllowedOrigins...)
	a.config.Mailer = runtimeSMTPMailer(settings.SMTP, smtpPassword)
}

func (a *App) applyInstalledRuntime(input InstallationInput, smtpPassword string) {
	a.runtimeMu.Lock()
	defer a.runtimeMu.Unlock()
	a.config.PublicBaseURL = input.ExternalBaseURL
	a.config.RegistrationOpen = input.RegistrationEnabled
	a.config.AllowedOrigins = append([]string(nil), input.AllowedOrigins...)
	a.config.Mailer = runtimeSMTPMailer(input.SMTP, smtpPassword)
}

type RateLimitConfig struct {
	Limit      int
	IPLimit    int
	Window     time.Duration
	MaxBuckets int
}

type rateLimiter struct {
	limit      int
	window     time.Duration
	maxBuckets int
	mu         sync.Mutex
	buckets    map[string]*rateLimitBucket
}

type rateLimitBucket struct {
	hits     []time.Time
	lastSeen time.Time
}

func NewApp(store *Store, config Config) *App {
	if config.CookieName == "" {
		config.CookieName = "fullpro_session"
	}
	if config.InstallCookieName == "" {
		config.InstallCookieName = strings.TrimSuffix(config.CookieName, "_session") + "_install"
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = 1 << 20
	}
	if config.PasswordHashConcurrency == 0 {
		config.PasswordHashConcurrency = defaultPasswordHashConcurrency
	}
	configurePasswordHashConcurrency(config.PasswordHashConcurrency)
	if config.AuthRateLimit.Limit <= 0 {
		config.AuthRateLimit.Limit = 20
	}
	if config.AuthRateLimit.Window <= 0 {
		config.AuthRateLimit.Window = time.Minute
	}
	if config.AuthRateLimit.IPLimit <= 0 {
		config.AuthRateLimit.IPLimit = config.AuthRateLimit.Limit * 5
	}
	if config.AuthRateLimit.MaxBuckets <= 0 {
		config.AuthRateLimit.MaxBuckets = 4096
	}
	adminSubtree, _ := fs.Sub(embeddedAdminFiles, "web/admin")
	accountLimiter := newRateLimiter(config.AuthRateLimit)
	ipLimitConfig := config.AuthRateLimit
	ipLimitConfig.Limit = config.AuthRateLimit.IPLimit
	app := &App{
		store:            store,
		config:           config,
		admin:            http.FileServer(http.FS(adminSubtree)),
		authLimiter:      accountLimiter,
		authIPLimiter:    newRateLimiter(ipLimitConfig),
		shutdownRequests: make(chan func() error, 1),
	}
	if store != nil && store.db != nil {
		var backupTable, activeRestores int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='backup_records'`).Scan(&backupTable); err != nil {
			app.maintenanceMode.Store(true)
		} else if backupTable != 0 {
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM backup_records WHERE status='restoring'`).Scan(&activeRestores); err != nil || activeRestores != 0 {
				app.maintenanceMode.Store(true)
			}
		}
	}
	return app
}

func (a *App) ShutdownRequests() <-chan func() error {
	return a.shutdownRequests
}

func newRateLimiter(config RateLimitConfig) *rateLimiter {
	return &rateLimiter{
		limit:      config.Limit,
		window:     config.Window,
		maxBuckets: config.MaxBuckets,
		buckets:    map[string]*rateLimitBucket{},
	}
}

func (l *rateLimiter) Allow(key string, now time.Time) (bool, time.Duration) {
	if l == nil || l.limit <= 0 || l.window <= 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := now.Add(-l.window)
	for bucketKey, bucket := range l.buckets {
		if !bucket.lastSeen.After(cutoff) {
			delete(l.buckets, bucketKey)
		}
	}
	bucket := l.buckets[key]
	if bucket == nil {
		if len(l.buckets) >= l.maxBuckets {
			oldestKey := ""
			var oldest time.Time
			for bucketKey, candidate := range l.buckets {
				if oldestKey == "" || candidate.lastSeen.Before(oldest) {
					oldestKey, oldest = bucketKey, candidate.lastSeen
				}
			}
			delete(l.buckets, oldestKey)
		}
		bucket = &rateLimitBucket{}
		l.buckets[key] = bucket
	}
	kept := bucket.hits[:0]
	for _, hit := range bucket.hits {
		if hit.After(cutoff) {
			kept = append(kept, hit)
		}
	}
	bucket.hits = kept
	bucket.lastSeen = now
	if len(bucket.hits) >= l.limit {
		retryAfter := bucket.hits[0].Add(l.window).Sub(now)
		if retryAfter < time.Second {
			retryAfter = time.Second
		}
		return false, retryAfter
	}
	bucket.hits = append(bucket.hits, now)
	return true, 0
}

func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.handleHealth)
	mux.HandleFunc("GET /health/live", a.handleHealth)
	mux.HandleFunc("GET /health/ready", a.handleReady)
	mux.HandleFunc("GET /install", a.requireAdminNetwork(a.handleInstallIndex))
	mux.HandleFunc("GET /install/", a.requireAdminNetwork(a.handleInstallIndex))
	mux.HandleFunc("GET /install/api/v1/status", a.requireAdminNetwork(a.handleInstallStatus))
	mux.HandleFunc("POST /install/api/v1/session", a.requireAdminNetwork(a.withAuthRateLimit(a.handleInstallSession)))
	mux.HandleFunc("POST /install/api/v1/preflight", a.requireAdminNetwork(a.handleInstallPreflight))
	mux.HandleFunc("POST /install/api/v1/smtp-test", a.requireAdminNetwork(a.handleInstallSMTPTest))
	mux.HandleFunc("POST /install/api/v1/complete", a.requireAdminNetwork(a.handleInstallComplete))
	mux.HandleFunc("GET /api/admin/v1/auth/session", a.requireAdminNetwork(a.handleAdminPreauthSession))
	mux.HandleFunc("POST /api/admin/v1/auth/login", a.requireAdminNetwork(a.withAuthRateLimit(a.handleAdminLoginV1)))
	mux.HandleFunc("POST /api/admin/v1/auth/logout", a.requireAdminNetwork(a.requireAdminV1(a.handleAdminLogoutV1)))
	a.registerAdminV1Routes(mux)
	mux.HandleFunc("POST /api/v1/auth/register", a.withAuthRateLimit(a.handleRegisterV1))
	mux.HandleFunc("POST /api/v1/auth/verify-email", a.withAuthRateLimit(a.handleVerifyEmailV1))
	mux.HandleFunc("POST /api/v1/auth/resend-verification", a.withAuthRateLimit(a.handleResendVerificationV1))
	mux.HandleFunc("POST /api/v1/auth/login", a.withAuthRateLimit(a.handlePluginLoginV1))
	mux.HandleFunc("POST /api/v1/auth/refresh", a.withAuthRateLimit(a.handlePluginRefreshV1))
	mux.HandleFunc("POST /api/v1/auth/logout", a.handlePluginLogoutV1)
	mux.HandleFunc("POST /api/v1/auth/forgot-password", a.withAuthRateLimit(a.handleForgotPasswordV1))
	mux.HandleFunc("POST /api/v1/auth/reset-password", a.withAuthRateLimit(a.handleResetPasswordV1))
	mux.HandleFunc("GET /api/v1/me", a.handlePluginMeV1)
	mux.HandleFunc("GET /api/v1/sync/profile", a.handleGetSyncProfileV1)
	mux.HandleFunc("PUT /api/v1/sync/profile", a.handlePutSyncProfileV1)
	a.registerSyncVersionV1Routes(mux)
	mux.HandleFunc("GET /api/v1/app/bootstrap", a.handleBootstrapV1)
	mux.HandleFunc("GET /account/verify", a.handleAccountVerifyPage)
	mux.HandleFunc("GET /account/reset", a.handleAccountResetPage)
	mux.HandleFunc("GET /account/assets/account.css", a.handleAccountAsset)
	mux.HandleFunc("GET /account/assets/verify.js", a.handleAccountAsset)
	mux.HandleFunc("GET /account/assets/reset.js", a.handleAccountAsset)
	mux.HandleFunc("GET /api/v1/catalog/wallpapers/official", a.requirePluginAccessV1(a.handleCatalogOfficialWallpapersV1))
	mux.HandleFunc("GET /api/v1/catalog/wallpapers/web", a.requirePluginAccessV1(a.handleCatalogWebWallpapersV1))
	mux.HandleFunc("GET /api/v1/catalog/styles", a.requirePluginAccessV1(a.handleCatalogStylesV1))
	mux.HandleFunc("GET /admin", a.handleAdminIndex)
	mux.HandleFunc("GET /admin/", a.handleAdminIndex)
	mux.Handle("GET /admin/assets/", a.requireAdminNetworkHandler(http.StripPrefix("/admin/assets/", a.admin)))

	mux.HandleFunc("POST /api/auth/register", a.handleProtocolUpgradeRequired)
	mux.HandleFunc("POST /api/auth/login", a.handleProtocolUpgradeRequired)
	mux.HandleFunc("POST /api/auth/logout", a.handleLogout)
	mux.HandleFunc("GET /api/me", a.handleMe)
	mux.HandleFunc("GET /api/profile", a.handleGetProfile)
	mux.HandleFunc("PUT /api/profile", a.handlePutProfile)
	mux.HandleFunc("GET /api/profile/versions", a.handleProfileVersions)
	mux.HandleFunc("POST /api/profile/versions/{id}/restore", a.handleProtocolUpgradeRequired)

	mux.HandleFunc("GET /api/app/bootstrap", a.handleBootstrap)
	mux.HandleFunc("GET /api/wallpapers/web", a.requireUser(a.handlePublicWebWallpapers))
	mux.HandleFunc("GET /api/wallpapers/official", a.handlePublicOfficialWallpapers)
	mux.HandleFunc("GET /api/styles", a.requireUser(a.handlePublicStyles))

	mux.HandleFunc("GET /api/admin/summary", a.requireAdminNetwork(a.requireAdmin(a.handleAdminSummary)))
	mux.HandleFunc("GET /api/admin/users", a.requireAdminNetwork(a.requireAdmin(a.handleAdminUsers)))
	mux.HandleFunc("GET /api/admin/logs", a.requireAdminNetwork(a.requireAdmin(a.handleAdminLogs)))
	mux.HandleFunc("GET /api/admin/wallpapers/web", a.requireAdminNetwork(a.requireAdmin(a.handleAdminListWebWallpapers)))
	mux.HandleFunc("POST /api/admin/wallpapers/web", a.requireAdminNetwork(a.requireAdmin(a.handleProtocolUpgradeRequired)))
	mux.HandleFunc("DELETE /api/admin/wallpapers/web/{id}", a.requireAdminNetwork(a.requireAdmin(a.handleProtocolUpgradeRequired)))
	mux.HandleFunc("GET /api/admin/wallpapers/official", a.requireAdminNetwork(a.requireAdmin(a.handleAdminListOfficialWallpapers)))
	mux.HandleFunc("POST /api/admin/wallpapers/official", a.requireAdminNetwork(a.requireAdmin(a.handleProtocolUpgradeRequired)))
	mux.HandleFunc("DELETE /api/admin/wallpapers/official/{id}", a.requireAdminNetwork(a.requireAdmin(a.handleProtocolUpgradeRequired)))
	mux.HandleFunc("GET /api/admin/styles", a.requireAdminNetwork(a.requireAdmin(a.handleAdminListStyles)))
	mux.HandleFunc("POST /api/admin/styles", a.requireAdminNetwork(a.requireAdmin(a.handleProtocolUpgradeRequired)))
	mux.HandleFunc("DELETE /api/admin/styles/{id}", a.requireAdminNetwork(a.requireAdmin(a.handleProtocolUpgradeRequired)))
	mux.HandleFunc("GET /api/admin/releases", a.requireAdminNetwork(a.requireAdmin(a.handleAdminListReleases)))
	mux.HandleFunc("POST /api/admin/releases", a.requireAdminNetwork(a.requireAdmin(a.handleProtocolUpgradeRequired)))

	return a.withSecurityHeaders(a.withCORS(a.withAPILogging(a.withMaintenanceGate(mux))))
}

func (a *App) withMaintenanceGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.maintenanceMode.Load() && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			w.Header().Set("Retry-After", "30")
			writeAPIError(w, http.StatusServiceUnavailable, "MAINTENANCE_MODE", "服务正在排空请求并准备恢复备份")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data: https:; connect-src 'self'; frame-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		if r.TLS != nil || (ipInPrefixes(mustRequestIP(r.RemoteAddr), trustedProxyPrefixes(a.config.TrustedProxyCIDRs)) && strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")) {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

func mustRequestIP(remoteAddr string) netip.Addr {
	ip, _ := parseRequestIP(remoteAddr)
	return ip
}

func (a *App) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if a.originAllowed(r) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-FullPro-Device, X-CSRF-Token")
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		}
		if r.Method == http.MethodOptions {
			if !a.originAllowed(r) {
				writeAPIError(w, http.StatusForbidden, "ORIGIN_REJECTED", "请求来源不在允许列表")
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) withAPILogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !shouldLogRequest(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		started := time.Now()
		user, _ := a.userForAPILog(r)
		recorder := &loggedResponseWriter{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		if recorder.status == 0 {
			recorder.status = http.StatusOK
		}

		if recorder.status == http.StatusNotFound && user.ID == "" {
			return
		}
		ip := ""
		if clientIP, ok := a.clientIP(r); ok {
			ip = clientIP.String()
		}
		requestBytes := r.ContentLength
		if requestBytes < 0 {
			requestBytes = 0
		}
		logRecord := APILogRecord{
			UserID:         user.ID,
			UserEmail:      user.Email,
			Role:           string(user.Role),
			IP:             ip,
			Method:         r.Method,
			Path:           r.URL.Path,
			RouteGroup:     routeGroup(r.URL.Path),
			Status:         recorder.status,
			DurationMS:     time.Since(started).Milliseconds(),
			RequestBytes:   requestBytes,
			ResponseBytes:  recorder.bytes,
			IdempotencyKey: r.Header.Get("Idempotency-Key"),
			UserAgent:      r.UserAgent(),
		}
		if recorder.status >= 400 {
			logRecord.Error = http.StatusText(recorder.status)
		}
		_ = a.store.InsertAPILog(r.Context(), logRecord)
	})
}

func shouldLogRequest(path string) bool {
	if path == "/healthz" || path == "/health/live" || path == "/health/ready" || path == "/admin" || path == "/admin/" {
		return false
	}
	if strings.HasPrefix(path, "/admin/assets/") || strings.HasPrefix(path, "/api/admin/") {
		return false
	}
	return true
}

func (a *App) withAuthRateLimit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := "unknown"
		if parsedIP, ok := a.clientIP(r); ok {
			ip = parsedIP.String()
		}
		identity := a.authRateLimitIdentity(r)
		key := r.URL.Path + "|" + ip + "|" + identity
		now := time.Now()
		allowed, retryAfter := a.authIPLimiter.Allow(r.URL.Path+"|"+ip, now)
		if allowed {
			allowed, retryAfter = a.authLimiter.Allow(key, now)
		}
		if !allowed {
			seconds := int((retryAfter + time.Second - 1) / time.Second)
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			writeAPIError(w, http.StatusTooManyRequests, "RATE_LIMITED", "请求过于频繁，请稍后重试")
			return
		}
		next(w, r)
	}
}

func (a *App) authRateLimitIdentity(r *http.Request) string {
	if r.Body == nil {
		return "anonymous"
	}
	limit := a.config.MaxBodyBytes
	if limit <= 0 {
		limit = 1 << 20
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return "unreadable"
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if int64(len(body)) > limit {
		return "oversized"
	}
	var input struct {
		Email        string `json:"email"`
		Token        string `json:"token"`
		RefreshToken string `json:"refreshToken"`
		InstallCode  string `json:"installCode"`
	}
	if json.Unmarshal(body, &input) != nil {
		return "malformed"
	}
	identity := normalizeEmail(input.Email)
	if identity == "" {
		identity = strings.TrimSpace(input.RefreshToken)
	}
	if identity == "" {
		identity = strings.TrimSpace(input.Token)
	}
	if identity == "" {
		identity = strings.TrimSpace(input.InstallCode)
	}
	if identity == "" {
		return "anonymous"
	}
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])
}

type loggedResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *loggedResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *loggedResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += int64(n)
	return n, err
}

func routeGroup(path string) string {
	switch {
	case strings.HasPrefix(path, "/api/admin/"):
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) >= 3 {
			return "/" + strings.Join(parts[:3], "/")
		}
		return "/api/admin"
	case strings.HasPrefix(path, "/api/profile/versions/"):
		return "/api/profile/versions"
	case strings.HasPrefix(path, "/api/wallpapers/"):
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) >= 3 {
			return "/" + strings.Join(parts[:3], "/")
		}
	case strings.HasPrefix(path, "/api/auth/"):
		return path
	}
	return path
}

func (a *App) originAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err == nil && parsed.User == nil && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == "" && (parsed.Scheme == "http" || parsed.Scheme == "https") {
		originHost, originOK := canonicalOriginHost(parsed.Host)
		requestHost, requestOK := canonicalOriginHost(r.Host)
		if originOK && requestOK && strings.EqualFold(parsed.Scheme, a.effectiveScheme(r)) && originHost == requestHost {
			return true
		}
	}
	if a.config.DevelopmentMode && validChromeExtensionOrigin(origin) {
		return true
	}
	a.runtimeMu.RLock()
	defer a.runtimeMu.RUnlock()
	if a.config.AllowedOrigin != "" && origin == a.config.AllowedOrigin {
		return true
	}
	for _, allowed := range a.config.AllowedOrigins {
		if strings.TrimSpace(allowed) == origin {
			return true
		}
	}
	return false
}

func validChromeExtensionOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "chrome-extension" || parsed.Opaque != "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Port() != "" {
		return false
	}
	id := parsed.Host
	if len(id) != 32 {
		return false
	}
	for _, character := range id {
		if character < 'a' || character > 'p' {
			return false
		}
	}
	return true
}

func canonicalOriginHost(raw string) (string, bool) {
	parsed, err := url.Parse("//" + raw)
	if err != nil || parsed.User != nil || parsed.Hostname() == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	hostname := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if hostname == "" {
		return "", false
	}
	if strings.Contains(hostname, ":") {
		hostname = "[" + hostname + "]"
	}
	if port := parsed.Port(); port != "" {
		hostname += ":" + port
	}
	return hostname, true
}

func (a *App) effectiveScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	remote, ok := parseRequestIP(r.RemoteAddr)
	if ok && ipInPrefixes(remote, trustedProxyPrefixes(a.config.TrustedProxyCIDRs)) {
		parts := strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")
		if len(parts) > 0 {
			scheme := strings.ToLower(strings.TrimSpace(parts[len(parts)-1]))
			if scheme == "http" || scheme == "https" {
				return scheme
			}
		}
	}
	return "http"
}

func (a *App) secureCookie(r *http.Request) bool {
	return a.config.CookieSecure || a.effectiveScheme(r) == "https"
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *App) handleAdminIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin" && !strings.HasPrefix(r.URL.Path, "/admin/") {
		http.NotFound(w, r)
		return
	}
	if !a.isAdminNetworkRequest(r) {
		writeError(w, http.StatusForbidden, "admin network required")
		return
	}
	if !a.isAdminTransportSecure(r) {
		writeAPIError(w, http.StatusUpgradeRequired, "HTTPS_REQUIRED", "非 loopback 管理请求必须使用 HTTPS")
		return
	}
	data, err := embeddedAdminFiles.ReadFile("web/admin/index.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "admin page missing")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (a *App) handleInstallIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/install" && !strings.HasPrefix(r.URL.Path, "/install/") {
		http.NotFound(w, r)
		return
	}
	state, err := a.store.InstallationState(r.Context())
	if err != nil || state == "installed" {
		http.NotFound(w, r)
		return
	}
	data, err := embeddedAdminFiles.ReadFile("web/admin/index.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "install page missing")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (a *App) handleRegister(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !a.decodeJSON(w, r, &input) {
		return
	}
	user, err := a.store.CreateUser(r.Context(), input.Email, input.Password)
	if err != nil {
		if errors.Is(err, ErrDuplicateEmail) {
			writeError(w, http.StatusConflict, "该邮箱已注册，请直接登录")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	token, err := a.setSession(w, r, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session failed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user": user, "token": token})
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !a.decodeJSON(w, r, &input) {
		return
	}
	user, err := a.store.AuthenticateUser(r.Context(), input.Email, input.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	token, err := a.setSession(w, r, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user, "token": token})
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if token := bearerToken(r); token != "" {
		_ = a.store.DeleteSession(r.Context(), token)
	}
	if cookie, err := r.Cookie(a.config.CookieName); err == nil {
		_ = a.store.DeleteSession(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     a.config.CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.secureCookie(r),
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	user, err := a.currentUser(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (a *App) handleGetProfile(w http.ResponseWriter, r *http.Request) {
	user, err := a.currentUser(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	profile, err := a.store.GetProfile(r.Context(), user.ID)
	if err != nil {
		writeError(w, statusFromError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (a *App) handlePutProfile(w http.ResponseWriter, r *http.Request) {
	a.handleProtocolUpgradeRequired(w, r)
	return
	/*
		user, err := a.currentUser(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not signed in")
			return
		}
		rawBody, ok := a.readRequestBody(w, r)
		if !ok {
			return
		}
		requestHash := requestBodyHash(rawBody)
		idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if idempotencyKey != "" {
			existing, err := a.store.GetIdempotencyResponse(r.Context(), user.ID, idempotencyKey)
			if err == nil {
				if existing.RequestHash != requestHash {
					writeError(w, http.StatusConflict, "idempotency key already used with different request")
					return
				}
				writeStoredJSON(w, existing.Status, existing.ResponseJSON)
				return
			}
			if !errors.Is(err, ErrNotFound) {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}

		var input struct {
			Profile          json.RawMessage `json:"profile"`
			BaseVersion      int             `json:"baseVersion,omitempty"`
			ClientMutationID string          `json:"clientMutationId,omitempty"`
			DeviceID         string          `json:"deviceId,omitempty"`
		}
		if !decodeJSONBytes(w, rawBody, &input) {
			return
		}
		if len(input.Profile) == 0 {
			writeError(w, http.StatusBadRequest, "profile is required")
			return
		}
		profile, err := a.store.SaveProfile(r.Context(), user.ID, input.Profile)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		responseBody, err := json.Marshal(profile)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encode profile failed")
			return
		}
		if idempotencyKey != "" {
			_ = a.store.SaveIdempotencyResponse(r.Context(), IdempotencyRecord{
				UserID:         user.ID,
				IdempotencyKey: idempotencyKey,
				Method:         r.Method,
				Path:           r.URL.Path,
				RequestHash:    requestHash,
				Status:         http.StatusOK,
				ResponseJSON:   string(responseBody),
			})
		}
		writeStoredJSON(w, http.StatusOK, string(responseBody))
	*/
}

func (a *App) handleProtocolUpgradeRequired(w http.ResponseWriter, _ *http.Request) {
	writeAPIError(w, http.StatusUpgradeRequired, "SYNC_PROTOCOL_UPGRADE_REQUIRED", "该旧版写接口已停用，请升级扩展")
}

func (a *App) handleProfileVersions(w http.ResponseWriter, r *http.Request) {
	user, err := a.currentUser(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	versions, err := a.store.ListProfileVersions(r.Context(), user.ID, 20)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": versions})
}

func (a *App) handleRestoreProfileVersion(w http.ResponseWriter, r *http.Request) {
	user, err := a.currentUser(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	profile, err := a.store.RestoreProfileVersion(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, statusFromError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (a *App) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	channel, validChannel := releaseChannelFromRequest(r)
	if !validChannel {
		writeError(w, http.StatusBadRequest, "invalid release channel")
		return
	}
	summary, _ := a.store.AdminSummary(r.Context())
	release, releaseErr := a.store.LatestPublishedRelease(r.Context(), channel)
	var latest *ReleaseRecord
	if releaseErr == nil {
		latest = &release
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"service": "full-pro-backend",
		"summary": map[string]int{
			"officialWallpapers": summary.OfficialWallpapers,
			"webWallpapers":      summary.WebWallpapers,
		},
		"latestRelease": latest,
	})
}

func (a *App) handlePublicWebWallpapers(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	result, err := a.store.ListClientWebWallpapers(r.Context(), WallpaperListFilter{
		Category: r.URL.Query().Get("category"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *App) handlePublicOfficialWallpapers(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListPublicOfficialWallpapers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) handlePublicStyles(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListPublicStylePackages(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) handleAdminSummary(w http.ResponseWriter, r *http.Request) {
	summary, err := a.store.AdminSummary(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (a *App) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.store.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": users})
}

func (a *App) handleAdminLogs(w http.ResponseWriter, r *http.Request) {
	status, _ := strconv.Atoi(r.URL.Query().Get("status"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	logs, err := a.store.ListAPILogs(r.Context(), APILogFilter{
		UserEmail:  r.URL.Query().Get("userEmail"),
		RouteGroup: r.URL.Query().Get("routeGroup"),
		Status:     status,
		Limit:      limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": logs})
}

func (a *App) handleAdminListWebWallpapers(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListAdminWebWallpapers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) handleAdminUpsertWebWallpaper(w http.ResponseWriter, r *http.Request) {
	var input WebWallpaperRecord
	if !a.decodeJSON(w, r, &input) {
		return
	}
	if err := a.store.UpsertWebWallpaper(r.Context(), input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

func (a *App) handleAdminDeleteWebWallpaper(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteWebWallpaper(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, statusFromError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) handleAdminListOfficialWallpapers(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListAdminOfficialWallpapers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) handleAdminUpsertOfficialWallpaper(w http.ResponseWriter, r *http.Request) {
	var input OfficialWallpaperRecord
	if !a.decodeJSON(w, r, &input) {
		return
	}
	if err := a.store.UpsertOfficialWallpaper(r.Context(), input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

func (a *App) handleAdminDeleteOfficialWallpaper(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteOfficialWallpaper(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, statusFromError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) handleAdminListStyles(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListAdminStylePackages(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) handleAdminUpsertStyle(w http.ResponseWriter, r *http.Request) {
	var input StylePackageRecord
	if !a.decodeJSON(w, r, &input) {
		return
	}
	if err := a.store.UpsertStylePackage(r.Context(), input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

func (a *App) handleAdminDeleteStyle(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteStylePackage(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, statusFromError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) handleAdminListReleases(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListReleases(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) handleAdminAddRelease(w http.ResponseWriter, r *http.Request) {
	var input ReleaseRecord
	if !a.decodeJSON(w, r, &input) {
		return
	}
	release, err := a.store.AddRelease(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, release)
}

func (a *App) setSession(w http.ResponseWriter, r *http.Request, userID string) (string, error) {
	token, err := a.store.CreateSession(r.Context(), userID, 30*24*time.Hour)
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     a.config.CookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   30 * 24 * 60 * 60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.secureCookie(r),
	})
	return token, nil
}

func (a *App) currentUser(r *http.Request) (User, error) {
	if token := bearerToken(r); token != "" {
		return a.store.UserBySession(r.Context(), token)
	}
	cookie, err := r.Cookie(a.config.CookieName)
	if err != nil || cookie.Value == "" {
		return User{}, ErrUnauthorized
	}
	return a.store.UserBySession(r.Context(), cookie.Value)
}

func (a *App) userForAPILog(r *http.Request) (User, error) {
	if token := bearerToken(r); token != "" {
		if r.URL.Path == "/api/v1" || strings.HasPrefix(r.URL.Path, "/api/v1/") {
			return a.store.UserByAccessToken(r.Context(), token)
		}
		return a.store.UserBySession(r.Context(), token)
	}
	return a.currentUser(r)
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if len(header) < len("Bearer ")+1 {
		return ""
	}
	if !strings.EqualFold(header[:len("Bearer ")], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(header[len("Bearer "):])
}

func (a *App) requireAdminNetwork(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.isAdminNetworkRequest(r) {
			writeError(w, http.StatusForbidden, "admin network required")
			return
		}
		if !a.isAdminTransportSecure(r) {
			writeAPIError(w, http.StatusUpgradeRequired, "HTTPS_REQUIRED", "非 loopback 管理请求必须使用 HTTPS")
			return
		}
		next(w, r)
	}
}

func (a *App) requireAdminNetworkHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.isAdminNetworkRequest(r) {
			writeError(w, http.StatusForbidden, "admin network required")
			return
		}
		if !a.isAdminTransportSecure(r) {
			writeAPIError(w, http.StatusUpgradeRequired, "HTTPS_REQUIRED", "非 loopback 管理请求必须使用 HTTPS")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) isAdminTransportSecure(r *http.Request) bool {
	if a.config.AllowInsecureAdminHTTP || r.TLS != nil {
		return true
	}
	client, ok := a.clientIP(r)
	if !ok {
		return false
	}
	if client.IsLoopback() {
		return true
	}
	remote, ok := parseRequestIP(r.RemoteAddr)
	if !ok {
		return false
	}
	if ipInPrefixes(remote, trustedProxyPrefixes(a.config.TrustedProxyCIDRs)) {
		parts := strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")
		if len(parts) > 0 && strings.EqualFold(strings.TrimSpace(parts[len(parts)-1]), "https") {
			return true
		}
	}
	return false
}

func (a *App) isAdminNetworkRequest(r *http.Request) bool {
	ip, ok := a.clientIP(r)
	if !ok {
		return false
	}
	return ipInPrefixes(ip, adminAllowedPrefixes(a.config.AdminAllowedCIDRs))
}

func (a *App) clientIP(r *http.Request) (netip.Addr, bool) {
	remote, ok := parseRequestIP(r.RemoteAddr)
	if !ok {
		return netip.Addr{}, false
	}

	trusted := trustedProxyPrefixes(a.config.TrustedProxyCIDRs)
	if ipInPrefixes(remote, trusted) {
		forwardedFor := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
		if forwardedFor != "" {
			chain := parseForwardedChain(forwardedFor)
			if len(chain) == 0 {
				return netip.Addr{}, false
			}
			for index := len(chain) - 1; index >= 0; index-- {
				if !ipInPrefixes(chain[index], trusted) {
					return chain[index], true
				}
			}
			return chain[0], true
		}
		realIP := strings.TrimSpace(r.Header.Get("X-Real-IP"))
		if realIP == "" {
			return netip.Addr{}, false
		}
		ip, err := netip.ParseAddr(realIP)
		if err != nil {
			return netip.Addr{}, false
		}
		return ip.Unmap(), true
	}

	return remote, true
}

func parseRequestIP(remoteAddr string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return netip.Addr{}, false
	}
	return ip.Unmap(), true
}

func parseForwardedChain(value string) []netip.Addr {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	chain := make([]netip.Addr, 0, len(parts))
	for _, part := range parts {
		ip, err := netip.ParseAddr(strings.TrimSpace(part))
		if err != nil {
			return nil
		}
		chain = append(chain, ip.Unmap())
	}
	return chain
}

func adminAllowedPrefixes(raw []string) []netip.Prefix {
	if len(raw) == 0 {
		raw = []string{
			"127.0.0.1/32",
			"::1/128",
			"10.0.0.0/8",
			"172.16.0.0/12",
			"192.168.0.0/16",
			"fc00::/7",
		}
	}
	return parsePrefixes(raw)
}

func trustedProxyPrefixes(raw []string) []netip.Prefix {
	return parsePrefixes(raw)
}

func parsePrefixes(raw []string) []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, len(raw))
	for _, item := range raw {
		value := strings.TrimSpace(item)
		if value == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			if ip, ipErr := netip.ParseAddr(value); ipErr == nil {
				bits := 128
				if ip.Is4() {
					bits = 32
				}
				prefix = netip.PrefixFrom(ip.Unmap(), bits)
			} else {
				continue
			}
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes
}

func ipInPrefixes(ip netip.Addr, prefixes []netip.Prefix) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

func (a *App) requireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := a.currentUser(r); err != nil {
			writeError(w, http.StatusUnauthorized, "not signed in")
			return
		}
		next(w, r)
	}
}

func (a *App) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cookie, cookieErr := r.Cookie(a.config.CookieName); cookieErr == nil && cookie.Value != "" {
			if _, _, adminErr := a.store.AdminBySession(r.Context(), cookie.Value); adminErr == nil {
				next(w, r)
				return
			}
		}
		user, err := a.currentUser(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not signed in")
			return
		}
		if user.Role != RoleAdmin {
			writeError(w, http.StatusForbidden, "admin required")
			return
		}
		next(w, r)
	}
}

func (a *App) decodeJSON(w http.ResponseWriter, r *http.Request, dest any) bool {
	raw, ok := a.readRequestBody(w, r)
	if !ok {
		return false
	}
	return decodeJSONBytes(w, raw, dest)
}

func (a *App) readRequestBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	defer r.Body.Close()
	limited := io.LimitReader(r.Body, a.config.MaxBodyBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body failed")
		return nil, false
	}
	if int64(len(raw)) > a.config.MaxBodyBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return nil, false
	}
	return raw, true
}

func decodeJSONBytes(w http.ResponseWriter, raw []byte, dest any) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid json: multiple values")
		return false
	}
	return true
}

func requestBodyHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func writeStoredJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
	if !strings.HasSuffix(body, "\n") {
		_, _ = w.Write([]byte("\n"))
	}
}
