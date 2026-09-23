package mongo

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/mongodriver"

	"github.com/xraph/ledger/id"
)

// indexNameDoc decodes just the name field of a listIndexes result document.
type indexNameDoc struct {
	Name string `bson:"name"`
}

// listIndexNames returns every index name currently defined on col.
func listIndexNames(ctx context.Context, t *testing.T, s *Store, col string) map[string]bool {
	t.Helper()

	cur, err := s.mdb.Collection(col).Indexes().List(ctx)
	if err != nil {
		t.Fatalf("list indexes on %s: %v", col, err)
	}
	defer func() { _ = cur.Close(ctx) }()

	var docs []indexNameDoc
	if err := cur.All(ctx, &docs); err != nil {
		t.Fatalf("decode index list on %s: %v", col, err)
	}

	names := make(map[string]bool, len(docs))
	for _, d := range docs {
		names[d.Name] = true
	}
	return names
}

// newUnmigratedInternalTestStore is like newInternalTestStore but
// deliberately does NOT call Migrate, so a test can seed a specific
// pre-migration schema by hand first (the old sparse idempotency_key index,
// specific documents) before exercising Migrate itself. Skips like
// newInternalTestStore does when LEDGER_TEST_MONGO_URI isn't set.
func newUnmigratedInternalTestStore(t *testing.T) *Store {
	t.Helper()

	uri := os.Getenv("LEDGER_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set LEDGER_TEST_MONGO_URI to run this test")
	}

	ctx := context.Background()

	md := mongodriver.New()
	if err := md.Open(ctx, uri); err != nil {
		t.Fatalf("open mongo driver: %v", err)
	}
	t.Cleanup(func() { _ = md.Close() })

	db, err := grove.Open(md)
	if err != nil {
		t.Fatalf("grove.Open: %v", err)
	}

	return New(db) // deliberately not migrated
}

// resetUsageEventsCollection drops ledger_usage_events outright (a no-op,
// not an error, if it doesn't exist yet). Every test in this file that
// seeds a hand-built index over the WHOLE collection needs to start from an
// empty collection: these tests share one scratch database with every other
// test in this package and with storetest's conformance subtests, all of
// which leave their own fixture rows behind (scoped by unique tenant/app
// values, which is enough isolation for THEM, but not enough here - a new
// unique index build scans every existing document in the collection
// regardless of tenant, so leftover rows from an earlier test with the same
// idempotency_key value, most commonly "", would make a fresh index build
// fail with a spurious duplicate-key error that has nothing to do with what
// this test is actually seeding).
func resetUsageEventsCollection(ctx context.Context, t *testing.T, s *Store) {
	t.Helper()

	if err := s.mdb.Collection(colUsageEvents).Drop(ctx); err != nil {
		t.Fatalf("reset ledger_usage_events before seeding: %v", err)
	}
}

// seedOldSparseIdempotencyKeyIndex creates, by hand and under its original
// default name, the old unique+sparse index this task's fix replaces. Tests
// use this to put a scratch database into the pre-fix schema state before
// calling Migrate, since a freshly created database never had this index
// and so cannot exercise the "drop the old one" half of Migrate at all.
func seedOldSparseIdempotencyKeyIndex(ctx context.Context, t *testing.T, s *Store) {
	t.Helper()

	_, err := s.mdb.Collection(colUsageEvents).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "idempotency_key", Value: 1}},
		Options: options.Index().SetUnique(true).SetSparse(true),
	})
	if err != nil {
		t.Fatalf("seed old sparse idempotency_key index: %v", err)
	}
}

// TestMigrateTransitionsFromOldSparseIndexWithoutLosingData seeds a scratch
// database with exactly the pre-fix schema and data shapes this task's fix
// exists to handle - the OLD sparse unique index, plus usage-event documents
// holding an empty string, a BSON null, a genuinely missing field, and two
// distinct non-empty keys - then proves Migrate transitions it cleanly and
// twice over (idempotent), without disturbing any of that seeded data or its
// dedup guarantees.
//
// This is the seeded-old-schema counterpart to an earlier version of this
// test, which ran Migrate against a freshly created database that never had
// the old index in the first place - meaning it could not actually exercise
// the "drop the superseded sparse index" behavior at all; removing that step
// from Migrate would have left it green by accident. Seeding here closes
// that gap: confirmed directly by temporarily disabling Migrate's drop step
// (migrationSupersededIndexes returning an empty map) and re-running this
// test, which then failed exactly as expected - recorded in the task's
// fix-round-1 report, restored immediately after.
func TestMigrateTransitionsFromOldSparseIndexWithoutLosingData(t *testing.T) {
	ctx := context.Background()
	s := newUnmigratedInternalTestStore(t)
	resetUsageEventsCollection(ctx, t, s)

	seedOldSparseIdempotencyKeyIndex(ctx, t, s)

	tenantID := "tenant-" + id.New(id.Prefix("test")).String()
	appID := "app-" + id.New(id.Prefix("test")).String()
	const featureKey = "old-schema-seed"
	keyA := "seed-key-a-" + id.New(id.Prefix("test")).String()
	keyB := "seed-key-b-" + id.New(id.Prefix("test")).String()
	now := time.Now().UTC()

	// Inserted directly via the official driver, not through the store's
	// own IngestBatch/toUsageEventModel path, so each document's
	// idempotency_key shape is exactly what's being asserted: an empty
	// string, a BSON null, the field genuinely absent, and two distinct
	// non-empty keys. A sparse index only excludes documents missing the
	// field entirely - "" and null are both present values and, being
	// different from each other, coexist under the old unique+sparse index
	// without conflict; neither collides with the two distinct real keys
	// either.
	seedDocs := []any{
		bson.M{"_id": id.NewUsageEventID().String(), "tenant_id": tenantID, "app_id": appID, "feature_key": featureKey, "quantity": int64(1), "timestamp": now, "idempotency_key": "", "created_at": now},
		bson.M{"_id": id.NewUsageEventID().String(), "tenant_id": tenantID, "app_id": appID, "feature_key": featureKey, "quantity": int64(1), "timestamp": now, "idempotency_key": nil, "created_at": now},
		bson.M{"_id": id.NewUsageEventID().String(), "tenant_id": tenantID, "app_id": appID, "feature_key": featureKey, "quantity": int64(1), "timestamp": now, "created_at": now}, // idempotency_key genuinely absent
		bson.M{"_id": id.NewUsageEventID().String(), "tenant_id": tenantID, "app_id": appID, "feature_key": featureKey, "quantity": int64(1), "timestamp": now, "idempotency_key": keyA, "created_at": now},
		bson.M{"_id": id.NewUsageEventID().String(), "tenant_id": tenantID, "app_id": appID, "feature_key": featureKey, "quantity": int64(1), "timestamp": now, "idempotency_key": keyB, "created_at": now},
	}
	if _, err := s.mdb.Collection(colUsageEvents).InsertMany(ctx, seedDocs); err != nil {
		t.Fatalf("seed old-schema documents: %v", err)
	}

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("first Migrate call (transitioning from the old schema): got error %v, want nil", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate call: got error %v, want nil (Migrate must be idempotent)", err)
	}

	names := listIndexNames(ctx, t, s, colUsageEvents)
	if names[oldIdempotencyKeyIndexName] {
		t.Errorf("index %q is still present after Migrate; it must be dropped", oldIdempotencyKeyIndexName)
	}
	if !names[newIdempotencyKeyIndexName] {
		t.Errorf("index %q is missing after Migrate; want it present", newIdempotencyKeyIndexName)
	}

	// Seeded data survived untouched: all 5 seed documents are still there.
	seedCount, err := s.mdb.Collection(colUsageEvents).CountDocuments(ctx, bson.M{"tenant_id": tenantID, "app_id": appID, "feature_key": featureKey})
	if err != nil {
		t.Fatalf("count seeded documents: %v", err)
	}
	if seedCount != 5 {
		t.Errorf("got %d seeded document(s) after Migrate, want 5 (Migrate must never delete data)", seedCount)
	}

	// Keyless ingest now keeps every event: two brand-new keyless events for
	// a fresh tenant/app/feature must both survive under the new partial
	// index, which is exactly the defect this task's fix exists to close.
	freshTenant := "tenant-fresh-" + id.New(id.Prefix("test")).String()
	events := []*usageEventModel{
		{ID: id.NewUsageEventID().String(), TenantID: freshTenant, AppID: appID, FeatureKey: featureKey, Quantity: 1, Timestamp: now, CreatedAt: now},
		{ID: id.NewUsageEventID().String(), TenantID: freshTenant, AppID: appID, FeatureKey: featureKey, Quantity: 1, Timestamp: now, CreatedAt: now},
	}
	for _, e := range events {
		if _, err := s.mdb.NewInsert(e).Exec(ctx); err != nil {
			t.Fatalf("insert post-migrate keyless event: %v", err)
		}
	}
	keylessCount, err := s.mdb.Collection(colUsageEvents).CountDocuments(ctx, bson.M{"tenant_id": freshTenant})
	if err != nil {
		t.Fatalf("count post-migrate keyless events: %v", err)
	}
	if keylessCount != 2 {
		t.Errorf("got %d keyless event(s) after Migrate, want 2 (both keyless events must be kept, not collided as false duplicates)", keylessCount)
	}

	// A key already stored is still deduplicated: inserting another
	// document that reuses keyA must fail under the new partial unique
	// index exactly as it would have under the old sparse one.
	dup := usageEventModel{ID: id.NewUsageEventID().String(), TenantID: tenantID, AppID: appID, FeatureKey: featureKey, Quantity: 1, Timestamp: now, IdempotencyKey: keyA, CreatedAt: now}
	_, err = s.mdb.NewInsert(&dup).Exec(ctx)
	if err == nil {
		t.Fatal("inserting a document that reuses an already-stored non-empty key: got nil error, want a duplicate key error")
	}
	if !isDuplicateKeyError(err) {
		t.Fatalf("isDuplicateKeyError(%v) = false, want true (a repeated non-empty key must still be rejected after Migrate)", err)
	}
}

// TestPartialAndSparseIndexesCanCoexistOnSameKeyPattern pins down the one
// mongo-server-version-specific fact Migrate's create-before-drop design
// depends on: a partial unique index can be created over the SAME key
// pattern as an existing sparse index, without dropping the sparse one
// first.
//
// This does NOT test Migrate - it builds both indexes directly to isolate
// exactly that one server behavior. The actual proof that Migrate itself
// transitions a seeded old-schema database correctly (create new, then drop
// superseded) is TestMigrateTransitionsFromOldSparseIndexWithoutLosingData
// above; the live discrimination proof against Migrate's real drop step,
// temporarily disabled and restored, is recorded in the task's fix-round-1
// report rather than kept as a permanent test (a discrimination proof
// belongs in a temporary mutation of the code under test, not in a
// permanent copy of it that could silently drift out of sync).
func TestPartialAndSparseIndexesCanCoexistOnSameKeyPattern(t *testing.T) {
	ctx := context.Background()
	s := newUnmigratedInternalTestStore(t)
	resetUsageEventsCollection(ctx, t, s)

	seedOldSparseIdempotencyKeyIndex(ctx, t, s)

	uniqueModels := migrationUniqueIndexes()[colUsageEvents]
	if len(uniqueModels) != 1 {
		t.Fatalf("migrationUniqueIndexes()[%q] has %d model(s), want exactly 1", colUsageEvents, len(uniqueModels))
	}
	if _, err := s.mdb.Collection(colUsageEvents).Indexes().CreateOne(ctx, uniqueModels[0]); err != nil {
		t.Fatalf("create the new partial index while the old sparse index still exists: %v", err)
	}

	names := listIndexNames(ctx, t, s, colUsageEvents)
	if !names[oldIdempotencyKeyIndexName] {
		t.Error("old sparse index missing after creating the new one alongside it")
	}
	if !names[newIdempotencyKeyIndexName] {
		t.Error("new partial index missing after creating it")
	}
}

// TestMigrateReportsDuplicatesWithoutDeletingAnything covers the case I2
// calls out explicitly: existing documents already violate the new unique
// index (two share one non-empty key), and no index protects the
// collection at the moment Migrate runs (simulating data written during a
// window where the index had been dropped, by hand, for some unrelated
// reason - the worst case, not the common one). Migrate must:
//   - fail loudly, naming the collection, rather than silently succeeding
//     with a corrupt, unprotected collection;
//   - describe what actually happened, and nothing false: it must NOT
//     claim an old index was "left in place" to protect the data, since a
//     genuinely superseded old index (the sparse one, while it exists)
//     already enforces uniqueness over every document with a real key,
//     making this failure impossible to reach at all - so whenever it DOES
//     fire, there either never was an old index, or it was already gone;
//   - never delete the duplicate documents to "fix" the conflict;
//   - never leave the collection in a WORSE state than it found it in;
//   - still create ledger_usage_events' other, unrelated query indexes,
//     since the unique index's own failed build must not block them (this
//     is why that index is created in its own call, separate from the
//     others - see migrationUniqueIndexes).
//
// Exact state documented here rather than left implicit: this test seeds
// zero protection (it creates the old index and then immediately drops it
// again before inserting the conflicting documents), so "no worse than
// before" means Migrate's failed attempt to create the new partial index
// must not leave behind a partially built or corrupted index of any kind -
// after the failed call, neither the old nor the new idempotency_key index
// exists, identical to the zero-protection state the test put the
// collection in before calling Migrate. Migrate is not asked to (and does
// not) restore the old sparse index in this scenario - only to avoid making
// things worse and to fail with a clear, accurate, named error instead of
// silently leaving duplicates uncaught.
func TestMigrateReportsDuplicatesWithoutDeletingAnything(t *testing.T) {
	ctx := context.Background()
	s := newUnmigratedInternalTestStore(t)
	resetUsageEventsCollection(ctx, t, s)

	seedOldSparseIdempotencyKeyIndex(ctx, t, s)
	if err := s.mdb.Collection(colUsageEvents).Indexes().DropOne(ctx, oldIdempotencyKeyIndexName); err != nil {
		t.Fatalf("drop the just-seeded old index: %v", err)
	}

	tenantID := "tenant-" + id.New(id.Prefix("test")).String()
	appID := "app-" + id.New(id.Prefix("test")).String()
	const featureKey = "duplicate-during-gap"
	sharedKey := "shared-" + id.New(id.Prefix("test")).String()
	now := time.Now().UTC()

	// With no index at all in place, both inserts succeed - nothing stops
	// them, which is the point: this reproduces data written during a
	// window with no protective index whatsoever.
	dupDocs := []any{
		bson.M{"_id": id.NewUsageEventID().String(), "tenant_id": tenantID, "app_id": appID, "feature_key": featureKey, "quantity": int64(1), "timestamp": now, "idempotency_key": sharedKey, "created_at": now},
		bson.M{"_id": id.NewUsageEventID().String(), "tenant_id": tenantID, "app_id": appID, "feature_key": featureKey, "quantity": int64(1), "timestamp": now, "idempotency_key": sharedKey, "created_at": now},
	}
	if _, err := s.mdb.Collection(colUsageEvents).InsertMany(ctx, dupDocs); err != nil {
		t.Fatalf("seed conflicting duplicate documents: %v", err)
	}
	// This test deliberately leaves the collection with duplicate,
	// unprotected data for Migrate to reject - a state later tests sharing
	// this scratch database must not inherit, since their own Migrate calls
	// need to succeed. Migrate itself must never delete data (that's the
	// behavior under test), but cleaning up after our OWN test fixture once
	// the test is done is ordinary test hygiene, not something Migrate did.
	t.Cleanup(func() {
		_, _ = s.mdb.Collection(colUsageEvents).DeleteMany(context.Background(), bson.M{"tenant_id": tenantID, "app_id": appID, "feature_key": featureKey})
	})

	err := s.Migrate(ctx)
	if err == nil {
		t.Fatal("Migrate over a collection with duplicate non-empty keys: got nil error, want one naming the collection")
	}
	msg := err.Error()
	if !strings.Contains(msg, colUsageEvents) {
		t.Errorf("Migrate error %q does not name the collection %q", msg, colUsageEvents)
	}
	// The message must not claim an old index protected this data - that is
	// never true when this error can fire (see the doc comment above and
	// duplicateKeyMigrateError's doc comment in store.go for why).
	if strings.Contains(msg, "left in place") || strings.Contains(msg, "old index") {
		t.Errorf("Migrate error %q falsely claims an old index was left in place/kept; this error only ever fires when no such protection existed", msg)
	}
	// It should instead say what actually happened: something was left
	// unchanged, and the operator needs to resolve duplicates and re-run.
	if !strings.Contains(msg, "no data was changed") {
		t.Errorf("Migrate error %q does not state that no data was changed", msg)
	}
	if !strings.Contains(msg, "re-run Migrate") {
		t.Errorf("Migrate error %q does not tell the operator to re-run Migrate after resolving the duplicates", msg)
	}
	// errors.As (which isDuplicateKeyError uses under the hood) walks an
	// errors.Join tree's Unwrap() []error just as it walks a single Unwrap()
	// error chain, so this reaches the underlying E11000 regardless of how
	// many other collections' results Migrate joined alongside it.
	if !isDuplicateKeyError(err) {
		t.Errorf("Migrate error %v does not wrap a duplicate key error", err)
	}

	// Data untouched: both conflicting documents are still there.
	count, err := s.mdb.Collection(colUsageEvents).CountDocuments(ctx, bson.M{"tenant_id": tenantID, "app_id": appID, "feature_key": featureKey})
	if err != nil {
		t.Fatalf("count conflicting documents: %v", err)
	}
	if count != 2 {
		t.Errorf("got %d document(s) after the failed Migrate, want 2 (Migrate must never delete data)", count)
	}

	// No worse than before on the idempotency_key front: before this
	// Migrate call there was zero idempotency_key protection (this test
	// dropped the old index itself); after the failed call there must
	// still be none of either kind - not a half-built new index, not a
	// resurrected old one.
	names := listIndexNames(ctx, t, s, colUsageEvents)
	if names[oldIdempotencyKeyIndexName] {
		t.Errorf("old index %q unexpectedly present after a failed Migrate", oldIdempotencyKeyIndexName)
	}
	if names[newIdempotencyKeyIndexName] {
		t.Errorf("new index %q unexpectedly present after a failed Migrate (it must not partially succeed)", newIdempotencyKeyIndexName)
	}

	// But strictly better than "everything on this collection failed": the
	// two plain query indexes bundled in migrationIndexes()[colUsageEvents]
	// must still have been created, since the unique index that failed to
	// build lives in its own separate CreateOne call (migrationUniqueIndexes)
	// precisely so a failure there doesn't block them. Counted rather than
	// matched by exact name, since mongo's default index names are an
	// implementation detail this test shouldn't need to hardcode: every
	// index on the collection other than "_id_" and the two
	// idempotency_key ones must be one of those two query indexes.
	other := 0
	for name := range names {
		if name == "_id_" || name == oldIdempotencyKeyIndexName || name == newIdempotencyKeyIndexName {
			continue
		}
		other++
	}
	if other != 2 {
		t.Errorf("got %d non-idempotency-key index(es) on %s after the failed Migrate, want 2 "+
			"(the collection's other query indexes must still be created even when the unique "+
			"index's own build fails)", other, colUsageEvents)
	}
}

// TestMigrateDropOldIndexToleratesAlreadyDropped exercises DropOne against
// an index name that mongo has never seen on this collection at all,
// confirming isIndexNotFound recognizes that error shape too (not just the
// "dropped by an earlier Migrate call" case the tests above cover).
func TestMigrateDropOldIndexToleratesAlreadyDropped(t *testing.T) {
	ctx := context.Background()
	s := newInternalTestStore(t)

	err := s.mdb.Collection(colUsageEvents).Indexes().DropOne(ctx, "definitely_never_existed_index_name")
	if err == nil {
		t.Fatal("DropOne on a nonexistent index name: got nil error, want an index-not-found error")
	}
	if !isIndexNotFound(err) {
		t.Fatalf("isIndexNotFound(%v) = false, want true", err)
	}
}
