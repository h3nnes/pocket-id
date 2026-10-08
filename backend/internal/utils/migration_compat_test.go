//go:build unit

package utils

import (
	"context"
	"database/sql"
	"encoding/json"
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

	for _, version := range []uint{20260726153900, 20260726153901, 20261008120000} {
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

	// The description backfill for oidc_clients is a Go-side repair, so it must also succeed on a healthy database without touching anything
	err = EnsureSqliteOidcClientDescriptionColumn(sqlDb)
	require.NoError(t, err)
	assert.True(t, sqliteColumnExists(t, sqlDb, "oidc_clients", "description"), "expected the description column to exist")
	assert.False(t, sqliteTableExists(t, sqlDb, "app_config_variables"), "expected app_config_variables to stay dropped on a fresh database")
}

// TestMigrateDatabaseRepairsSkippedMigrations simulates a database that recorded the old PKCE migration timestamp
// Such a database never received the backdated migrations below that timestamp, so the repair must restore their effects
func TestMigrateDatabaseRepairsSkippedMigrations(t *testing.T) {
	common.EnvConfig.DbProvider = common.DbProviderSqlite
	common.EnvConfig.AllowDowngrade = false

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	sqlDb, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer sqlDb.Close()

	// Build a healthy database first so all tables exist in their current shape
	err = MigrateDatabase(context.Background(), sqlDb)
	require.NoError(t, err)

	// Seed one client whose data must survive the repairs
	_, err = sqlDb.ExecContext(t.Context(), "INSERT INTO oidc_clients (id, created_at, name) VALUES ('client-1', '2026-01-01 00:00:00', 'Test Client')")
	require.NoError(t, err)

	// Remove the description column like a database that skipped its migration
	_, err = sqlDb.ExecContext(t.Context(), "ALTER TABLE oidc_clients DROP COLUMN description")
	require.NoError(t, err)

	// Restore the old config table with rows that were never frozen into kv
	_, err = sqlDb.ExecContext(t.Context(), "CREATE TABLE \"app_config_variables\" (\"key\" TEXT NOT NULL PRIMARY KEY, \"value\" TEXT NOT NULL)")
	require.NoError(t, err)
	_, err = sqlDb.ExecContext(t.Context(), "INSERT INTO app_config_variables (\"key\", \"value\") VALUES ('instanceId', 'instance-old'), ('smtpHost', 'smtp.example.com')")
	require.NoError(t, err)

	// Empty kv so the backfills have to repopulate it
	_, err = sqlDb.ExecContext(t.Context(), "DELETE FROM kv")
	require.NoError(t, err)

	// Restore the old signup token tables with one token that was never frozen
	_, err = sqlDb.ExecContext(t.Context(), "CREATE TABLE signup_tokens (id TEXT NOT NULL PRIMARY KEY, created_at DATETIME NOT NULL, token TEXT NOT NULL UNIQUE, expires_at DATETIME NOT NULL, usage_limit INTEGER NOT NULL DEFAULT 1, usage_count INTEGER NOT NULL DEFAULT 0)")
	require.NoError(t, err)
	_, err = sqlDb.ExecContext(t.Context(), "CREATE TABLE signup_tokens_user_groups (signup_token_id TEXT NOT NULL, user_group_id TEXT NOT NULL, PRIMARY KEY (signup_token_id, user_group_id))")
	require.NoError(t, err)
	_, err = sqlDb.ExecContext(t.Context(), "CREATE TABLE one_time_access_tokens (id TEXT NOT NULL PRIMARY KEY, created_at DATETIME NOT NULL, token TEXT NOT NULL UNIQUE, expires_at DATETIME NOT NULL, user_id TEXT NOT NULL REFERENCES users ON DELETE CASCADE, device_token TEXT)")
	require.NoError(t, err)
	_, err = sqlDb.ExecContext(t.Context(), "INSERT INTO signup_tokens (id, created_at, token, expires_at) VALUES ('st-1', '2026-01-01 00:00:00', 'st-token-1', '2026-12-31 00:00:00')")
	require.NoError(t, err)
	_, err = sqlDb.ExecContext(t.Context(), "INSERT INTO signup_tokens_user_groups (signup_token_id, user_group_id) VALUES ('st-1', 'g-1')")
	require.NoError(t, err)

	// Rebuild oauth2_jtis at its old integer-timestamp shape with one row that must survive the normalization
	_, err = sqlDb.ExecContext(t.Context(), "DROP TABLE oauth2_jtis")
	require.NoError(t, err)
	_, err = sqlDb.ExecContext(t.Context(), "CREATE TABLE oauth2_jtis (id TEXT NOT NULL PRIMARY KEY, created_at INTEGER NOT NULL, jti TEXT NOT NULL UNIQUE, expires_at INTEGER NOT NULL)")
	require.NoError(t, err)
	_, err = sqlDb.ExecContext(t.Context(), "INSERT INTO oauth2_jtis (id, created_at, jti, expires_at) VALUES ('jt-1', 1700000000, 'jti-value-1', 1800000000)")
	require.NoError(t, err)

	// Rewind the recorded version so only the backfill migration runs again
	_, err = sqlDb.ExecContext(t.Context(), "UPDATE schema_migrations SET version = 20260929120000, dirty = 0")
	require.NoError(t, err)

	err = MigrateDatabase(context.Background(), sqlDb)
	require.NoError(t, err)

	err = EnsureSqliteOidcClientDescriptionColumn(sqlDb)
	require.NoError(t, err)

	// The description column is back and the seeded client survived with the defaulted description
	assert.True(t, sqliteColumnExists(t, sqlDb, "oidc_clients", "description"), "expected the description column to be restored")
	var name, description string
	err = sqlDb.QueryRowContext(t.Context(), "SELECT name, description FROM oidc_clients WHERE id = 'client-1'").Scan(&name, &description)
	require.NoError(t, err, "expected the seeded client to survive the repairs")
	assert.Equal(t, "Test Client", name)
	assert.Empty(t, description)

	// The instance id moved to kv and kept its value while the config freeze only captured the remaining rows
	assert.Equal(t, "instance-old", sqliteKvValue(t, sqlDb, "instance_id"))
	configMigrated := sqliteKvValue(t, sqlDb, "config_migrated")
	require.NotEmpty(t, configMigrated, "expected the config to be frozen into kv")
	var frozenConfig map[string]any
	require.NoError(t, json.Unmarshal([]byte(configMigrated), &frozenConfig))
	assert.Equal(t, "smtp.example.com", frozenConfig["smtpHost"])
	_, hasInstanceId := frozenConfig["instanceId"]
	assert.False(t, hasInstanceId, "instanceId must not appear in the frozen config")

	// The signup token was frozen into kv with its user group link
	signupMigrated := sqliteKvValue(t, sqlDb, "signup_tokens_migrated")
	require.NotEmpty(t, signupMigrated, "expected the signup tokens to be frozen into kv")
	var frozenTokens []map[string]any
	require.NoError(t, json.Unmarshal([]byte(signupMigrated), &frozenTokens))
	require.Len(t, frozenTokens, 1)
	assert.Equal(t, "st-token-1", frozenTokens[0]["token"])
	assert.Equal(t, "st-1", frozenTokens[0]["id"])

	// The old tables are gone after the backfill
	for _, table := range []string{"app_config_variables", "signup_tokens", "signup_tokens_user_groups", "one_time_access_tokens"} {
		assert.False(t, sqliteTableExists(t, sqlDb, table), "expected table %s to be dropped", table)
	}

	// The jtis table is normalized to DATETIME columns and its row survived
	assert.Equal(t, "DATETIME", sqliteColumnType(t, sqlDb, "oauth2_jtis", "created_at"), "expected oauth2_jtis.created_at to be normalized")
	var jtiCount int
	err = sqlDb.QueryRowContext(t.Context(), "SELECT count(*) FROM oauth2_jtis WHERE jti = 'jti-value-1'").Scan(&jtiCount)
	require.NoError(t, err)
	assert.Equal(t, 1, jtiCount, "expected the seeded jti row to survive the normalization")
}

// sqliteTableExists reports whether the given table exists in the database
func sqliteTableExists(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	var name string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name)
	return err == nil
}

// sqliteColumnExists reports whether the given column exists on the table
func sqliteColumnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	return sqliteColumnType(t, db, table, column) != ""
}

// sqliteColumnType returns the declared type of the given column, or an empty string when the column does not exist
func sqliteColumnType(t *testing.T, db *sql.DB, table, column string) string {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	require.NoError(t, err)
	defer rows.Close()

	var found string
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dfltValue sql.NullString
		require.NoError(t, rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk))
		if name == column {
			found = colType
		}
	}
	require.NoError(t, rows.Err())
	return found
}

// sqliteKvValue returns the value stored under the given key in the kv table
func sqliteKvValue(t *testing.T, db *sql.DB, key string) string {
	t.Helper()
	var value string
	err := db.QueryRow("SELECT \"value\" FROM kv WHERE \"key\" = ?", key).Scan(&value)
	if err != nil {
		return ""
	}
	return value
}
