package server

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestFailedMigrationRollsBackAllEarlierDDLAndDataChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken-legacy.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy fixture: %v", err)
	}
	for _, statement := range []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY,email TEXT NOT NULL UNIQUE,password_hash TEXT NOT NULL,role TEXT NOT NULL,created_at TEXT NOT NULL,last_login_at TEXT NOT NULL DEFAULT '')`,
		`INSERT INTO users(id,email,password_hash,role,created_at,last_login_at) VALUES('legacy-admin','owner@example.test','hash','admin','2026-01-01T00:00:00Z','')`,
		`CREATE TABLE installation_state (id INTEGER PRIMARY KEY)`,
	} {
		if _, err := raw.Exec(statement); err != nil {
			t.Fatalf("seed legacy fixture: %v", err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close legacy fixture: %v", err)
	}

	if store, err := OpenStore(path); err == nil {
		store.Close()
		t.Fatal("malformed legacy schema unexpectedly migrated")
	}
	backups, err := filepath.Glob(path + ".pre-migration-v*.sqlite")
	if err != nil || len(backups) != 1 {
		t.Fatalf("failed migration backup files=%v err=%v, want exactly one verified snapshot", backups, err)
	}
	verify, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen failed migration fixture: %v", err)
	}
	defer verify.Close()
	var role string
	if err := verify.QueryRow(`SELECT role FROM users WHERE id='legacy-admin'`).Scan(&role); err != nil || role != "admin" {
		t.Fatalf("failed migration changed original role=%q err=%v", role, err)
	}
	var migrationTable int
	if err := verify.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&migrationTable); err != nil || migrationTable != 0 {
		t.Fatalf("failed migration left schema_migrations table=%d err=%v", migrationTable, err)
	}
}

func TestOpenStoreRejectsFutureSchemaBeforeCreatingMigrationSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatalf("create current store: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, schemaVersion+1, nowString()); err != nil {
		t.Fatalf("seed future migration: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close future store fixture: %v", err)
	}

	if reopened, err := OpenStore(path); err == nil {
		_ = reopened.Close()
		t.Fatal("future schema unexpectedly opened")
	} else if !strings.Contains(err.Error(), "unsupported schema migration state") {
		t.Fatalf("future schema error = %v", err)
	}
	backups, err := filepath.Glob(path + ".pre-migration-v*.sqlite")
	if err != nil || len(backups) != 0 {
		t.Fatalf("future schema created migration snapshots=%v err=%v", backups, err)
	}
}

func TestOpenStoreRejectsNonContiguousSchemaBeforeCreatingMigrationSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migration-hole.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatalf("create current store: %v", err)
	}
	if _, err := store.db.Exec(`DELETE FROM schema_migrations WHERE version=3`); err != nil {
		t.Fatalf("create schema hole: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close schema-hole fixture: %v", err)
	}

	if reopened, err := OpenStore(path); err == nil {
		_ = reopened.Close()
		t.Fatal("non-contiguous schema unexpectedly opened")
	} else if !strings.Contains(err.Error(), "unsupported schema migration state") {
		t.Fatalf("schema-hole error = %v", err)
	}
	backups, err := filepath.Glob(path + ".pre-migration-v*.sqlite")
	if err != nil || len(backups) != 0 {
		t.Fatalf("schema hole created migration snapshots=%v err=%v", backups, err)
	}
}
