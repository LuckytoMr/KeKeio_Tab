package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type captureMailer struct {
	mu       sync.Mutex
	messages []MailMessage
}

type failingMailer struct{}

func (failingMailer) Send(context.Context, MailMessage) error {
	return errors.New("simulated SMTP failure")
}

func (m *captureMailer) Send(_ context.Context, message MailMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, message)
	return nil
}

func (m *captureMailer) token(kind string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := len(m.messages) - 1; index >= 0; index-- {
		if m.messages[index].Kind == kind {
			return m.messages[index].Token
		}
	}
	return ""
}

func newV1AuthApp(t *testing.T) (http.Handler, *Store, *captureMailer) {
	t.Helper()
	store := newTestStore(t)
	if _, err := store.BeginInstallation(t.Context(), InstallationInput{
		Mode: "fresh_install", Email: "owner@example.com", DisplayName: "Owner",
		Password: "correct horse battery staple", ExternalBaseURL: "https://fullpro.example",
		AllowedOrigins: []string{"chrome-extension://abcdefghijklmnopabcdefghijklmnop"},
	}); err != nil {
		t.Fatalf("begin installation: %v", err)
	}
	if err := store.FinishInstallation(t.Context()); err != nil {
		t.Fatalf("finish installation: %v", err)
	}
	mailer := &captureMailer{}
	app := NewApp(store, Config{
		CookieName:         "fullpro_test_session",
		Mailer:             mailer,
		PublicBaseURL:      "https://fullpro.example",
		TokenDerivationKey: []byte("0123456789abcdef0123456789abcdef"),
		RegistrationOpen:   true,
		AllowedOrigins:     []string{"chrome-extension://abcdefghijklmnopabcdefghijklmnop"},
		AdminAllowedCIDRs:  []string{"127.0.0.1/32"},
	})
	return app.Routes(), store, mailer
}

func decodeV1Data(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode v1 response: %v; body=%s", err, response.Body.String())
	}
	return body.Data
}

func postV1(t *testing.T, handler http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.RemoteAddr = "198.51.100.9:1234"
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func getV1Bearer(t *testing.T, handler http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = "198.51.100.9:1234"
	req.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func TestV1RegistrationVerificationLoginAndRefreshReplay(t *testing.T) {
	handler, store, mailer := newV1AuthApp(t)

	register := postV1(t, handler, "/api/v1/auth/register", `{"email":"User@Example.com","password":"safe-password-123"}`)
	if register.Code != http.StatusCreated || !strings.Contains(register.Body.String(), `"status":"pending_verification"`) {
		t.Fatalf("register = %d %s", register.Code, register.Body.String())
	}
	if strings.Contains(register.Body.String(), "accessToken") || strings.Contains(register.Body.String(), "refreshToken") {
		t.Fatalf("registration issued credentials before verification: %s", register.Body.String())
	}
	verificationToken := mailer.token("verify_email")
	if verificationToken == "" {
		t.Fatalf("registration did not send verification token")
	}

	blocked := postV1(t, handler, "/api/v1/auth/login", `{"email":"user@example.com","password":"safe-password-123","deviceId":"dev_a"}`)
	if blocked.Code != http.StatusForbidden || !strings.Contains(blocked.Body.String(), "EMAIL_NOT_VERIFIED") {
		t.Fatalf("unverified login = %d %s", blocked.Code, blocked.Body.String())
	}

	verified := postV1(t, handler, "/api/v1/auth/verify-email", `{"token":"`+verificationToken+`"}`)
	if verified.Code != http.StatusOK {
		t.Fatalf("verify email = %d %s", verified.Code, verified.Body.String())
	}

	login := postV1(t, handler, "/api/v1/auth/login", `{"email":"user@example.com","password":"safe-password-123","deviceId":"dev_a"}`)
	if login.Code != http.StatusOK {
		t.Fatalf("login = %d %s", login.Code, login.Body.String())
	}
	loginData := decodeV1Data(t, login)
	accessToken, _ := loginData["accessToken"].(string)
	refreshToken, _ := loginData["refreshToken"].(string)
	if accessToken == "" || refreshToken == "" {
		t.Fatalf("login tokens missing: %#v", loginData)
	}
	accessExpiresAt, accessExpiryOK := loginData["accessExpiresAt"].(string)
	refreshExpiresAt, refreshExpiryOK := loginData["refreshExpiresAt"].(string)
	if !accessExpiryOK || accessExpiresAt == "" || !refreshExpiryOK || refreshExpiresAt == "" {
		t.Fatalf("login expiry timestamps missing: %#v", loginData)
	}
	me := getV1Bearer(t, handler, "/api/v1/me", accessToken)
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), "user@example.com") {
		t.Fatalf("me = %d %s", me.Code, me.Body.String())
	}

	rotated := postV1(t, handler, "/api/v1/auth/refresh", `{"refreshToken":"`+refreshToken+`","requestId":"refresh-request-1"}`)
	if rotated.Code != http.StatusOK {
		t.Fatalf("refresh = %d %s", rotated.Code, rotated.Body.String())
	}
	rotatedData := decodeV1Data(t, rotated)
	newAccess, _ := rotatedData["accessToken"].(string)
	newRefresh, _ := rotatedData["refreshToken"].(string)
	if newAccess == "" || newRefresh == "" || newRefresh == refreshToken {
		t.Fatalf("refresh did not rotate credentials: %#v", rotatedData)
	}
	rotatedAccessExpiresAt, rotatedAccessExpiryOK := rotatedData["accessExpiresAt"].(string)
	rotatedRefreshExpiresAt, rotatedRefreshExpiryOK := rotatedData["refreshExpiresAt"].(string)
	if !rotatedAccessExpiryOK || rotatedAccessExpiresAt == "" || !rotatedRefreshExpiryOK || rotatedRefreshExpiresAt == "" {
		t.Fatalf("refresh expiry timestamps missing: %#v", rotatedData)
	}
	refreshedUser, ok := rotatedData["user"].(map[string]any)
	if !ok || refreshedUser["email"] != "user@example.com" {
		t.Fatalf("refresh user missing: %#v", rotatedData)
	}
	idempotentReplay := postV1(t, handler, "/api/v1/auth/refresh", `{"refreshToken":"`+refreshToken+`","requestId":"refresh-request-1"}`)
	if idempotentReplay.Code != http.StatusOK {
		t.Fatalf("same refresh request replay = %d %s", idempotentReplay.Code, idempotentReplay.Body.String())
	}
	replayedData := decodeV1Data(t, idempotentReplay)
	if replayedData["accessToken"] != newAccess || replayedData["refreshToken"] != newRefresh {
		t.Fatalf("idempotent refresh response changed: first=%#v replay=%#v", rotatedData, replayedData)
	}
	if _, err := store.db.Exec(`UPDATE access_tokens SET expires_at = ? WHERE token_hash = ?`, "2000-01-01T00:00:00Z", tokenHash(newAccess)); err != nil {
		t.Fatalf("expire rotated access token: %v", err)
	}
	unsafeRecovery := postV1(t, handler, "/api/v1/auth/refresh", `{"refreshToken":"`+refreshToken+`","requestId":"refresh-request-1"}`)
	if unsafeRecovery.Code != http.StatusUnauthorized || !strings.Contains(unsafeRecovery.Body.String(), "REFRESH_REPLAY") {
		t.Fatalf("refresh recovery with expired child access = %d %s", unsafeRecovery.Code, unsafeRecovery.Body.String())
	}
	assertRefreshFamilyRevoked(t, store, refreshToken)

	replay := postV1(t, handler, "/api/v1/auth/refresh", `{"refreshToken":"`+refreshToken+`","requestId":"refresh-request-2"}`)
	if replay.Code != http.StatusUnauthorized || !strings.Contains(replay.Body.String(), "REFRESH_REPLAY") {
		t.Fatalf("refresh replay = %d %s", replay.Code, replay.Body.String())
	}
	if afterReplay := getV1Bearer(t, handler, "/api/v1/me", newAccess); afterReplay.Code != http.StatusUnauthorized {
		t.Fatalf("family access token survived replay: %d %s", afterReplay.Code, afterReplay.Body.String())
	}
}

func TestV1RegistrationRollsBackPendingUserWhenVerificationMailFails(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.BeginInstallation(t.Context(), InstallationInput{
		Mode: "fresh_install", Email: "owner@example.com", DisplayName: "Owner",
		Password: "correct horse battery staple", ExternalBaseURL: "https://fullpro.example",
	}); err != nil {
		t.Fatalf("begin installation: %v", err)
	}
	if err := store.FinishInstallation(t.Context()); err != nil {
		t.Fatalf("finish installation: %v", err)
	}
	handler := NewApp(store, Config{
		Mailer:             failingMailer{},
		PublicBaseURL:      "https://fullpro.example",
		RegistrationOpen:   true,
		TokenDerivationKey: []byte("0123456789abcdef0123456789abcdef"),
	}).Routes()

	response := postV1(t, handler, "/api/v1/auth/register", `{"email":"mail-failure@example.com","password":"safe-password-123"}`)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), `"code":"MAIL_SEND_FAILED"`) {
		t.Fatalf("registration mail failure = %d %s", response.Code, response.Body.String())
	}
	var users, tokens int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM users WHERE email='mail-failure@example.com'`).Scan(&users); err != nil {
		t.Fatalf("count pending users: %v", err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM email_verification_tokens`).Scan(&tokens); err != nil {
		t.Fatalf("count verification tokens: %v", err)
	}
	if users != 0 || tokens != 0 {
		t.Fatalf("failed verification delivery consumed registration state: users=%d tokens=%d", users, tokens)
	}
}

func TestV1RegistrationDoesNotRevealWhetherEmailAlreadyExists(t *testing.T) {
	handler, _, _ := newV1AuthApp(t)
	body := `{"email":"non-enumerating@example.com","password":"safe-password-123"}`
	created := postV1(t, handler, "/api/v1/auth/register", body)
	duplicate := postV1(t, handler, "/api/v1/auth/register", body)
	if created.Code != http.StatusCreated || duplicate.Code != http.StatusCreated {
		t.Fatalf("registration statuses differ: created=%d duplicate=%d", created.Code, duplicate.Code)
	}
	createdData := decodeV1Data(t, created)
	duplicateData := decodeV1Data(t, duplicate)
	if len(createdData) != 1 || len(duplicateData) != 1 ||
		createdData["status"] != "pending_verification" || duplicateData["status"] != "pending_verification" {
		t.Fatalf("registration response reveals account existence: created=%#v duplicate=%#v", createdData, duplicateData)
	}
}

func TestV1RefreshIdempotentRecoveryExpiresAfterSixtySeconds(t *testing.T) {
	store := newTestStore(t)
	pair := newVerifiedTokenFamilyForRefreshTest(t, store, "refresh-window@example.com")
	const requestID = "refresh-window-request"
	if _, err := store.RotateRefreshToken(t.Context(), pair.RefreshToken, requestID, []byte("0123456789abcdef0123456789abcdef")); err != nil {
		t.Fatalf("initial refresh rotation: %v", err)
	}
	usedAt := time.Now().UTC().Add(-61 * time.Second).Format(time.RFC3339Nano)
	if _, err := store.db.Exec(`UPDATE refresh_tokens SET used_at=? WHERE token_hash=?`, usedAt, tokenHash(pair.RefreshToken)); err != nil {
		t.Fatalf("age parent refresh token: %v", err)
	}
	if _, err := store.RotateRefreshToken(t.Context(), pair.RefreshToken, requestID, []byte("0123456789abcdef0123456789abcdef")); !errors.Is(err, ErrTokenReplay) {
		t.Fatalf("recovery after 60 seconds err=%v, want ErrTokenReplay", err)
	}
	assertRefreshFamilyRevoked(t, store, pair.RefreshToken)
}

func TestRefreshRecoveryWindowBoundary(t *testing.T) {
	now := time.Date(2026, time.July, 12, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		usedAt time.Time
		want   bool
	}{
		{name: "just used", usedAt: now, want: true},
		{name: "inside window", usedAt: now.Add(-60*time.Second + time.Nanosecond), want: true},
		{name: "at boundary", usedAt: now.Add(-60 * time.Second), want: false},
		{name: "after boundary", usedAt: now.Add(-60*time.Second - time.Nanosecond), want: false},
		{name: "future timestamp", usedAt: now.Add(time.Nanosecond), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := refreshRecoveryWithinWindow(test.usedAt, now); got != test.want {
				t.Fatalf("refreshRecoveryWithinWindow(%s, %s)=%v, want %v", test.usedAt, now, got, test.want)
			}
		})
	}
}

func TestV1RefreshIdempotentRecoveryRequiresUsableChildTokens(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Store, TokenPair) error
	}{
		{
			name: "missing child access",
			mutate: func(store *Store, child TokenPair) error {
				_, err := store.db.Exec(`DELETE FROM access_tokens WHERE token_hash=?`, tokenHash(child.AccessToken))
				return err
			},
		},
		{
			name: "revoked child access",
			mutate: func(store *Store, child TokenPair) error {
				_, err := store.db.Exec(`UPDATE access_tokens SET revoked_at=? WHERE token_hash=?`, nowString(), tokenHash(child.AccessToken))
				return err
			},
		},
		{
			name: "expired child access",
			mutate: func(store *Store, child TokenPair) error {
				_, err := store.db.Exec(`UPDATE access_tokens SET expires_at=? WHERE token_hash=?`, "2000-01-01T00:00:00Z", tokenHash(child.AccessToken))
				return err
			},
		},
		{
			name: "missing child refresh",
			mutate: func(store *Store, child TokenPair) error {
				_, err := store.db.Exec(`DELETE FROM refresh_tokens WHERE token_hash=?`, tokenHash(child.RefreshToken))
				return err
			},
		},
		{
			name: "used child refresh",
			mutate: func(store *Store, child TokenPair) error {
				_, err := store.db.Exec(`UPDATE refresh_tokens SET used_at=? WHERE token_hash=?`, nowString(), tokenHash(child.RefreshToken))
				return err
			},
		},
		{
			name: "expired child refresh",
			mutate: func(store *Store, child TokenPair) error {
				_, err := store.db.Exec(`UPDATE refresh_tokens SET expires_at=? WHERE token_hash=?`, "2000-01-01T00:00:00Z", tokenHash(child.RefreshToken))
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t)
			parent := newVerifiedTokenFamilyForRefreshTest(t, store, "child-state@example.com")
			const requestID = "child-state-request"
			child, err := store.RotateRefreshToken(t.Context(), parent.RefreshToken, requestID, []byte("0123456789abcdef0123456789abcdef"))
			if err != nil {
				t.Fatalf("initial refresh rotation: %v", err)
			}
			if err := test.mutate(store, child); err != nil {
				t.Fatalf("mutate child token state: %v", err)
			}
			if _, err := store.RotateRefreshToken(t.Context(), parent.RefreshToken, requestID, []byte("0123456789abcdef0123456789abcdef")); !errors.Is(err, ErrTokenReplay) {
				t.Fatalf("unsafe child recovery err=%v, want ErrTokenReplay", err)
			}
			assertRefreshFamilyRevoked(t, store, parent.RefreshToken)
		})
	}
}

func newVerifiedTokenFamilyForRefreshTest(t *testing.T, store *Store, email string) TokenPair {
	t.Helper()
	userID := newID("user_")
	now := nowString()
	if _, err := store.db.Exec(`INSERT INTO users(id,email,password_hash,role,created_at,last_login_at,status,email_verified_at,updated_at) VALUES(?,?,?,'user',?,'','active',?,?)`,
		userID, email, "unused", now, now, now); err != nil {
		t.Fatalf("seed verified user: %v", err)
	}
	pair, err := store.CreateTokenFamily(t.Context(), userID, "refresh-test-device")
	if err != nil {
		t.Fatalf("create token family: %v", err)
	}
	return pair
}

func assertRefreshFamilyRevoked(t *testing.T, store *Store, refreshToken string) {
	t.Helper()
	var revokedAt string
	if err := store.db.QueryRow(`SELECT f.revoked_at FROM refresh_token_families f JOIN refresh_tokens t ON t.family_id=f.id WHERE t.token_hash=?`, tokenHash(refreshToken)).Scan(&revokedAt); err != nil {
		t.Fatalf("read refresh family revocation: %v", err)
	}
	if revokedAt == "" {
		t.Fatal("refresh token family was not revoked")
	}
}

func TestV1PasswordResetIsSingleUseAndRevokesSessions(t *testing.T) {
	handler, store, mailer := newV1AuthApp(t)
	register := postV1(t, handler, "/api/v1/auth/register", `{"email":"reset@example.com","password":"old-password-123"}`)
	if register.Code != http.StatusCreated {
		t.Fatalf("register = %d %s", register.Code, register.Body.String())
	}
	verificationToken := mailer.token("verify_email")
	if verified := postV1(t, handler, "/api/v1/auth/verify-email", `{"token":"`+verificationToken+`"}`); verified.Code != http.StatusOK {
		t.Fatalf("verify = %d %s", verified.Code, verified.Body.String())
	}
	login := postV1(t, handler, "/api/v1/auth/login", `{"email":"reset@example.com","password":"old-password-123","deviceId":"dev_reset"}`)
	loginData := decodeV1Data(t, login)
	oldAccess, _ := loginData["accessToken"].(string)
	oldRefresh, _ := loginData["refreshToken"].(string)
	loginUser, _ := loginData["user"].(map[string]any)
	userID, _ := loginUser["id"].(string)
	legacySession, err := store.CreateSession(t.Context(), userID, time.Hour)
	if err != nil {
		t.Fatalf("create legacy session: %v", err)
	}

	missing := postV1(t, handler, "/api/v1/auth/forgot-password", `{"email":"missing@example.com"}`)
	existing := postV1(t, handler, "/api/v1/auth/forgot-password", `{"email":"reset@example.com"}`)
	if missing.Code != http.StatusAccepted || existing.Code != http.StatusAccepted ||
		!strings.Contains(missing.Body.String(), `"accepted":true`) || !strings.Contains(existing.Body.String(), `"accepted":true`) {
		t.Fatalf("forgot-password leaks account existence: missing=%d %s existing=%d %s", missing.Code, missing.Body.String(), existing.Code, existing.Body.String())
	}
	resetToken := mailer.token("reset_password")
	if resetToken == "" {
		t.Fatal("forgot-password did not send reset token")
	}
	if secondForgot := postV1(t, handler, "/api/v1/auth/forgot-password", `{"email":"reset@example.com"}`); secondForgot.Code != http.StatusAccepted {
		t.Fatalf("second forgot-password = %d %s", secondForgot.Code, secondForgot.Body.String())
	}
	newerResetToken := mailer.token("reset_password")
	if newerResetToken == "" || newerResetToken == resetToken {
		t.Fatalf("second reset token did not rotate: old=%q new=%q", resetToken, newerResetToken)
	}
	reset := postV1(t, handler, "/api/v1/auth/reset-password", `{"token":"`+newerResetToken+`","password":"new-password-456"}`)
	if reset.Code != http.StatusOK {
		t.Fatalf("reset password = %d %s", reset.Code, reset.Body.String())
	}
	secondUse := postV1(t, handler, "/api/v1/auth/reset-password", `{"token":"`+newerResetToken+`","password":"another-password-789"}`)
	if secondUse.Code != http.StatusBadRequest {
		t.Fatalf("reset token reused = %d %s", secondUse.Code, secondUse.Body.String())
	}
	staleUse := postV1(t, handler, "/api/v1/auth/reset-password", `{"token":"`+resetToken+`","password":"another-password-789"}`)
	if staleUse.Code != http.StatusBadRequest {
		t.Fatalf("older reset token survived successful reset = %d %s", staleUse.Code, staleUse.Body.String())
	}
	if me := getV1Bearer(t, handler, "/api/v1/me", oldAccess); me.Code != http.StatusUnauthorized {
		t.Fatalf("old access survived password reset: %d %s", me.Code, me.Body.String())
	}
	if refreshed := postV1(t, handler, "/api/v1/auth/refresh", `{"refreshToken":"`+oldRefresh+`","requestId":"after-password-reset"}`); refreshed.Code != http.StatusUnauthorized {
		t.Fatalf("old refresh survived password reset: %d %s", refreshed.Code, refreshed.Body.String())
	}
	if _, err := store.UserBySession(t.Context(), legacySession); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("legacy session survived password reset: %v", err)
	}
	oldLogin := postV1(t, handler, "/api/v1/auth/login", `{"email":"reset@example.com","password":"old-password-123","deviceId":"dev_reset"}`)
	if oldLogin.Code != http.StatusUnauthorized {
		t.Fatalf("old password survived reset: %d %s", oldLogin.Code, oldLogin.Body.String())
	}
	newLogin := postV1(t, handler, "/api/v1/auth/login", `{"email":"reset@example.com","password":"new-password-456","deviceId":"dev_reset"}`)
	if newLogin.Code != http.StatusOK {
		t.Fatalf("new password login = %d %s", newLogin.Code, newLogin.Body.String())
	}
	newData := decodeV1Data(t, newLogin)
	newAccess, _ := newData["accessToken"].(string)
	newRefresh, _ := newData["refreshToken"].(string)
	logout := postV1(t, handler, "/api/v1/auth/logout", `{"refreshToken":"`+newRefresh+`"}`)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout = %d %s", logout.Code, logout.Body.String())
	}
	if me := getV1Bearer(t, handler, "/api/v1/me", newAccess); me.Code != http.StatusUnauthorized {
		t.Fatalf("access survived logout: %d %s", me.Code, me.Body.String())
	}
}

func TestPasswordResetValidatesTokenBeforeExpensivePasswordWork(t *testing.T) {
	store := newTestStore(t)
	err := store.ResetPluginPassword(t.Context(), "missing-reset-token", "short")
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("invalid reset token returned %v before token lookup; want ErrInvalidToken", err)
	}
}

func TestV1ResendVerificationIsNonEnumeratingAndInvalidatesOldToken(t *testing.T) {
	handler, _, mailer := newV1AuthApp(t)
	register := postV1(t, handler, "/api/v1/auth/register", `{"email":"resend@example.com","password":"safe-password-123"}`)
	if register.Code != http.StatusCreated {
		t.Fatalf("register = %d %s", register.Code, register.Body.String())
	}
	oldToken := mailer.token("verify_email")
	missing := postV1(t, handler, "/api/v1/auth/resend-verification", `{"email":"missing@example.com"}`)
	existing := postV1(t, handler, "/api/v1/auth/resend-verification", `{"email":"resend@example.com"}`)
	if missing.Code != http.StatusAccepted || existing.Code != http.StatusAccepted ||
		!strings.Contains(missing.Body.String(), `"accepted":true`) || !strings.Contains(existing.Body.String(), `"accepted":true`) {
		t.Fatalf("resend leaks account existence: missing=%d %s existing=%d %s", missing.Code, missing.Body.String(), existing.Code, existing.Body.String())
	}
	newToken := mailer.token("verify_email")
	if newToken == "" || newToken == oldToken {
		t.Fatalf("verification token did not rotate: old=%q new=%q", oldToken, newToken)
	}
	oldVerify := postV1(t, handler, "/api/v1/auth/verify-email", `{"token":"`+oldToken+`"}`)
	if oldVerify.Code != http.StatusBadRequest {
		t.Fatalf("old verification token remained valid: %d %s", oldVerify.Code, oldVerify.Body.String())
	}
	newVerify := postV1(t, handler, "/api/v1/auth/verify-email", `{"token":"`+newToken+`"}`)
	if newVerify.Code != http.StatusOK {
		t.Fatalf("new verification token = %d %s", newVerify.Code, newVerify.Body.String())
	}
}
