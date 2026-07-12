package server

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	adminV1BackupKDFMemory      = 64 * 1024
	adminV1BackupKDFIterations  = 3
	adminV1BackupKDFParallelism = 2
)

type adminV1EncryptedSecretsEnvelope struct {
	Version    int    `json:"version"`
	KDF        string `json:"kdf"`
	Cipher     string `json:"cipher"`
	Salt       string `json:"salt"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func adminV1EncryptSecrets(plaintext []byte, passphrase string) ([]byte, error) {
	if len(passphrase) < 12 {
		return nil, fmt.Errorf("recovery passphrase must contain at least 12 characters")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key := argon2.IDKey([]byte(passphrase), salt, adminV1BackupKDFIterations, adminV1BackupKDFMemory, adminV1BackupKDFParallelism, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, []byte("fullpro-backup-secrets-v1"))
	envelope := adminV1EncryptedSecretsEnvelope{
		Version: 1, KDF: "Argon2id", Cipher: "AES-256-GCM",
		Salt: base64.RawStdEncoding.EncodeToString(salt), Nonce: base64.RawStdEncoding.EncodeToString(nonce), Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext),
	}
	return json.Marshal(envelope)
}

func adminV1DecryptSecrets(encoded []byte, passphrase string) ([]byte, error) {
	decoder := json.NewDecoder(io.LimitReader(bytes.NewReader(encoded), 128<<10))
	decoder.DisallowUnknownFields()
	var envelope adminV1EncryptedSecretsEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode encrypted secrets: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("encrypted secrets contain trailing data")
	}
	if envelope.Version != 1 || envelope.KDF != "Argon2id" || envelope.Cipher != "AES-256-GCM" || len(passphrase) < 12 {
		return nil, fmt.Errorf("unsupported encrypted secrets envelope")
	}
	salt, saltErr := base64.RawStdEncoding.DecodeString(envelope.Salt)
	nonce, nonceErr := base64.RawStdEncoding.DecodeString(envelope.Nonce)
	ciphertext, ciphertextErr := base64.RawStdEncoding.DecodeString(envelope.Ciphertext)
	if saltErr != nil || nonceErr != nil || ciphertextErr != nil || len(salt) != 16 {
		return nil, fmt.Errorf("invalid encrypted secrets encoding")
	}
	key := argon2.IDKey([]byte(passphrase), salt, adminV1BackupKDFIterations, adminV1BackupKDFMemory, adminV1BackupKDFParallelism, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(nonce) != aead.NonceSize() {
		return nil, fmt.Errorf("invalid encrypted secrets nonce")
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, []byte("fullpro-backup-secrets-v1"))
	if err != nil {
		return nil, fmt.Errorf("invalid recovery passphrase or encrypted secrets")
	}
	return plaintext, nil
}

func adminV1ValidateSecretsJSON(raw []byte) error {
	decoder := json.NewDecoder(io.LimitReader(bytes.NewReader(raw), 32<<10))
	decoder.DisallowUnknownFields()
	var secrets Secrets
	if err := decoder.Decode(&secrets); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("secrets contain trailing data")
	}
	if len(secrets.TokenDerivationKey) != 32 || len(secrets.CookieKey) != 32 {
		return fmt.Errorf("secrets keys must each contain 256 bits")
	}
	return nil
}

func adminV1PrepareDataOnlyRestore(path string) error {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	tx, err := db.Begin()
	if err != nil {
		return errors.Join(err, db.Close())
	}
	for _, table := range []string{
		"admin_sessions", "admin_login_sessions", "sessions", "access_tokens",
		"refresh_tokens", "refresh_token_families", "install_sessions",
		"email_verification_tokens", "password_reset_tokens",
	} {
		if _, err := tx.Exec(`DELETE FROM ` + table); err != nil {
			_ = tx.Rollback()
			return errors.Join(fmt.Errorf("revoke restored %s: %w", table, err), db.Close())
		}
	}
	if err := tx.Commit(); err != nil {
		return errors.Join(err, db.Close())
	}
	return adminV1FinalizeStagedSQLite(context.Background(), db, path)
}

func adminV1CheckpointSQLite(ctx context.Context, db *sql.DB) error {
	var busy, logFrames, checkpointedFrames int
	if err := db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointedFrames); err != nil {
		return fmt.Errorf("SQLite WAL checkpoint failed: %w", err)
	}
	if busy != 0 || (logFrames >= 0 && checkpointedFrames != logFrames) {
		return fmt.Errorf("SQLite WAL checkpoint busy: busy=%d log=%d checkpointed=%d", busy, logFrames, checkpointedFrames)
	}
	return nil
}

func adminV1FinalizeStagedSQLite(ctx context.Context, db *sql.DB, path string) error {
	checkpointErr := adminV1CheckpointSQLite(ctx, db)
	if checkpointErr != nil {
		return errors.Join(checkpointErr, db.Close())
	}
	var journalMode string
	modeErr := db.QueryRowContext(ctx, `PRAGMA journal_mode=DELETE`).Scan(&journalMode)
	if modeErr == nil && !strings.EqualFold(journalMode, "delete") {
		modeErr = fmt.Errorf("SQLite journal mode remained %q", journalMode)
	}
	closeErr := db.Close()
	if modeErr != nil || closeErr != nil {
		return errors.Join(modeErr, closeErr)
	}
	for _, sidecar := range []string{path + "-wal", path + "-shm"} {
		if err := removeAdminV1File(sidecar); err != nil {
			return fmt.Errorf("remove finalized SQLite sidecar %s: %w", sidecar, err)
		}
		if _, err := os.Stat(sidecar); err == nil {
			return fmt.Errorf("finalized SQLite sidecar still exists: %s", sidecar)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect finalized SQLite sidecar %s: %w", sidecar, err)
		}
	}
	return adminV1CheckSQLiteMainFileOnly(path)
}

func adminV1CheckSQLiteMainFileOnly(path string) error {
	directory, err := os.MkdirTemp(filepath.Dir(path), ".admin-v1-main-only-check-*")
	if err != nil {
		return err
	}
	copyPath := filepath.Join(directory, "snapshot.sqlite")
	defer func() {
		_ = os.Remove(copyPath)
		_ = os.Remove(directory)
	}()
	if err := adminV1CopyFileAtomic(path, copyPath, 0o600); err != nil {
		return fmt.Errorf("copy standalone SQLite main file: %w", err)
	}
	if err := adminV1CheckSQLiteSnapshot(copyPath); err != nil {
		return fmt.Errorf("validate standalone SQLite main file: %w", err)
	}
	return nil
}
