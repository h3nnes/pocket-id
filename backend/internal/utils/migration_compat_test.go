//go:build unit

package utils

import (
	"context"
	"database/sql"
	"io"
	"path/filepath"
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/libtnb/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pocket-id/pocket-id/backend/internal/common"
	sqliteutil "github.com/pocket-id/pocket-id/backend/internal/utils/sqlite"
	"github.com/pocket-id/pocket-id/backend/resources"
)

func init() {
	sqliteutil.RegisterSqliteFunctions()
}

// TestCompatMigrationFilesAreEmbedded verifies that the compatibility migration files for the old PKCE timestamp versions are present in the embedded filesystem
// This directly reproduces the root cause of the startup failure: a database at version 20260726153901 could not find its migration file
func TestCompatMigrationFilesAreEmbedded(t *testing.T) {
	source, err := iofs.New(resources.FS, "migrations/sqlite")
	require.NoError(t, err)

	for _, version := range []uint{20260726153900, 20260726153901} {
		r, identifier, err := source.ReadUp(version)
		require.NoError(t, err, "ReadUp should find migration for version %d", version)
		_, _ = io.ReadAll(r)
		_ = r.Close()
		assert.NotEmpty(t, identifier, "identifier should not be empty for version %d", version)

		r, identifier, err = source.ReadDown(version)
		require.NoError(t, err, "ReadDown should find migration for version %d", version)
		_, _ = io.ReadAll(r)
		_ = r.Close()
		assert.NotEmpty(t, identifier, "identifier should not be empty for version %d", version)
	}
}

// TestMigrateDatabaseFromScratchWithCompatMigrations applies all migrations from a fresh database and verifies the compatibility tables exist
func TestMigrateDatabaseFromScratchWithCompatMigrations(t *testing.T) {
	common.EnvConfig.DbProvider = common.DbProviderSqlite
	common.EnvConfig.AllowDowngrade = false

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	sqlDb, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer sqlDb.Close()

	err = MigrateDatabase(context.Background(), sqlDb)
	require.NoError(t, err)

	// The compat migrations at 20260726153900 and 20260726153901 create or verify the oauth-apis tables
	// On a fresh database these tables are created by 20260707170000_oauth_apis, but the compat migrations must not interfere
	for _, table := range []string{"apis", "api_permissions", "oidc_clients_allowed_api_permissions"} {
		var name string
		err := sqlDb.QueryRowContext(t.Context(), "SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name)
		require.NoError(t, err, "expected table %s to exist", table)
		assert.Equal(t, table, name, "expected table %s", table)
	}
}
