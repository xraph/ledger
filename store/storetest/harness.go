// Package storetest provides a conformance suite every store.Store
// implementation must pass, plus constructors for each backend.
//
// Memory and SQLite run in process and need no setup. Postgres and Mongo
// read a DSN from the environment and skip when it is absent, so
// `go test ./...` stays runnable with nothing installed.
package storetest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/mongodriver"
	"github.com/xraph/grove/drivers/pgdriver"
	"github.com/xraph/grove/drivers/sqlitedriver"

	// Registers the migration executor for each driver. Without these
	// blank imports Migrate fails with "no executor registered for
	// driver". This is not discoverable from the driver's own API.
	_ "github.com/xraph/grove/drivers/mongodriver/mongomigrate"
	_ "github.com/xraph/grove/drivers/pgdriver/pgmigrate"
	_ "github.com/xraph/grove/drivers/sqlitedriver/sqlitemigrate"

	ledgerstore "github.com/xraph/ledger/store"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/store/mongo"
	"github.com/xraph/ledger/store/postgres"
	"github.com/xraph/ledger/store/sqlite"
)

// NewMemory returns an in-memory store. It never skips.
func NewMemory(t *testing.T) ledgerstore.Store {
	t.Helper()

	return memory.New()
}

// NewSQLite returns a migrated SQLite store backed by a file under the
// test's temp directory.
//
// ":memory:" cannot be used here: sqlitedriver opens the database through
// database/sql's pooled *sql.DB, and modernc.org/sqlite treats ":memory:"
// as a fresh, independent database on every new connection the pool opens.
// A concurrency test that expects two goroutines' connections to see each
// other's writes (a unique-constraint race, for instance) would silently
// pass or fail for the wrong reason against ":memory:" - the two writes
// would land in two different databases and never conflict at all. A file
// under t.TempDir() is shared by every connection the pool opens, and a
// busy_timeout pragma makes a losing writer wait for the lock instead of
// failing immediately with SQLITE_BUSY. The DSN query parameter form
// (rather than a one-off PRAGMA exec after Open) matters too: modernc's
// driver re-parses the DSN for every new pooled connection, so this is the
// only way to guarantee busy_timeout is set on connections opened later in
// the pool's life, not just the first one.
//
// This deliberately does NOT set _txlock=immediate. An earlier version of
// RedeemCoupon opened its transaction with a SELECT before writing, which
// only takes a SHARED lock going in; two such transactions racing the same
// coupon could both hold SHARED and then both try to upgrade to RESERVED at
// their first write, which SQLite resolves as SQLITE_BUSY immediately
// rather than queuing the upgrade behind busy_timeout. That version relied
// on _txlock=immediate here to mask the problem - but the documented
// production DSN (docs/content/docs/stores/sqlite.mdx) does not set it, so
// the tests were passing against a configuration production does not run.
// RedeemCoupon's transactions now open with a write (the application
// INSERT) as their first statement instead, which takes the RESERVED lock
// up front with no later upgrade to fail, so this harness runs the same
// DSN shape production does and still exercises the real lock behavior.
func NewSQLite(t *testing.T) ledgerstore.Store {
	t.Helper()
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "ledger.db")
	dsn := "file:" + dbPath + "?_pragma=busy_timeout(5000)"

	sdb := sqlitedriver.New()
	if err := sdb.Open(ctx, dsn); err != nil {
		t.Fatalf("storetest: open sqlite driver: %v", err)
	}
	t.Cleanup(func() { _ = sdb.Close() })

	db, err := grove.Open(sdb)
	if err != nil {
		t.Fatalf("storetest: grove.Open: %v", err)
	}

	s := sqlite.New(db)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("storetest: migrate sqlite: %v", err)
	}

	return s
}

// NewPostgres returns a migrated PostgreSQL store, or skips the test when
// LEDGER_TEST_POSTGRES_DSN is unset.
//
// The DSN's database is migrated in place and not torn down, so point it at
// a scratch database and not at anything you care about.
func NewPostgres(t *testing.T) ledgerstore.Store {
	t.Helper()

	dsn := os.Getenv("LEDGER_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("storetest: set LEDGER_TEST_POSTGRES_DSN to run the postgres conformance suite")
	}

	ctx := context.Background()

	pg := pgdriver.New()
	if err := pg.Open(ctx, dsn); err != nil {
		t.Fatalf("storetest: open postgres driver: %v", err)
	}
	t.Cleanup(func() { _ = pg.Close() })

	db, err := grove.Open(pg)
	if err != nil {
		t.Fatalf("storetest: grove.Open: %v", err)
	}

	s := postgres.New(db)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("storetest: migrate postgres: %v", err)
	}

	return s
}

// NewMongo returns a migrated MongoDB store, or skips the test when
// LEDGER_TEST_MONGO_URI is unset.
func NewMongo(t *testing.T) ledgerstore.Store {
	t.Helper()

	uri := os.Getenv("LEDGER_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("storetest: set LEDGER_TEST_MONGO_URI to run the mongo conformance suite")
	}

	ctx := context.Background()

	md := mongodriver.New()
	if err := md.Open(ctx, uri); err != nil {
		t.Fatalf("storetest: open mongo driver: %v", err)
	}
	t.Cleanup(func() { _ = md.Close() })

	db, err := grove.Open(md)
	if err != nil {
		t.Fatalf("storetest: grove.Open: %v", err)
	}

	s := mongo.New(db)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("storetest: migrate mongo: %v", err)
	}

	return s
}
