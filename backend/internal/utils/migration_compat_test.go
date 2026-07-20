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
	if _, err := sqlDb.Exec("CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER, dirty INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDb.Exec("INSERT INTO schema_migrations (version, dirty) VALUES (20260726153900, 0)"); err != nil {
		t.Fatal(err)
	}

	if err := MigrateDatabase(context.Background(), sqlDb); err != nil {
		t.Fatal(err)
	}
}
