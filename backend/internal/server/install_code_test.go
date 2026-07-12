package server

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnsureInstallCodeCreates0600FileOnlyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "install-code")
	code, created, err := EnsureInstallCode(path, "", "uninitialized")
	if err != nil {
		t.Fatalf("ensure install code: %v", err)
	}
	if !created || len(code) != 32 {
		t.Fatalf("generated code len=%d created=%v", len(code), created)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat install code: %v", err)
	}
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != 0o600 {
		t.Fatalf("install code mode = %o, want 600", got)
	}

	again, createdAgain, err := EnsureInstallCode(path, "", "uninitialized")
	if err != nil {
		t.Fatalf("reuse install code: %v", err)
	}
	if createdAgain || again != code {
		t.Fatalf("second ensure code=%q created=%v, want same code and false", again, createdAgain)
	}
}

func TestEnsureInstallCodeUsesExplicitOverrideAndInstalledStateDeletesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "install-code")
	configured := "abcdef0123456789abcdef0123456789"
	code, created, err := EnsureInstallCode(path, configured, "requires_admin_reset")
	if err != nil {
		t.Fatalf("configured install code: %v", err)
	}
	if code != configured || created {
		t.Fatalf("configured code=%q created=%v", code, created)
	}
	if err := os.WriteFile(path, []byte(configured+"\n"), 0o600); err != nil {
		t.Fatalf("seed install code file: %v", err)
	}
	code, created, err = EnsureInstallCode(path, "", "installed")
	if err != nil {
		t.Fatalf("installed cleanup: %v", err)
	}
	if code != "" || created {
		t.Fatalf("installed ensure code=%q created=%v", code, created)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("installed state retained install code: %v", err)
	}
}
