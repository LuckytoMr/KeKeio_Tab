package server

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
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
	secrets := Secrets{TokenDerivationKey: make([]byte, 32), CookieKey: make([]byte, 32)}
	if _, err := rand.Read(secrets.TokenDerivationKey); err != nil {
		return Secrets{}, false, err
	}
	if _, err := rand.Read(secrets.CookieKey); err != nil {
		return Secrets{}, false, err
	}
	if err := writeFileAtomicDurable(path, ".secrets-*", 0o600, func(writer io.Writer) error {
		return json.NewEncoder(writer).Encode(secrets)
	}); err != nil {
		return Secrets{}, false, err
	}
	return secrets, true, nil
}

func SaveSecrets(path string, secrets Secrets) error {
	if path == "" || len(secrets.TokenDerivationKey) != 32 || len(secrets.CookieKey) != 32 {
		return fmt.Errorf("valid secrets path and 256-bit keys are required")
	}
	return writeFileAtomicDurable(path, ".secrets-update-*", 0o600, func(writer io.Writer) error {
		return json.NewEncoder(writer).Encode(secrets)
	})
}
