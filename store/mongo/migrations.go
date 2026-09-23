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

				// The idempotency_key index here must stay in sync with
				// migrationIndexes() in store.go, which is what Migrate()
				// actually builds indexes from - this migrate.Group is not
				// currently invoked by Migrate(). It is a PARTIAL unique
				// index (non-empty keys only, via $gt ""), not a sparse one:
				// a sparse index still enforces uniqueness among documents
				// that do carry the field, even with an empty string value,
				// which silently drops the second and later keyless usage
				// events ingested. See store.go's index name constants for
				// the full rationale.
				return mexec.CreateIndexes(ctx, colUsageEvents, []mongo.IndexModel{
					{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "app_id", Value: 1}, {Key: "feature_key", Value: 1}, {Key: "timestamp", Value: -1}}},
					{Keys: bson.D{{Key: "timestamp", Value: -1}}},
					{
						Keys: bson.D{{Key: "idempotency_key", Value: 1}},
						Options: options.Index().
							SetName(newIdempotencyKeyIndexName).
							SetUnique(true).
							SetPartialFilterExpression(bson.M{"idempotency_key": bson.M{"$gt": ""}}),
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
	)
}
