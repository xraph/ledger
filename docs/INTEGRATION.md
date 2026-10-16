# Ledger Integration Guide

How to wire Ledger into a Forge app, an audit trail, a payment provider, your database and your monitoring. Every snippet here uses the API as it exists in this repo. [API.md](API.md) has the full reference.

## Contents

1. [Forge](#forge)
2. [Tenant and app context](#tenant-and-app-context)
3. [Audit trail](#audit-trail)
4. [Payment providers](#payment-providers)
5. [Database setup](#database-setup)
6. [Production](#production)
7. [Metrics](#metrics)
8. [Things that catch people](#things-that-catch-people)

## Forge

The `extension` package registers the engine with a Forge app:

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

On `Register` the extension reads its config, picks a store, builds the engine and provides `*ledger.Ledger` to the container. Resolve it with `forge.Inject[*ledger.Ledger](app.Container())`. `Start` and `Stop` follow the app, and the health check pings the store.

The store comes from, in order: `ledgerext.WithStore(s)`, a named `*grove.DB` from `WithGroveDatabase(name)` or `grove_database`, the default `*grove.DB` in the container (its driver picks the Postgres, SQLite or Mongo store), and finally the in-memory store, which loses everything on restart.

The config has `base_path` and `disable_routes` keys (and `WithBasePath`, `WithDisableRoutes`), but the extension registers no HTTP routes today, so they do nothing. You write the handlers yourself, as below. The extension does register a contract contributor for the Forge dashboard.

## Tenant and app context

`Entitled`, `Remaining` and `Meter` read the tenant and app from the context values `"tenant_id"` and `"app_id"`, as plain string keys. They don't read Forge's scope, and there is no option to plug in your own extractor. If either value is missing, `Meter` returns `ErrInvalidInput` and `Entitled` answers not allowed with the reason `"missing tenant or app context"`.

So put them on the context in your handler (or a middleware) before you call Ledger:

```go
func generateReport(l *ledger.Ledger) forge.Handler {
    return func(c forge.Context) error {
        // Take the tenant from your auth layer. A header keeps the example short.
        ctx := context.WithValue(c.Context(), "tenant_id", c.Header("X-Tenant-ID"))
        ctx = context.WithValue(ctx, "app_id", "my-app")

        res, err := l.Entitled(ctx, "reports")
        if err != nil {
            return err
        }
        if !res.Allowed {
            return c.JSON(http.StatusPaymentRequired, map[string]string{"error": res.Reason})
        }

        // ... build the report ...

        if err := l.Meter(ctx, "reports", 1); err != nil {
            return err
        }

        return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
    }
}
```

If you need to check a tenant other than the one on the context, `l.InspectEntitlement(ctx, tenantID, appID, featureKey)` takes them as arguments.

## Audit trail

`github.com/xraph/ledger/audit_hook` (package `audithook`) is a plugin that turns billing events into `AuditEvent` values and hands each one to a `Recorder`. It defines its own `Recorder` interface so it doesn't import Chronicle. Write a small adapter over Chronicle, or use `RecorderFunc`:

```go
import audithook "github.com/xraph/ledger/audit_hook"

recorder := audithook.RecorderFunc(func(ctx context.Context, ev *audithook.AuditEvent) error {
    // Forward to Chronicle, a log, a queue, whatever you use.
    log.Printf("%s %s/%s outcome=%s", ev.Action, ev.Resource, ev.ResourceID, ev.Outcome)
    return nil
})

auditExt := audithook.New(recorder,
    audithook.WithEnabledActions(
        audithook.ActionSubscriptionCreated,
        audithook.ActionSubscriptionCanceled,
        audithook.ActionInvoicePaid,
        audithook.ActionQuotaExceeded,
    ),
)

l := ledger.New(st, ledger.WithPlugin(auditExt))
```

Without `WithEnabledActions` every action is recorded. `WithDisabledActions(...)` starts from all of them and removes the ones you list. The action names are constants in the package (`plan.created`, `subscription.canceled`, `invoice.past_due`, `quota.exceeded` and so on).

Some constants have no event behind them yet. The hook never records `entitlement.checked` or `entitlement.denied`, and a plan change is always recorded as `subscription.upgraded`, because the hook doesn't compare the two plans. `subscription.expired`, `usage.ingested`, `soft_limit.reached` and `invoice.failed` never fire either, since the engine doesn't emit those hooks.

## Payment providers

Ledger doesn't charge cards, and it ships no provider. To connect Stripe, Paddle or anything else, you implement `provider.Provider` (sync, import, payment methods and webhooks) and register it through a plugin that implements `plugin.PaymentProviderPlugin`:

```go
type stripePlugin struct{ p provider.Provider }

func (s *stripePlugin) Name() string                { return "stripe" }
func (s *stripePlugin) Provider() provider.Provider { return s.p }

l := ledger.New(st, ledger.WithPlugin(&stripePlugin{p: myStripeProvider}))
```

Then the engine's `SyncPlanToProvider`, `SyncSubscriptionToProvider`, `SyncInvoiceToProvider`, the `Import...FromProvider` methods and `ListPaymentMethods` work. Ledger doesn't serve a webhook endpoint, so mount your own and pass the body through:

```go
http.HandleFunc("/webhooks/stripe", func(w http.ResponseWriter, r *http.Request) {
    payload, err := io.ReadAll(r.Body)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    if _, err := l.HandleWebhook(r.Context(), "stripe", payload); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    w.WriteHeader(http.StatusOK)
})
```

The provider name you pass is the one your `Provider.Name()` returns. Verifying the webhook signature is your provider's job.

## Database setup

You don't write the schema by hand. The Postgres, SQLite and Mongo stores are built on [Grove](https://github.com/xraph/grove) and carry their own migrations, which `l.Start` runs before it starts the workers:

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
if err := l.Start(ctx); err != nil { // migrates, then starts the meter and lifecycle workers
    log.Fatal(err)
}
defer l.Stop()
```

If you run migrations separately, pass `ledger.WithoutMigrate()` (or `disable_migrate: true` in the Forge extension) and call `Migrate(ctx)` on the store yourself. There is no Redis or caching store. Entitlement results are cached through the store you already have.

## Production

The Forge extension reads `extensions.ledger`, and falls back to a top-level `ledger` key:

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

Plugins aren't configured in YAML. Register them in code with `ledgerext.WithPlugin(p)`.

Outside Forge, `l.Health(ctx)` pings the store, so a health endpoint is a one-liner around it. For shutdown, call `l.Stop()` once your server has stopped taking requests. It flushes the usage still in the meter buffer, so anything metered before that point is stored:

```go
sig := make(chan os.Signal, 1)
signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
<-sig

if err := l.Stop(); err != nil {
    log.Printf("ledger stop: %v", err)
}
```

The lifecycle clock (period rollover, scheduled cancels, trial ends, past-due invoices) runs every minute in every replica. Its writes are conditional, so that's safe. If you'd rather drive it from your own scheduler, set `ledger.WithLifecycleInterval(0)` and call `l.Advance(ctx, time.Now())`.

## Metrics

The engine records its own metrics and spans. Through the Forge extension they're wired for you; on its own, pass `ledger.WithMetrics(m)` with a go-utils `metrics.Metrics` and install an OpenTelemetry tracer provider. The [Observability guide](content/docs/guides/observability.mdx) lists every span and metric, with the values each label takes.

`observability.NewMetricsExtension(factory)` is an older plugin that counts plan, subscription, usage, quota and invoice events through a `MetricFactory` (`Counter(name)` and `Histogram(name)`). Forge's `app.Metrics()` doesn't satisfy that interface directly (its `Counter` takes variadic options), even though the package comment suggests it, so wrap it:

```go
// forgeMetrics adapts Forge's metrics to the factory the plugin expects.
type forgeMetrics struct{ m forge.Metrics }

func (f forgeMetrics) Counter(name string) observability.Counter     { return f.m.Counter(name) }
func (f forgeMetrics) Histogram(name string) observability.Histogram { return f.m.Histogram(name) }

metricsPlugin := observability.NewMetricsExtension(forgeMetrics{app.Metrics()})
```

For anything else, write a plugin with the hooks you care about. The hook arguments are `interface{}`, so assert them:

```go
type quotaAlerts struct{}

func (quotaAlerts) Name() string { return "quota-alerts" }

func (quotaAlerts) OnQuotaExceeded(ctx context.Context, tenantID, featureKey string, used, limit int64) error {
    log.Printf("tenant %s hit %s: %d of %d", tenantID, featureKey, used, limit)
    return nil
}

func (quotaAlerts) OnSubscriptionCreated(ctx context.Context, sub interface{}) error {
    s := sub.(*subscription.Subscription)
    log.Printf("tenant %s subscribed to %s", s.TenantID, s.PlanID)
    return nil
}
```

`ledger.WithLogger` takes a `github.com/xraph/go-utils/log` logger, not a `*slog.Logger`.

## Things that catch people

Usage is buffered. `Meter` returns before anything is written, and the event only counts toward `Entitled` and invoices after the next flush (100 events or 5 seconds by default). A test that meters and then checks the quota straight away sees the old number. When the 10,000-event buffer is full, `Meter` returns `ErrMeterBufferFull` instead of blocking.

There's no batch metering call and no idempotency key on `Meter`. If you need de-duplication, write events through the store's `IngestBatch` with `IdempotencyKey` set.

A soft limit still allows the request. Check the reason:

```go
res, err := l.Entitled(ctx, "storage_gb")
if err != nil {
    return err
}
switch {
case !res.Allowed:
    return ledger.ErrQuotaExceeded
case res.Reason == "over soft limit":
    notifyOverLimit(res.Feature, res.Used, res.Limit)
}
```

`ChangePlan` takes effect from the next invoice. Nothing is prorated.
