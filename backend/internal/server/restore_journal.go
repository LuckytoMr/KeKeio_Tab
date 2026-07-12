package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	restoreIntentVersion          = 1
	restorePhasePrepared          = "prepared"
	restorePhaseDatabaseBackedUp  = "database-backed-up"
	restorePhaseSecretsBackedUp   = "secrets-backed-up"
	restorePhaseDatabaseInstalled = "database-installed"
	restorePhaseSecretsInstalled  = "secrets-installed"
	restorePhaseCompleted         = "completed"
)

var (
	errAdminV1RestoreCrashInjected = errors.New("admin restore crash injected")
	adminV1RestoreCrashHook        func(string) bool
)

type adminV1RestoreIntent struct {
	Version            int    `json:"version"`
	BackupID           string `json:"backupId"`
	LiveDatabase       string `json:"liveDatabase"`
	StagedDatabase     string `json:"stagedDatabase"`
	RollbackDatabase   string `json:"rollbackDatabase"`
	LiveSecrets        string `json:"liveSecrets,omitempty"`
	StagedSecrets      string `json:"stagedSecrets,omitempty"`
	RollbackSecrets    string `json:"rollbackSecrets,omitempty"`
	LiveSecretsExisted bool   `json:"liveSecretsExisted"`
	CreatedAt          string `json:"createdAt"`
}

func adminV1RestoreJournalPath(liveDatabase string) string {
	return filepath.Clean(liveDatabase) + ".restore-intent.json"
}

func adminV1RestorePhasePath(liveDatabase, phase string) string {
	return adminV1RestoreJournalPath(liveDatabase) + "." + phase
}

func newAdminV1RestoreIntent(backupID, liveDatabase, stagedDatabase, liveSecrets, stagedSecrets string) (adminV1RestoreIntent, error) {
	liveDatabase = filepath.Clean(liveDatabase)
	stagedDatabase = filepath.Clean(stagedDatabase)
	if liveDatabase == "." || stagedDatabase == "." || liveDatabase == stagedDatabase {
		return adminV1RestoreIntent{}, fmt.Errorf("distinct live and staged database paths are required")
	}
	rollbackSuffix := ".pre-restore-" + strconvRestoreTimestamp()
	intent := adminV1RestoreIntent{
		Version:          restoreIntentVersion,
		BackupID:         backupID,
		LiveDatabase:     liveDatabase,
		StagedDatabase:   stagedDatabase,
		RollbackDatabase: liveDatabase + rollbackSuffix,
		CreatedAt:        nowString(),
	}
	if liveSecrets != "" || stagedSecrets != "" {
		if liveSecrets == "" || stagedSecrets == "" {
			return adminV1RestoreIntent{}, fmt.Errorf("both live and staged secrets paths are required")
		}
		intent.LiveSecrets = filepath.Clean(liveSecrets)
		intent.StagedSecrets = filepath.Clean(stagedSecrets)
		intent.RollbackSecrets = intent.LiveSecrets + rollbackSuffix
		_, err := os.Stat(intent.LiveSecrets)
		intent.LiveSecretsExisted = err == nil
		if err != nil && !os.IsNotExist(err) {
			return adminV1RestoreIntent{}, err
		}
	}
	if err := validateAdminV1RestoreIntent(intent, liveDatabase, liveSecrets); err != nil {
		return adminV1RestoreIntent{}, err
	}
	return intent, nil
}

func strconvRestoreTimestamp() string {
	return fmt.Sprintf("%d", time.Now().UTC().UnixNano())
}

func validateAdminV1RestoreIntent(intent adminV1RestoreIntent, expectedDatabase, expectedSecrets string) error {
	if intent.Version != restoreIntentVersion || intent.LiveDatabase == "" || intent.StagedDatabase == "" || intent.RollbackDatabase == "" {
		return fmt.Errorf("invalid restore intent")
	}
	pathsEqual := func(left, right string) bool {
		leftAbs, leftErr := filepath.Abs(filepath.Clean(left))
		rightAbs, rightErr := filepath.Abs(filepath.Clean(right))
		if leftErr != nil || rightErr != nil {
			return false
		}
		if runtime.GOOS == "windows" {
			return strings.EqualFold(leftAbs, rightAbs)
		}
		return leftAbs == rightAbs
	}
	if !pathsEqual(intent.LiveDatabase, expectedDatabase) {
		return fmt.Errorf("restore intent database path mismatch")
	}
	if expectedSecrets == "" {
		if intent.LiveSecrets != "" || intent.StagedSecrets != "" || intent.RollbackSecrets != "" {
			return fmt.Errorf("unexpected restore secrets paths")
		}
	} else if !pathsEqual(intent.LiveSecrets, expectedSecrets) || intent.StagedSecrets == "" || intent.RollbackSecrets == "" {
		return fmt.Errorf("restore intent secrets path mismatch")
	}
	for _, pair := range [][2]string{
		{filepath.Dir(intent.LiveDatabase), intent.StagedDatabase},
		{filepath.Dir(intent.LiveDatabase), intent.RollbackDatabase},
		{filepath.Dir(intent.LiveSecrets), intent.StagedSecrets},
		{filepath.Dir(intent.LiveSecrets), intent.RollbackSecrets},
	} {
		if pair[1] != "" && !adminV1PathWithin(pair[0], pair[1]) {
			return fmt.Errorf("restore intent path escapes its live directory")
		}
	}
	return nil
}

func writeAdminV1RestoreIntent(intent adminV1RestoreIntent) error {
	journalPath := adminV1RestoreJournalPath(intent.LiveDatabase)
	if _, err := os.Stat(journalPath); err == nil {
		return fmt.Errorf("restore intent already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, phase := range adminV1RestorePhases() {
		if err := removeAdminV1File(adminV1RestorePhasePath(intent.LiveDatabase, phase)); err != nil {
			return err
		}
	}
	encoded, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		return err
	}
	if err := adminV1WriteFileAtomic(journalPath, append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	if err := syncAdminV1Directory(filepath.Dir(journalPath)); err != nil {
		_ = os.Remove(journalPath)
		return err
	}
	if err := recordAdminV1RestorePhase(intent, restorePhasePrepared); err != nil {
		_ = os.Remove(journalPath)
		return err
	}
	return nil
}

func readAdminV1RestoreIntent(liveDatabase, liveSecrets string) (adminV1RestoreIntent, error) {
	journalPath := adminV1RestoreJournalPath(liveDatabase)
	raw, err := os.ReadFile(journalPath)
	if err != nil {
		return adminV1RestoreIntent{}, err
	}
	decoder := json.NewDecoder(io.LimitReader(bytes.NewReader(raw), 64<<10))
	decoder.DisallowUnknownFields()
	var intent adminV1RestoreIntent
	if err := decoder.Decode(&intent); err != nil {
		return adminV1RestoreIntent{}, fmt.Errorf("decode restore intent: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return adminV1RestoreIntent{}, fmt.Errorf("restore intent contains trailing data")
	}
	if err := validateAdminV1RestoreIntent(intent, liveDatabase, liveSecrets); err != nil {
		return adminV1RestoreIntent{}, err
	}
	return intent, nil
}

func recordAdminV1RestorePhase(intent adminV1RestoreIntent, phase string) error {
	payload, err := json.Marshal(map[string]string{"phase": phase, "recordedAt": nowString()})
	if err != nil {
		return err
	}
	path := adminV1RestorePhasePath(intent.LiveDatabase, phase)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := adminV1WriteFileAtomic(path, append(payload, '\n'), 0o600); err != nil {
		return err
	}
	return syncAdminV1Directory(filepath.Dir(path))
}

func adminV1RestorePhaseCompleted(intent adminV1RestoreIntent) bool {
	_, err := os.Stat(adminV1RestorePhasePath(intent.LiveDatabase, restorePhaseCompleted))
	return err == nil
}

func maybeCrashAdminV1Restore(phase string) error {
	if adminV1RestoreCrashHook != nil && adminV1RestoreCrashHook(phase) {
		return errAdminV1RestoreCrashInjected
	}
	return nil
}

// RecoverPendingRestore must be called before OpenStore. It resolves an
// interrupted multi-file restore to either the original pair or, once the
// completed marker is durable, the fully installed pair.
func RecoverPendingRestore(liveDatabase, liveSecrets string) error {
	journalPath := adminV1RestoreJournalPath(liveDatabase)
	if _, err := os.Stat(journalPath); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	intent, err := readAdminV1RestoreIntent(liveDatabase, liveSecrets)
	if err != nil {
		return err
	}
	if adminV1RestorePhaseCompleted(intent) {
		if err := validateAdminV1InstalledRestore(intent); err == nil {
			return cleanupAdminV1CompletedRestore(intent)
		}
	}
	return rollbackAdminV1RestoreIntent(intent)
}

func validateAdminV1InstalledRestore(intent adminV1RestoreIntent) error {
	if err := adminV1CheckSQLiteSnapshot(intent.LiveDatabase); err != nil {
		return err
	}
	if intent.LiveSecrets != "" {
		raw, err := os.ReadFile(intent.LiveSecrets)
		if err != nil {
			return err
		}
		if err := adminV1ValidateSecretsJSON(raw); err != nil {
			return err
		}
	}
	return nil
}

func rollbackAdminV1RestoreIntent(intent adminV1RestoreIntent) error {
	var rollbackErrors []error
	if fileExists(intent.RollbackDatabase) {
		if err := removeAdminV1File(intent.LiveDatabase); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("remove installed database: %w", err))
		} else if err := renameAdminV1File(intent.RollbackDatabase, intent.LiveDatabase); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restore original database: %w", err))
		}
	} else if !fileExists(intent.LiveDatabase) {
		rollbackErrors = append(rollbackErrors, fmt.Errorf("both live and rollback databases are missing"))
	}
	if intent.LiveSecrets != "" {
		if intent.LiveSecretsExisted {
			if fileExists(intent.RollbackSecrets) {
				if err := removeAdminV1File(intent.LiveSecrets); err != nil {
					rollbackErrors = append(rollbackErrors, fmt.Errorf("remove installed secrets: %w", err))
				} else if err := renameAdminV1File(intent.RollbackSecrets, intent.LiveSecrets); err != nil {
					rollbackErrors = append(rollbackErrors, fmt.Errorf("restore original secrets: %w", err))
				}
			} else if !fileExists(intent.LiveSecrets) {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("both live and rollback secrets are missing"))
			}
		} else if err := removeAdminV1File(intent.LiveSecrets); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("remove newly installed secrets: %w", err))
		}
	}
	if len(rollbackErrors) != 0 {
		return errors.Join(rollbackErrors...)
	}
	if err := resetAdminV1RestoreRecord(intent.LiveDatabase, intent.BackupID); err != nil {
		return err
	}
	for _, path := range []string{intent.StagedDatabase, intent.StagedSecrets, intent.RollbackDatabase, intent.RollbackSecrets} {
		if err := removeAdminV1File(path); err != nil {
			return err
		}
	}
	return cleanupAdminV1RestoreJournal(intent)
}

func resetAdminV1RestoreRecord(databasePath, backupID string) error {
	if backupID == "" || !fileExists(databasePath) {
		return nil
	}
	db, err := sql.Open("sqlite", databasePath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer db.Close()
	var tableExists int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='backup_records'`).Scan(&tableExists); err != nil {
		return err
	}
	if tableExists == 0 {
		return nil
	}
	_, err = db.Exec(`UPDATE backup_records SET status='ready',restored_at='' WHERE id=? AND status='restoring'`, backupID)
	return err
}

func cleanupAdminV1CompletedRestore(intent adminV1RestoreIntent) error {
	for _, path := range []string{intent.StagedDatabase, intent.StagedSecrets, intent.RollbackDatabase, intent.RollbackSecrets} {
		if err := removeAdminV1File(path); err != nil {
			return err
		}
	}
	return cleanupAdminV1RestoreJournal(intent)
}

func cleanupAdminV1RestoreJournal(intent adminV1RestoreIntent) error {
	if err := removeAdminV1File(adminV1RestoreJournalPath(intent.LiveDatabase)); err != nil {
		return err
	}
	for _, phase := range adminV1RestorePhases() {
		if err := removeAdminV1File(adminV1RestorePhasePath(intent.LiveDatabase, phase)); err != nil {
			return err
		}
	}
	return nil
}

func adminV1RestorePhases() []string {
	return []string{restorePhasePrepared, restorePhaseDatabaseBackedUp, restorePhaseSecretsBackedUp, restorePhaseDatabaseInstalled, restorePhaseSecretsInstalled, restorePhaseCompleted}
}

func discardAdminV1PreparedRestore(intent adminV1RestoreIntent) error {
	var discardErrors []error
	for _, path := range []string{intent.StagedDatabase, intent.StagedSecrets} {
		if err := removeAdminV1File(path); err != nil {
			discardErrors = append(discardErrors, err)
		}
	}
	if err := cleanupAdminV1RestoreJournal(intent); err != nil {
		discardErrors = append(discardErrors, err)
	}
	return errors.Join(discardErrors...)
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func renameAdminV1File(source, destination string) error {
	if source == "" || destination == "" {
		return fmt.Errorf("rename paths are required")
	}
	if err := os.Rename(source, destination); err != nil {
		return err
	}
	if err := syncAdminV1Directory(filepath.Dir(source)); err != nil {
		return err
	}
	if filepath.Dir(source) != filepath.Dir(destination) {
		return syncAdminV1Directory(filepath.Dir(destination))
	}
	return nil
}

func removeAdminV1File(path string) error {
	if path == "" {
		return nil
	}
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return syncAdminV1Directory(filepath.Dir(path))
}

func syncAdminV1Directory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	err = directory.Sync()
	closeErr := directory.Close()
	if runtime.GOOS == "windows" {
		// Go cannot request FILE_FLAG_BACKUP_SEMANTICS through os.Open. The
		// Sync call is still attempted; Windows may reject directory flushes.
		err = nil
	}
	return errors.Join(err, closeErr)
}
