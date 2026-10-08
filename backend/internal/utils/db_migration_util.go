package utils

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	postgresMigrate "github.com/golang-migrate/migrate/v4/database/postgres"
	sqliteMigrate "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/pocket-id/pocket-id/backend/internal/common"
	"github.com/pocket-id/pocket-id/backend/resources"
)

// MigrateDatabase applies database migrations using embedded migration files or fetches them from GitHub if a downgrade is detected.
func MigrateDatabase(ctx context.Context, sqlDb *sql.DB) error {
	m, cleanup, err := GetEmbeddedMigrateInstance(ctx, sqlDb)
	if err != nil {
		return fmt.Errorf("failed to get migrate instance: %w", err)
	}
	defer cleanup()

	path := "migrations/" + string(common.EnvConfig.DbProvider)
	requiredVersion, err := getRequiredMigrationVersion(path)
	if err != nil {
		return fmt.Errorf("failed to get last migration version: %w", err)
	}

	currentVersion, _, _ := m.Version()
	if currentVersion > requiredVersion {
		slog.Warn("Database version is newer than the application supports, possible downgrade detected", slog.Uint64("db_version", uint64(currentVersion)), slog.Uint64("app_version", uint64(requiredVersion)))
		if !common.EnvConfig.AllowDowngrade {
			return fmt.Errorf("database version (%d) is newer than application version (%d), downgrades are not allowed (set ALLOW_DOWNGRADE=true to enable)", currentVersion, requiredVersion)
		}
		slog.Info("Fetching migrations from GitHub to handle possible downgrades")
		return migrateDatabaseFromGitHub(ctx, sqlDb, requiredVersion, currentVersion)
	}

	err = m.Migrate(requiredVersion)
	switch {
	case errors.Is(err, migrate.ErrNoChange):
		// All good
		return nil
	case errors.As(err, &migrate.ErrDirty{}):
		return fmt.Errorf("database migration failed. Please create an issue on GitHub and temporarely downgrade to the previous version: %w", err)
	case err != nil:
		return fmt.Errorf("failed to apply embedded migrations: %w", err)
	}

	return nil
}

// EnsureSqliteOidcClientDescriptionColumn restores the description column on oidc_clients when its migration was skipped
// golang-migrate silently ignores backdated migration files on databases that already recorded a higher version, so affected databases never received the column
// SQLite cannot conditionally alter a table in pure SQL, so the existence check has to happen in code instead of in a migration file
// Later migrations reference the description column, so the repair must run before MigrateDatabase applies them
// The rebuild preserves whatever columns the table currently has, because databases mid-upgrade still carry columns that pending migrations expect to change
func EnsureSqliteOidcClientDescriptionColumn(ctx context.Context, db *sql.DB) error {
	// A missing table means the database is fresh, and migrations create it with the description column included
	var createSQL sql.NullString
	err := db.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'oidc_clients'").Scan(&createSQL)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to inspect oidc_clients table: %w", err)
	}

	rows, err := db.QueryContext(ctx, "PRAGMA table_info(oidc_clients)")
	if err != nil {
		return fmt.Errorf("failed to inspect oidc_clients columns: %w", err)
	}
	defer rows.Close()

	// PRAGMA table_info returns one row per column: cid, name, type, notnull, dflt_value, pk
	var columnNames []string
	hasDescription := false
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("failed to read oidc_clients columns: %w", err)
		}
		columnNames = append(columnNames, name)
		if name == "description" {
			hasDescription = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to read oidc_clients columns: %w", err)
	}

	if hasDescription {
		return nil
	}

	// The rebuild copies every existing column and only defaults the missing description, which cannot lose data because the column is known to be absent
	// The table definition is taken from sqlite_master so the rebuilt table keeps the exact shape the database already has
	// Quoting the table name keeps the rename valid regardless of how the original definition quoted it
	baseDDL := strings.TrimRight(createSQL.String, " \t\r\n")
	if !strings.HasSuffix(baseDDL, ")") {
		return fmt.Errorf("unexpected oidc_clients definition, does not end with ')': %s", createSQL.String)
	}
	newDDL := strings.Replace(baseDDL, `"oidc_clients"`, `"oidc_clients_rebuilt"`, 1)
	if newDDL == baseDDL {
		newDDL = strings.Replace(baseDDL, "oidc_clients", "oidc_clients_rebuilt", 1)
	}
	newDDL = strings.TrimSuffix(newDDL, ")") + ",\n    description TEXT NOT NULL DEFAULT '')"

	quotedColumns := make([]string, 0, len(columnNames))
	for _, name := range columnNames {
		quotedColumns = append(quotedColumns, "\""+name+"\"")
	}
	columnList := strings.Join(quotedColumns, ", ")

	_, err = db.ExecContext(ctx, fmt.Sprintf(`
PRAGMA foreign_keys = OFF;
BEGIN;

%s;

INSERT INTO "oidc_clients_rebuilt" (%s)
SELECT %s
FROM "oidc_clients";

DROP TABLE "oidc_clients";
ALTER TABLE "oidc_clients_rebuilt" RENAME TO "oidc_clients";

COMMIT;
PRAGMA foreign_keys = ON;
`, newDDL, columnList, columnList))
	if err != nil {
		return fmt.Errorf("failed to rebuild oidc_clients with the description column: %w", err)
	}

	slog.Info("Restored the missing description column on oidc_clients")

	return nil
}

// GetEmbeddedMigrateInstance creates a migrate.Migrate instance using embedded migration files.
// The returned cleanup function must always be called once the instance is no longer needed: for Postgres it releases the dedicated connection the driver holds (see newMigrationDriver).
func GetEmbeddedMigrateInstance(ctx context.Context, sqlDb *sql.DB) (m *migrate.Migrate, cleanup func(), err error) {
	path := "migrations/" + string(common.EnvConfig.DbProvider)
	source, err := iofs.New(resources.FS, path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create embedded migration source: %w", err)
	}

	driver, cleanup, err := newMigrationDriver(ctx, sqlDb, common.EnvConfig.DbProvider)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create migration driver: %w", err)
	}

	m, err = migrate.NewWithInstance("iofs", source, "pocket-id", driver)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("failed to create migration instance: %w", err)
	}
	return m, cleanup, nil
}

// newMigrationDriver creates a database.Driver instance based on the given database provider.
// The returned cleanup function releases any resources the driver holds and must always be called.
func newMigrationDriver(ctx context.Context, sqlDb *sql.DB, dbProvider common.DbProvider) (driver database.Driver, cleanup func(), err error) {
	// Default cleanup is a no-op
	cleanup = func() {}

	switch dbProvider {
	case common.DbProviderSqlite:
		// The SQLite driver runs statements on the pool directly, so it holds no dedicated connection.
		driver, err = sqliteMigrate.WithInstance(sqlDb, &sqliteMigrate.Config{
			NoTxWrap: true,
		})
	case common.DbProviderPostgres:
		// The Postgres driver checks out a dedicated connection
		// Use WithConnection (rather than WithInstance) with a connection we own so cleanup can return it to the pool without closing the shared *sql.DB
		// Otherwise the leaked connection makes pgxpool.Close() block on shutdown
		var conn *sql.Conn
		conn, err = sqlDb.Conn(ctx)
		if err != nil {
			return nil, cleanup, fmt.Errorf("failed to acquire migration connection: %w", err)
		}
		cleanup = func() {
			// Close the connection
			_ = conn.Close()
		}
		driver, err = postgresMigrate.WithConnection(ctx, conn, &postgresMigrate.Config{})
	default:
		// Should never happen at this point
		return nil, cleanup, fmt.Errorf("unsupported database provider: %s", common.EnvConfig.DbProvider)
	}
	if err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("failed to create migration driver: %w", err)
	}

	return driver, cleanup, nil
}

// migrateDatabaseFromGitHub applies database migrations fetched from GitHub to handle downgrades.
func migrateDatabaseFromGitHub(ctx context.Context, sqlDb *sql.DB, requiredVersion uint, currentVersion uint) error {
	srcURL := "github://pocket-id/pocket-id/backend/resources/migrations/" + string(common.EnvConfig.DbProvider)

	driver, cleanup, err := newMigrationDriver(ctx, sqlDb, common.EnvConfig.DbProvider)
	if err != nil {
		return fmt.Errorf("failed to create migration driver: %w", err)
	}
	defer cleanup()

	m, err := migrate.NewWithDatabaseInstance(srcURL, "pocket-id", driver)
	if err != nil {
		return fmt.Errorf("failed to create GitHub migration instance: %w", err)
	}

	// Reset the dirty state before forcing the version
	if err := m.Force(int(currentVersion)); err != nil { //nolint:gosec
		return fmt.Errorf("failed to force database version: %w", err)
	}

	if err := m.Migrate(requiredVersion); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			return nil
		}
		return fmt.Errorf("failed to apply GitHub migrations: %w", err)
	}

	return nil
}

// getRequiredMigrationVersion reads the embedded migration files and returns the highest version number found.
func getRequiredMigrationVersion(path string) (uint, error) {
	entries, err := resources.FS.ReadDir(path)
	if err != nil {
		return 0, fmt.Errorf("failed to read migration directory: %w", err)
	}

	var maxVersion uint64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		verString, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			continue
		}
		version, err := strconv.ParseUint(verString, 10, 64)
		if err != nil {
			continue
		}

		if version > maxVersion {
			maxVersion = version
		}
	}

	if maxVersion > uint64(^uint(0)) {
		// We do not support 32-bit systems
		panic("32-bit systems are unsupported")
	}

	return uint(maxVersion), nil
}
