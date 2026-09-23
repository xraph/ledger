package mongo

import (
	"context"
	"testing"
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

// TestMigrateIsIdempotentAndReplacesTheSparseIndex pins down two things
// Migrate must guarantee on mongo:
//
//  1. Calling it twice in a row against the same database succeeds. This
//     matters because storetest.Run's newStore is invoked once per subtest,
//     so a persistent scratch database (unlike sqlite/memory, torn down
//     fresh each time) gets Migrate() called dozens of times across one
//     suite run, and again on every later run against a database that
//     never gets dropped in between.
//  2. After Migrate runs, the old sparse idempotency_key_1 index is gone
//     and the new partial idempotency_key_unique_partial index is present.
//     Mongo refuses to create an index with the same keys as an existing
//     one but different options, so Migrate must drop the old index by
//     name first - and do so tolerating "index not found" so the drop
//     itself doesn't break idempotency on a database that has already been
//     migrated under the new scheme.
//
// Needs a real mongod (LEDGER_TEST_MONGO_URI); skips without one.
func TestMigrateIsIdempotentAndReplacesTheSparseIndex(t *testing.T) {
	ctx := context.Background()
	s := newInternalTestStore(t) // newInternalTestStore already ran Migrate once.

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate call: got error %v, want nil (Migrate must be idempotent)", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("third Migrate call: got error %v, want nil (Migrate must be idempotent)", err)
	}

	names := listIndexNames(ctx, t, s, colUsageEvents)
	if names[oldIdempotencyKeyIndexName] {
		t.Errorf("index %q is still present after Migrate; it must be dropped", oldIdempotencyKeyIndexName)
	}
	if !names[newIdempotencyKeyIndexName] {
		t.Errorf("index %q is missing after Migrate; want it present", newIdempotencyKeyIndexName)
	}
}

// TestMigrateDropOldIndexToleratesAlreadyDropped exercises DropOne against
// an index name that mongo has never seen on this collection at all,
// confirming isIndexNotFound recognizes that error shape too (not just the
// "dropped by an earlier Migrate call" case the idempotency test above
// covers).
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
