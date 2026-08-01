package server

import (
	"database/sql"
	"strings"
	"testing"
)

func TestIdentifyClientBrowser(t *testing.T) {
	tests := []struct {
		name      string
		userAgent string
		want      clientBrowser
	}{
		{
			name:      "Edge 优先于 UA 中的 Chrome 标记",
			userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/127.0.0.0 Safari/537.36 Edg/127.0.2651.74",
			want:      clientBrowser{Family: "edge", Version: "127.0.2651.74"},
		},
		{
			name:      "Chrome",
			userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0.0.0 Safari/537.36",
			want:      clientBrowser{Family: "chrome", Version: "126.0.0.0"},
		},
		{
			name:      "Firefox",
			userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:128.0) Gecko/20100101 Firefox/128.0",
			want:      clientBrowser{Family: "firefox", Version: "128.0"},
		},
		{
			name:      "Safari",
			userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Version/17.5 Safari/605.1.15",
			want:      clientBrowser{Family: "safari", Version: "17.5"},
		},
		{name: "缺少 UA", userAgent: "", want: clientBrowser{Family: "unknown"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := identifyClientBrowser(test.userAgent); got != test.want {
				t.Fatalf("identifyClientBrowser() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestRefreshTokenFamilyBrowserMigrationPreservesLegacyRows(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE refresh_token_families (
		id TEXT PRIMARY KEY,user_id TEXT NOT NULL,device_id TEXT NOT NULL,scope TEXT NOT NULL,
		created_at TEXT NOT NULL,expires_at TEXT NOT NULL,revoked_at TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO refresh_token_families(id,user_id,device_id,scope,created_at,expires_at,revoked_at)
		VALUES('family-old','user-old','device-old','full','2026-01-01T00:00:00Z','2027-01-01T00:00:00Z','')`); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		tx, err := db.Begin()
		if err != nil {
			t.Fatalf("begin migration: %v", err)
		}
		if err := addRefreshTokenFamilyBrowserColumns(t.Context(), tx); err != nil {
			_ = tx.Rollback()
			t.Fatalf("apply browser migration: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit browser migration: %v", err)
		}
	}

	var family, version string
	if err := db.QueryRow(`SELECT browser_family,browser_version FROM refresh_token_families WHERE id='family-old'`).Scan(&family, &version); err != nil {
		t.Fatalf("read migrated row: %v", err)
	}
	if family != "" || version != "" {
		t.Fatalf("legacy browser metadata = %q/%q, want empty defaults", family, version)
	}
	var columns string
	rows, err := db.Query(`PRAGMA table_info(refresh_token_families)`)
	if err != nil {
		t.Fatalf("read table info: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan table info: %v", err)
		}
		columns += name + " "
	}
	if !strings.Contains(columns, "browser_family ") || !strings.Contains(columns, "browser_version ") {
		t.Fatalf("browser columns missing after migration: %s", columns)
	}
}
