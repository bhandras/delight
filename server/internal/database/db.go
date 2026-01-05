package database

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// schemaMigrationsFS embeds the SQL schema migrations so the server can run
// as a single binary without needing access to the source tree at runtime.
//
//go:embed migrations/*.sql
var schemaMigrationsFS embed.FS

type DB struct {
	*sql.DB
}

// Open opens a connection to the SQLite database and runs migrations
func Open(dbPath string) (*DB, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test the connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Run migrations
	if err := runMigrations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return &DB{db}, nil
}

// runMigrations applies the SQL schema migrations embedded in the binary.
func runMigrations(db *sql.DB) error {
	// Create migrations tracking table
	_, err := db.Exec(`
			CREATE TABLE IF NOT EXISTS schema_migrations (
				version TEXT PRIMARY KEY,
				applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
			)
		`)
	if err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	migrations, err := listEmbeddedMigrations(schemaMigrationsFS)
	if err != nil {
		return err
	}

	for _, migration := range migrations {
		if err := applyMigration(db, migration); err != nil {
			return err
		}
	}
	return nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.DB.Close()
}

// listEmbeddedMigrations enumerates embedded migration SQL files in the
// migrations folder and returns them in stable order.
func listEmbeddedMigrations(root fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(root, "migrations")
	if err != nil {
		return nil, fmt.Errorf("failed to read migrations dir: %w", err)
	}

	var migrations []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		migrations = append(migrations, filepath.Join("migrations", name))
	}
	sort.Strings(migrations)
	return migrations, nil
}

// applyMigration applies a single embedded SQL migration if it has not already
// been recorded in schema_migrations.
func applyMigration(db *sql.DB, migrationPath string) error {
	version := strings.TrimSuffix(filepath.Base(migrationPath), filepath.Ext(migrationPath))
	if version == "" {
		return fmt.Errorf("invalid migration filename: %s", migrationPath)
	}

	var count int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM schema_migrations WHERE version = ?",
		version,
	).Scan(&count); err != nil {
		return fmt.Errorf("failed to check migration status (%s): %w", version, err)
	}
	if count > 0 {
		return nil
	}

	migrationSQL, err := fs.ReadFile(schemaMigrationsFS, migrationPath)
	if err != nil {
		return fmt.Errorf("failed to read migration file (%s): %w", version, err)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to start migration tx (%s): %w", version, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(string(migrationSQL)); err != nil {
		return fmt.Errorf("failed to execute migration (%s): %w", version, err)
	}
	if _, err := tx.Exec(
		"INSERT INTO schema_migrations (version) VALUES (?)",
		version,
	); err != nil {
		return fmt.Errorf("failed to record migration (%s): %w", version, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit migration (%s): %w", version, err)
	}
	return nil
}
