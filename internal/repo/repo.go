// Package repo is Loupe's persistence layer. It exposes a single engine-agnostic
// Repo interface in terms of the domain structs (Source, Collection, Item) and
// backs it with sqlc-generated code for SQLite (default/embedded), PostgreSQL and
// MySQL/MariaDB. The engine is chosen at runtime by Open(driver, dsn).
//
// The three generated packages (internal/db/{sqlite,postgres,mysql}) have
// identical method signatures but distinct param/row *types*, so they can't share
// one Go interface directly — the per-engine adapters (sqlite.go, postgres.go,
// mysql.go) map those types to the domain structs below. `gone` is stored as a
// BIGINT 0/1 and converted to bool at this boundary.
package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

/* ------------------------------------------------------------------ model */

type Source struct {
	ID          string `json:"id"`          // uuid — identity on the programming side
	Name        string `json:"name"`        // human label
	Description string `json:"description"` // free text
	URL         string `json:"url"`
	ConfigFile  string `json:"configFile"` // optional gallery-dl config file (-c), additive to the defaults
	ConfigJSON  string `json:"configJson"` // optional inline gallery-dl config (pasted JSON), applied after the file
	ConfigID    string `json:"configId"`   // optional reference to a shared Config, resolved at poll time
	AddedAt     int64  `json:"addedAt"`
	LastPoll    int64  `json:"lastPoll"`
	LastAdded   int    `json:"lastAdded"`
	LastError   string `json:"lastError"`
}

// A Collection groups sources so their new items can be reviewed together.
type Collection struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	SourceIDs   []string `json:"sourceIds"`
	AddedAt     int64    `json:"addedAt"`
}

// A Config is a named, reusable gallery-dl config body that sources can share by
// reference (Source.ConfigID). Editing it propagates to every referencing source
// because the body is resolved at poll time, never copied into the source.
type Config struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ConfigJSON string `json:"configJson"`
	AddedAt    int64  `json:"addedAt"`
}

// An Item is one image *as seen through one source*. The same picture coming
// from two sources is two Items with independent status — that is the point.
type Item struct {
	ID        string `json:"id"` // opaque: base64(sourceID \0 imageKey)
	SourceID  string `json:"sourceId"`
	Label     string `json:"label"`
	ImageKey  string `json:"-"`
	URL       string `json:"url"`
	Sample    string `json:"sample"`
	Title     string `json:"title"`
	Status    string `json:"status"` // new | good | bad
	Gone      bool   `json:"gone"`   // no longer present in the source (e.g. URL changed) — kept, not deleted
	AddedAt   int64  `json:"addedAt"`
	LastSeen  int64  `json:"lastSeen"`
	DecidedAt int64  `json:"decidedAt"`
}

// State is the small bag of poll bookkeeping that used to live in Store.State.
type State struct {
	LastPoll  int64
	LastAdded int
}

// StatusGoneCount / SourceStatusGoneCount are the GROUP BY count rows the stats
// and source/collection listings fold into the new/good/bad/gone tallies in Go.
type StatusGoneCount struct {
	Status string
	Gone   bool
	N      int
}

type SourceStatusGoneCount struct {
	SourceID string
	Status   string
	Gone     bool
	N        int
}

/* ------------------------------------------------------------------ repo */

// Repo is the full data-access surface used by the handlers. Methods that can
// "not find" a row return a bool (found) so callers can map to a 404. All reads
// and writes hit the database — there is no in-memory store.
type Repo interface {
	// items
	ListReviewable(ctx context.Context) ([]*Item, error)
	ListStaleNew(ctx context.Context) ([]*Item, error)
	ListGood(ctx context.Context) ([]*Item, error)
	GetItem(ctx context.Context, id string) (*Item, bool, error)
	InsertItemIfNew(ctx context.Context, it *Item) (inserted bool, err error)
	UnstaleItem(ctx context.Context, id string, lastSeen int64) error
	DecideItem(ctx context.Context, id, status string, decidedAt int64) (found bool, err error)
	SetItemStatus(ctx context.Context, id, status string) (found bool, err error)
	ResetItem(ctx context.Context, id string) (found bool, err error)
	MarkSourceGone(ctx context.Context, sourceID string) error
	PurgeStale(ctx context.Context, sourceID string) (purged int, err error)
	DeleteNewBySource(ctx context.Context, sourceID string) error
	CountByStatusGone(ctx context.Context) ([]StatusGoneCount, error)
	CountBySourceStatusGone(ctx context.Context) ([]SourceStatusGoneCount, error)

	// sources
	ListSources(ctx context.Context) ([]*Source, error)
	GetSource(ctx context.Context, id string) (*Source, bool, error)
	InsertSource(ctx context.Context, s *Source) error
	UpdateSource(ctx context.Context, s *Source) error
	MarkPolled(ctx context.Context, id string, lastPoll int64, lastAdded int) error
	SetSourceError(ctx context.Context, id, msg string, lastPoll int64) error
	DeleteSource(ctx context.Context, id string) error

	// collections
	ListCollections(ctx context.Context) ([]*Collection, error)
	GetCollection(ctx context.Context, id string) (*Collection, bool, error)
	InsertCollection(ctx context.Context, c *Collection) error
	UpdateCollection(ctx context.Context, c *Collection) error
	DeleteCollection(ctx context.Context, id string) error

	// configs (named, reusable gallery-dl config bodies)
	ListConfigs(ctx context.Context) ([]*Config, error)
	GetConfig(ctx context.Context, id string) (*Config, bool, error)
	InsertConfig(ctx context.Context, c *Config) error
	UpdateConfig(ctx context.Context, c *Config) error
	DeleteConfig(ctx context.Context, id string) error

	// state
	GetState(ctx context.Context) (State, error)
	SetState(ctx context.Context, s State) error

	// WithinTx runs fn against a Repo bound to a single transaction.
	WithinTx(ctx context.Context, fn func(Repo) error) error
	Close() error
}

/* ---------------------------------------------------------------- factory */

// DefaultSQLiteDSN points at the embedded file store with WAL + a busy timeout so
// the single writer never trips SQLITE_BUSY. Pragmas live in the DSN because they
// are per-connection and must apply to every pooled connection.
const DefaultSQLiteDSN = "file:data/loupe.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)"

// Open selects the engine from driver (sqlite|postgres|mysql, default sqlite),
// opens the pool, runs the idempotent schema, and returns a ready Repo plus the
// underlying *sql.DB (so main can close it / inspect stats).
func Open(driver, dsn string) (Repo, error) {
	switch normalizeDriver(driver) {
	case "sqlite":
		if dsn == "" {
			dsn = DefaultSQLiteDSN
		}
		if err := ensureParentDir(dsn); err != nil {
			return nil, err
		}
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			return nil, err
		}
		// One writer connection avoids "database is locked" entirely for this
		// low-concurrency app.
		db.SetMaxOpenConns(1)
		return newSQLiteRepo(db)
	case "postgres":
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(20)
		return newPostgresRepo(db)
	case "mysql":
		db, err := sql.Open("mysql", dsn)
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(20)
		return newMySQLRepo(db)
	default:
		return nil, fmt.Errorf("unknown DB_DRIVER %q (want sqlite|postgres|mysql)", driver)
	}
}

func normalizeDriver(d string) string {
	switch strings.ToLower(strings.TrimSpace(d)) {
	case "", "sqlite", "sqlite3":
		return "sqlite"
	case "postgres", "postgresql", "pgx":
		return "postgres"
	case "mysql", "mariadb":
		return "mysql"
	default:
		return d
	}
}

// ensureParentDir creates the directory for a sqlite file DSN (e.g. data/) so the
// first run on a fresh checkout doesn't fail to create the database file.
func ensureParentDir(dsn string) error {
	p := strings.TrimPrefix(dsn, "file:")
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	if p == "" || p == ":memory:" || strings.HasPrefix(p, ":") {
		return nil
	}
	dir := filepath.Dir(p)
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

// runMigrations brings the database up to the latest schema using goose against
// the engine's embedded migrations FS. The baseline migration is idempotent
// (CREATE ... IF NOT EXISTS), so a database created before goose was adopted is
// simply recorded at that version with its existing tables untouched, then any
// newer migrations are applied. dialect is goose's name for the engine
// ("sqlite3" | "postgres" | "mysql"). goose's global setters are fine here:
// exactly one engine is opened per process.
func runMigrations(db *sql.DB, migrations fs.FS, dialect string) error {
	goose.SetBaseFS(migrations)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect(dialect); err != nil {
		return fmt.Errorf("goose dialect %q: %w", dialect, err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// b2i / i2b convert between the domain bool `gone` and the stored BIGINT 0/1.
func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
func i2b(n int64) bool { return n != 0 }

// encodeIDs / decodeIDs marshal a collection's SourceIDs to/from the JSON-array
// TEXT column. A nil/empty slice persists as "[]" so the column is never empty.
func encodeIDs(ids []string) string {
	if len(ids) == 0 {
		return "[]"
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func decodeIDs(s string) []string {
	if s == "" {
		return nil
	}
	var ids []string
	if json.Unmarshal([]byte(s), &ids) != nil {
		return nil
	}
	return ids
}
