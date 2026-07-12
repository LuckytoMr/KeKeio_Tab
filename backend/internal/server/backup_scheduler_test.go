package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunAutomaticBackupCreatesVerifiedDataOnlySnapshot(t *testing.T) {
	app, store := newTestApp(t)
	_ = app.Routes()
	backupDirectory := filepath.Join(t.TempDir(), "automatic-backups")
	setBackupDirectoryForTest(t, store, backupDirectory)
	if _, err := store.db.Exec(`INSERT INTO settings(key,value,updated_at) VALUES('automatic_backup_marker','present',?)`, nowString()); err != nil {
		t.Fatalf("seed automatic backup marker: %v", err)
	}

	result, err := store.RunAutomaticBackup(t.Context(), time.Date(2026, 7, 12, 2, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("run automatic backup: %v", err)
	}
	if !strings.HasPrefix(result.BackupID, "backup_auto_") {
		t.Fatalf("automatic backup id=%q", result.BackupID)
	}
	var kind, status, databasePath, manifestPath string
	if err := store.db.QueryRow(`SELECT kind,status,database_path,manifest_path FROM backup_records WHERE id=?`, result.BackupID).Scan(&kind, &status, &databasePath, &manifestPath); err != nil {
		t.Fatalf("read automatic backup record: %v", err)
	}
	if kind != "data-only" || status != "ready" {
		t.Fatalf("automatic backup kind=%q status=%q", kind, status)
	}
	if err := adminV1CheckSQLiteSnapshot(databasePath); err != nil {
		t.Fatalf("automatic backup snapshot invalid: %v", err)
	}
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read automatic manifest: %v", err)
	}
	var manifest adminV1BackupManifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil || manifest.Format != "fullpro-backup" || manifest.FormatVersion != 1 || manifest.ID != result.BackupID || manifest.Kind != "data-only" || manifest.SHA256 == "" {
		t.Fatalf("automatic manifest=%+v err=%v", manifest, err)
	}
}

func TestAutomaticBackupSerializesOnBackupMutex(t *testing.T) {
	app, store := newTestApp(t)
	_ = app.Routes()
	setBackupDirectoryForTest(t, store, filepath.Join(t.TempDir(), "serialized-backups"))

	store.backupMu.Lock()
	type outcome struct {
		result AutomaticBackupResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := store.RunAutomaticBackup(context.Background(), time.Now().UTC())
		done <- outcome{result: result, err: err}
	}()
	select {
	case got := <-done:
		store.backupMu.Unlock()
		t.Fatalf("automatic backup bypassed backup mutex: result=%+v err=%v", got.result, got.err)
	case <-time.After(150 * time.Millisecond):
	}
	store.backupMu.Unlock()
	select {
	case got := <-done:
		if got.err != nil || got.result.BackupID == "" {
			t.Fatalf("serialized automatic backup result=%+v err=%v", got.result, got.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("automatic backup did not resume after backup mutex release")
	}
}

func TestBackgroundMaintenanceSkipsWithoutWritesDuringPersistedMaintenance(t *testing.T) {
	for _, active := range []string{"maintenance", "restore"} {
		t.Run(active, func(t *testing.T) {
			app, store := newTestApp(t)
			_ = app.Routes()
			backupDirectory := filepath.Join(t.TempDir(), "must-remain-empty")
			setBackupDirectoryForTest(t, store, backupDirectory)
			now := time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)
			if err := store.InsertAPILog(t.Context(), APILogRecord{ID: "old-log", CreatedAt: now.Add(-365 * 24 * time.Hour).Format(time.RFC3339Nano), IP: "127.0.0.1", Method: "GET", Path: "/", RouteGroup: "/", Status: 200}); err != nil {
				t.Fatalf("seed old log: %v", err)
			}
			switch active {
			case "maintenance":
				if _, err := store.db.Exec(`INSERT INTO maintenance_jobs(id,kind,status,detail,error,created_at,started_at,finished_at) VALUES('job-active','cleanup','running','','',?,?,'')`, nowString(), nowString()); err != nil {
					t.Fatalf("seed active maintenance: %v", err)
				}
			case "restore":
				if _, err := store.db.Exec(`INSERT INTO backup_records(id,kind,status,database_path,manifest_path,checksum,size_bytes,created_at,restored_at) VALUES('backup-active','data-only','restoring','backup.sqlite','manifest.json','sha',1,?,'')`, nowString()); err != nil {
					t.Fatalf("seed active restore: %v", err)
				}
			}

			retention, err := store.RunRetention(t.Context(), now)
			if err != nil || !retention.Skipped {
				t.Fatalf("retention during %s = %+v err=%v", active, retention, err)
			}
			automatic, err := store.RunAutomaticBackup(t.Context(), now)
			if err != nil || !automatic.Skipped || automatic.BackupID != "" || automatic.Deleted != 0 {
				t.Fatalf("automatic backup during %s = %+v err=%v", active, automatic, err)
			}
			var oldLogs int
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM api_logs WHERE id='old-log'`).Scan(&oldLogs); err != nil || oldLogs != 1 {
				t.Fatalf("retention wrote during %s: oldLogs=%d err=%v", active, oldLogs, err)
			}
			if entries, err := os.ReadDir(backupDirectory); err == nil && len(entries) != 0 {
				t.Fatalf("automatic backup wrote %d files during %s", len(entries), active)
			} else if err != nil && !os.IsNotExist(err) {
				t.Fatalf("read skipped backup directory: %v", err)
			}
		})
	}
}

func TestAutomaticBackupRetentionKeepsSevenDailyAndFourWeekly(t *testing.T) {
	app, store := newTestApp(t)
	_ = app.Routes()
	directory := filepath.Join(t.TempDir(), "retention-backups")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("create retention backup directory: %v", err)
	}
	now := time.Date(2026, 7, 12, 2, 0, 0, 0, time.UTC)
	for day := 0; day < 42; day++ {
		id := fmt.Sprintf("backup_auto_%02d", day)
		databasePath := filepath.Join(directory, id+".sqlite")
		manifestPath := filepath.Join(directory, id+".manifest.json")
		if err := os.WriteFile(databasePath, []byte("database"), 0o600); err != nil {
			t.Fatalf("write retained database: %v", err)
		}
		if err := os.WriteFile(manifestPath, []byte("{}"), 0o600); err != nil {
			t.Fatalf("write retained manifest: %v", err)
		}
		createdAt := now.Add(-time.Duration(day) * 24 * time.Hour).Format(time.RFC3339Nano)
		if _, err := store.db.Exec(`INSERT INTO backup_records(id,kind,status,database_path,manifest_path,checksum,size_bytes,created_at,restored_at) VALUES(?, 'data-only','ready',?,?, 'sha',8,?,'')`, id, databasePath, manifestPath, createdAt); err != nil {
			t.Fatalf("seed automatic backup day %d: %v", day, err)
		}
	}
	manualDatabase := filepath.Join(directory, "manual.sqlite")
	manualManifest := filepath.Join(directory, "manual.manifest.json")
	_ = os.WriteFile(manualDatabase, []byte("manual"), 0o600)
	_ = os.WriteFile(manualManifest, []byte("{}"), 0o600)
	if _, err := store.db.Exec(`INSERT INTO backup_records(id,kind,status,database_path,manifest_path,checksum,size_bytes,created_at,restored_at) VALUES('backup_manual','data-only','ready',?,?,'sha',6,?,'')`, manualDatabase, manualManifest, now.Add(-100*24*time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("seed manual backup: %v", err)
	}

	deleted, err := store.pruneAutomaticBackups(t.Context(), now)
	if err != nil {
		t.Fatalf("prune automatic backups: %v", err)
	}
	if deleted != 31 {
		t.Fatalf("automatic backups deleted=%d want=31", deleted)
	}
	var automaticCount, manualCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM backup_records WHERE id LIKE 'backup_auto_%'`).Scan(&automaticCount); err != nil || automaticCount != 11 {
		t.Fatalf("automatic backup count=%d err=%v want=11", automaticCount, err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM backup_records WHERE id='backup_manual'`).Scan(&manualCount); err != nil || manualCount != 1 {
		t.Fatalf("manual backup count=%d err=%v", manualCount, err)
	}
	for day := 0; day < 7; day++ {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM backup_records WHERE id=?`, fmt.Sprintf("backup_auto_%02d", day)).Scan(&count); err != nil || count != 1 {
			t.Fatalf("daily backup day=%d count=%d err=%v", day, count, err)
		}
	}
}

func TestBackupSchedulerRunsImmediatelyAndStopsOnCancellation(t *testing.T) {
	app, store := newTestApp(t)
	_ = app.Routes()
	setBackupDirectoryForTest(t, store, filepath.Join(t.TempDir(), "scheduled-backups"))
	ctx, cancel := context.WithCancel(context.Background())
	reports := make(chan AutomaticBackupResult, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		store.RunBackupScheduler(ctx, time.Hour, func(result AutomaticBackupResult, err error) {
			if err == nil {
				reports <- result
			}
		})
	}()
	select {
	case result := <-reports:
		if result.BackupID == "" {
			t.Fatal("scheduler reported an empty backup id")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("backup scheduler did not run at startup")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("backup scheduler did not stop after cancellation")
	}
}

func setBackupDirectoryForTest(t *testing.T, store *Store, directory string) {
	t.Helper()
	limits, err := json.Marshal(map[string]any{"backupDirectory": directory})
	if err != nil {
		t.Fatalf("marshal backup limits: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO settings(key,value,updated_at) VALUES('limits',?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, string(limits), nowString()); err != nil {
		t.Fatalf("set backup directory: %v", err)
	}
}
