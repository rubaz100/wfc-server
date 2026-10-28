-- Key/value store for Host Manager admin settings: the hashed admin password
-- and GUI preferences, kept in the database rather than config.xml so they can
-- be changed at runtime from the panel.
CREATE TABLE IF NOT EXISTS admin_config (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
