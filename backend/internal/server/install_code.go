package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func EnsureInstallCode(path, configured, installationState string) (string, bool, error) {
	if installationState == "installed" {
		if path != "" {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return "", false, err
			}
		}
		return "", false, nil
	}
	configured = strings.TrimSpace(configured)
	if configured != "" {
		if !validInstallCode(configured) {
			return "", false, fmt.Errorf("install code must be exactly 128 bits encoded as 32 hex characters")
		}
		return configured, false, nil
	}
	if path == "" {
		return "", false, fmt.Errorf("install code path is required")
	}
	if existing, err := os.ReadFile(path); err == nil {
		code := strings.TrimSpace(string(existing))
		if !validInstallCode(code) {
			return "", false, fmt.Errorf("invalid install code file")
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return "", false, err
		}
		return code, false, nil
	} else if !os.IsNotExist(err) {
		return "", false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", false, err
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", false, err
	}
	code := hex.EncodeToString(raw)
	temporary, err := os.CreateTemp(filepath.Dir(path), ".install-code-*")
	if err != nil {
		return "", false, err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return "", false, err
	}
	if _, err := temporary.WriteString(code + "\n"); err != nil {
		_ = temporary.Close()
		return "", false, err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return "", false, err
	}
	if err := temporary.Close(); err != nil {
		return "", false, err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return "", false, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", false, err
	}
	return code, true, nil
}

func validInstallCode(code string) bool {
	if len(code) != 32 {
		return false
	}
	raw, err := hex.DecodeString(code)
	return err == nil && len(raw) == 16
}
