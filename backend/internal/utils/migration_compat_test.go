//go:build unit

package utils

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/libtnb/sqlite"
	"github.com/pocket-id/pocket-id/backend/internal/common"
	sqliteutil "github.com/pocket-id/pocket-id/backend/internal/utils/sqlite"
)

func init() {
	sqliteutil.RegisterSqliteFunctions()
}

func TestMigrateDatabaseAcceptsOldRenamedPKCEVersion(t *testing.T) {
	common.EnvConfig.DbProvider = common.DbProviderSqlite
	common.EnvConfig.AllowDowngrade = false

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	sqlDb, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDb.Close()

	// Simulate a database that recorded the previous PKCE migration timestamp
	if _, err := sqlDb.ExecContext(t.Context(), "CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER, dirty INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDb.ExecContext(t.Context(), "INSERT INTO schema_migrations (version, dirty) VALUES (20260726153900, 0)"); err != nil {
		t.Fatal(err)
	}

	if err := MigrateDatabase(context.Background(), sqlDb); err != nil {
		t.Fatal(err)
	}

	// Databases that recorded the old PKCE timestamp may have skipped the oauth_apis migration
	// Make sure the missing tables are created by the compatibility migration
	for _, table := range []string{"apis", "api_permissions", "oidc_clients_allowed_api_permissions"} {
		var name string
		err := sqlDb.QueryRowContext(t.Context(), "SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name)
		if err != nil {
			t.Fatalf("expected table %s to exist: %v", table, err)
		}
		if name != table {
			t.Fatalf("expected table %s, got %s", table, name)
		}
	}
}
