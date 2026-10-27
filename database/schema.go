package database

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrate applies every embedded migration file that has not yet been recorded
// in schema_migrations, in filename order. Each file is identified by the
// numeric prefix of its name (e.g. 0001_init.sql -> "0001").
func (c *Connection) migrate() error {
	_, err := c.pool.Exec(c.ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		version := strings.SplitN(name, "_", 2)[0]

		var exists bool
		if err := c.pool.QueryRow(c.ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}

		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}

		if _, err := c.pool.Exec(c.ctx, string(body)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := c.pool.Exec(c.ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			return err
		}
	}

	return nil
}

// MigrationVersion returns the highest applied migration version, or "" if none
// have been applied.
func (c *Connection) MigrationVersion() (string, error) {
	var v sql.NullString
	err := c.pool.QueryRow(c.ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v)
	if err != nil {
		return "", err
	}
	return v.String, nil
}

// UpdateTables previously applied idempotent Postgres ALTERs at startup. The
// SQLite schema is now fully defined by the embedded migrations, so this is a
// no-op kept for call-site compatibility.
func (c *Connection) UpdateTables() {}
