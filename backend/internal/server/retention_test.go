package server

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestRunRetentionRechecksQueuedMaintenanceStateBeforeDeleting(t *testing.T) {
	for _, active := range []string{"maintenance", "restore"} {
		t.Run(active, func(t *testing.T) {
			app, store := newTestApp(t)
			_ = app.Routes()
			now := time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)
			if err := store.InsertAPILog(t.Context(), APILogRecord{ID: "queued-retention-old-log", CreatedAt: now.Add(-365 * 24 * time.Hour).Format(time.RFC3339Nano), Method: "GET", Path: "/old", RouteGroup: "/old", Status: 200}); err != nil {
				t.Fatalf("seed old log: %v", err)
			}
			store.maintenanceMu.Lock()
			type outcome struct {
				result RetentionResult
				err    error
			}
			started := make(chan struct{})
			done := make(chan outcome, 1)
			go func() {
				close(started)
				result, err := store.RunRetention(context.Background(), now)
				done <- outcome{result: result, err: err}
			}()
			<-started
			time.Sleep(100 * time.Millisecond)
			switch active {
			case "maintenance":
				if _, err := store.db.Exec(`INSERT INTO maintenance_jobs(id,kind,status,detail,error,created_at,started_at,finished_at) VALUES('queued-job','cleanup','running','','',?,?,'')`, nowString(), nowString()); err != nil {
					store.maintenanceMu.Unlock()
					t.Fatalf("seed queued maintenance: %v", err)
				}
			case "restore":
				if _, err := store.db.Exec(`INSERT INTO backup_records(id,kind,status,database_path,manifest_path,checksum,size_bytes,created_at,restored_at) VALUES('queued-restore','data-only','restoring','backup.sqlite','manifest.json','sha',1,?,'')`, nowString()); err != nil {
					store.maintenanceMu.Unlock()
					t.Fatalf("seed queued restore: %v", err)
				}
			}
			store.maintenanceMu.Unlock()
			select {
			case got := <-done:
				if got.err != nil || !got.result.Skipped {
					t.Fatalf("queued retention during %s = %+v err=%v", active, got.result, got.err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("queued retention did not finish")
			}
			var rows int
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM api_logs WHERE id='queued-retention-old-log'`).Scan(&rows); err != nil || rows != 1 {
				t.Fatalf("queued retention wrote during %s: rows=%d err=%v", active, rows, err)
			}
		})
	}
}

func TestRunRetentionAppliesPersistedVersionAndLogLimits(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.db.Exec(`INSERT INTO settings(key,value,updated_at) VALUES('limits',?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		`{"versionsPerUser":2,"accessLogDays":7,"auditLogDays":30}`, nowString()); err != nil {
		t.Fatalf("set retention limits: %v", err)
	}
	user, err := store.CreateUser(t.Context(), "retention@example.com", "safe-password-123")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	now := time.Now().UTC()
	for version := 1; version <= 4; version++ {
		if _, err := store.db.Exec(`INSERT INTO sync_profile_versions(id,user_id,version,schema_version,profile_json,profile_hash,mutation_id,created_at) VALUES(?,?,?,?,?,?,?,?)`,
			fmt.Sprintf("retention-version-%d", version), user.ID, version, 2, `{}`, fmt.Sprintf("hash-%d", version), fmt.Sprintf("mutation-%d", version), now.Add(time.Duration(version)*time.Minute).Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("insert version %d: %v", version, err)
		}
	}
	for id, createdAt := range map[string]time.Time{
		"old-access": now.Add(-8 * 24 * time.Hour),
		"new-access": now.Add(-6 * 24 * time.Hour),
	} {
		if err := store.InsertAPILog(t.Context(), APILogRecord{ID: id, CreatedAt: createdAt.Format(time.RFC3339Nano), IP: "127.0.0.1", Method: "GET", Path: "/api/v1/me", RouteGroup: "/api/v1/me", Status: 200}); err != nil {
			t.Fatalf("insert access log %s: %v", id, err)
		}
	}
	for id, createdAt := range map[string]time.Time{
		"old-audit": now.Add(-31 * 24 * time.Hour),
		"new-audit": now.Add(-29 * 24 * time.Hour),
	} {
		if _, err := store.db.Exec(`INSERT INTO admin_audit_logs(id,created_at,admin_id,action,target_type,target_id,request_id,ip,details_json) VALUES(?,?,?,?,?,?,?,?,?)`,
			id, createdAt.Format(time.RFC3339Nano), "admin-retention", "test", "system", "retention", "request-retention", "127.0.0.1", `{}`); err != nil {
			t.Fatalf("insert audit log %s: %v", id, err)
		}
	}

	result, err := store.RunRetention(t.Context(), now)
	if err != nil {
		t.Fatalf("run retention: %v", err)
	}
	if result.ProfileVersionsDeleted != 2 || result.AccessLogsDeleted != 1 || result.AdminAuditLogsDeleted != 1 {
		t.Fatalf("retention result = %#v", result)
	}
	for table, want := range map[string]int{"sync_profile_versions": 2, "api_logs": 1, "admin_audit_logs": 1} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count=%d err=%v want=%d", table, count, err, want)
		}
	}
}

func TestRunRetentionUsesProtocolCutoffsAndClearsExpiredCredentials(t *testing.T) {
	app, store := newTestApp(t)
	_ = app.Routes()
	user, err := store.CreateUser(t.Context(), "protocol-retention@example.test", "safe-password-123")
	if err != nil {
		t.Fatalf("create retention user: %v", err)
	}
	var adminID string
	if err := store.db.QueryRow(`SELECT id FROM admin_users LIMIT 1`).Scan(&adminID); err != nil {
		t.Fatalf("read retention admin: %v", err)
	}
	now := time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)
	old25Hours := now.Add(-25 * time.Hour).Format(time.RFC3339Nano)
	new23Hours := now.Add(-23 * time.Hour).Format(time.RFC3339Nano)
	old91Days := now.Add(-91 * 24 * time.Hour).Format(time.RFC3339Nano)
	new89Days := now.Add(-89 * 24 * time.Hour).Format(time.RFC3339Nano)
	expired := now.Add(-time.Hour).Format(time.RFC3339Nano)
	future := now.Add(24 * time.Hour).Format(time.RFC3339Nano)

	for _, row := range []struct{ id, created string }{{"legacy-idem-old", old25Hours}, {"legacy-idem-new", new23Hours}} {
		if _, err := store.db.Exec(`INSERT INTO idempotency_keys(id,user_id,idempotency_key,method,path,request_hash,status,response_json,created_at) VALUES(?,?,?,'PUT','/api/profile','hash',200,'{}',?)`, row.id, user.ID, row.id, row.created); err != nil {
			t.Fatalf("seed idempotency response: %v", err)
		}
	}
	for _, row := range []struct{ id, created string }{{"sync-mutation-old", old91Days}, {"sync-mutation-new", new89Days}} {
		if _, err := store.db.Exec(`INSERT INTO sync_mutations(id,user_id,mutation_id,route,request_hash,base_version,result_version,status,response_json,created_at) VALUES(?,?,?,'profile','hash',0,1,200,'{}',?)`, row.id, user.ID, row.id, row.created); err != nil {
			t.Fatalf("seed sync mutation: %v", err)
		}
	}
	for _, table := range []string{"email_verification_tokens", "password_reset_tokens"} {
		for _, row := range []struct{ suffix, expires, consumed string }{{"expired", expired, ""}, {"consumed", future, expired}, {"active", future, ""}} {
			if _, err := store.db.Exec(`INSERT INTO `+table+`(token_hash,user_id,created_at,expires_at,consumed_at) VALUES(?,?,?, ?, ?)`, table+row.suffix, user.ID, old25Hours, row.expires, row.consumed); err != nil {
				t.Fatalf("seed %s: %v", table, err)
			}
		}
	}
	for _, row := range []struct{ token, expires string }{{"plugin-expired", expired}, {"plugin-active", future}} {
		if _, err := store.db.Exec(`INSERT INTO sessions(token_hash,user_id,created_at,expires_at) VALUES(?,?,?,?)`, row.token, user.ID, old25Hours, row.expires); err != nil {
			t.Fatalf("seed plugin session: %v", err)
		}
	}
	for _, row := range []struct{ token, expires string }{{"admin-expired", expired}, {"admin-active", future}} {
		if _, err := store.db.Exec(`INSERT INTO admin_sessions(token_hash,admin_id,csrf_hash,created_at,expires_at,last_seen_at) VALUES(?,?, 'csrf',?,?,?)`, row.token, adminID, old25Hours, row.expires, old25Hours); err != nil {
			t.Fatalf("seed admin session: %v", err)
		}
		if _, err := store.db.Exec(`INSERT INTO admin_login_sessions(token_hash,csrf_hash,created_at,expires_at) VALUES(?, 'csrf',?,?)`, "login-"+row.token, old25Hours, row.expires); err != nil {
			t.Fatalf("seed admin login session: %v", err)
		}
	}
	for _, row := range []struct{ token, expires string }{{"install-expired", expired}, {"install-active", future}} {
		if _, err := store.db.Exec(`INSERT INTO install_sessions(token_hash,csrf_hash,created_at,last_seen_at,expires_at,absolute_expires_at) VALUES(?, 'csrf',?,?,?,?)`, row.token, old25Hours, old25Hours, row.expires, row.expires); err != nil {
			t.Fatalf("seed install session: %v", err)
		}
	}
	for _, family := range []struct{ id, expires, revoked string }{{"family-expired", expired, ""}, {"family-revoked", future, expired}, {"family-active", future, ""}} {
		if _, err := store.db.Exec(`INSERT INTO refresh_token_families(id,user_id,device_id,scope,created_at,expires_at,revoked_at) VALUES(?,?,?,'full',?,?,?)`, family.id, user.ID, family.id, old25Hours, family.expires, family.revoked); err != nil {
			t.Fatalf("seed refresh family: %v", err)
		}
		if _, err := store.db.Exec(`INSERT INTO refresh_tokens(token_hash,family_id,created_at,expires_at,used_at,replaced_by_hash,rotation_request_id) VALUES(?,?,?,?,'','','')`, "refresh-"+family.id, family.id, old25Hours, family.expires); err != nil {
			t.Fatalf("seed refresh token: %v", err)
		}
		if _, err := store.db.Exec(`INSERT INTO access_tokens(token_hash,user_id,family_id,device_id,scope,created_at,expires_at,revoked_at) VALUES(?,?,?,?, 'full',?,?,?)`, "access-"+family.id, user.ID, family.id, family.id, old25Hours, family.expires, family.revoked); err != nil {
			t.Fatalf("seed access token: %v", err)
		}
	}

	result, err := store.RunRetention(t.Context(), now)
	if err != nil {
		t.Fatalf("run protocol retention: %v", err)
	}
	if result.IdempotencyResponsesDeleted != 1 || result.SyncMutationsDeleted != 1 || result.EmailVerificationTokensDeleted != 2 || result.PasswordResetTokensDeleted != 2 || result.PluginSessionsDeleted != 1 || result.AdminSessionsDeleted != 1 || result.AdminLoginSessionsDeleted != 1 || result.InstallSessionsDeleted != 1 || result.RefreshFamiliesDeleted != 2 {
		t.Fatalf("protocol retention result=%#v", result)
	}
	for table, want := range map[string]int{
		"idempotency_keys": 1, "sync_mutations": 1, "email_verification_tokens": 1, "password_reset_tokens": 1,
		"sessions": 1, "admin_sessions": 1, "admin_login_sessions": 1, "install_sessions": 1,
		"refresh_token_families": 1, "refresh_tokens": 1, "access_tokens": 1,
	} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count=%d err=%v want=%d", table, count, err, want)
		}
	}
}
