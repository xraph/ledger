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
// This is the seeded-old-schema counterpart to the earlier version of this
// test, which ran Migrate against a freshly created database that never had
// the old index in the first place - meaning it could not actually exercise
// the "drop the superseded sparse index" behavior at all; removing that step
// from Migrate would have left it green by accident. Seeding here closes
// that gap. See TestMigrateDiscriminatesOnMissingDropStep below for the
// direct proof.
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

// TestMigrateDiscriminatesOnMissingDropStep is the direct proof that
// TestMigrateTransitionsFromOldSparseIndexWithoutLosingData actually
// exercises the drop-the-old-index behavior, rather than passing vacuously
// against a database that never had the old index to begin with. It
// reproduces that test's scenario against Migrate with the drop step
// removed (inlined here rather than by editing store.go, so the two tests
// stay independent of each other's ordering) and confirms the old index
// remains - i.e. confirms this test SHAPE fails without the fix, which is
// what makes the real test above meaningful when it passes.
func TestMigrateDiscriminatesOnMissingDropStep(t *testing.T) {
	ctx := context.Background()
	s := newUnmigratedInternalTestStore(t)
	resetUsageEventsCollection(ctx, t, s)

	seedOldSparseIdempotencyKeyIndex(ctx, t, s)

	// Create-only, deliberately skipping the drop-superseded-indexes half of
	// Migrate, to reproduce what the real Migrate would do if that step
	// were ever deleted.
	if _, err := s.mdb.Collection(colUsageEvents).Indexes().CreateMany(ctx, migrationIndexes()[colUsageEvents]); err != nil {
		t.Fatalf("create-only step: %v", err)
	}

	names := listIndexNames(ctx, t, s, colUsageEvents)
	if !names[oldIdempotencyKeyIndexName] {
		t.Fatal("sanity check failed: the old index should still be present when the drop step is skipped")
	}
	if !names[newIdempotencyKeyIndexName] {
		t.Fatal("sanity check failed: the new index should have been created")
	}
	// This is the assertion the real Migrate must satisfy and this
	// create-only reproduction must fail: with the drop step skipped, the
	// old index is still here, which is exactly the bug a reviewer would
	// catch if the drop step were ever deleted from Migrate.
	t.Log("confirmed: without the drop step, the old sparse index survives Migrate's create phase - this is the failure TestMigrateTransitionsFromOldSparseIndexWithoutLosingData exists to catch")
}

// TestMigrateReportsDuplicatesWithoutDeletingAnything covers the case I2
// calls out explicitly: existing documents already violate the new unique
// index (two share one non-empty key), and no index protects the
// collection at the moment Migrate runs (simulating data written during a
// window where the index had been dropped, by hand, for some unrelated
// reason - the worst case, not the common one). Migrate must:
//   - fail loudly, naming the collection, rather than silently succeeding
//     with a corrupt, unprotected collection;
//   - never delete the duplicate documents to "fix" the conflict;
//   - never leave the collection in a WORSE state than it found it in.
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
// things worse and to fail with a clear, named error instead of silently
// leaving duplicates uncaught.
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
	if !strings.Contains(err.Error(), colUsageEvents) {
		t.Errorf("Migrate error %q does not name the collection %q", err.Error(), colUsageEvents)
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

	// No worse than before: before this Migrate call there was zero
	// idempotency_key protection (this test dropped the old index itself);
	// after the failed call there must still be none of either kind - not
	// a half-built new index, not a resurrected old one.
	names := listIndexNames(ctx, t, s, colUsageEvents)
	if names[oldIdempotencyKeyIndexName] {
		t.Errorf("old index %q unexpectedly present after a failed Migrate", oldIdempotencyKeyIndexName)
	}
	if names[newIdempotencyKeyIndexName] {
		t.Errorf("new index %q unexpectedly present after a failed Migrate (it must not partially succeed)", newIdempotencyKeyIndexName)
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
