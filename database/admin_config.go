package database

import (
	"database/sql"
	"errors"
)

// GetAdminConfig returns the value stored for key in the admin_config table.
// The bool is false when no row exists. Read failures are returned separately
// so a database error cannot be mistaken for first-run initialization.
func (c *Connection) GetAdminConfig(key string) (string, bool, error) {
	var value string
	err := c.pool.QueryRow(c.ctx, `SELECT value FROM admin_config WHERE key = $1`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// SetAdminConfigIfAbsent initializes a key without replacing an existing value,
// including one written by another server between our initial read and insert.
func (c *Connection) SetAdminConfigIfAbsent(key, value string) (bool, error) {
	result, err := c.pool.Exec(c.ctx,
		`INSERT INTO admin_config (key, value) VALUES ($1, $2)
		 ON CONFLICT(key) DO NOTHING`, key, value)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

// SetAdminConfig stores (inserting or replacing) the value for key.
func (c *Connection) SetAdminConfig(key, value string) error {
	_, err := c.pool.Exec(c.ctx,
		`INSERT INTO admin_config (key, value) VALUES ($1, $2)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// DeleteAdminConfig removes the row for key, if any.
func (c *Connection) DeleteAdminConfig(key string) error {
	_, err := c.pool.Exec(c.ctx, `DELETE FROM admin_config WHERE key = $1`, key)
	return err
}
