// Package ledger provides a composable usage-based billing engine for Go applications.
//
// Ledger is designed as a library, not a service. Import it directly into your Go
// application and run it against your own database. It provides:
//
//   - Entitlement checks against a plan's limits, cached in the store
//   - Usage metering with buffered, batched ingestion
//   - Flexible pricing models (graduated, volume, flat tiers)
//   - Invoice generation with base, overage, seat, discount and tax lines
//   - Pluggable payment provider integration through provider.Provider (no
//     provider implementation ships with the module)
//   - An audit trail bridge (package audithook) for Chronicle or any recorder
//   - Metrics through a small MetricFactory interface (package observability)
//
// # Quick Start
//
// Create a ledger instance with your preferred store:
//
//	import (
//	    "github.com/xraph/grove"
//	    "github.com/xraph/grove/drivers/pgdriver"
//	    "github.com/xraph/ledger"
//	    "github.com/xraph/ledger/store/postgres"
//	)
//
//	// Open a Grove database
//	pg := pgdriver.New()
//	if err := pg.Open(ctx, databaseURL); err != nil {
//	    log.Fatal(err)
//	}
//	db, err := grove.Open(pg)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Create ledger
//	l := ledger.New(postgres.New(db))
//
//	// Start the ledger (migrates the store, then starts background workers)
//	if err := l.Start(ctx); err != nil {
//	    log.Fatal(err)
//	}
//	defer l.Stop()
//
// # Core Concepts
//
// Plans define what features are available and at what limits:
//
//	p := &plan.Plan{
//	    Name:     "Pro",
//	    Slug:     "pro",
//	    Currency: "usd",
//	    Status:   plan.StatusActive,
//	    Features: []plan.Feature{
//	        {Key: "api_calls", Limit: 10000, Type: plan.FeatureMetered, Period: plan.PeriodMonthly},
//	        {Key: "seats", Limit: 5, Type: plan.FeatureSeat},
//	        {Key: "sso", Limit: 1, Type: plan.FeatureBoolean},
//	    },
//	}
//	err := l.CreatePlan(ctx, p)
//
// Subscriptions connect tenants to plans:
//
//	sub := &subscription.Subscription{TenantID: tenantID, PlanID: p.ID}
//	err := l.CreateSubscription(ctx, sub)
//
// Entitlements check if a tenant can use a feature. Entitled, Check, Meter
// and MeterWithKey read the tenant and app from the context. Set them with
// WithTenant; the plain string keys "tenant_id" and "app_id" still work:
//
//	ctx = ledger.WithTenant(ctx, tenantID, appID)
//	result, err := l.Entitled(ctx, "api_calls")
//	if result.Allowed {
//	    // Process API call
//	    l.Meter(ctx, "api_calls", 1)
//	}
//
// Entitled reads a store outage as no access. Check returns the same answer
// but reports an outage as an error wrapping ErrUnavailable. MeterWithKey is
// Meter for an event with an idempotency key: the store counts a key once, and
// a failed write is retried instead of dropped.
//
// All monetary calculations use integer arithmetic to avoid floating-point
// precision issues. The Money type represents amounts in the smallest currency
// unit (cents for USD, pence for GBP, etc).
//
// # Integration
//
// Ledger works with the rest of the Forgery ecosystem:
//
//   - Forge: the extension package registers the engine as a Forge extension
//   - Grove: the PostgreSQL, SQLite and MongoDB stores run on a *grove.DB
//   - Chronicle: package audithook turns billing events into audit records
//   - go-utils: logging, and metrics through package observability
//
// # TypeID
//
// All entities use TypeID for globally unique, type-safe identifiers:
//
//	plan_01h2xcejqtf2nbrexx3vqjhp41  // Plan ID
//	sub_01h2xcejqtf2nbrexx3vqjhp41   // Subscription ID
//	inv_01h455vb4pex5vsknk084sn02q   // Invoice ID
//
// TypeIDs are K-sortable, making them ideal for database indexes and
// providing natural time-ordering of entities.
package ledger
