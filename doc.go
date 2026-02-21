// Package ledger provides a composable usage-based billing engine for Go applications.
//
// Ledger is designed as a library, not a service. Import it directly into your Go
// application for maximum performance and flexibility. It provides:
//
//   - Sub-millisecond entitlement checks with multi-layer caching
//   - High-throughput usage metering with batched ingestion
//   - Flexible pricing models (graduated, volume, flat tiers)
//   - Automated invoice generation with line-item detail
//   - Pluggable payment provider integration (Stripe built-in)
//   - Comprehensive audit trail via Chronicle
//   - Production metrics via go-utils MetricFactory
//
// # Quick Start
//
// Create a ledger instance with your preferred store:
//
//	import (
//	    "github.com/xraph/ledger"
//	    "github.com/xraph/ledger/store/postgres"
//	)
//
//	// Initialize store
//	store, err := postgres.New(databaseURL)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Create ledger
//	l := ledger.New(store)
//
//	// Start the ledger (begins background workers)
//	if err := l.Start(ctx); err != nil {
//	    log.Fatal(err)
//	}
//	defer l.Stop()
//
// # Core Concepts
//
// Plans define what features are available and at what limits:
//
//	plan := &plan.Plan{
//	    Name: "Pro",
//	    Features: []plan.Feature{
//	        {Key: "api_calls", Limit: 10000, Type: plan.FeatureMetered},
//	        {Key: "seats", Limit: 5, Type: plan.FeatureSeat},
//	        {Key: "sso", Limit: 1, Type: plan.FeatureBoolean},
//	    },
//	}
//
// Subscriptions connect tenants to plans:
//
//	sub, err := l.CreateSubscription(ctx, tenantID, planID)
//
// Entitlements check if a tenant can use a feature:
//
//	result, err := l.Entitled(ctx, "api_calls")
//	if result.Allowed {
//	    // Process API call
//	    l.Meter(ctx, "api_calls", 1)
//	}
//
// # Performance
//
// Ledger is optimized for production workloads:
//
//   - Entitlement checks: < 1ms with cache hit, < 10ms with cache miss
//   - Usage ingestion: > 10,000 events/second with batching
//   - Invoice generation: < 100ms for 1000 line items
//
// All monetary calculations use integer arithmetic to avoid floating-point
// precision issues. The Money type represents amounts in the smallest currency
// unit (cents for USD, pence for GBP, etc).
//
// # Integration
//
// Ledger integrates seamlessly with the Forgery ecosystem:
//
//   - Forge: Scope extraction and tenant isolation
//   - Chronicle: Audit trail for all billing events
//   - Authsome: Billing module for authentication
//   - go-utils: Production metrics and observability
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
