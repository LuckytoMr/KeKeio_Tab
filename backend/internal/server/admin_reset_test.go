package server

import (
	"testing"
	"time"
)

func TestRequireAdminResetRevokesSessionsAndReopensOnlyResetInstallMode(t *testing.T) {
	app, store := newTestApp(t)
	handler := app.Routes()
	adminCookie := fixedAdminCookie(t, handler)
	if _, _, err := store.AdminBySession(t.Context(), adminCookie.Value); err != nil {
		t.Fatalf("admin session before reset: %v", err)
	}

	if err := store.RequireAdminReset(t.Context()); err != nil {
		t.Fatalf("require admin reset: %v", err)
	}
	state, err := store.InstallationState(t.Context())
	if err != nil || state != "requires_admin_reset" {
		t.Fatalf("installation state after reset = %q err=%v", state, err)
	}
	if _, _, err := store.AdminBySession(t.Context(), adminCookie.Value); err == nil {
		t.Fatal("admin session remained valid after local recovery request")
	}
	var audits int
	if err := store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM admin_audit_logs WHERE action='auth.admin_reset_required'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("admin reset audit rows=%d err=%v", audits, err)
	}
	if _, _, _, err := store.CreateInstallSession(t.Context(), 30*time.Minute, 2*time.Hour); err != nil {
		t.Fatalf("reset install session was not reopened: %v", err)
	}
}

func TestRequireAdminResetRejectsUninitializedStore(t *testing.T) {
	store := newTestStore(t)
	if err := store.RequireAdminReset(t.Context()); err == nil {
		t.Fatal("uninitialized store accepted admin reset")
	}
}
