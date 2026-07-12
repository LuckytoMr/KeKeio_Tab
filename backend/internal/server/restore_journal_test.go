package server

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoverPendingRestoreRollsBackEveryIncompleteSwapStage(t *testing.T) {
	for _, crashPhase := range []string{
		restorePhaseDatabaseBackedUp,
		restorePhaseSecretsBackedUp,
		restorePhaseDatabaseInstalled,
		restorePhaseSecretsInstalled,
	} {
		t.Run(crashPhase, func(t *testing.T) {
			app, liveDatabase, stagedDatabase, liveSecrets, stagedSecrets := newRestoreJournalFixture(t)
			intent, err := newAdminV1RestoreIntent("backup-crash", liveDatabase, stagedDatabase, liveSecrets, stagedSecrets)
			if err != nil {
				t.Fatalf("create restore intent: %v", err)
			}
			if err := writeAdminV1RestoreIntent(intent); err != nil {
				t.Fatalf("write restore intent: %v", err)
			}

			previousHook := adminV1RestoreCrashHook
			adminV1RestoreCrashHook = func(phase string) bool { return phase == crashPhase }
			t.Cleanup(func() { adminV1RestoreCrashHook = previousHook })
			if err := applyAdminV1Restore(app, liveDatabase, stagedDatabase, liveSecrets, stagedSecrets); !errors.Is(err, errAdminV1RestoreCrashInjected) {
				t.Fatalf("apply restore crash phase %s err=%v", crashPhase, err)
			}
			adminV1RestoreCrashHook = previousHook

			if err := RecoverPendingRestore(liveDatabase, liveSecrets); err != nil {
				t.Fatalf("recover incomplete restore: %v", err)
			}
			assertRestoreFixtureState(t, liveDatabase, liveSecrets, "original", "original-secret")
			if _, err := os.Stat(adminV1RestoreJournalPath(liveDatabase)); !os.IsNotExist(err) {
				t.Fatalf("restore journal survived recovery: %v", err)
			}
		})
	}
}

func TestRecoverPendingRestoreKeepsCompletedPairAfterCrashBeforeCleanup(t *testing.T) {
	app, liveDatabase, stagedDatabase, liveSecrets, stagedSecrets := newRestoreJournalFixture(t)
	intent, err := newAdminV1RestoreIntent("backup-complete", liveDatabase, stagedDatabase, liveSecrets, stagedSecrets)
	if err != nil {
		t.Fatalf("create restore intent: %v", err)
	}
	if err := writeAdminV1RestoreIntent(intent); err != nil {
		t.Fatalf("write restore intent: %v", err)
	}
	previousHook := adminV1RestoreCrashHook
	adminV1RestoreCrashHook = func(phase string) bool { return phase == restorePhaseCompleted }
	t.Cleanup(func() { adminV1RestoreCrashHook = previousHook })
	if err := applyAdminV1Restore(app, liveDatabase, stagedDatabase, liveSecrets, stagedSecrets); !errors.Is(err, errAdminV1RestoreCrashInjected) {
		t.Fatalf("apply completed restore crash err=%v", err)
	}
	adminV1RestoreCrashHook = previousHook

	if err := RecoverPendingRestore(liveDatabase, liveSecrets); err != nil {
		t.Fatalf("recover completed restore: %v", err)
	}
	assertRestoreFixtureState(t, liveDatabase, liveSecrets, "restored", "restored-secret")
}

func TestApplyRestoreRejectsBusyWALCheckpointAndPreservesCommittedWrite(t *testing.T) {
	app, liveDatabase, stagedDatabase, liveSecrets, stagedSecrets := newRestoreJournalFixture(t)
	t.Cleanup(func() { _ = app.store.Close() })
	reader, err := sql.Open("sqlite", liveDatabase)
	if err != nil {
		t.Fatalf("open independent live reader: %v", err)
	}
	readerTx, err := reader.Begin()
	if err != nil {
		_ = reader.Close()
		t.Fatalf("begin independent live read: %v", err)
	}
	var marker string
	if err := readerTx.QueryRow(`SELECT value FROM settings WHERE key='restore_marker'`).Scan(&marker); err != nil {
		_ = readerTx.Rollback()
		_ = reader.Close()
		t.Fatalf("establish independent reader snapshot: %v", err)
	}
	if _, err := app.store.db.Exec(`INSERT INTO settings(key,value,updated_at) VALUES('committed_during_drain','preserve-me',?)`, nowString()); err != nil {
		_ = readerTx.Rollback()
		_ = reader.Close()
		t.Fatalf("commit live WAL write: %v", err)
	}
	intent, err := newAdminV1RestoreIntent("backup-busy", liveDatabase, stagedDatabase, liveSecrets, stagedSecrets)
	if err != nil {
		t.Fatalf("create restore intent: %v", err)
	}
	if err := writeAdminV1RestoreIntent(intent); err != nil {
		t.Fatalf("write restore intent: %v", err)
	}

	applyErr := applyAdminV1Restore(app, liveDatabase, stagedDatabase, liveSecrets, stagedSecrets)
	if applyErr == nil || !strings.Contains(strings.ToLower(applyErr.Error()), "checkpoint") {
		_ = readerTx.Rollback()
		_ = reader.Close()
		t.Fatalf("restore accepted a busy live WAL checkpoint: %v", applyErr)
	}
	if err := readerTx.Rollback(); err != nil {
		t.Fatalf("release independent live reader: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close independent live reader: %v", err)
	}
	check, err := sql.Open("sqlite", liveDatabase+"?_pragma=query_only(1)")
	if err != nil {
		t.Fatalf("reopen live database: %v", err)
	}
	defer check.Close()
	var preserved string
	if err := check.QueryRow(`SELECT value FROM settings WHERE key='committed_during_drain'`).Scan(&preserved); err != nil || preserved != "preserve-me" {
		t.Fatalf("committed live WAL write was lost: value=%q err=%v", preserved, err)
	}
}

func TestNewAppFailsClosedWhenRestoreRecordIsStillRunning(t *testing.T) {
	store := newTestStore(t)
	app := NewApp(store, Config{})
	_ = app.Routes()
	if _, err := store.db.Exec(`INSERT INTO backup_records(id,kind,status,database_path,manifest_path,checksum,size_bytes,created_at,restored_at)
		VALUES('backup-stale','data-only','restoring','backup.sqlite','backup.json','sha',1,?,'')`, nowString()); err != nil {
		t.Fatalf("seed active restore: %v", err)
	}
	app = NewApp(store, Config{})
	if !app.maintenanceMode.Load() {
		t.Fatal("new app accepted writes while persisted restore was running")
	}
}

func TestNewRestoreIntentRemovesOrphanedPhaseMarkers(t *testing.T) {
	app, liveDatabase, stagedDatabase, liveSecrets, stagedSecrets := newRestoreJournalFixture(t)
	defer app.store.Close()
	orphan := adminV1RestorePhasePath(liveDatabase, restorePhaseCompleted)
	if err := os.WriteFile(orphan, []byte(`{"phase":"completed"}`), 0o600); err != nil {
		t.Fatalf("write orphan restore marker: %v", err)
	}
	intent, err := newAdminV1RestoreIntent("backup-new", liveDatabase, stagedDatabase, liveSecrets, stagedSecrets)
	if err != nil {
		t.Fatalf("create new restore intent: %v", err)
	}
	if err := writeAdminV1RestoreIntent(intent); err != nil {
		t.Fatalf("write new restore intent: %v", err)
	}
	if adminV1RestorePhaseCompleted(intent) {
		t.Fatal("orphan completed marker contaminated a new restore")
	}
}

func newRestoreJournalFixture(t *testing.T) (*App, string, string, string, string) {
	t.Helper()
	directory := t.TempDir()
	liveDatabase := filepath.Join(directory, "live.sqlite")
	store, err := OpenStore(liveDatabase)
	if err != nil {
		t.Fatalf("open live restore fixture: %v", err)
	}
	app := NewApp(store, Config{})
	_ = app.Routes()
	if _, err := store.db.Exec(`INSERT INTO settings(key,value,updated_at) VALUES('restore_marker','original',?)`, nowString()); err != nil {
		t.Fatalf("seed original restore marker: %v", err)
	}
	stagedDatabase := filepath.Join(directory, "staged.sqlite")
	if _, err := store.db.Exec(`VACUUM INTO ?`, stagedDatabase); err != nil {
		t.Fatalf("snapshot staged restore fixture: %v", err)
	}
	staged, err := sql.Open("sqlite", stagedDatabase)
	if err != nil {
		t.Fatalf("open staged restore fixture: %v", err)
	}
	if _, err := staged.Exec(`UPDATE settings SET value='restored' WHERE key='restore_marker'`); err != nil {
		_ = staged.Close()
		t.Fatalf("update staged restore marker: %v", err)
	}
	if err := staged.Close(); err != nil {
		t.Fatalf("close staged restore fixture: %v", err)
	}

	liveSecrets := filepath.Join(directory, "secrets.json")
	stagedSecrets := filepath.Join(directory, "staged-secrets.json")
	original := Secrets{TokenDerivationKey: []byte("0123456789abcdef0123456789abcdef"), CookieKey: []byte("abcdef0123456789abcdef0123456789"), SMTPPassword: "original-secret"}
	restored := Secrets{TokenDerivationKey: []byte("11111111111111111111111111111111"), CookieKey: []byte("22222222222222222222222222222222"), SMTPPassword: "restored-secret"}
	if err := SaveSecrets(liveSecrets, original); err != nil {
		t.Fatalf("write original secrets: %v", err)
	}
	if err := SaveSecrets(stagedSecrets, restored); err != nil {
		t.Fatalf("write staged secrets: %v", err)
	}
	app.config.SecretsPath = liveSecrets
	return app, liveDatabase, stagedDatabase, liveSecrets, stagedSecrets
}

func assertRestoreFixtureState(t *testing.T, databasePath, secretsPath, marker, smtpPassword string) {
	t.Helper()
	db, err := sql.Open("sqlite", databasePath+"?_pragma=query_only(1)")
	if err != nil {
		t.Fatalf("open recovered database: %v", err)
	}
	defer db.Close()
	var actualMarker string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='restore_marker'`).Scan(&actualMarker); err != nil || actualMarker != marker {
		t.Fatalf("recovered marker=%q err=%v want=%q", actualMarker, err, marker)
	}
	secrets, _, err := LoadOrCreateSecrets(secretsPath)
	if err != nil || secrets.SMTPPassword != smtpPassword {
		t.Fatalf("recovered secrets=%#v err=%v want SMTP=%q", secrets, err, smtpPassword)
	}
}
