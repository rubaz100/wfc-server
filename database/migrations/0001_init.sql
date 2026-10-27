-- mkwfc initial SQLite schema.
-- Translated from the upstream PostgreSQL schema.sql + the startup ALTERs that
-- used to live in database/schema.go's UpdateTables(). See FORK-NOTES.md.
--
-- Key divergences from upstream:
--   * users.profile_id is AUTOINCREMENT, seeded so the first generated id is
--     1000000000 (matching the upstream sequence START WITH).
--   * ng_device_id (a Postgres bigint[]) is normalised into user_ng_device_ids.
--   * jsonb columns become TEXT validated with json_valid(); the SAKE field-count
--     limit is enforced in Go instead of a CHECK.

CREATE TABLE IF NOT EXISTS users (
    profile_id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id            INTEGER NOT NULL,
    gsbrcd             TEXT NOT NULL,
    password           TEXT NOT NULL,
    email              TEXT NOT NULL,
    unique_nick        TEXT NOT NULL,
    firstname          TEXT,
    lastname           TEXT DEFAULT '',
    last_ip_address    TEXT DEFAULT '',
    last_ingamesn      TEXT DEFAULT '',
    has_ban            INTEGER DEFAULT 0,
    ban_issued         DATETIME,
    ban_expires        DATETIME,
    ban_reason         TEXT,
    ban_reason_hidden  TEXT,
    ban_moderator      TEXT,
    ban_tos            INTEGER,
    open_host          INTEGER DEFAULT 0,
    allow_default_keys INTEGER DEFAULT 0
);

-- Seed the autoincrement counter so generated profile IDs start at 1000000000.
-- Explicitly-assigned profile IDs are always < 1000000000 (enforced in Go) and
-- therefore never advance this counter.
INSERT OR IGNORE INTO sqlite_sequence (name, seq) VALUES ('users', 999999999);

-- Replaces the Postgres ng_device_id bigint[] column. One row per (user, device).
CREATE TABLE IF NOT EXISTS user_ng_device_ids (
    profile_id INTEGER NOT NULL REFERENCES users(profile_id) ON DELETE CASCADE ON UPDATE CASCADE,
    device_id  INTEGER NOT NULL,
    PRIMARY KEY (profile_id, device_id)
);
CREATE INDEX IF NOT EXISTS idx_user_ng_device_ids_device ON user_ng_device_ids(device_id);

CREATE TABLE IF NOT EXISTS sake_records (
    game_id     INTEGER NOT NULL,
    table_id    TEXT NOT NULL,
    record_id   INTEGER NOT NULL,
    owner_id    INTEGER NOT NULL,
    fields      TEXT NOT NULL CHECK (json_valid(fields)),
    create_time DATETIME DEFAULT CURRENT_TIMESTAMP,
    update_time DATETIME DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT one_sake_record_constraint UNIQUE (game_id, table_id, record_id)
);

CREATE TABLE IF NOT EXISTS mario_kart_wii_sake (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    regionid    INTEGER NOT NULL CHECK (regionid >= 1 AND regionid <= 7),
    courseid    INTEGER NOT NULL CHECK (courseid >= 0 AND courseid <= 32767),
    score       INTEGER NOT NULL CHECK (score > 0 AND score < 360000),
    pid         INTEGER NOT NULL CHECK (pid > 0),
    playerinfo  TEXT NOT NULL CHECK (LENGTH(playerinfo) = 108),
    ghost       BLOB CHECK (ghost IS NULL OR (LENGTH(ghost) BETWEEN 148 AND 10240)),
    upload_time DATETIME,
    CONSTRAINT one_time_per_course_constraint UNIQUE (courseid, pid)
);

CREATE TABLE IF NOT EXISTS gamestats_public_data (
    profile_id    INTEGER NOT NULL,
    dindex        TEXT NOT NULL,
    ptype         TEXT NOT NULL,
    pdata         TEXT NOT NULL,
    modified_time DATETIME NOT NULL,
    CONSTRAINT one_pdata_constraint UNIQUE (profile_id, dindex, ptype)
);

CREATE TABLE IF NOT EXISTS events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL,
    event_data TEXT NOT NULL CHECK (json_valid(event_data)),
    event_time DATETIME DEFAULT CURRENT_TIMESTAMP
);
