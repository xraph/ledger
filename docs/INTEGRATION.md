# Ledger Integration Guide

This guide covers how to integrate Ledger into your application ecosystem, including Forge, Chronicle, Authsome, and payment providers.

## Table of Contents

1. [Forge Integration](#forge-integration)
2. [Chronicle Audit Trail](#chronicle-audit-trail)
3. [Authsome Integration](#authsome-integration)
4. [Payment Provider Integration](#payment-provider-integration)
5. [Database Setup](#database-setup)
6. [Production Deployment](#production-deployment)
7. [Monitoring & Observability](#monitoring--observability)

## Forge Integration

Ledger is designed to work seamlessly with Forge's application framework.

### Tenant Isolation

Ledger automatically extracts tenant and app IDs from Forge scope:

```go
import (
    "github.com/xraph/forge"
    "github.com/xraph/ledger"
)

// Forge middleware sets the scope
func BillingMiddleware(l *ledger.Ledger) forge.Handler {
    return func(ctx context.Context, req *forge.Request) (*forge.Response, error) {
        // Forge automatically sets tenant scope
        // Example: "tenant:123:app:456"

        // Ledger extracts from context
        result, err := l.Entitled(ctx, "api_calls")
        if err != nil {
            return nil, err
        }

        if !result.Allowed {
            return forge.Error(402, "Payment Required"), nil
        }

        // Continue processing...
        return next(ctx, req)
    }
}
```

### Custom Context Extractor

If you use a different context structure:

```go
func extractTenantID(ctx context.Context) string {
    // Custom extraction from Forge scope
    scope := forge.GetScope(ctx)
    return scope.TenantID()
}

func extractAppID(ctx context.Context) string {
    scope := forge.GetScope(ctx)
    return scope.AppID()
}

// Override default extractors
ledger.WithContextExtractors(extractTenantID, extractAppID)
```

## Chronicle Audit Trail

Integrate Ledger with Chronicle for comprehensive audit logging.

### Basic Setup

```go
import (
    "github.com/xraph/ledger/audit_hook"
    "github.com/xraph/chronicle"
)

// Create Chronicle instance
chr := chronicle.New(
    chronicle.WithStore(auditStore),
    chronicle.WithRetention(90 * 24 * time.Hour),
)

// Create recorder adapter
recorder := audit_hook.RecorderFunc(func(ctx context.Context, event *audit_hook.AuditEvent) error {
    // Transform to Chronicle event
    chronicleEvent := &chronicle.Event{
        Action:     event.Action,
        Resource:   event.Resource,
        ResourceID: event.ResourceID,
        Category:   event.Category,
        Outcome:    event.Outcome,
        Severity:   event.Severity,
        Metadata:   event.Metadata,
        Timestamp:  time.Now(),

        // Add context
        TenantID:   extractTenantID(ctx),
        UserID:     extractUserID(ctx),
        RequestID:  extractRequestID(ctx),
    }

    return chr.Emit(ctx, chronicleEvent)
})

// Register audit extension
auditExt := audit_hook.New(recorder,
    audit_hook.WithLogger(logger),
    audit_hook.WithEnabledActions(
        audit_hook.ActionSubscriptionCreated,
        audit_hook.ActionSubscriptionCanceled,
        audit_hook.ActionInvoicePaid,
        audit_hook.ActionInvoiceFailed,
        audit_hook.ActionQuotaExceeded,
    ),
)

// Add to Ledger
l := ledger.New(store,
    ledger.WithPlugin(auditExt),
)
```

### Selective Auditing

Control which events to audit:

```go
// Only audit critical events
auditExt := audit_hook.New(recorder,
    audit_hook.WithDisabledActions(
        audit_hook.ActionEntitlementChecked, // Too noisy
        audit_hook.ActionUsageIngested,      // High volume
    ),
)

// Or explicitly enable specific events
auditExt := audit_hook.New(recorder,
    audit_hook.WithEnabledActions(
        audit_hook.ActionSubscriptionCreated,
        audit_hook.ActionSubscriptionCanceled,
        audit_hook.ActionInvoicePaid,
    ),
)
```

## Authsome Integration

Ledger serves as the billing module for Authsome authentication.

### Billing Module Setup

```go
import (
    "github.com/xraph/authsome"
    "github.com/xraph/ledger"
)

// Create Authsome billing adapter
type LedgerBillingModule struct {
    ledger *ledger.Ledger
}

func (m *LedgerBillingModule) CheckFeature(ctx context.Context, feature string) (bool, error) {
    result, err := m.ledger.Entitled(ctx, feature)
    if err != nil {
        return false, err
    }
    return result.Allowed, nil
}

func (m *LedgerBillingModule) GetSubscription(ctx context.Context, tenantID string) (*authsome.Subscription, error) {
    sub, err := m.ledger.GetActiveSubscription(ctx, tenantID, "")
    if err != nil {
        return nil, err
    }

    // Transform to Authsome subscription
    return &authsome.Subscription{
        ID:       sub.ID.String(),
        TenantID: sub.TenantID,
        PlanID:   sub.PlanID.String(),
        Status:   string(sub.Status),
        Features: m.extractFeatures(sub),
    }, nil
}

// Register with Authsome
auth := authsome.New(
    authsome.WithBilling(&LedgerBillingModule{
        ledger: l,
    }),
)
```

### Feature Gates

Implement feature gates in your API:

```go
func RequireFeature(feature string) authsome.Middleware {
    return func(next authsome.Handler) authsome.Handler {
        return func(ctx context.Context, req *authsome.Request) (*authsome.Response, error) {
            // Authsome checks via Ledger billing module
            if !auth.Can(ctx, feature) {
                return authsome.Forbidden("Feature not available in your plan"), nil
            }
            return next(ctx, req)
        }
    }
}

// Usage
router.Post("/api/generate-report",
    RequireFeature("advanced_reports"),
    generateReportHandler,
)
```

## Payment Provider Integration

### Stripe Integration

```go
import (
    "github.com/stripe/stripe-go/v74"
    "github.com/xraph/ledger/providers/stripe"
)

// Initialize Stripe
stripe.Key = os.Getenv("STRIPE_SECRET_KEY")

// Create Stripe provider plugin
stripeProvider := stripe.NewProvider(
    stripe.WithWebhookSecret(os.Getenv("STRIPE_WEBHOOK_SECRET")),
    stripe.WithSyncInterval(5 * time.Minute),
)

// Register with Ledger
l := ledger.New(store,
    ledger.WithPlugin(stripeProvider),
)

// Handle webhooks
http.HandleFunc("/webhooks/stripe", func(w http.ResponseWriter, r *http.Request) {
    payload, err := ioutil.ReadAll(r.Body)
    if err != nil {
        http.Error(w, err.Error(), 400)
        return
    }

    // Provider handles webhook
    if err := stripeProvider.HandleWebhook(r.Context(), payload); err != nil {
        http.Error(w, err.Error(), 400)
        return
    }

    w.WriteHeader(200)
})
```

### Custom Provider

Implement your own payment provider:

```go
type CustomProvider struct {
    client *customsdk.Client
}

func (p *CustomProvider) Name() string { return "custom-provider" }

func (p *CustomProvider) ProviderName() string { return "custom" }

func (p *CustomProvider) CreateCustomer(ctx context.Context, tenant interface{}) (string, error) {
    // Create customer in provider
    customer, err := p.client.CreateCustomer(&customsdk.Customer{
        Email: tenant.(*Tenant).Email,
        Name:  tenant.(*Tenant).Name,
    })
    if err != nil {
        return "", err
    }
    return customer.ID, nil
}

func (p *CustomProvider) CreateSubscription(ctx context.Context, sub interface{}) (string, error) {
    // Create subscription in provider
    s := sub.(*subscription.Subscription)
    providerSub, err := p.client.CreateSubscription(&customsdk.Subscription{
        CustomerID: s.ProviderCustomerID,
        PlanID:     s.PlanID.String(),
    })
    if err != nil {
        return "", err
    }
    return providerSub.ID, nil
}

func (p *CustomProvider) ChargeInvoice(ctx context.Context, inv interface{}) error {
    // Charge invoice through provider
    i := inv.(*invoice.Invoice)
    _, err := p.client.CreateCharge(&customsdk.Charge{
        CustomerID: i.ProviderCustomerID,
        Amount:     i.Total.Amount,
        Currency:   i.Total.Currency,
    })
    return err
}

func (p *CustomProvider) HandleWebhook(ctx context.Context, payload []byte) error {
    // Parse and process webhook
    event, err := p.client.ParseWebhook(payload)
    if err != nil {
        return err
    }

    switch event.Type {
    case "subscription.updated":
        // Update subscription in Ledger
    case "invoice.paid":
        // Mark invoice as paid in Ledger
    }

    return nil
}
```

## Database Setup

### PostgreSQL Schema

```sql
-- Create schema
CREATE SCHEMA IF NOT EXISTS billing;

-- Enable extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pg_trgm";

-- Set search path
SET search_path TO billing;

-- Plans table
CREATE TABLE plans (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    slug VARCHAR(100) NOT NULL,
    description TEXT,
    currency VARCHAR(3) NOT NULL,
    status VARCHAR(20) NOT NULL,
    trial_days INTEGER DEFAULT 0,
    features JSONB,
    pricing JSONB,
    app_id VARCHAR(100) NOT NULL,
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(slug, app_id)
);

-- Subscriptions table
CREATE TABLE subscriptions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id VARCHAR(100) NOT NULL,
    plan_id VARCHAR(36) NOT NULL REFERENCES plans(id),
    status VARCHAR(20) NOT NULL,
    current_period_start TIMESTAMPTZ NOT NULL,
    current_period_end TIMESTAMPTZ NOT NULL,
    trial_start TIMESTAMPTZ,
    trial_end TIMESTAMPTZ,
    canceled_at TIMESTAMPTZ,
    cancel_at TIMESTAMPTZ,
    ended_at TIMESTAMPTZ,
    app_id VARCHAR(100) NOT NULL,
    provider_id VARCHAR(255),
    provider_name VARCHAR(50),
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    INDEX idx_tenant_app (tenant_id, app_id),
    INDEX idx_status (status)
);

-- Usage events table (partitioned by month)
CREATE TABLE usage_events (
    id VARCHAR(36) NOT NULL,
    tenant_id VARCHAR(100) NOT NULL,
    app_id VARCHAR(100) NOT NULL,
    feature_key VARCHAR(100) NOT NULL,
    quantity BIGINT NOT NULL,
    timestamp TIMESTAMPTZ NOT NULL,
    idempotency_key VARCHAR(255),
    metadata JSONB,

    PRIMARY KEY (id, timestamp)
) PARTITION BY RANGE (timestamp);

-- Create monthly partitions
CREATE TABLE usage_events_2024_01 PARTITION OF usage_events
    FOR VALUES FROM ('2024-01-01') TO ('2024-02-01');
-- ... create more partitions as needed

-- Invoices table
CREATE TABLE invoices (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id VARCHAR(100) NOT NULL,
    subscription_id VARCHAR(36) NOT NULL REFERENCES subscriptions(id),
    status VARCHAR(20) NOT NULL,
    currency VARCHAR(3) NOT NULL,
    subtotal BIGINT NOT NULL,
    tax_amount BIGINT NOT NULL DEFAULT 0,
    discount_amount BIGINT NOT NULL DEFAULT 0,
    total BIGINT NOT NULL,
    line_items JSONB NOT NULL,
    period_start TIMESTAMPTZ NOT NULL,
    period_end TIMESTAMPTZ NOT NULL,
    due_date TIMESTAMPTZ,
    paid_at TIMESTAMPTZ,
    voided_at TIMESTAMPTZ,
    void_reason TEXT,
    payment_ref VARCHAR(255),
    provider_id VARCHAR(255),
    app_id VARCHAR(100) NOT NULL,
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    INDEX idx_tenant_status (tenant_id, status),
    INDEX idx_period (period_start, period_end)
);

-- Coupons table
CREATE TABLE coupons (
    id VARCHAR(36) PRIMARY KEY,
    code VARCHAR(50) NOT NULL,
    name VARCHAR(255) NOT NULL,
    type VARCHAR(20) NOT NULL,
    amount BIGINT,
    percentage INTEGER,
    currency VARCHAR(3),
    max_redemptions INTEGER,
    times_redeemed INTEGER DEFAULT 0,
    valid_from TIMESTAMPTZ,
    valid_until TIMESTAMPTZ,
    app_id VARCHAR(100) NOT NULL,
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(code, app_id)
);

-- Row Level Security
ALTER TABLE plans ENABLE ROW LEVEL SECURITY;
ALTER TABLE subscriptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE usage_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoices ENABLE ROW LEVEL SECURITY;
ALTER TABLE coupons ENABLE ROW LEVEL SECURITY;

-- Create policies
CREATE POLICY tenant_isolation ON subscriptions
    FOR ALL TO billing_user
    USING (app_id = current_setting('app.current_app_id'));

-- Indexes for performance
CREATE INDEX idx_usage_aggregate ON usage_events (tenant_id, app_id, feature_key, timestamp);
CREATE INDEX idx_subscription_active ON subscriptions (tenant_id, app_id, status)
    WHERE status IN ('active', 'trialing');
```

### Redis Cache Setup

```go
import (
    "github.com/redis/go-redis/v9"
    "github.com/xraph/ledger/store/redis"
)

// Create Redis client
rdb := redis.NewClient(&redis.Options{
    Addr:     "localhost:6379",
    Password: "",
    DB:       0,

    // Connection pool
    PoolSize:     100,
    MinIdleConns: 10,
})

// Create Redis store for caching
cacheStore := redis.NewStore(rdb,
    redis.WithPrefix("ledger:"),
    redis.WithTTL(30 * time.Second),
)

// Use as cache layer
import "github.com/xraph/ledger/store/multi"

store := multi.NewStore(
    multi.WithCache(cacheStore),
    multi.WithPrimary(postgresStore),
)
```

## Production Deployment

### Configuration

```yaml
# config/production.yaml
ledger:
  database:
    url: ${DATABASE_URL}
    max_connections: 25
    max_idle: 5
    max_lifetime: 5m

  redis:
    url: ${REDIS_URL}
    pool_size: 100

  metering:
    batch_size: 100
    flush_interval: 5s
    buffer_size: 10000

  entitlements:
    cache_ttl: 30s

  plugins:
    - name: audit_hook
      enabled: true
      actions:
        - subscription.created
        - subscription.canceled
        - invoice.paid
        - quota.exceeded

    - name: metrics
      enabled: true
      prefix: billing

    - name: stripe
      enabled: true
      webhook_secret: ${STRIPE_WEBHOOK_SECRET}
```

### Health Checks

```go
// Health check endpoint
http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
    ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
    defer cancel()

    // Check store connectivity
    if err := store.Ping(ctx); err != nil {
        w.WriteHeader(503)
        json.NewEncoder(w).Encode(map[string]string{
            "status": "unhealthy",
            "error":  err.Error(),
        })
        return
    }

    w.WriteHeader(200)
    json.NewEncoder(w).Encode(map[string]string{
        "status": "healthy",
    })
})
```

### Graceful Shutdown

```go
func main() {
    // Initialize Ledger
    l := ledger.New(store, opts...)

    // Start Ledger
    if err := l.Start(context.Background()); err != nil {
        log.Fatal(err)
    }

    // Setup signal handling
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

    // Wait for shutdown signal
    <-sigChan

    // Graceful shutdown
    log.Println("Shutting down...")

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    // Stop Ledger (flushes pending usage events)
    if err := l.Stop(); err != nil {
        log.Printf("Error during shutdown: %v", err)
    }

    log.Println("Shutdown complete")
}
```

## Monitoring & Observability

### Metrics with Prometheus

```go
import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/xraph/ledger/observability"
)

// Create Prometheus metrics
var (
    subscriptionsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "billing_subscriptions_total",
            Help: "Total number of subscriptions created",
        },
        []string{"plan", "status"},
    )

    entitlementChecks = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "billing_entitlement_checks_total",
            Help: "Total entitlement checks",
        },
        []string{"feature", "result"},
    )

    usageEvents = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "billing_usage_events_batch_size",
            Help:    "Size of usage event batches",
            Buckets: []float64{1, 10, 50, 100, 500, 1000},
        },
        []string{"feature"},
    )
)

// Register metrics
prometheus.MustRegister(subscriptionsTotal, entitlementChecks, usageEvents)

// Create metrics adapter
type PrometheusMetrics struct{}

func (m *PrometheusMetrics) Name() string { return "prometheus-metrics" }

func (m *PrometheusMetrics) OnSubscriptionCreated(ctx context.Context, sub interface{}) error {
    s := sub.(*subscription.Subscription)
    subscriptionsTotal.WithLabelValues(s.PlanID.String(), string(s.Status)).Inc()
    return nil
}

func (m *PrometheusMetrics) OnEntitlementChecked(ctx context.Context, result interface{}) error {
    r := result.(*entitlement.Result)
    outcome := "denied"
    if r.Allowed {
        outcome = "allowed"
    }
    entitlementChecks.WithLabelValues(r.Feature, outcome).Inc()
    return nil
}

func (m *PrometheusMetrics) OnUsageFlushed(ctx context.Context, count int, elapsed time.Duration) error {
    usageEvents.WithLabelValues("batch").Observe(float64(count))
    return nil
}

// Register with Ledger
l := ledger.New(store,
    ledger.WithPlugin(&PrometheusMetrics{}),
)
```

### Logging

```go
import (
    "log/slog"
    "github.com/xraph/ledger"
)

// Configure structured logging
logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelInfo,
}))

// Add context fields
logger = logger.With(
    "service", "billing",
    "version", "1.0.0",
)

// Use with Ledger
l := ledger.New(store,
    ledger.WithLogger(logger),
)
```

### Distributed Tracing

```go
import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/trace"
)

// Create tracer
tracer := otel.Tracer("billing")

// Wrap Ledger calls with spans
func CheckEntitlement(ctx context.Context, feature string) (*entitlement.Result, error) {
    ctx, span := tracer.Start(ctx, "billing.check_entitlement")
    defer span.End()

    span.SetAttributes(
        attribute.String("feature", feature),
        attribute.String("tenant_id", extractTenantID(ctx)),
    )

    result, err := l.Entitled(ctx, feature)
    if err != nil {
        span.RecordError(err)
        return nil, err
    }

    span.SetAttributes(
        attribute.Bool("allowed", result.Allowed),
        attribute.Int64("limit", result.Limit),
        attribute.Int64("used", result.Used),
    )

    return result, nil
}
```

## Best Practices

### 1. Always Use Context

```go
// Good: Context with tenant/app info
ctx := context.WithValue(ctx, "tenant_id", tenantID)
ctx = context.WithValue(ctx, "app_id", appID)
result, err := l.Entitled(ctx, "feature")

// Bad: Empty context
result, err := l.Entitled(context.Background(), "feature")
```

### 2. Handle Soft Limits

```go
result, err := l.Entitled(ctx, "storage_gb")
if err != nil {
    return err
}

if result.Allowed {
    if result.SoftLimit && result.Used > result.Limit {
        // Notify user they're over limit
        notifyOverLimit(result.Feature, result.Used, result.Limit)
    }
    // Continue operation
} else {
    // Hard limit reached
    return ErrQuotaExceeded
}
```

### 3. Batch Usage Events

```go
// Good: Batch multiple events
events := make([]UsageEvent, 0, 100)
for _, item := range items {
    events = append(events, UsageEvent{
        Feature:  "api_calls",
        Quantity: 1,
    })
}
l.MeterBatch(ctx, events)

// Bad: Individual calls in loop
for _, item := range items {
    l.Meter(ctx, "api_calls", 1) // Inefficient
}
```

### 4. Use Idempotency Keys

```go
// Prevent duplicate usage events
event := &meter.UsageEvent{
    FeatureKey:     "api_calls",
    Quantity:       1,
    IdempotencyKey: fmt.Sprintf("%s:%s:%d", requestID, feature, timestamp),
}
```

### 5. Monitor Cache Hit Rates

```go
// Track cache performance
var (
    cacheHits   = prometheus.NewCounter(...)
    cacheMisses = prometheus.NewCounter(...)
)

// In your entitlement checks
result, err := l.Entitled(ctx, feature)
if err == ledger.ErrCacheMiss {
    cacheMisses.Inc()
} else {
    cacheHits.Inc()
}
```

## Troubleshooting

### Common Issues

1. **Import cycles**: Ensure types are in the `types` package
2. **Currency mismatches**: Always use same currency in operations
3. **Cache misses**: Check TTL configuration
4. **Webhook failures**: Verify signature and payload format
5. **Performance issues**: Check database indexes and cache configuration

### Debug Mode

```go
// Enable debug logging
logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelDebug,
}))

l := ledger.New(store,
    ledger.WithLogger(logger),
    ledger.WithDebugMode(true),
)
```