package server

import (
	"embed"
	"net/http"
	"path"
	"strings"
)

//go:embed web/account/*.html web/account/assets/*
var embeddedAccountFiles embed.FS

func (a *App) handleAccountVerifyPage(w http.ResponseWriter, _ *http.Request) {
	serveAccountFile(w, "web/account/verify.html", "text/html; charset=utf-8")
}

func (a *App) handleAccountResetPage(w http.ResponseWriter, _ *http.Request) {
	serveAccountFile(w, "web/account/reset.html", "text/html; charset=utf-8")
}

func (a *App) handleAccountAsset(w http.ResponseWriter, r *http.Request) {
	name := path.Base(r.URL.Path)
	contentType := "application/javascript; charset=utf-8"
	if strings.HasSuffix(name, ".css") {
		contentType = "text/css; charset=utf-8"
	}
	serveAccountFile(w, "web/account/assets/"+name, contentType)
}

func serveAccountFile(w http.ResponseWriter, name, contentType string) {
	content, err := embeddedAccountFiles.ReadFile(name)
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}
