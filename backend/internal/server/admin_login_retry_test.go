package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWrongAdminPasswordDoesNotConsumePreauthSession(t *testing.T) {
	app, _ := newTestApp(t)
	handler := app.Routes()
	preauth := get(t, handler, "/api/admin/v1/auth/session")
	preauthCookie := responseCookie(t, preauth, "fullpro_test_preauth")
	var envelope struct {
		Data struct {
			CSRFToken string `json:"csrfToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(preauth.Body.Bytes(), &envelope); err != nil || envelope.Data.CSRFToken == "" {
		t.Fatalf("decode preauth: %v %s", err, preauth.Body.String())
	}
	login := func(password string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/auth/login", strings.NewReader(`{"email":"owner@example.com","password":"`+password+`"}`))
		request.RemoteAddr = "127.0.0.1:1234"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "http://localhost:5173")
		request.Header.Set("X-CSRF-Token", envelope.Data.CSRFToken)
		request.AddCookie(preauthCookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if wrong := login("wrong-password"); wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d %s", wrong.Code, wrong.Body.String())
	}
	if correct := login("correct horse battery staple"); correct.Code != http.StatusOK {
		t.Fatalf("correct password after retry = %d %s", correct.Code, correct.Body.String())
	}
}
