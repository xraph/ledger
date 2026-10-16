# Ledger API Reference

This is a map of Ledger's Go API as it stands in the code. For anything not listed here, `go doc github.com/xraph/ledger/...` or [pkg.go.dev](https://pkg.go.dev/github.com/xraph/ledger) is the source of truth.

## Core types

### IDs

Every entity gets a [TypeID](https://github.com/jetify-com/typeid). There is one `id.ID` struct, and the named types are aliases of it, so the prefix is what tells them apart.

```go
import "github.com/xraph/ledger/id"

type ID struct{ /* ... */ }

type PlanID = ID                // prefix "plan"
type FeatureID = ID             // prefix "feat"
type PriceID = ID               // prefix "price"
type SubscriptionID = ID        // prefix "sub"
type UsageEventID = ID          // prefix "uevt"
type EntitlementID = ID         // prefix "ent"
type InvoiceID = ID             // prefix "inv"
type LineItemID = ID            // prefix "li"
type CouponID = ID              // prefix "cpn"
type PaymentID = ID             // prefix "pay"
type CouponApplicationID = ID   // prefix "cpna"

func NewPlanID() ID             // and NewFeatureID, NewSubscriptionID, NewInvoiceID, ...
func ParsePlanID(s string) (ID, error) // and one Parse function per prefix
func Parse(s string) (ID, error)
func ParseAny(s string) (ID, error)
func ParseWithPrefix(s string, expected Prefix) (ID, error)
func MustParse(s string) ID
```

### Money

Integer arithmetic in the currency's smallest unit.

```go
import "github.com/xraph/ledger/types"

type Money struct {
    Amount   int64  `json:"amount"`   // cents, pence, etc.
    Currency string `json:"currency"` // ISO 4217, lowercase
}

func USD(cents int64) Money
func EUR(cents int64) Money
func GBP(pence int64) Money
func JPY(yen int64) Money
func CAD(cents int64) Money
func AUD(cents int64) Money
func Zero(currency string) Money
func Sum(values ...Money) Money

// Arithmetic. These panic when the currencies differ.
func (m Money) Add(other Money) Money
func (m Money) Subtract(other Money) Money
func (m Money) Multiply(qty int64) Money
func (m Money) Divide(divisor int64) Money   // integer division
func (m Money) Percent(pct int) Money        // truncates toward zero
func (m Money) Negate() Money
func (m Money) Abs() Money

// Overflow-checked versions return types.ErrOverflow instead of wrapping.
func (m Money) CheckedAdd(other Money) (Money, error)
func (m Money) CheckedSubtract(other Money) (Money, error)
func (m Money) CheckedMultiply(qty int64) (Money, error)
func (m Money) CheckedPercent(pct int) (Money, error)

func (m Money) IsZero() bool
func (m Money) IsPositive() bool
func (m Money) IsNegative() bool
func (m Money) Equal(other Money) bool
func (m Money) LessThan(other Money) bool
func (m Money) GreaterThan(other Money) bool
func (m Money) Min(other Money) Money
func (m Money) Max(other Money) Money

func (m Money) String() string      // "$49.00"
func (m Money) FormatMajor() string // "49.00"
```

### Entity

Every model embeds `types.Entity` for its timestamps.

```go
type Entity struct {
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}

func NewEntity() Entity
func (e *Entity) Touch()
func (e Entity) Age() time.Duration
func (e Entity) LastModified() time.Duration
func (e Entity) IsNew() bool
func (e Entity) IsStale(staleDuration time.Duration) bool
```

## The engine

```go
import "github.com/xraph/ledger"

func New(s store.Store, opts ...Option) *Ledger

func WithLogger(logger log.Logger) Option // github.com/xraph/go-utils/log
func WithPlugin(p plugin.Plugin) Option
func WithMeterConfig(batchSize int, flushInterval time.Duration) Option // default 100, 5s
func WithEntitlementCacheTTL(ttl time.Duration) Option                  // default 30s
func WithLifecycleInterval(d time.Duration) Option                      // default 1m, 0 turns the clock off
func WithClock(now func() time.Time) Option
func WithoutMigrate() Option

func (l *Ledger) Start(ctx context.Context) error // migrates the store unless WithoutMigrate, then starts the workers
func (l *Ledger) Stop() error                     // flushes buffered usage and stops the workers
func (l *Ledger) Health(ctx context.Context) error
func (l *Ledger) Store() store.Store
```

### Plans and the feature catalog

```go
func (l *Ledger) CreatePlan(ctx context.Context, p *plan.Plan) error
func (l *Ledger) UpdatePlan(ctx context.Context, p *plan.Plan) error
func (l *Ledger) ActivatePlan(ctx context.Context, planID id.PlanID) error
func (l *Ledger) ArchivePlan(ctx context.Context, planID id.PlanID) error
func (l *Ledger) DeletePlan(ctx context.Context, planID id.PlanID) error
func (l *Ledger) GetPlan(ctx context.Context, planID id.PlanID) (*plan.Plan, error)
func (l *Ledger) GetPlanBySlug(ctx context.Context, slug, appID string) (*plan.Plan, error)

func (l *Ledger) CreateFeature(ctx context.Context, f *feature.Feature) error
func (l *Ledger) UpdateFeature(ctx context.Context, f *feature.Feature) error
func (l *Ledger) ArchiveFeature(ctx context.Context, featureID id.FeatureID) error
func (l *Ledger) DeleteFeature(ctx context.Context, featureID id.FeatureID) error
func (l *Ledger) GetFeature(ctx context.Context, featureID id.FeatureID) (*feature.Feature, error)
func (l *Ledger) GetFeatureByKey(ctx context.Context, key, appID string) (*feature.Feature, error)
func (l *Ledger) ListFeatures(ctx context.Context, appID string, opts feature.ListOpts) ([]*feature.Feature, error)
func (l *Ledger) ListGlobalFeatures(ctx context.Context, opts feature.ListOpts) ([]*feature.Feature, error)
```

`CreatePlan` checks the price tiers when you save the plan, so a broken ladder fails there with `invoice.ErrInvalidTiers`.

The plan model, from `github.com/xraph/ledger/plan`:

```go
type Plan struct {
    types.Entity
    ID           id.PlanID         `json:"id"`
    Name         string            `json:"name"`
    Slug         string            `json:"slug"`
    Description  string            `json:"description"`
    Currency     string            `json:"currency"`
    Status       Status            `json:"status"` // "draft", "active", "archived"
    TrialDays    int               `json:"trial_days"`
    Features     []Feature         `json:"features"`
    Pricing      *Pricing          `json:"pricing,omitempty"`
    AppID        string            `json:"app_id"`
    ProviderID   string            `json:"provider_id,omitempty"`
    ProviderName string            `json:"provider_name,omitempty"`
    Metadata     map[string]string `json:"metadata,omitempty"`
}

type Feature struct {
    types.Entity
    ID        id.FeatureID      `json:"id"`
    CatalogID id.FeatureID      `json:"catalog_id,omitempty"` // links to a catalog feature
    Key       string            `json:"key"`
    Name      string            `json:"name"`
    Type      FeatureType       `json:"type"`   // FeatureMetered, FeatureBoolean, FeatureSeat
    Limit     int64             `json:"limit"`  // -1 = unlimited
    Period    Period            `json:"period"` // PeriodMonthly, PeriodYearly, PeriodNone
    SoftLimit bool              `json:"soft_limit"`
    Metadata  map[string]string `json:"metadata,omitempty"`
}

type Pricing struct {
    types.Entity
    ID            id.PriceID  `json:"id"`
    PlanID        id.PlanID   `json:"plan_id"`
    BaseAmount    types.Money `json:"base_amount"`
    BillingPeriod Period      `json:"billing_period"`
    Tiers         []PriceTier `json:"tiers,omitempty"`
}

type PriceTier struct {
    FeatureKey string      `json:"feature_key"`
    Type       TierType    `json:"type"`  // TierGraduated, TierVolume, TierFlat
    UpTo       int64       `json:"up_to"` // -1 (or any value <= 0) = unbounded
    UnitAmount types.Money `json:"unit_amount"`
    FlatAmount types.Money `json:"flat_amount"`
    Priority   int         `json:"priority"`
}
```

A metered feature's `Limit` is both the quota `Entitled` enforces and the allowance the invoice covers before overage. A boolean feature is allowed when its `Limit` is above zero. Two metadata keys change how a feature is billed: `pricing_strategy` (on the feature, or on the plan for all of its features) names a `PricingStrategy` plugin, and `aggregator` on the feature names a `UsageAggregator` plugin. An unregistered name falls back to the built-in tiers or a plain sum and logs a warning.

The catalog's `feature.Feature` is a standalone definition (`Key`, `Name`, `Description`, `Type`, `DefaultLimit`, `Period`, `SoftLimit`, `Status`, `AppID`, provider fields, `Metadata`). An empty `AppID` makes it global.

### Subscriptions

```go
func (l *Ledger) CreateSubscription(ctx context.Context, sub *subscription.Subscription) error
func (l *Ledger) GetSubscription(ctx context.Context, subID id.SubscriptionID) (*subscription.Subscription, error)
func (l *Ledger) GetActiveSubscription(ctx context.Context, tenantID, appID string) (*subscription.Subscription, error)
func (l *Ledger) ChangePlan(ctx context.Context, subID id.SubscriptionID, planID id.PlanID, quantity map[string]int64) (*subscription.Subscription, error)
func (l *Ledger) PauseSubscription(ctx context.Context, subID id.SubscriptionID) (*subscription.Subscription, error)
func (l *Ledger) ResumeSubscription(ctx context.Context, subID id.SubscriptionID) (*subscription.Subscription, error)
func (l *Ledger) CancelSubscription(ctx context.Context, subID id.SubscriptionID, immediately bool) error
```

`ChangePlan` takes effect from the next invoice and prorates nothing. A nil `quantity` keeps the current seat counts. Without `immediately`, `CancelSubscription` schedules the cancel for the end of the current period, and the lifecycle clock enacts it.

```go
type Subscription struct {
    types.Entity
    ID                 id.SubscriptionID `json:"id"`
    TenantID           string            `json:"tenant_id"`
    PlanID             id.PlanID         `json:"plan_id"`
    Status             Status            `json:"status"`
    CurrentPeriodStart time.Time         `json:"current_period_start"`
    CurrentPeriodEnd   time.Time         `json:"current_period_end"`
    TrialStart         *time.Time        `json:"trial_start,omitempty"`
    TrialEnd           *time.Time        `json:"trial_end,omitempty"`
    CanceledAt         *time.Time        `json:"canceled_at,omitempty"`
    CancelAt           *time.Time        `json:"cancel_at,omitempty"`
    EndedAt            *time.Time        `json:"ended_at,omitempty"`
    PausedAt           *time.Time        `json:"paused_at,omitempty"`
    Stretch            *Stretch          `json:"-"` // internal, used by ledger.ForPeriod
    AppID              string            `json:"app_id"`
    ProviderID         string            `json:"provider_id,omitempty"`
    ProviderName       string            `json:"provider_name,omitempty"`
    Metadata           map[string]string `json:"metadata,omitempty"`
    Quantity           map[string]int64  `json:"quantity,omitempty"` // seat counts by feature key
}

// Status values: "active", "trialing", "past_due", "canceled", "expired", "paused".
```

### Metering

```go
func (l *Ledger) Meter(ctx context.Context, featureKey string, quantity int64) error
```

`Meter` reads the tenant and app from the context values `"tenant_id"` and `"app_id"` (plain string keys) and returns `ErrInvalidInput` when either is missing. It doesn't write to the store. The event goes into a buffer of 10,000, and a worker flushes it in batches (100 events or every 5 seconds by default). When the buffer is full, `Meter` returns `ErrMeterBufferFull`. Usage you record only shows up in quota checks and invoices after the flush.

```go
ctx = context.WithValue(ctx, "tenant_id", "acme")
ctx = context.WithValue(ctx, "app_id", "my-app")
err := l.Meter(ctx, "api_calls", 1)
```

The stored event, from `github.com/xraph/ledger/meter`:

```go
type UsageEvent struct {
    ID             id.UsageEventID   `json:"id"`
    TenantID       string            `json:"tenant_id"`
    AppID          string            `json:"app_id"`
    FeatureKey     string            `json:"feature_key"`
    Quantity       int64             `json:"quantity"`
    Timestamp      time.Time         `json:"timestamp"`
    IdempotencyKey string            `json:"idempotency_key,omitempty"`
    Metadata       map[string]string `json:"metadata,omitempty"`
}
```

`Meter` never sets `IdempotencyKey`. Only a caller writing events through the store's `IngestBatch` can.

### Entitlements

```go
func (l *Ledger) Entitled(ctx context.Context, featureKey string) (*entitlement.Result, error)
func (l *Ledger) Remaining(ctx context.Context, featureKey string) (int64, error)
func (l *Ledger) InspectEntitlement(ctx context.Context, tenantID, appID, featureKey string) (*entitlement.Result, error)
```

`Entitled` and `Remaining` read the tenant and app from the context like `Meter`. Without them, the result is not allowed with the reason `"missing tenant or app context"`. `InspectEntitlement` takes them as arguments instead.

```go
type Result struct {
    Allowed   bool   `json:"allowed"`
    Feature   string `json:"feature"`
    Used      int64  `json:"used"`
    Limit     int64  `json:"limit"`     // -1 = unlimited
    Remaining int64  `json:"remaining"` // -1 when unlimited, never below 0 otherwise
    SoftLimit bool   `json:"soft_limit"`
    Reason    string `json:"reason,omitempty"`
}
```

The reasons you'll see are `"no active subscription"`, `"plan not found"`, `"feature not in plan"`, `"over soft limit"` (allowed) and `"quota exceeded"` (not allowed). Results are cached in the store for the entitlement cache TTL.

### Invoices

```go
func (l *Ledger) GenerateInvoice(ctx context.Context, subID id.SubscriptionID, opts ...InvoiceOption) (*invoice.Invoice, error)
func ForPeriod(start, end time.Time) InvoiceOption // bill a past period instead of the current one

func (l *Ledger) FinalizeInvoice(ctx context.Context, invID id.InvoiceID) error
func (l *Ledger) MarkInvoicePaid(ctx context.Context, invID id.InvoiceID, paidAt time.Time, paymentRef string) error
func (l *Ledger) MarkInvoiceVoided(ctx context.Context, invID id.InvoiceID, reason string) error
func (l *Ledger) ExportInvoice(ctx context.Context, invID id.InvoiceID, format string) ([]byte, error)
func (l *Ledger) InvoiceFormats() []string
```

`ExportInvoice` only works for a format an `InvoiceFormatter` plugin registered. Ledger ships no formatter, so `InvoiceFormats` is empty until you add one.

```go
type Invoice struct {
    types.Entity
    ID             id.InvoiceID      `json:"id"`
    TenantID       string            `json:"tenant_id"`
    SubscriptionID id.SubscriptionID `json:"subscription_id"`
    Status         Status            `json:"status"` // "draft", "pending", "paid", "past_due", "voided"
    Currency       string            `json:"currency"`
    Subtotal       types.Money       `json:"subtotal"`
    TaxAmount      types.Money       `json:"tax_amount"`
    DiscountAmount types.Money       `json:"discount_amount"` // always positive
    Total          types.Money       `json:"total"`
    LineItems      []LineItem        `json:"line_items"`
    PeriodStart    time.Time         `json:"period_start"`
    PeriodEnd      time.Time         `json:"period_end"`
    DueDate        *time.Time        `json:"due_date,omitempty"`
    PaidAt         *time.Time        `json:"paid_at,omitempty"`
    VoidedAt       *time.Time        `json:"voided_at,omitempty"`
    VoidReason     string            `json:"void_reason,omitempty"`
    PaymentRef     string            `json:"payment_ref,omitempty"`
    ProviderID     string            `json:"provider_id,omitempty"`
    ProviderName   string            `json:"provider_name,omitempty"`
    AppID          string            `json:"app_id"`
    Metadata       map[string]string `json:"metadata,omitempty"`
}

type LineItem struct {
    ID          id.LineItemID     `json:"id"`
    InvoiceID   id.InvoiceID      `json:"invoice_id"`
    FeatureKey  string            `json:"feature_key,omitempty"`
    Description string            `json:"description"`
    Quantity    int64             `json:"quantity"`
    UnitAmount  types.Money       `json:"unit_amount"` // zero for tier-priced lines
    Amount      types.Money       `json:"amount"`      // negative for a discount line
    Type        LineItemType      `json:"type"`        // "base", "usage", "overage", "seat", "discount", "tax"
    Metadata    map[string]string `json:"metadata,omitempty"`
}
```

When a discount is larger than the subtotal, the net amount clamps at zero before tax, so `Total` never goes negative.

### Coupons

```go
func (l *Ledger) CreateCoupon(ctx context.Context, c *coupon.Coupon) error
func (l *Ledger) UpdateCoupon(ctx context.Context, c *coupon.Coupon) error
func (l *Ledger) DeleteCoupon(ctx context.Context, couponID id.CouponID) error
func (l *Ledger) ApplyCoupon(ctx context.Context, subID id.SubscriptionID, code string) (*coupon.Coupon, error)
func (l *Ledger) ListAppliedCoupons(ctx context.Context, subID id.SubscriptionID) ([]*coupon.Coupon, error)
```

```go
type Coupon struct {
    types.Entity
    ID             id.CouponID       `json:"id"`
    Code           string            `json:"code"`
    Name           string            `json:"name"`
    Type           CouponType        `json:"type"` // CouponTypePercentage ("percentage") or CouponTypeAmount ("amount")
    Amount         types.Money       `json:"amount,omitempty"`
    Percentage     int               `json:"percentage,omitempty"`
    Currency       string            `json:"currency"`
    MaxRedemptions int               `json:"max_redemptions"` // 0 or less = unlimited
    TimesRedeemed  int               `json:"times_redeemed"`
    ValidFrom      *time.Time        `json:"valid_from,omitempty"`
    ValidUntil     *time.Time        `json:"valid_until,omitempty"`
    AppID          string            `json:"app_id"`
    Metadata       map[string]string `json:"metadata,omitempty"`
}
```

### Lifecycle clock

```go
func (l *Ledger) Advance(ctx context.Context, now time.Time) (LifecycleReport, error)
func (l *Ledger) AdvancePeriods(ctx context.Context, now time.Time) ([]id.SubscriptionID, error)
func (l *Ledger) EnactCancels(ctx context.Context, now time.Time) ([]id.SubscriptionID, error)
func (l *Ledger) EndTrials(ctx context.Context, now time.Time) ([]id.SubscriptionID, error)
func (l *Ledger) MarkInvoicesPastDue(ctx context.Context, now time.Time) ([]id.InvoiceID, error)
func (l *Ledger) LifecycleInterval() time.Duration

type LifecycleReport struct {
    PeriodsAdvanced []id.SubscriptionID
    CancelsEnacted  []id.SubscriptionID
    TrialsEnded     []id.SubscriptionID
    InvoicesPastDue []id.InvoiceID
}
```

`Advance` runs the four steps in that order. `Start` runs it every minute unless you set `WithLifecycleInterval(0)`. Each change is a conditional store write, so several replicas can run the clock at once.

### Payment providers

Ledger ships no payment provider. You write one against `provider.Provider` and register it through a plugin that implements `plugin.PaymentProviderPlugin`:

```go
type Provider interface {
    Name() string

    ListPaymentMethods(ctx context.Context, tenantID string) ([]PaymentMethod, error)

    SyncPlan(ctx context.Context, p *plan.Plan) (providerID string, err error)
    SyncFeature(ctx context.Context, f *feature.Feature) (providerID string, err error)
    SyncSubscription(ctx context.Context, s *subscription.Subscription) (providerID string, err error)
    SyncInvoice(ctx context.Context, inv *invoice.Invoice) (providerID string, err error)

    ImportPlan(ctx context.Context, providerID string) (*plan.Plan, error)
    ImportFeature(ctx context.Context, providerID string) (*feature.Feature, error)
    ImportSubscription(ctx context.Context, providerID string) (*subscription.Subscription, error)
    ImportInvoice(ctx context.Context, providerID string) (*invoice.Invoice, error)

    HandleWebhook(ctx context.Context, payload []byte) (*WebhookResult, error)
}
```

The engine methods that use it:

```go
func (l *Ledger) HasProviders() bool
func (l *Ledger) ProviderNames() []string
func (l *Ledger) ListPaymentMethods(ctx context.Context, tenantID string) ([]provider.PaymentMethod, error)
func (l *Ledger) SyncPlanToProvider(ctx context.Context, planID id.PlanID) (*provider.SyncResult, error)
func (l *Ledger) SyncFeatureToProvider(ctx context.Context, featureID id.FeatureID) (*provider.SyncResult, error)
func (l *Ledger) SyncSubscriptionToProvider(ctx context.Context, subID id.SubscriptionID) (*provider.SyncResult, error)
func (l *Ledger) SyncInvoiceToProvider(ctx context.Context, invID id.InvoiceID) (*provider.SyncResult, error)
func (l *Ledger) ImportPlanFromProvider(ctx context.Context, providerName, providerID string, opts ...ImportOption) (*plan.Plan, error)
func (l *Ledger) ImportFeatureFromProvider(ctx context.Context, providerName, providerID string, opts ...ImportOption) (*feature.Feature, error)
func (l *Ledger) ImportSubscriptionFromProvider(ctx context.Context, providerName, providerID string, opts ...ImportOption) (*subscription.Subscription, error)
func (l *Ledger) ImportInvoiceFromProvider(ctx context.Context, providerName, providerID string, opts ...ImportOption) (*invoice.Invoice, error)
func (l *Ledger) HandleWebhook(ctx context.Context, providerName string, payload []byte) (*provider.WebhookResult, error)
func ImportInto(appID string) ImportOption
```

Ledger doesn't serve a webhook endpoint. You mount your own HTTP handler and pass the body to `HandleWebhook`.

## Plugin system

A plugin is anything with `Name() string`, registered with `ledger.WithPlugin`. It opts into hooks by implementing the interfaces in `github.com/xraph/ledger/plugin`. Each hook embeds `Plugin`.

| Area | Hooks |
| --- | --- |
| Engine | `OnInit(ctx, l interface{})`, `OnShutdown(ctx)` |
| Plans | `OnPlanCreated(ctx, plan)`, `OnPlanUpdated(ctx, oldPlan, newPlan)`, `OnPlanArchived(ctx, planID string)` |
| Catalog | `OnFeatureCreated`, `OnFeatureUpdated`, `OnFeatureDeleted`, `OnFeatureArchived` |
| Subscriptions | `OnSubscriptionCreated`, `OnSubscriptionChanged(ctx, sub, oldPlan, newPlan)`, `OnSubscriptionCanceled`, `OnSubscriptionCancelScheduled`, `OnSubscriptionTrialEnded`, `OnSubscriptionRenewed(ctx, renewal)` |
| Usage | `OnUsageFlushed(ctx, count int, elapsed time.Duration)` |
| Entitlements | `OnEntitlementChecked(ctx, result)`, `OnQuotaExceeded(ctx, tenantID, featureKey string, used, limit int64)` |
| Invoices | `OnInvoiceGenerated`, `OnInvoiceFinalized`, `OnInvoicePaid`, `OnInvoiceVoided(ctx, inv, reason string)`, `OnInvoicePastDue` |
| Providers | `OnProviderSync(ctx, provider string, success bool, err error)`, `OnWebhookReceived(ctx, provider string, payload []byte)` |

Model arguments are passed as `interface{}`, so assert them to the concrete type (`*subscription.Subscription`, `*invoice.Invoice` and so on).

Four more hook interfaces are declared but the engine never calls them yet: `OnSubscriptionExpired`, `OnUsageIngested`, `OnSoftLimitReached` and `OnInvoiceFailed`. Implementing them does nothing today.

These interfaces change what the engine computes rather than observe it:

```go
type PricingStrategy interface {
    Plugin
    StrategyName() string
    Compute(tiers []interface{}, usage, included int64, currency string) interface{} // returns types.Money
}

type UsageAggregator interface {
    Plugin
    AggregatorName() string
    Aggregate(ctx context.Context, events []interface{}) (int64, error)
}

type TaxCalculator interface {
    Plugin
    CalculateTax(ctx context.Context, subtotal interface{}, tenantID string) (interface{}, error) // returns types.Money
}

type InvoiceFormatter interface {
    Plugin
    Format() string // "pdf", "html", "csv", ...
    Render(ctx context.Context, inv interface{}, w interface{}) error // w is an io.Writer
}

type CouponValidator interface {
    Plugin
    ValidateCoupon(ctx context.Context, coupon interface{}, sub interface{}) error
}

type PaymentProviderPlugin interface {
    Plugin
    Provider() provider.Provider
}
```

Two plugins ship with the module. `github.com/xraph/ledger/audit_hook` (package `audithook`) turns events into audit records through a `Recorder` you supply, and `observability.NewMetricsExtension(factory)` records counters and histograms through a `MetricFactory`. The engine's own spans and metrics need neither: `ledger.WithMetrics(m)` turns the metrics on, and the Forge extension passes `app.Metrics()` for you.

## Store interface

`ledger.New` takes a `store.Store`. The interface in `store/store.go` has these groups of methods:

| Group | Methods |
| --- | --- |
| Plans | `CreatePlan`, `GetPlan`, `GetPlanBySlug`, `ListPlans`, `UpdatePlan`, `DeletePlan`, `ArchivePlan` |
| Feature catalog | `CreateFeature`, `GetFeature`, `GetFeatureByKey`, `ListFeatures`, `ListGlobalFeatures`, `UpdateFeature`, `DeleteFeature`, `ArchiveFeature` |
| Subscriptions | `CreateSubscription`, `GetSubscription`, `GetActiveSubscription`, `ListSubscriptions`, `UpdateSubscription`, `CancelSubscription`, `PauseSubscription`, `ResumeSubscription`, `ChangeSubscriptionPlan`, `SetSubscriptionProvider` |
| Usage | `IngestBatch`, `Aggregate`, `AggregateMulti`, `QueryUsage`, `PurgeUsage` |
| Entitlement cache | `GetCached`, `SetCached`, `Invalidate`, `InvalidateFeature` |
| Invoices | `CreateInvoice`, `GetInvoice`, `ListInvoices`, `UpdateInvoice`, `GetInvoiceByPeriod`, `ListPendingInvoices`, `MarkInvoicePaid`, `MarkInvoiceVoided`, `SetInvoiceProvider` |
| Coupons | `CreateCoupon`, `GetCoupon`, `GetCouponByID`, `ListCoupons`, `UpdateCoupon`, `DeleteCoupon`, `ApplyCoupon`, `ListAppliedCoupons`, `IncrementCouponRedemptions`, `RedeemCoupon` |
| Lifecycle clock | `ListDueSubscriptions`, `ListOverdueInvoices`, `EndSubscriptionTrial`, `EnactSubscriptionCancel`, `AdvanceSubscriptionPeriod`, `MarkInvoicePastDue` |
| Core | `Migrate`, `Ping`, `Close` |

The comments in `store/store.go` spell out the concurrency contract for each write, and `store/storetest` is the conformance suite every shipped store passes. Run it against your own store before you trust it.

The shipped stores are `store/memory`, `store/postgres`, `store/sqlite` and `store/mongo`. The last three take a `*grove.DB`.

## Errors

Sentinels live in the root package. Match them with `errors.Is`.

| Area | Errors |
| --- | --- |
| General | `ErrNotFound`, `ErrAlreadyExists`, `ErrInvalidInput`, `ErrUnauthorized`, `ErrForbidden` |
| Plans and catalog | `ErrPlanNotFound`, `ErrPlanArchived`, `ErrPlanInUse`, `ErrFeatureNotFound`, `ErrFeatureArchived`, `ErrInvalidPricing`, `ErrDuplicateFeature` |
| Subscriptions | `ErrSubscriptionNotFound`, `ErrSubscriptionExists`, `ErrSubscriptionCanceled`, `ErrSubscriptionExpired`, `ErrInvalidUpgrade`, `ErrInvalidDowngrade`, `ErrTrialExpired`, `ErrNoActiveSubscription` |
| Metering | `ErrMeterBufferFull`, `ErrInvalidQuantity`, `ErrDuplicateEvent`, `ErrEventTooOld` |
| Entitlements | `ErrQuotaExceeded`, `ErrFeatureDisabled`, `ErrHardLimitReached`, `ErrSoftLimitReached`, `ErrNoEntitlement` |
| Invoices | `ErrInvoiceNotFound`, `ErrInvoiceFinalized`, `ErrInvoicePaid`, `ErrInvoiceVoided`, `ErrInvoiceIncomplete`, `ErrInvalidDiscount` |
| Coupons | `ErrCouponNotFound`, `ErrCouponExpired`, `ErrCouponInvalid`, `ErrCouponExhausted`, `ErrCouponNotStarted`, `ErrCouponAlreadyApplied` |
| Providers | `ErrProviderNotFound`, `ErrProviderSync`, `ErrProviderWebhook`, `ErrProviderNotConfigured` |
| Stores | `ErrStoreNotReady`, `ErrStoreClosed`, `ErrTransactionFailed`, `ErrMigrationFailed`, `ErrCacheMiss`, `ErrCacheInvalidate` |

`ErrCacheMiss` is what a store's `GetCached` returns on a miss. `Entitled` handles it internally and never returns it to you.

There are also three helpers, `IsNotFound(err)`, `IsQuotaError(err)` and `IsRetryable(err)`, and two error types, `ValidationError{Field, Message}` and `MultiError{Errors}`.
