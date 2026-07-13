// Package sqlite implements the firehose cache using the pure-Go (no cgo)
// SQLite driver, modernc.org/sqlite, for portability reasons across various
// Linux architectures. The cache holds dedupe keys, conditional-GET validators,
// retention data, and feed-health state — never read state.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	osuser "os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mwyvr/firehose"

	_ "modernc.org/sqlite"
)

//go:embed migration/*.sql
var migrationFS embed.FS

// driverName is the modernc.org/sqlite driver registration name.
const driverName = "sqlite"

// DB wraps a *sql.DB with firehose-specific open/migrate behavior. It also
// carries the display *time.Location so times read back from the cache are
// interpreted consistently (SQLite has no native tz; we store UTC and present
// in the configured location at render time).
type DB struct {
	*sql.DB
	dsn string
	loc *time.Location
}

// NewDB constructs a DB for the SQLite file at dsn. loc is the display location
// used when scanning timestamps. Call Open to connect and migrate.
func NewDB(dsn string, loc *time.Location) *DB {
	if loc == nil {
		loc = time.UTC
	}
	return &DB{dsn: dsn, loc: loc}
}

// Open connects to the database, applies pragmas, and runs migrations. It is
// safe to call once at startup.
func (db *DB) Open(ctx context.Context) error {
	if db.dsn == "" {
		return firehose.Errorf(firehose.EINVALID, "sqlite: empty dsn")
	}

	// modernc treats a DSN without a "file:" prefix as a literal filename, so
	// query parameters (and ":memory:") only work in URI form.
	dsn := db.dsn
	if !strings.HasPrefix(dsn, "file:") {
		dsn = "file:" + dsn
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	dsn += sep + "_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(on)" +
		"&_pragma=synchronous(NORMAL)"

	sqlDB, err := sql.Open(driverName, dsn)
	if err != nil {
		return firehose.Errorf(firehose.EINTERNAL, "sqlite: open: %v", err)
	}
	db.DB = sqlDB

	// A single writer is plenty for a batch job and avoids WAL write
	// contention with the pure-Go driver. Pinning the connection (idle count 1,
	// no idle timeout) also keeps a ":memory:" database alive: database/sql may
	// otherwise close an idle connection and reopen a fresh one, silently
	// discarding an in-memory schema between operations.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(0)
	db.SetConnMaxLifetime(0)

	if err := db.PingContext(ctx); err != nil {
		return firehose.Errorf(firehose.EINTERNAL, "sqlite: open %s: %v%s",
			db.dsn, err, explainOpenError(db.dsn, err))
	}
	if err := db.migrate(ctx); err != nil {
		return err
	}
	return nil
}

// migrate applies any embedded migrations not yet recorded, in filename order.
// A minimal schema_migrations table records applied files by name.
func (db *DB) migrate(ctx context.Context) error {
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			name    TEXT PRIMARY KEY,
			applied TIMESTAMP NOT NULL
		)`); err != nil {
		return firehose.Errorf(firehose.EINTERNAL, "sqlite: create schema_migrations: %v", err)
	}

	names, err := migrationNames()
	if err != nil {
		return err
	}

	for _, name := range names {
		var exists int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM schema_migrations WHERE name = ?`, name,
		).Scan(&exists); err != nil {
			return firehose.Errorf(firehose.EINTERNAL, "sqlite: check migration %s: %v", name, err)
		}
		if exists > 0 {
			continue
		}

		body, err := migrationFS.ReadFile("migration/" + name)
		if err != nil {
			return firehose.Errorf(firehose.EINTERNAL, "sqlite: read migration %s: %v", name, err)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return firehose.Errorf(firehose.EINTERNAL, "sqlite: begin migration %s: %v", name, err)
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return firehose.Errorf(firehose.EINTERNAL, "sqlite: apply migration %s: %v", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (name, applied) VALUES (?, ?)`,
			name, time.Now().UTC(),
		); err != nil {
			_ = tx.Rollback()
			return firehose.Errorf(firehose.EINTERNAL, "sqlite: record migration %s: %v", name, err)
		}
		if err := tx.Commit(); err != nil {
			return firehose.Errorf(firehose.EINTERNAL, "sqlite: commit migration %s: %v", name, err)
		}
	}
	return nil
}

// migrationNames lists embedded migration files in lexical (apply) order.
func migrationNames() ([]string, error) {
	entries, err := fs.ReadDir(migrationFS, "migration")
	if err != nil {
		return nil, firehose.Errorf(firehose.EINTERNAL, "sqlite: list migrations: %v", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// nullTime converts a *time.Time to a driver-friendly value (UTC or NULL).
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

// explainOpenError helps to narrow down certain common causes
func explainOpenError(dsn string, err error) string {
	msg := err.Error()
	if !strings.Contains(msg, "unable to open database file") &&
		!strings.Contains(msg, "out of memory (14)") {
		return ""
	}
	user := "unknown"
	if u, uerr := osuser.Current(); uerr == nil {
		user = u.Username
	}
	dir := filepath.Dir(strings.TrimPrefix(dsn, "file:"))
	return fmt.Sprintf(
		"\n  hint: running as user %q; is %s missing, or owned by the service user?"+
			"\n  the cache directory is deliberately private to the service account —"+
			"\n  manual passes: sudo -u firehose firehose -config /etc/firehose/config.toml [-force]",
		user, dir)
}

// scanTime reads a nullable timestamp column into a time.Time in db.loc,
// returning the zero time for NULL.
func (db *DB) scanTime(src sql.NullTime) time.Time {
	if !src.Valid {
		return time.Time{}
	}
	return src.Time.In(db.loc)
}

// page applies offset/limit to a result slice. Filters here run partly in Go
// (categories are a joined TEXT column), so paging happens after filtering.
func page[T any](s []T, offset, limit int) []T {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(s) {
		return nil
	}
	s = s[offset:]
	if limit > 0 && limit < len(s) {
		s = s[:limit]
	}
	return s
}
