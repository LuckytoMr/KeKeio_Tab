package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type AutomaticBackupResult struct {
	BackupID string `json:"backupId,omitempty"`
	Deleted  int64  `json:"deleted"`
	Skipped  bool   `json:"skipped,omitempty"`
}

type automaticBackupRecord struct {
	ID           string
	DatabasePath string
	ManifestPath string
	CreatedAt    time.Time
}

func (s *Store) RunAutomaticBackup(ctx context.Context, now time.Time) (AutomaticBackupResult, error) {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	s.maintenanceMu.Lock()
	defer s.maintenanceMu.Unlock()
	active, err := s.persistedMaintenanceActive(ctx)
	if err != nil {
		return AutomaticBackupResult{}, fmt.Errorf("check persisted maintenance state: %w", err)
	}
	if active {
		return AutomaticBackupResult{Skipped: true}, nil
	}
	state, err := s.InstallationState(ctx)
	if err != nil {
		return AutomaticBackupResult{}, err
	}
	if state != "installed" {
		return AutomaticBackupResult{Skipped: true}, nil
	}
	livePath, err := adminV1DatabasePath(ctx, s.db)
	if err != nil || livePath == "" {
		return AutomaticBackupResult{}, fmt.Errorf("locate automatic backup database: %w", err)
	}
	backupDirectory, err := s.backupDirectory(ctx, livePath)
	if err != nil {
		return AutomaticBackupResult{}, err
	}
	if err := os.MkdirAll(backupDirectory, 0o700); err != nil {
		return AutomaticBackupResult{}, err
	}
	backupID := newID("backup_auto_")
	createdAt := now.UTC().Format(time.RFC3339Nano)
	databasePath := filepath.Join(backupDirectory, backupID+".sqlite")
	manifestPath := filepath.Join(backupDirectory, backupID+".manifest.json")
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(databasePath)
			_ = os.Remove(manifestPath)
		}
	}()
	if err := createSQLiteOnlineBackup(ctx, s.db, databasePath); err != nil {
		return AutomaticBackupResult{}, err
	}
	if err := os.Chmod(databasePath, 0o600); err != nil {
		return AutomaticBackupResult{}, err
	}
	if err := adminV1CheckSQLiteSnapshot(databasePath); err != nil {
		return AutomaticBackupResult{}, err
	}
	checksum, sizeBytes, err := adminV1FileSHA256(databasePath)
	if err != nil {
		return AutomaticBackupResult{}, err
	}
	manifest := adminV1BackupManifest{
		Format: adminV1BackupManifestFormat, FormatVersion: adminV1BackupManifestVersion,
		ID: backupID, Kind: "data-only", CreatedAt: createdAt, SchemaVersion: schemaVersion,
		DatabaseFile: filepath.Base(databasePath), SizeBytes: sizeBytes, SHA256: checksum,
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return AutomaticBackupResult{}, err
	}
	if err := adminV1WriteFileAtomic(manifestPath, append(manifestJSON, '\n'), 0o600); err != nil {
		return AutomaticBackupResult{}, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO backup_records(id,kind,status,database_path,manifest_path,checksum,size_bytes,created_at,restored_at)
		VALUES(?, 'data-only','ready',?,?,?,?,?,'')`, backupID, databasePath, manifestPath, checksum, sizeBytes, createdAt); err != nil {
		return AutomaticBackupResult{}, err
	}
	cleanup = false
	deleted, err := s.pruneAutomaticBackupsLocked(ctx, now.UTC())
	return AutomaticBackupResult{BackupID: backupID, Deleted: deleted}, err
}

func (s *Store) RunBackupScheduler(ctx context.Context, interval time.Duration, report func(AutomaticBackupResult, error)) {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	run := func() {
		result, err := s.RunAutomaticBackup(ctx, time.Now().UTC())
		if report != nil {
			report(result, err)
		}
	}
	select {
	case <-ctx.Done():
		return
	default:
		run()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func (s *Store) SetBackupDirectoryOverride(directory string) error {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		s.backupDirectoryOverride = ""
		return nil
	}
	if !filepath.IsAbs(directory) {
		return fmt.Errorf("backup directory override must be an absolute path")
	}
	directory = filepath.Clean(directory)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create backup directory override: %w", err)
	}
	probe, err := os.CreateTemp(directory, ".fullpro-write-check-*")
	if err != nil {
		return fmt.Errorf("open backup directory write check: %w", err)
	}
	probePath := probe.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = probe.Close()
			_ = os.Remove(probePath)
		}
	}()
	if _, err := probe.WriteString("ok\n"); err != nil {
		return fmt.Errorf("write backup directory check: %w", err)
	}
	if err := probe.Sync(); err != nil {
		return fmt.Errorf("sync backup directory check: %w", err)
	}
	if err := probe.Close(); err != nil {
		return fmt.Errorf("close backup directory check: %w", err)
	}
	if err := os.Remove(probePath); err != nil {
		return fmt.Errorf("remove backup directory check: %w", err)
	}
	cleanup = false
	s.backupDirectoryOverride = directory
	return nil
}

func (s *Store) backupDirectory(ctx context.Context, livePath string) (string, error) {
	if s.backupDirectoryOverride != "" {
		return s.backupDirectoryOverride, nil
	}
	settings, err := s.LoadRuntimeSettings(ctx)
	if err != nil {
		return "", err
	}
	directory, _ := settings.Limits["backupDirectory"].(string)
	directory = strings.TrimSpace(directory)
	if directory == "" {
		directory = filepath.Join(filepath.Dir(livePath), "backups")
	} else if !filepath.IsAbs(directory) {
		directory = filepath.Join(filepath.Dir(livePath), directory)
	}
	return filepath.Clean(directory), nil
}

func (s *Store) pruneAutomaticBackups(ctx context.Context, now time.Time) (int64, error) {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	return s.pruneAutomaticBackupsLocked(ctx, now)
}

func (s *Store) pruneAutomaticBackupsLocked(ctx context.Context, now time.Time) (int64, error) {
	_ = now
	rows, err := s.db.QueryContext(ctx, `SELECT id,database_path,manifest_path,created_at FROM backup_records
		WHERE id GLOB 'backup_auto_*' AND status='ready' ORDER BY created_at DESC`)
	if err != nil {
		return 0, err
	}
	var records []automaticBackupRecord
	for rows.Next() {
		var record automaticBackupRecord
		var createdAt string
		if err := rows.Scan(&record.ID, &record.DatabasePath, &record.ManifestPath, &createdAt); err != nil {
			_ = rows.Close()
			return 0, err
		}
		parsed, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("parse automatic backup time: %w", err)
		}
		record.CreatedAt = parsed.UTC()
		records = append(records, record)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	sort.SliceStable(records, func(left, right int) bool { return records[left].CreatedAt.After(records[right].CreatedAt) })
	keep := map[string]bool{}
	dailyDays := map[string]bool{}
	for _, record := range records {
		day := record.CreatedAt.Format("2006-01-02")
		if len(dailyDays) < 7 && !dailyDays[day] {
			dailyDays[day] = true
			keep[record.ID] = true
		}
	}
	weeklyBuckets := map[string]bool{}
	for _, record := range records {
		if keep[record.ID] || dailyDays[record.CreatedAt.Format("2006-01-02")] {
			continue
		}
		year, week := record.CreatedAt.ISOWeek()
		bucket := fmt.Sprintf("%04d-%02d", year, week)
		if len(weeklyBuckets) < 4 && !weeklyBuckets[bucket] {
			weeklyBuckets[bucket] = true
			keep[record.ID] = true
		}
	}
	var deleted int64
	for _, record := range records {
		if keep[record.ID] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		if err := removeAdminV1File(record.DatabasePath); err != nil {
			return deleted, err
		}
		if err := removeAdminV1File(record.ManifestPath); err != nil {
			return deleted, err
		}
		result, err := s.db.ExecContext(ctx, `DELETE FROM backup_records WHERE id=? AND status='ready'`, record.ID)
		if err != nil {
			return deleted, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return deleted, err
		}
		deleted += affected
	}
	return deleted, nil
}
