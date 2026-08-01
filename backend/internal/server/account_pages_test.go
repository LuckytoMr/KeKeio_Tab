package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicAccountRecoveryPagesUseFragmentTokensAndSameOriginAssets(t *testing.T) {
	store := newTestStore(t)
	handler := NewApp(store, Config{}).Routes()

	for _, testCase := range []struct {
		path       string
		scriptSrc  string
		scriptPath string
		apiPath    string
		title      string
	}{
		{path: "/account/verify", scriptSrc: "assets/verify.js", scriptPath: "/account/assets/verify.js", apiPath: "/api/v1/auth/verify-email", title: "验证邮箱 · kekeio"},
		{path: "/account/reset", scriptSrc: "assets/reset.js", scriptPath: "/account/assets/reset.js", apiPath: "/api/v1/auth/reset-password", title: "重置密码 · kekeio"},
	} {
		t.Run(testCase.path, func(t *testing.T) {
			page := httptest.NewRecorder()
			handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, testCase.path, nil))
			if page.Code != http.StatusOK {
				t.Fatalf("page status = %d body=%s", page.Code, page.Body.String())
			}
			if contentType := page.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
				t.Fatalf("page content type = %q", contentType)
			}
			if !strings.Contains(page.Body.String(), `src="`+testCase.scriptSrc+`"`) {
				t.Fatalf("page does not load same-origin script %q: %s", testCase.scriptSrc, page.Body.String())
			}
			if !strings.Contains(page.Body.String(), "<title>"+testCase.title+"</title>") {
				t.Fatalf("page title does not use product brand %q: %s", testCase.title, page.Body.String())
			}
			if !strings.Contains(page.Body.String(), `class="brand-mark" aria-hidden="true">k</div>`) || !strings.Contains(page.Body.String(), ">kekeio Account</p>") {
				t.Fatalf("page does not use the kekeio compact brand: %s", page.Body.String())
			}
			if strings.Contains(page.Body.String(), "token=") {
				t.Fatalf("page leaked a token into HTML: %s", page.Body.String())
			}

			script := httptest.NewRecorder()
			handler.ServeHTTP(script, httptest.NewRequest(http.MethodGet, testCase.scriptPath, nil))
			if script.Code != http.StatusOK {
				t.Fatalf("script status = %d body=%s", script.Code, script.Body.String())
			}
			body := script.Body.String()
			for _, required := range []string{"location.hash", "history.replaceState", testCase.apiPath} {
				if !strings.Contains(body, required) {
					t.Fatalf("script missing %q: %s", required, body)
				}
			}
			if strings.Contains(body, "location.search") {
				t.Fatalf("script must not accept query-string tokens: %s", body)
			}
		})
	}

	style := httptest.NewRecorder()
	handler.ServeHTTP(style, httptest.NewRequest(http.MethodGet, "/account/assets/account.css", nil))
	if style.Code != http.StatusOK || !strings.HasPrefix(style.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("style = %d %q", style.Code, style.Header().Get("Content-Type"))
	}
}

func TestAccountMailContentUsesFragmentLinkWithoutBareToken(t *testing.T) {
	for _, testCase := range []struct {
		kind    string
		path    string
		subject string
	}{
		{kind: "verify_email", path: "/account/verify", subject: "kekeio 邮箱验证"},
		{kind: "reset_password", path: "/account/reset", subject: "kekeio 密码重置"},
	} {
		subject, body, err := accountMailContent(MailMessage{
			Kind:    testCase.kind,
			Token:   "secret_token_123",
			BaseURL: "https://sync.example.test/root/",
		})
		if err != nil {
			t.Fatalf("compose %s mail: %v", testCase.kind, err)
		}
		if subject != testCase.subject {
			t.Fatalf("subject = %q, want %q", subject, testCase.subject)
		}
		want := "https://sync.example.test/root" + testCase.path + "#token=secret_token_123"
		if !strings.Contains(body, want) {
			t.Fatalf("mail link missing %q: %s", want, body)
		}
		if strings.Contains(body, "?token=") || strings.Contains(body, "Token: secret_token_123") {
			t.Fatalf("mail exposes a query or bare token: %s", body)
		}
	}
}

func TestPasswordResetPageUsesFourCharacterMinimum(t *testing.T) {
	store := newTestStore(t)
	handler := NewApp(store, Config{}).Routes()

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/account/reset", nil))
	if page.Code != http.StatusOK || strings.Count(page.Body.String(), `minlength="4"`) != 2 || strings.Contains(page.Body.String(), `minlength="8"`) {
		t.Fatalf("reset page does not fix the minimum password length at four: status=%d body=%s", page.Code, page.Body.String())
	}

	script := httptest.NewRecorder()
	handler.ServeHTTP(script, httptest.NewRequest(http.MethodGet, "/account/assets/reset.js", nil))
	body := script.Body.String()
	for _, required := range []string{"minimumPluginPasswordLength = 4", "Array.from(password).length"} {
		if script.Code != http.StatusOK || !strings.Contains(body, required) {
			t.Fatalf("reset script missing %q: status=%d body=%s", required, script.Code, body)
		}
	}
	if strings.Contains(body, "至少 8 位") || strings.Contains(body, "password.length < 8") {
		t.Fatalf("reset script restored the old eight-character rule: %s", body)
	}
}
