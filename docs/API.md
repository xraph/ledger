# Ledger API Reference

## Core Types

### ID System

All entities use TypeID v2 for globally unique, K-sortable identifiers.

```go
import "github.com/xraph/ledger/id"

// Available ID types
type PlanID = typeid.TypeID          // Prefix: "plan"
type SubscriptionID = typeid.TypeID   // Prefix: "sub"
type InvoiceID = typeid.TypeID        // Prefix: "inv"
type FeatureID = typeid.TypeID        // Prefix: "feat"
type PriceID = typeid.TypeID          // Prefix: "price"
type LineItemID = typeid.TypeID       // Prefix: "li"
type CouponID = typeid.TypeID         // Prefix: "coup"
type UsageEventID = typeid.TypeID     // Prefix: "use"

// Constructors
func NewPlanID() PlanID
func NewSubscriptionID() SubscriptionID
func NewInvoiceID() InvoiceID
func NewFeatureID() FeatureID
func NewPriceID() PriceID
func NewLineItemID() LineItemID
func NewCouponID() CouponID
func NewUsageEventID() UsageEventID

// Parse from string
func ParsePlanID(s string) (PlanID, error)
func ParseSubscriptionID(s string) (SubscriptionID, error)
// ... etc
```

### Money Type

Integer-only monetary arithmetic in the smallest currency unit.

```go
import "github.com/xraph/ledger/types"

type Money struct {
    Amount   int64  `json:"amount"`   // Smallest unit (cents)
    Currency string `json:"currency"` // ISO 4217 lowercase
}

// Constructors
func USD(cents int64) Money
func EUR(cents int64) Money
func GBP(pence int64) Money
func JPY(yen int64) Money
func Zero(currency string) Money

// Arithmetic (panics on currency mismatch)
func (m Money) Add(other Money) Money
func (m Money) Subtract(other Money) Money
func (m Money) Multiply(qty int64) Money
func (m Money) Divide(divisor int64) Money
func (m Money) Negate() Money
func (m Money) Abs() Money

// Comparison
func (m Money) IsZero() bool
func (m Money) IsPositive() bool
func (m Money) IsNegative() bool
func (m Money) Equal(other Money) bool
func (m Money) LessThan(other Money) bool
func (m Money) GreaterThan(other Money) bool
func (m Money) Min(other Money) Money
func (m Money) Max(other Money) Money

// Formatting
func (m Money) String() string        // "$49.00"
func (m Money) FormatMajor() string   // "49.00"
```

### Entity Base Type

All domain models embed this for automatic timestamp handling.

```go
import "github.com/xraph/ledger/types"

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

## Ledger Engine

### Initialization

```go
import "github.com/xraph/ledger"

// Create new Ledger instance
func New(store Store, opts ...Option) *Ledger

// Options
func WithLogger(logger *slog.Logger) Option
func WithPlugin(p plugin.Plugin) Option
func WithMeterConfig(batchSize int, flushInterval time.Duration) Option
func WithEntitlementCacheTTL(ttl time.Duration) Option

// Lifecycle
func (l *Ledger) Start(ctx context.Context) error
func (l *Ledger) Stop() error
```

### Plan Management

```go
// Create a new plan
func (l *Ledger) CreatePlan(ctx context.Context, p *plan.Plan) error

// Retrieve plans
func (l *Ledger) GetPlan(ctx context.Context, planID id.PlanID) (*plan.Plan, error)
func (l *Ledger) GetPlanBySlug(ctx context.Context, slug, appID string) (*plan.Plan, error)
```

#### Plan Model

```go
type Plan struct {
    types.Entity
    ID          id.PlanID         `json:"id"`
    Name        string            `json:"name"`
    Slug        string            `json:"slug"`
    Description string            `json:"description"`
    Currency    string            `json:"currency"`
    Status      Status            `json:"status"`
    TrialDays   int               `json:"trial_days"`
    Features    []Feature         `json:"features"`
    Pricing     *Pricing          `json:"pricing,omitempty"`
    AppID       string            `json:"app_id"`
    Metadata    map[string]string `json:"metadata,omitempty"`
}

type Status string
const (
    StatusActive   Status = "active"
    StatusArchived Status = "archived"
    StatusDraft    Status = "draft"
)

type Feature struct {
    types.Entity
    ID        id.FeatureID      `json:"id"`
    Key       string            `json:"key"`
    Name      string            `json:"name"`
    Type      FeatureType       `json:"type"`
    Limit     int64             `json:"limit"`     // -1 = unlimited
    Period    Period            `json:"period"`
    SoftLimit bool              `json:"soft_limit"`
    Metadata  map[string]string `json:"metadata,omitempty"`
}

type FeatureType string
const (
    FeatureMetered FeatureType = "metered"
    FeatureBoolean FeatureType = "boolean"
    FeatureSeat    FeatureType = "seat"
)

type Period string
const (
    PeriodMonthly Period = "monthly"
    PeriodYearly  Period = "yearly"
    PeriodNone    Period = "none"
)

type Pricing struct {
    types.Entity
    ID            id.PriceID    `json:"id"`
    PlanID        id.PlanID     `json:"plan_id"`
    BaseAmount    types.Money   `json:"base_amount"`
    BillingPeriod Period        `json:"billing_period"`
    Tiers         []PriceTier   `json:"tiers,omitempty"`
}

type PriceTier struct {
    FeatureKey string      `json:"feature_key"`
    Type       TierType    `json:"type"`
    UpTo       int64       `json:"up_to"`      // -1 = unlimited
    UnitAmount types.Money `json:"unit_amount"`
    FlatAmount types.Money `json:"flat_amount"`
    Priority   int         `json:"priority"`
}

type TierType string
const (
    TierGraduated TierType = "graduated"
    TierVolume    TierType = "volume"
    TierFlat      TierType = "flat"
)
```

### Subscription Management

```go
// Create subscription
func (l *Ledger) CreateSubscription(ctx context.Context, sub *subscription.Subscription) error

// Retrieve subscriptions
func (l *Ledger) GetSubscription(ctx context.Context, subID id.SubscriptionID) (*subscription.Subscription, error)
func (l *Ledger) GetActiveSubscription(ctx context.Context, tenantID, appID string) (*subscription.Subscription, error)

// Cancel subscription
func (l *Ledger) CancelSubscription(ctx context.Context, subID id.SubscriptionID, immediately bool) error
```

#### Subscription Model

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
    AppID              string            `json:"app_id"`
    ProviderID         string            `json:"provider_id,omitempty"`
    ProviderName       string            `json:"provider_name,omitempty"`
    Metadata           map[string]string `json:"metadata,omitempty"`
}

type Status string
const (
    StatusActive    Status = "active"
    StatusTrialing  Status = "trialing"
    StatusPastDue   Status = "past_due"
    StatusCanceled  Status = "canceled"
    StatusExpired   Status = "expired"
    StatusPaused    Status = "paused"
)
```

### Usage Metering

```go
// Record usage (non-blocking, returns immediately)
func (l *Ledger) Meter(ctx context.Context, featureKey string, quantity int64) error

// Context must contain tenant_id and app_id
ctx := context.WithValue(ctx, "tenant_id", "tenant_123")
ctx = context.WithValue(ctx, "app_id", "app_456")
```

#### Usage Event Model

```go
type UsageEvent struct {
    ID             id.UsageEventID `json:"id"`
    TenantID       string          `json:"tenant_id"`
    AppID          string          `json:"app_id"`
    FeatureKey     string          `json:"feature_key"`
    Quantity       int64           `json:"quantity"`
    Timestamp      time.Time       `json:"timestamp"`
    IdempotencyKey string          `json:"idempotency_key,omitempty"`
    Metadata       map[string]any  `json:"metadata,omitempty"`
}
```

### Entitlement Checks

```go
// Check if tenant can use feature
func (l *Ledger) Entitled(ctx context.Context, featureKey string) (*entitlement.Result, error)

// Get remaining quota
func (l *Ledger) Remaining(ctx context.Context, featureKey string) (int64, error)
```

#### Entitlement Result

```go
type Result struct {
    Allowed   bool   `json:"allowed"`
    Feature   string `json:"feature"`
    Used      int64  `json:"used"`
    Limit     int64  `json:"limit"`      // -1 = unlimited
    Remaining int64  `json:"remaining"`  // -1 = unlimited
    SoftLimit bool   `json:"soft_limit"`
    Reason    string `json:"reason,omitempty"`
}
```

### Invoice Generation

```go
// Generate invoice for subscription period
func (l *Ledger) GenerateInvoice(ctx context.Context, subID id.SubscriptionID) (*invoice.Invoice, error)
```

#### Invoice Model

```go
type Invoice struct {
    types.Entity
    ID             id.InvoiceID      `json:"id"`
    TenantID       string            `json:"tenant_id"`
    SubscriptionID id.SubscriptionID `json:"subscription_id"`
    Status         Status            `json:"status"`
    Currency       string            `json:"currency"`
    Subtotal       types.Money       `json:"subtotal"`
    TaxAmount      types.Money       `json:"tax_amount"`
    DiscountAmount types.Money       `json:"discount_amount"`
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
    AppID          string            `json:"app_id"`
    Metadata       map[string]string `json:"metadata,omitempty"`
}

type Status string
const (
    StatusDraft   Status = "draft"
    StatusPending Status = "pending"
    StatusPaid    Status = "paid"
    StatusPastDue Status = "past_due"
    StatusVoided  Status = "voided"
)

type LineItem struct {
    ID          id.LineItemID     `json:"id"`
    InvoiceID   id.InvoiceID      `json:"invoice_id"`
    FeatureKey  string            `json:"feature_key,omitempty"`
    Description string            `json:"description"`
    Quantity    int64             `json:"quantity"`
    UnitAmount  types.Money       `json:"unit_amount"`
    Amount      types.Money       `json:"amount"`
    Type        LineItemType      `json:"type"`
    Metadata    map[string]string `json:"metadata,omitempty"`
}

type LineItemType string
const (
    LineItemBase     LineItemType = "base"
    LineItemUsage    LineItemType = "usage"
    LineItemOverage  LineItemType = "overage"
    LineItemSeat     LineItemType = "seat"
    LineItemDiscount LineItemType = "discount"
    LineItemTax      LineItemType = "tax"
)
```

## Plugin System

### Plugin Interface

```go
type Plugin interface {
    Name() string
}

// Lifecycle hooks (all optional)
type OnInit interface {
    OnInit(ctx context.Context, ledger interface{}) error
}

type OnShutdown interface {
    OnShutdown(ctx context.Context) error
}

// Plan hooks
type OnPlanCreated interface {
    OnPlanCreated(ctx context.Context, plan interface{}) error
}

type OnPlanUpdated interface {
    OnPlanUpdated(ctx context.Context, oldPlan, newPlan interface{}) error
}

type OnPlanArchived interface {
    OnPlanArchived(ctx context.Context, planID string) error
}

// Subscription hooks
type OnSubscriptionCreated interface {
    OnSubscriptionCreated(ctx context.Context, sub interface{}) error
}

type OnSubscriptionChanged interface {
    OnSubscriptionChanged(ctx context.Context, sub interface{}, oldPlan, newPlan interface{}) error
}

type OnSubscriptionCanceled interface {
    OnSubscriptionCanceled(ctx context.Context, sub interface{}) error
}

// Usage hooks
type OnUsageIngested interface {
    OnUsageIngested(ctx context.Context, event interface{}) error
}

type OnUsageFlushed interface {
    OnUsageFlushed(ctx context.Context, count int, elapsed time.Duration) error
}

// Entitlement hooks
type OnEntitlementChecked interface {
    OnEntitlementChecked(ctx context.Context, result interface{}) error
}

type OnQuotaExceeded interface {
    OnQuotaExceeded(ctx context.Context, tenantID, featureKey string, used, limit int64) error
}

// Invoice hooks
type OnInvoiceGenerated interface {
    OnInvoiceGenerated(ctx context.Context, inv interface{}) error
}

type OnInvoiceFinalized interface {
    OnInvoiceFinalized(ctx context.Context, inv interface{}) error
}

type OnInvoicePaid interface {
    OnInvoicePaid(ctx context.Context, inv interface{}) error
}

type OnInvoiceFailed interface {
    OnInvoiceFailed(ctx context.Context, inv interface{}, err error) error
}

type OnInvoiceVoided interface {
    OnInvoiceVoided(ctx context.Context, inv interface{}, reason string) error
}
```

### Extension Plugins

```go
// Pricing strategies
type PricingStrategy interface {
    Plugin
    StrategyName() string
    CalculatePrice(usage int64, tiers []PriceTier) types.Money
}

// Usage aggregation
type UsageAggregator interface {
    Plugin
    AggregatorName() string
    Aggregate(events []*UsageEvent) int64
}

// Tax calculation
type TaxCalculator interface {
    Plugin
    CalculateTax(ctx context.Context, invoice *Invoice) (types.Money, error)
}

// Payment providers
type PaymentProviderPlugin interface {
    Plugin
    ProviderName() string
    CreateCustomer(ctx context.Context, tenant interface{}) (string, error)
    CreateSubscription(ctx context.Context, sub interface{}) (string, error)
    ChargeInvoice(ctx context.Context, inv interface{}) error
    HandleWebhook(ctx context.Context, payload []byte) error
}
```

## Store Interface

```go
type Store interface {
    // Plan methods
    CreatePlan(ctx context.Context, p *plan.Plan) error
    GetPlan(ctx context.Context, planID id.PlanID) (*plan.Plan, error)
    GetPlanBySlug(ctx context.Context, slug string, appID string) (*plan.Plan, error)
    ListPlans(ctx context.Context, appID string, opts plan.ListOpts) ([]*plan.Plan, error)
    UpdatePlan(ctx context.Context, p *plan.Plan) error
    DeletePlan(ctx context.Context, planID id.PlanID) error
    ArchivePlan(ctx context.Context, planID id.PlanID) error

    // Subscription methods
    CreateSubscription(ctx context.Context, s *subscription.Subscription) error
    GetSubscription(ctx context.Context, subID id.SubscriptionID) (*subscription.Subscription, error)
    GetActiveSubscription(ctx context.Context, tenantID string, appID string) (*subscription.Subscription, error)
    ListSubscriptions(ctx context.Context, tenantID string, appID string, opts subscription.ListOpts) ([]*subscription.Subscription, error)
    UpdateSubscription(ctx context.Context, s *subscription.Subscription) error
    CancelSubscription(ctx context.Context, subID id.SubscriptionID, cancelAt time.Time) error

    // Meter methods
    IngestBatch(ctx context.Context, events []*meter.UsageEvent) error
    Aggregate(ctx context.Context, tenantID, appID, featureKey string, period plan.Period) (int64, error)
    AggregateMulti(ctx context.Context, tenantID, appID string, featureKeys []string, period plan.Period) (map[string]int64, error)
    QueryUsage(ctx context.Context, tenantID, appID string, opts meter.QueryOpts) ([]*meter.UsageEvent, error)
    PurgeUsage(ctx context.Context, before time.Time) (int64, error)

    // Entitlement methods
    GetCached(ctx context.Context, tenantID, appID, featureKey string) (*entitlement.Result, error)
    SetCached(ctx context.Context, tenantID, appID, featureKey string, result *entitlement.Result, ttl time.Duration) error
    Invalidate(ctx context.Context, tenantID, appID string) error
    InvalidateFeature(ctx context.Context, tenantID, appID, featureKey string) error

    // Invoice methods
    CreateInvoice(ctx context.Context, inv *invoice.Invoice) error
    GetInvoice(ctx context.Context, invID id.InvoiceID) (*invoice.Invoice, error)
    ListInvoices(ctx context.Context, tenantID, appID string, opts invoice.ListOpts) ([]*invoice.Invoice, error)
    UpdateInvoice(ctx context.Context, inv *invoice.Invoice) error
    GetInvoiceByPeriod(ctx context.Context, tenantID, appID string, periodStart, periodEnd time.Time) (*invoice.Invoice, error)
    ListPendingInvoices(ctx context.Context, appID string) ([]*invoice.Invoice, error)
    MarkInvoicePaid(ctx context.Context, invID id.InvoiceID, paidAt time.Time, paymentRef string) error
    MarkInvoiceVoided(ctx context.Context, invID id.InvoiceID, reason string) error

    // Coupon methods
    CreateCoupon(ctx context.Context, c *coupon.Coupon) error
    GetCoupon(ctx context.Context, code string, appID string) (*coupon.Coupon, error)
    GetCouponByID(ctx context.Context, couponID id.CouponID) (*coupon.Coupon, error)
    ListCoupons(ctx context.Context, appID string, opts coupon.ListOpts) ([]*coupon.Coupon, error)
    UpdateCoupon(ctx context.Context, c *coupon.Coupon) error
    DeleteCoupon(ctx context.Context, couponID id.CouponID) error

    // Core methods
    Migrate(ctx context.Context) error
    Ping(ctx context.Context) error
    Close() error
}
```

## Error Types

```go
var (
    // Common errors
    ErrNotFound         = errors.New("ledger: not found")
    ErrAlreadyExists    = errors.New("ledger: already exists")
    ErrInvalidInput     = errors.New("ledger: invalid input")
    ErrCacheMiss        = errors.New("ledger: cache miss")

    // Plan errors
    ErrPlanNotFound     = errors.New("ledger: plan not found")
    ErrPlanArchived     = errors.New("ledger: plan is archived")

    // Subscription errors
    ErrSubscriptionNotFound   = errors.New("ledger: subscription not found")
    ErrNoActiveSubscription   = errors.New("ledger: no active subscription")
    ErrSubscriptionCanceled   = errors.New("ledger: subscription canceled")

    // Invoice errors
    ErrInvoiceNotFound  = errors.New("ledger: invoice not found")
    ErrInvoiceFinalized = errors.New("ledger: invoice already finalized")

    // Coupon errors
    ErrCouponNotFound   = errors.New("ledger: coupon not found")
    ErrCouponExpired    = errors.New("ledger: coupon expired")
    ErrCouponExhausted  = errors.New("ledger: coupon redemptions exhausted")

    // Meter errors
    ErrMeterBufferFull  = errors.New("ledger: meter buffer full")
)