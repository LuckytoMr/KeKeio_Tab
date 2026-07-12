package server

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type Secrets struct {
	TokenDerivationKey []byte `json:"tokenDerivationKey"`
	CookieKey          []byte `json:"cookieKey"`
	SMTPPassword       string `json:"smtpPassword,omitempty"`
}

func LoadOrCreateSecrets(path string) (Secrets, bool, error) {
	if path == "" {
		return Secrets{}, false, fmt.Errorf("secrets path is required")
	}
	if file, err := os.Open(path); err == nil {
		defer file.Close()
		decoder := json.NewDecoder(io.LimitReader(file, 16<<10))
		decoder.DisallowUnknownFields()
		var secrets Secrets
		if err := decoder.Decode(&secrets); err != nil {
			return Secrets{}, false, fmt.Errorf("decode secrets: %w", err)
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return Secrets{}, false, fmt.Errorf("secrets contain trailing data")
		}
		if len(secrets.TokenDerivationKey) != 32 || len(secrets.CookieKey) != 32 {
			return Secrets{}, false, fmt.Errorf("secrets keys must each contain 256 bits")
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return Secrets{}, false, err
		}
		return secrets, false, nil
	} else if !os.IsNotExist(err) {
		return Secrets{}, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Secrets{}, false, err
	}
	secrets := Secrets{TokenDerivationKey: make([]byte, 32), CookieKey: make([]byte, 32)}
	if _, err := rand.Read(secrets.TokenDerivationKey); err != nil {
		return Secrets{}, false, err
	}
	if _, err := rand.Read(secrets.CookieKey); err != nil {
		return Secrets{}, false, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".secrets-*")
	if err != nil {
		return Secrets{}, false, err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return Secrets{}, false, err
	}
	encoder := json.NewEncoder(temporary)
	if err := encoder.Encode(secrets); err != nil {
		_ = temporary.Close()
		return Secrets{}, false, err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return Secrets{}, false, err
	}
	if err := temporary.Close(); err != nil {
		return Secrets{}, false, err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return Secrets{}, false, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return Secrets{}, false, err
	}
	return secrets, true, nil
}

func SaveSecrets(path string, secrets Secrets) error {
	if path == "" || len(secrets.TokenDerivationKey) != 32 || len(secrets.CookieKey) != 32 {
		return fmt.Errorf("valid secrets path and 256-bit keys are required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".secrets-update-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := json.NewEncoder(temporary).Encode(secrets); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
