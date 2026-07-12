package server

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadOrCreateSecretsPersistsStableKeysAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	first, created, err := LoadOrCreateSecrets(path)
	if err != nil {
		t.Fatalf("create secrets: %v", err)
	}
	if !created || len(first.TokenDerivationKey) != 32 || len(first.CookieKey) != 32 {
		t.Fatalf("created secrets = %#v created=%v", first, created)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat secrets: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("secrets mode = %o, want 600", info.Mode().Perm())
	}

	second, createdAgain, err := LoadOrCreateSecrets(path)
	if err != nil {
		t.Fatalf("reload secrets: %v", err)
	}
	if createdAgain || !bytes.Equal(first.TokenDerivationKey, second.TokenDerivationKey) || !bytes.Equal(first.CookieKey, second.CookieKey) {
		t.Fatalf("secrets changed across restart: first=%#v second=%#v created=%v", first, second, createdAgain)
	}
	first.SMTPPassword = "smtp-secret"
	if err := SaveSecrets(path, first); err != nil {
		t.Fatalf("save smtp secret: %v", err)
	}
	reloaded, _, err := LoadOrCreateSecrets(path)
	if err != nil || reloaded.SMTPPassword != "smtp-secret" {
		t.Fatalf("reload smtp secret = %q err=%v", reloaded.SMTPPassword, err)
	}
}

func TestLoadOrCreateSecretsFailsClosedOnMalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	if err := os.WriteFile(path, []byte(`{"tokenDerivationKey":"short"}`), 0o600); err != nil {
		t.Fatalf("write malformed secrets: %v", err)
	}
	if _, _, err := LoadOrCreateSecrets(path); err == nil {
		t.Fatal("malformed secrets file was accepted")
	}
}
