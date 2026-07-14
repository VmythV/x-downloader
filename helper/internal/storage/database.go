package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"x-downloader/helper/internal/settings"

	_ "modernc.org/sqlite"
)

const schemaVersion = 1

type LegacyPaths struct {
	Candidates string
	Jobs       string
	Settings   string
	Defaults   settings.Values
}

type Database struct {
	db   *sql.DB
	path string
}

func Open(path string, legacy LegacyPaths) (*Database, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, errors.New("database path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	// A single connection gives the local Helper deterministic write ordering
	// and ensures connection-local PRAGMAs apply to every query. Transactions
	// are short, so this remains ample for the four-worker download limit.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &Database{db: db, path: path}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := store.initialize(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, fmt.Errorf("secure SQLite database: %w", err)
	}
	if err := store.importLegacy(ctx, legacy); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (database *Database) initialize(ctx context.Context) error {
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA synchronous=FULL",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := database.db.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("configure SQLite database (%s): %w", pragma, err)
		}
	}
	if _, err := database.db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("initialize SQLite schema: %w", err)
	}
	var version int
	if err := database.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read SQLite schema version: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("SQLite schema version %d is newer than supported version %d", version, schemaVersion)
	}
	if version < schemaVersion {
		if _, err := database.db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", schemaVersion)); err != nil {
			return fmt.Errorf("record SQLite schema version: %w", err)
		}
	}
	return nil
}

func (database *Database) Close() error {
	if database == nil || database.db == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = database.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return database.db.Close()
}

func (database *Database) Path() string { return database.path }

func milliseconds(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixMilli()
}

func timeFromMilliseconds(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(value).UTC()
}

func nullableMilliseconds(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.UTC().UnixMilli()
}

func stringValue(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func int64Value(value sql.NullInt64) int64 {
	if !value.Valid {
		return 0
	}
	return value.Int64
}
