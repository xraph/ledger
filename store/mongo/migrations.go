package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/xraph/grove/drivers/mongodriver/mongomigrate"
	"github.com/xraph/grove/migrate"
)

// Migrations is the grove migration group for the Ledger mongo store.
var Migrations = migrate.NewGroup("ledger")

func init() {
	Migrations.MustRegister(
		&migrate.Migration{
			Name:    "create_ledger_plans",
			Version: "20240101000001",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}

				if err := mexec.CreateCollection(ctx, (*planModel)(nil)); err != nil {
					return err
				}

				return mexec.CreateIndexes(ctx, colPlans, []mongo.IndexModel{
					{
						Keys:    bson.D{{Key: "slug", Value: 1}, {Key: "app_id", Value: 1}},
						Options: options.Index().SetUnique(true),
					},
					{Keys: bson.D{{Key: "app_id", Value: 1}, {Key: "status", Value: 1}}},
					{Keys: bson.D{{Key: "app_id", Value: 1}, {Key: "created_at", Value: 1}}},
				})
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}
				return mexec.DropCollection(ctx, (*planModel)(nil))
			},
		},
		&migrate.Migration{
			Name:    "create_ledger_subscriptions",
			Version: "20240101000002",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}

				if err := mexec.CreateCollection(ctx, (*subscriptionModel)(nil)); err != nil {
					return err
				}

				return mexec.CreateIndexes(ctx, colSubscriptions, []mongo.IndexModel{
					{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "app_id", Value: 1}, {Key: "status", Value: 1}}},
					{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "app_id", Value: 1}, {Key: "created_at", Value: -1}}},
					{Keys: bson.D{{Key: "plan_id", Value: 1}}},
				})
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}
				return mexec.DropCollection(ctx, (*subscriptionModel)(nil))
			},
		},
		&migrate.Migration{
			Name:    "create_ledger_usage_events",
			Version: "20240101000003",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}

				if err := mexec.CreateCollection(ctx, (*usageEventModel)(nil)); err != nil {
					return err
				}

				return mexec.CreateIndexes(ctx, colUsageEvents, []mongo.IndexModel{
					{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "app_id", Value: 1}, {Key: "feature_key", Value: 1}, {Key: "timestamp", Value: -1}}},
					{Keys: bson.D{{Key: "timestamp", Value: -1}}},
					{
						Keys:    bson.D{{Key: "idempotency_key", Value: 1}},
						Options: options.Index().SetUnique(true).SetSparse(true),
					},
				})
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}
				return mexec.DropCollection(ctx, (*usageEventModel)(nil))
			},
		},
		&migrate.Migration{
			Name:    "create_ledger_entitlement_cache",
			Version: "20240101000004",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}

				if err := mexec.CreateCollection(ctx, (*entitlementCacheModel)(nil)); err != nil {
					return err
				}

				return mexec.CreateIndexes(ctx, colEntitlements, []mongo.IndexModel{
					{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "app_id", Value: 1}}},
					{Keys: bson.D{{Key: "expires_at", Value: 1}}},
				})
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}
				return mexec.DropCollection(ctx, (*entitlementCacheModel)(nil))
			},
		},
		&migrate.Migration{
			Name:    "create_ledger_invoices",
			Version: "20240101000005",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}

				if err := mexec.CreateCollection(ctx, (*invoiceModel)(nil)); err != nil {
					return err
				}

				return mexec.CreateIndexes(ctx, colInvoices, []mongo.IndexModel{
					{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "app_id", Value: 1}, {Key: "created_at", Value: -1}}},
					{Keys: bson.D{{Key: "app_id", Value: 1}, {Key: "status", Value: 1}}},
					{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "app_id", Value: 1}, {Key: "period_start", Value: 1}, {Key: "period_end", Value: 1}}},
					{Keys: bson.D{{Key: "subscription_id", Value: 1}}},
				})
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}
				return mexec.DropCollection(ctx, (*invoiceModel)(nil))
			},
		},
		&migrate.Migration{
			Name:    "create_ledger_coupons",
			Version: "20240101000006",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}

				if err := mexec.CreateCollection(ctx, (*couponModel)(nil)); err != nil {
					return err
				}

				return mexec.CreateIndexes(ctx, colCoupons, []mongo.IndexModel{
					{
						Keys:    bson.D{{Key: "code", Value: 1}, {Key: "app_id", Value: 1}},
						Options: options.Index().SetUnique(true),
					},
					{Keys: bson.D{{Key: "app_id", Value: 1}, {Key: "created_at", Value: -1}}},
				})
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}
				return mexec.DropCollection(ctx, (*couponModel)(nil))
			},
		},
		&migrate.Migration{
			Name:    "create_ledger_features",
			Version: "20240101000007",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}

				if err := mexec.CreateCollection(ctx, (*featureCatalogModel)(nil)); err != nil {
					return err
				}

				return mexec.CreateIndexes(ctx, colFeatures, []mongo.IndexModel{
					{
						Keys:    bson.D{{Key: "key", Value: 1}, {Key: "app_id", Value: 1}},
						Options: options.Index().SetUnique(true),
					},
					{Keys: bson.D{{Key: "app_id", Value: 1}, {Key: "status", Value: 1}}},
					{Keys: bson.D{{Key: "app_id", Value: 1}, {Key: "created_at", Value: 1}}},
				})
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}
				return mexec.DropCollection(ctx, (*featureCatalogModel)(nil))
			},
		},
		&migrate.Migration{
			Name:    "create_ledger_coupon_applications",
			Version: "20240101000008",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}

				if err := mexec.CreateCollection(ctx, (*couponApplicationDoc)(nil)); err != nil {
					return err
				}

				return mexec.CreateIndexes(ctx, colCouponApplications, []mongo.IndexModel{
					{
						Keys:    bson.D{{Key: "coupon_id", Value: 1}, {Key: "subscription_id", Value: 1}},
						Options: options.Index().SetUnique(true),
					},
					{Keys: bson.D{{Key: "subscription_id", Value: 1}}},
				})
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}
				return mexec.DropCollection(ctx, (*couponApplicationDoc)(nil))
			},
		},
		&migrate.Migration{
			// Swaps ledger_usage_events' unique index on idempotency_key
			// from the original sparse index (migration 20240101000003,
			// left as originally written above) to a PARTIAL unique index
			// over non-empty keys only. A sparse index still enforces
			// uniqueness among documents that carry the field even with an
			// empty string value, so once one keyless usage event lands,
			// every later keyless event collides and is silently dropped -
			// see store.go's index name constants and Migrate for the full
			// rationale. This must run for databases migrated through
			// grove's orchestrator to receive the same fix Store.Migrate
			// applies directly via migrationIndexes(); keep the two in
			// sync.
			//
			// Create-then-drop, never drop-then-create: on MongoDB 7.0.41,
			// a partial index with the same key pattern as an existing
			// sparse one can be created while the old index still exists,
			// so there is no need to ever leave the collection with zero
			// uniqueness protection. Dropping first would open exactly
			// that gap - a duplicate non-empty key written in the gap
			// would defeat the very check this migration exists to fix.
			Name:    "usage_events_idempotency_key_partial_index",
			Version: "20240101000009",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}

				if err := mexec.CreateIndexes(ctx, colUsageEvents, []mongo.IndexModel{
					{
						Keys: bson.D{{Key: "idempotency_key", Value: 1}},
						Options: options.Index().
							SetName(newIdempotencyKeyIndexName).
							SetUnique(true).
							SetPartialFilterExpression(bson.M{"idempotency_key": bson.M{"$gt": ""}}),
					},
				}); err != nil {
					return fmt.Errorf("create new %s index: %w", newIdempotencyKeyIndexName, err)
				}

				if err := mexec.DB().Collection(colUsageEvents).Indexes().DropOne(ctx, oldIdempotencyKeyIndexName); err != nil {
					if !isIndexNotFound(err) {
						return fmt.Errorf("drop old %s index: %w", oldIdempotencyKeyIndexName, err)
					}
				}
				return nil
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}

				// Same discipline in reverse: recreate the old sparse index
				// before dropping the partial one, so there is no gap here
				// either.
				if err := mexec.CreateIndexes(ctx, colUsageEvents, []mongo.IndexModel{
					{
						Keys:    bson.D{{Key: "idempotency_key", Value: 1}},
						Options: options.Index().SetUnique(true).SetSparse(true),
					},
				}); err != nil {
					return fmt.Errorf("recreate old %s index: %w", oldIdempotencyKeyIndexName, err)
				}

				if err := mexec.DB().Collection(colUsageEvents).Indexes().DropOne(ctx, newIdempotencyKeyIndexName); err != nil {
					if !isIndexNotFound(err) {
						return fmt.Errorf("drop new %s index: %w", newIdempotencyKeyIndexName, err)
					}
				}
				return nil
			},
		},
		&migrate.Migration{
			// Indexes for the lifecycle clock's queries. Store.Migrate builds
			// the same ones through migrationIndexes; keep the two in step.
			Name:    "add_lifecycle_indexes",
			Version: "20240101000010",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}
				if err := mexec.CreateIndexes(ctx, colSubscriptions, lifecycleSubscriptionIndexes()); err != nil {
					return err
				}
				return mexec.CreateIndexes(ctx, colInvoices, lifecycleInvoiceIndexes())
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				mexec, ok := exec.(*mongomigrate.Executor)
				if !ok {
					return fmt.Errorf("expected mongomigrate executor, got %T", exec)
				}
				drops := map[string][]string{
					colSubscriptions: {"status_1_cancel_at_1", "status_1_trial_end_1", "status_1_current_period_end_1"},
					colInvoices:      {"status_1_due_date_1"},
				}
				for col, names := range drops {
					for _, name := range names {
						if err := mexec.DB().Collection(col).Indexes().DropOne(ctx, name); err != nil && !isIndexNotFound(err) {
							return fmt.Errorf("drop %s index %s: %w", col, name, err)
						}
					}
				}
				return nil
			},
		},
	)
}
