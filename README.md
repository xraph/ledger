# Ledger - Modern Billing Engine for SaaS

> Composable usage-based billing engine for Go. Meter events, check entitlements, compute invoices, manage plans and subscriptions, integrate payment providers.

## 🚀 Features

### Core Billing Capabilities
- **Subscription Management** - Full lifecycle: trials, upgrades, downgrades, cancellations
- **Usage-Based Billing** - High-throughput metering with batched ingestion
- **Entitlement System** - Sub-millisecond authorization checks with caching
- **Flexible Pricing** - Graduated, volume, flat-rate, and hybrid models
- **Invoice Generation** - Automated creation with line items, taxes, discounts
- **Coupon System** - Percentage and fixed-amount discounts with validation

### Technical Excellence
- **TypeID Integration** - Globally unique, K-sortable identifiers (TypeID v2)
- **Integer-Only Money** - No floating-point precision issues
- **Plugin Architecture** - Type-cached dispatch for O(1) performance
- **Multi-Store Support** - PostgreSQL, SQLite, Redis, Memory implementations
- **Audit Trail** - Chronicle integration for complete audit logging
- **Observability** - Built-in metrics with Forge MetricFactory

### Enterprise Ready
- **Multi-Tenancy** - Built-in tenant isolation with Forge scope
- **High Performance** - 100K+ events/sec ingestion, <1ms entitlement checks
- **Provider Integration** - Ready for Stripe, Paddle, custom providers
- **Webhook Support** - Robust handling with retry logic
- **Security** - Row-level security and tenant isolation
- **Authsome Ready** - Seamless billing module integration

## 🔧 Quick Start

```go
package main

import (
    "context"
    "log"
    "log/slog"
    "time"

    "github.com/xraph/ledger"
    "github.com/xraph/ledger/plan"
    "github.com/xraph/ledger/store/memory"
    "github.com/xraph/ledger/subscription"
    "github.com/xraph/ledger/types"
)

func main() {
    // Create store (memory for demo, use PostgreSQL in production)
    store := memory.New()

    // Initialize Ledger with plugins
    l := ledger.New(store,
        ledger.WithLogger(slog.Default()),
        ledger.WithMeterConfig(100, 5*time.Second),
        ledger.WithEntitlementCacheTTL(30*time.Second),
    )

    // Start the engine
    ctx := context.Background()
    if err := l.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer l.Stop()

    // Create a plan
    p := &plan.Plan{
        Name:     "Pro Plan",
        Slug:     "pro",
        Currency: "usd",
        Status:   plan.StatusActive,
        Features: []plan.Feature{
            {
                Key:       "api_calls",
                Name:      "API Calls",
                Type:      plan.FeatureMetered,
                Limit:     10000,
                Period:    plan.PeriodMonthly,
                SoftLimit: true, // Allow overage
            },
            {
                Key:   "seats",
                Name:  "Team Seats",
                Type:  plan.FeatureSeat,
                Limit: 5,
            },
        },
        Pricing: &plan.Pricing{
            BaseAmount:    types.USD(4900), // $49.00
            BillingPeriod: plan.PeriodMonthly,
            Tiers: []plan.PriceTier{
                {
                    FeatureKey: "api_calls",
                    Type:       plan.TierGraduated,
                    UpTo:       10000,
                    UnitAmount: types.Zero("usd"), // Included
                },
                {
                    FeatureKey: "api_calls",
                    Type:       plan.TierGraduated,
                    UpTo:       -1, // Unlimited
                    UnitAmount: types.USD(1), // $0.01 per call
                },
            },
        },
    }

    if err := l.CreatePlan(ctx, p); err != nil {
        log.Fatal(err)
    }

    // Create a subscription
    sub := &subscription.Subscription{
        TenantID: "tenant_123",
        PlanID:   p.ID,
        Status:   subscription.StatusActive,
        AppID:    "app_456",
    }

    if err := l.CreateSubscription(ctx, sub); err != nil {
        log.Fatal(err)
    }

    // Set context for tenant/app isolation
    ctx = context.WithValue(ctx, "tenant_id", "tenant_123")
    ctx = context.WithValue(ctx, "app_id", "app_456")

    // Check entitlement (< 1ms with cache)
    result, err := l.Entitled(ctx, "api_calls")
    if err != nil {
        log.Fatal(err)
    }

    if result.Allowed {
        log.Printf("API calls allowed. Remaining: %d\n", result.Remaining)

        // Meter usage (non-blocking, batched)
        l.Meter(ctx, "api_calls", 100)
    } else {
        log.Printf("API calls denied: %s\n", result.Reason)
    }

    // Generate invoice
    invoice, err := l.GenerateInvoice(ctx, sub.ID)
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("Invoice generated: %s\n", invoice.Total.String())
}
```

## Installation

```bash
go get github.com/xraph/ledger
```

## Architecture

Ledger is designed as a **library, not a service**. Import it directly into your Go application for maximum performance and flexibility.

### Core Components

- **Plans & Features** - Define your pricing catalog
- **Subscriptions** - Manage customer subscriptions
- **Metering** - High-throughput usage ingestion
- **Entitlements** - Sub-millisecond permission checks
- **Invoices** - Automated billing computation
- **Providers** - Payment gateway integration

### Store Backends

- **PostgreSQL** - Production primary store (pgx/v5)
- **Bun** - ORM-based store (uptrace/bun)
- **SQLite** - Embedded/edge deployments
- **Redis** - Entitlement cache & counters
- **Memory** - Testing and development

## Documentation

- [Quick Start Guide](docs/QUICKSTART.md)
- [API Reference](docs/API.md)
- [Integration Guide](docs/INTEGRATION.md)
- [Examples](_examples/)

## Development

```bash
# Setup
make setup

# Run tests
make test

# Run linter
make lint

# Run benchmarks
make bench
```

## Performance

- Entitlement checks: < 1ms (cache hit), < 10ms (cache miss)
- Usage ingestion: > 10,000 events/second
- Invoice generation: < 100ms for 1000 line items

## License

MIT