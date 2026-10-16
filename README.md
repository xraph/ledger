<p align="center">
  <img src="assets/readme-banner.svg" alt="Ledger, a usage-based billing engine for Go with metering, entitlements, plans and invoices" width="100%">
</p>

# Ledger: usage-based billing engine for Go

Ledger is a Go library for usage-based billing in SaaS products. You define plans and a feature catalog, subscribe your tenants, meter usage, check entitlements and quotas on the request path, and generate invoices with tiered pricing and coupons. It runs inside your process against your own database (PostgreSQL, SQLite, MongoDB or in-memory), and it ships an extension for [Forge](https://github.com/xraph/forge) so a Forge app can register it with one line.

[![Go 1.26+](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/dl/) [![CI](https://github.com/xraph/ledger/actions/workflows/ci.yml/badge.svg)](https://github.com/xraph/ledger/actions/workflows/ci.yml) [![Go Reference](https://pkg.go.dev/badge/github.com/xraph/ledger.svg)](https://pkg.go.dev/github.com/xraph/ledger) [![GitHub Stars](https://img.shields.io/github/stars/xraph/ledger?style=flat)](https://github.com/xraph/ledger/stargazers)

[Documentation](https://www.xraph.com/docs/ledger) · [Quick start](#quick-start) · [Features](#why-ledger) · [Stores](#pick-a-store) · [Forge](#use-it-with-forge) · [Configuration](#configuration) · [FAQ](#faq)

## Quick start

You need [Go 1.26 or later](https://go.dev/dl/).

```bash
go get github.com/xraph/ledger@latest
```

This program uses the in-memory store, so there's nothing to install. It creates a $49 monthly plan with an API call quota and a single sign-on flag, subscribes a tenant, checks the quota, records one call and bills the period:

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/xraph/ledger"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/subscription"
	"github.com/xraph/ledger/types"
)

func main() {
	ctx := context.Background()

	l := ledger.New(memory.New())
	if err := l.Start(ctx); err != nil {
		log.Fatal(err)
	}
	defer l.Stop()

	pro := &plan.Plan{
		Name:     "Pro",
		Slug:     "pro",
		Currency: "usd",
		Status:   plan.StatusActive,
		AppID:    "my-app",
		Features: []plan.Feature{
			{Key: "api_calls", Name: "API calls", Type: plan.FeatureMetered, Limit: 10000, Period: plan.PeriodMonthly},
			{Key: "sso", Name: "Single sign-on", Type: plan.FeatureBoolean, Limit: 1},
		},
		Pricing: &plan.Pricing{
			BaseAmount:    types.USD(4900), // $49.00
			BillingPeriod: plan.PeriodMonthly,
		},
	}
	if err := l.CreatePlan(ctx, pro); err != nil {
		log.Fatal(err)
	}

	sub := &subscription.Subscription{TenantID: "acme", AppID: "my-app", PlanID: pro.ID}
	if err := l.CreateSubscription(ctx, sub); err != nil {
		log.Fatal(err)
	}

	// Entitled and Meter read the tenant and app from the context.
	ctx = context.WithValue(ctx, "tenant_id", "acme")
	ctx = context.WithValue(ctx, "app_id", "my-app")

	res, err := l.Entitled(ctx, "api_calls")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("api_calls allowed=%v remaining=%d\n", res.Allowed, res.Remaining)

	if err := l.Meter(ctx, "api_calls", 1); err != nil {
		log.Fatal(err)
	}

	inv, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("invoice", inv.ID, "total", inv.Total)
}
```

Run it with `go run .` and you'll see something like this:

```text
api_calls allowed=true remaining=10000
invoice inv_01m4xw3arsegprh53198t2n0dg total $49.00
```

You should know two things before you go further. First, `Entitled` and `Meter` take the tenant and app from the context values `"tenant_id"` and `"app_id"` (plain string keys), and if either one is missing, `Meter` returns `ErrInvalidInput` and `Entitled` answers not allowed. Second, `Meter` doesn't write anything on the spot: it drops the event into a buffer that a background worker flushes to the store every 5 seconds, or sooner once 100 events have piled up, which means the usage you just recorded only counts toward your next quota check or invoice after that flush has happened. `Stop` flushes the rest.

## Why Ledger?

Billing code usually gets written twice. You write a quick quota check first, and then, months later, finance asks for invoices that agree with it and you write the pricing logic again somewhere else. With Ledger both read the same plan, so the limit your API enforces is the limit your invoice prices.

| You need | Ledger gives you |
| --- | --- |
| Pricing plans | Plans with metered, boolean and seat features, a base fee, trial days, and draft, active and archived states |
| Quota checks on the request path | `Entitled` and `Remaining`, with hard or soft limits, `-1` for unlimited, and results cached in the store for 30 seconds by default |
| Usage metering | `Meter`, a buffered and batched ingest path that never blocks your handler |
| Invoices | `GenerateInvoice` with base fee, overage, seat and discount line items, priced from graduated, volume or flat tiers |
| Discounts | Percentage and fixed-amount coupons, applied to a subscription with `ApplyCoupon` |
| Subscription lifecycle | Trials, plan changes, pause and resume, scheduled or immediate cancels, and a lifecycle clock that rolls periods forward and marks overdue invoices past due |
| Exact money | `types.Money`, integer minor units with overflow-checked arithmetic |
| Your own database | PostgreSQL, SQLite and MongoDB stores on [Grove](https://github.com/xraph/grove), plus an in-memory store for tests |
| Hooks into billing events | A plugin registry with typed hooks, an audit trail bridge and a metrics plugin |
| Telemetry | OpenTelemetry spans for lifecycle runs, meter flushes, invoicing and provider calls, plus metrics on Forge's `/_/metrics` or any go-utils sink |

Every entity gets a [TypeID](https://github.com/jetify-com/typeid) with its own prefix (`plan_`, `sub_`, `inv_`, `cpn_` and so on), so an ID in a log line tells you what it points at.

## Plans, entitlements and invoices

Each plan feature does two jobs. Its `Limit` is the quota `Entitled` enforces, and for a metered feature it is also the allowance an invoice includes before overage starts. `SoftLimit: true` lets a tenant go over (the result says `"over soft limit"`) and bills the extra through the price tiers you give that feature:

```go
Pricing: &plan.Pricing{
	BaseAmount:    types.USD(4900),
	BillingPeriod: plan.PeriodMonthly,
	Tiers: []plan.PriceTier{
		{FeatureKey: "api_calls", Type: plan.TierGraduated, UpTo: 10000, UnitAmount: types.Zero("usd")},
		{FeatureKey: "api_calls", Type: plan.TierGraduated, UpTo: -1, UnitAmount: types.USD(1)},
	},
},
```

With `Limit: 10000` on the feature, the first 10,000 calls are covered by the base fee and every call after that costs a cent. `CreatePlan` checks the ladder when you save the plan, so a broken one (mixed tier types, a currency that doesn't match the plan) fails right there with `invoice.ErrInvalidTiers`, long before a billing run.

These are the engine methods you'll reach for most:

| Area | Methods on `*ledger.Ledger` |
| --- | --- |
| Plans and catalog | `CreatePlan`, `UpdatePlan`, `ActivatePlan`, `ArchivePlan`, `CreateFeature`, `ListFeatures` |
| Subscriptions | `CreateSubscription`, `ChangePlan`, `PauseSubscription`, `ResumeSubscription`, `CancelSubscription` |
| Usage and quota | `Meter`, `Entitled`, `Remaining`, `InspectEntitlement` |
| Invoices | `GenerateInvoice` (with `ledger.ForPeriod` for a past period), `FinalizeInvoice`, `MarkInvoicePaid`, `MarkInvoiceVoided`, `ExportInvoice` |
| Coupons | `CreateCoupon`, `ApplyCoupon`, `ListAppliedCoupons` |
| Lifecycle | `Advance`, which runs `AdvancePeriods`, `EnactCancels`, `EndTrials` and `MarkInvoicesPastDue` in that order |

`ChangePlan` takes effect from the next invoice. Nothing is prorated. The lifecycle clock runs `Advance` once a minute in the background, and you can turn it off with `ledger.WithLifecycleInterval(0)` if you want to call `Advance` from your own scheduler. Every change it makes is a conditional store write, so you can run it on several replicas at once.

## Pick a store

`ledger.New` takes any `store.Store`. These ship with the module:

| Store | Package | Use it for |
| --- | --- | --- |
| In-memory | `github.com/xraph/ledger/store/memory` | Tests and local development |
| PostgreSQL | `github.com/xraph/ledger/store/postgres` | Production, through Grove's `pgdriver` |
| SQLite | `github.com/xraph/ledger/store/sqlite` | Single-node and embedded deployments, through Grove's `sqlitedriver` |
| MongoDB | `github.com/xraph/ledger/store/mongo` | Teams already on Mongo, through Grove's `mongodriver` (no transactions needed) |

The three database stores take a `*grove.DB`. For PostgreSQL:

```go
pg := pgdriver.New()
if err := pg.Open(ctx, os.Getenv("DATABASE_URL")); err != nil {
	log.Fatal(err)
}

db, err := grove.Open(pg)
if err != nil {
	log.Fatal(err)
}

l := ledger.New(postgres.New(db))
if err := l.Start(ctx); err != nil { // runs the store's migrations first
	log.Fatal(err)
}
defer l.Stop()
```

`Start` migrates the schema before it starts the workers. If you migrate separately, pass `ledger.WithoutMigrate()`. Every store passes the same conformance suite in `store/storetest`, and you can run it against your own `store.Store` too.

## Plugins, audit and metrics

Register a plugin with `ledger.WithPlugin(p)`. A plugin implements `plugin.Plugin` (just `Name()`) plus any hook interfaces it cares about, such as `OnSubscriptionCreated`, `OnQuotaExceeded`, `OnInvoicePaid` or `OnInvoicePastDue`. The full list is in [`plugin/plugin.go`](plugin/plugin.go).

Some plugin interfaces change what the engine computes. `PricingStrategy`, `UsageAggregator`, `TaxCalculator` and `InvoiceFormatter` let you replace tier pricing, usage totals, tax and export formats, and `PaymentProviderPlugin` connects a payment provider through the [`provider.Provider`](provider/provider.go) interface for sync, import and webhooks. Ledger doesn't ship a provider implementation, so you write the one for Stripe, Paddle or whoever you use.

Two plugins come in the box:

| Package | What it does |
| --- | --- |
| `github.com/xraph/ledger/audit_hook` (package `audithook`) | Turns plan, subscription, invoice and quota events into audit records through a `Recorder` you pass in, such as a small adapter over [Chronicle](https://github.com/xraph/chronicle) |
| `github.com/xraph/ledger/observability` | Counters and histograms for the same events, through `NewMetricsExtension(factory)` |

You don't need that second plugin to see what the engine is doing. Ledger traces its own work on the global OpenTelemetry tracer: a lifecycle run that moved something, each meter flush, invoice generation, every payment provider call, and entitlement checks made inside a traced request. Idle clock ticks leave no trace. Pass `ledger.WithMetrics(m)` and it also records lifecycle runs and transitions, metered events (stored, lost to a failed flush, or refused by a full buffer), entitlement checks, invoice generation time, provider call time and failed plugin hooks. The Forge extension wires both for you, so in a Forge app they show up as `forge_ledger_...` on `/_/metrics`. The [observability guide](docs/content/docs/guides/observability.mdx) lists every span and metric.

## Use it with Forge

The `extension` package wraps the engine as a Forge extension:

```go
import (
	"github.com/xraph/forge"
	ledgerext "github.com/xraph/ledger/extension"
)

app := forge.NewApp(forge.AppConfig{
	Name: "billing",
	Extensions: []forge.Extension{
		ledgerext.New(ledgerext.WithMeterFlushInterval(2 * time.Second)),
	},
})
```

On `Register` the extension reads its config, picks a store, builds the engine and provides `*ledger.Ledger` to the container, so your services can resolve it with `forge.Inject[*ledger.Ledger](app.Container())`. `Start` and `Stop` start and stop the engine with the app, and the extension's health check pings the store.

The store is chosen in this order:

1. `ledgerext.WithStore(s)`, if you passed one.
2. A named `*grove.DB` from the container, if you set `WithGroveDatabase(name)` or `grove_database`.
3. The default `*grove.DB` in the container, if there is one. The driver (`pg`, `sqlite` or `mongo`) picks the store.
4. The in-memory store, as a last resort. Fine for a demo, but nothing survives a restart.

The extension also registers a contract contributor for the [Forge dashboard](https://github.com/xraph/forge-dashboard), with queries and commands for plans, features, subscriptions, usage, invoices and coupons. The React pages that call them live in forge-dashboard's `plugin-ledger` package.

## Configuration

The extension reads YAML under `extensions.ledger`, and falls back to a top-level `ledger` key:

```yaml
extensions:
  ledger:
    grove_database: ""          # named grove.DB in the container; empty uses the default one
    disable_migrate: false
    meter_batch_size: 100
    meter_flush_interval: 5s
    entitlement_cache_ttl: 30s
    lifecycle_interval: 1m
    disable_lifecycle: false
    app_id: ""                  # scopes the dashboard contributor to one app
    require_app_claim: false
```

YAML wins over programmatic options for strings and numbers, and a programmatic `true` wins for the boolean flags. These options set the same fields in code: `WithStore`, `WithGroveDatabase`, `WithDisableMigrate`, `WithMeterBatchSize`, `WithMeterFlushInterval`, `WithEntitlementCacheTTL`, `WithDisableLifecycle`, `WithAppID`, `WithConfig` and `WithRequireConfig`. Use `WithPlugin` to register a plugin and `WithLedgerOption` to pass any `ledger.Option` through to the engine.

Outside Forge, the engine takes `ledger.WithLogger`, `WithPlugin`, `WithMeterConfig(batchSize, flushInterval)`, `WithEntitlementCacheTTL`, `WithLifecycleInterval`, `WithClock` and `WithoutMigrate`.

## Documentation

| If you want to | Read |
| --- | --- |
| Get a first plan billing | [Getting started](https://www.xraph.com/docs/ledger/getting-started) |
| Understand quotas and usage | [Entitlements](https://www.xraph.com/docs/ledger/subsystems/entitlements), [metering](https://www.xraph.com/docs/ledger/subsystems/metering) |
| Bill a period | [Invoicing](https://www.xraph.com/docs/ledger/subsystems/invoicing), [billing cycles](https://www.xraph.com/docs/ledger/subsystems/billing-cycles), [coupons](https://www.xraph.com/docs/ledger/subsystems/coupons) |
| Scope data per tenant and app | [Multi-tenancy](https://www.xraph.com/docs/ledger/concepts/multi-tenancy) |
| Pick and run a store | [PostgreSQL](https://www.xraph.com/docs/ledger/stores/postgres), [SQLite](https://www.xraph.com/docs/ledger/stores/sqlite), [MongoDB](https://www.xraph.com/docs/ledger/stores/mongo), [memory](https://www.xraph.com/docs/ledger/stores/memory) |
| Extend the engine | [Custom plugin](https://www.xraph.com/docs/ledger/guides/custom-plugin), [custom store](https://www.xraph.com/docs/ledger/guides/custom-store) |
| Wire it into Forge | [Forge extension guide](https://www.xraph.com/docs/ledger/guides/forge-extension) |

The Go API is on [pkg.go.dev](https://pkg.go.dev/github.com/xraph/ledger). If you're moving off the old templ dashboard, [MIGRATION.md](MIGRATION.md) lists what changed.

## FAQ

### How do I check a usage quota in Go before serving a request?

Put the tenant and app in the context and call `l.Entitled(ctx, "feature_key")`. `Allowed` tells you whether to serve the request, and `Used`, `Limit` and `Remaining` tell you where the tenant stands. Then call `l.Meter(ctx, "feature_key", 1)` once you've done the work.

### Does Ledger charge cards?

No. Ledger computes what a tenant owes and tracks invoice state (`FinalizeInvoice`, `MarkInvoicePaid`, `MarkInvoiceVoided`). Collecting the money is a payment provider's job, and you connect one by writing a `provider.Provider` and registering it as a plugin.

### Can I use Ledger without Forge?

Yes. The root package, the stores and the plugins have no Forge dependency in their API. You need Forge only if you import the `extension` package.

### Which databases does Ledger support?

PostgreSQL, SQLite and MongoDB through [Grove](https://github.com/xraph/grove), and an in-memory store for tests. Anything else works if you implement `store.Store` and pass the conformance suite in `store/storetest`.

### Does Ledger prorate plan changes?

Not today. `ChangePlan` switches the plan from the next invoice and leaves the current period alone.

## Contributing

Issues and focused pull requests are welcome on [GitHub](https://github.com/xraph/ledger/issues). Run everything from the repo root with `GOWORK=off`:

```bash
GOWORK=off go build ./...
make test                      # go test -v ./...
go test -race -count=1 ./...   # what the release workflow runs
make check                     # fmt, vet and lint
```

The memory and SQLite stores run the conformance suite on every `go test`. PostgreSQL and MongoDB skip unless you set `LEDGER_TEST_POSTGRES_DSN` and `LEDGER_TEST_MONGO_URI`, so if you touch `store/`, point them at throwaway containers and run `go test -v ./store/...`. [AGENTS.md](AGENTS.md) has the container commands, the lint rules that catch people, and how releases go out. Skip `make build`, `make run` and `make all`: they point at a `cmd/ledger` that doesn't exist. Commits follow [Conventional Commits](https://www.conventionalcommits.org/).

## License

See the [Ledger repository](https://github.com/xraph/ledger) for license terms.

Ledger is part of the [forgery stack](https://github.com/xraph/forge#the-forgery-stack) built on [Forge](https://github.com/xraph/forge), by [Rex Raphael](https://github.com/juicycleff) and [contributors](https://github.com/xraph/ledger/graphs/contributors).
