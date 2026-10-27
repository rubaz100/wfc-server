package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"sync"
	"wwfc/common"

	_ "modernc.org/sqlite"
)

// pool is a thin adapter over *sql.DB that preserves the call shape the rest of
// this package inherited from pgx (QueryRow/Query/Exec taking a context as the
// first argument). This keeps the ~dozen query files unchanged apart from the
// SQL itself when porting PostgreSQL -> SQLite.
type pool struct {
	db *sql.DB
}

func (p *pool) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return p.db.QueryRowContext(ctx, query, args...)
}

func (p *pool) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return p.db.QueryContext(ctx, query, args...)
}

func (p *pool) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return p.db.ExecContext(ctx, query, args...)
}

func (p *pool) Close() {
	_ = p.db.Close()
}

type Connection struct {
	pool *pool
	ctx  context.Context
}

// migrateMu serializes migration across the independent Start calls made by the
// various server packages at boot, so they never race to apply the same
// migration. migrate itself is idempotent (it skips versions already recorded
// in schema_migrations), so running it on every Start is cheap and correct.
var migrateMu sync.Mutex

// Start opens (creating if necessary) the SQLite database and applies any
// pending migrations. Several server packages call Start independently; each
// gets its own *sql.DB handle to the same file, which is safe under WAL with a
// busy timeout.
func Start(config common.Config) Connection {
	conn := Connection{
		ctx: context.Background(),
	}

	// WAL + a busy timeout let the multiple handles opened across packages share
	// the one file without "database is locked" errors; foreign keys enforce the
	// user_ng_device_ids -> users cascade.
	dsn := fmt.Sprintf(
		"file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)",
		url.PathEscape(config.DatabasePath),
	)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		panic(err)
	}

	conn.pool = &pool{db: db}

	migrateMu.Lock()
	err = conn.migrate()
	migrateMu.Unlock()
	if err != nil {
		panic(err)
	}

	return conn
}

func (c *Connection) Close() {
	if c != nil && c.pool != nil {
		c.pool.Close()
	}
}
