package database

// GetAdminConfig returns the value stored for key in the admin_config table.
// The bool is false when no row exists for that key.
func (c *Connection) GetAdminConfig(key string) (string, bool) {
	var value string
	err := c.pool.QueryRow(c.ctx, `SELECT value FROM admin_config WHERE key = $1`, key).Scan(&value)
	if err != nil {
		return "", false
	}
	return value, true
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
