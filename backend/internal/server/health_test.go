package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthLiveAndLegacyHealthzDoNotDependOnDatabase(t *testing.T) {
	store, err := OpenStore(t.TempDir() + "/health-live.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	handler := NewApp(store, Config{}).Routes()
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	for _, path := range []string{"/health/live", "/healthz"} {
		t.Run(path, func(t *testing.T) {
			response := healthRequest(handler, path)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"ok"`) {
				t.Fatalf("%s = %d %s, want liveness 200", path, response.Code, response.Body.String())
			}
			if got := response.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("%s Cache-Control=%q, want no-store", path, got)
			}
		})
	}

	ready := healthRequest(handler, "/health/ready")
	assertHealthNotReady(t, ready, "database")
}

func TestHealthReadyAcceptsInstalledIdleCurrentSchema(t *testing.T) {
	app, _ := newTestApp(t)
	response := healthRequest(app.Routes(), "/health/ready")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"ready"`) {
		t.Fatalf("ready = %d %s, want 200", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("ready Cache-Control=%q, want no-store", got)
	}
}

func TestHealthReadyRejectsNonInstalledState(t *testing.T) {
	store := newTestStore(t)
	response := healthRequest(NewApp(store, Config{}).Routes(), "/health/ready")
	assertHealthNotReady(t, response, "installation")
}

func TestHealthReadyRejectsSchemaDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Store) error
	}{
		{
			name: "missing migration",
			mutate: func(store *Store) error {
				_, err := store.db.Exec(`DELETE FROM schema_migrations WHERE version=?`, schemaVersion)
				return err
			},
		},
		{
			name: "future migration",
			mutate: func(store *Store) error {
				_, err := store.db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, schemaVersion+1, nowString())
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app, store := newTestApp(t)
			handler := app.Routes()
			if err := test.mutate(store); err != nil {
				t.Fatalf("mutate schema: %v", err)
			}
			assertHealthNotReady(t, healthRequest(handler, "/health/ready"), "schema")
		})
	}
}

func TestHealthReadyRejectsActiveMaintenanceAndRestore(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Store) error
	}{
		{
			name: "maintenance running",
			mutate: func(store *Store) error {
				_, err := store.db.Exec(`INSERT INTO maintenance_jobs(id,kind,status,detail,error,created_at,started_at,finished_at) VALUES('job_health','cleanup','running','','',?,?,'')`, nowString(), nowString())
				return err
			},
		},
		{
			name: "restore running",
			mutate: func(store *Store) error {
				_, err := store.db.Exec(`INSERT INTO backup_records(id,kind,status,database_path,manifest_path,checksum,size_bytes,created_at,restored_at) VALUES('backup_health','full','restoring','backup.db','manifest.json','checksum',1,?,'')`, nowString())
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app, store := newTestApp(t)
			handler := app.Routes()
			if err := test.mutate(store); err != nil {
				t.Fatalf("start operation: %v", err)
			}
			assertHealthNotReady(t, healthRequest(handler, "/health/ready"), "maintenance")
		})
	}
}

func TestHealthRoutesAreExcludedFromAccessLogs(t *testing.T) {
	app, store := newTestApp(t)
	handler := app.Routes()
	for _, path := range []string{"/healthz", "/health/live", "/health/ready"} {
		if shouldLogRequest(path) {
			t.Fatalf("health path %s should be excluded from access logs", path)
		}
		response := healthRequest(handler, path)
		if response.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", path, response.Code, response.Body.String())
		}
	}
	var rows int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM api_logs WHERE path IN ('/healthz','/health/live','/health/ready')`).Scan(&rows); err != nil {
		t.Fatalf("count health access logs: %v", err)
	}
	if rows != 0 {
		t.Fatalf("health requests created %d access log rows, want 0", rows)
	}
}

func healthRequest(handler http.Handler, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.RemoteAddr = "127.0.0.1:1234"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertHealthNotReady(t *testing.T, response *httptest.ResponseRecorder, reason string) {
	t.Helper()
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"status":"not_ready"`) || !strings.Contains(response.Body.String(), `"reason":"`+reason+`"`) {
		t.Fatalf("not ready = %d %s, want reason %q", response.Code, response.Body.String(), reason)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("not ready Cache-Control=%q, want no-store", got)
	}
}
