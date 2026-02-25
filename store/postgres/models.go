package postgres

import (
	"encoding/json"
	"time"

	"github.com/xraph/grove"

	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/entitlement"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/subscription"
	"github.com/xraph/ledger/types"
)

// ==================== Plan models ====================

type planModel struct {
	grove.BaseModel `grove:"table:ledger_plans"`

	ID          string            `grove:"id,pk"`
	Name        string            `grove:"name"`
	Slug        string            `grove:"slug"`
	Description string            `grove:"description"`
	Currency    string            `grove:"currency"`
	Status      string            `grove:"status"`
	TrialDays   int               `grove:"trial_days"`
	Features    json.RawMessage   `grove:"features,type:jsonb"`
	Pricing     json.RawMessage   `grove:"pricing,type:jsonb"`
	AppID       string            `grove:"app_id"`
	Metadata    map[string]string `grove:"metadata,type:jsonb"`
	CreatedAt   time.Time         `grove:"created_at"`
	UpdatedAt   time.Time         `grove:"updated_at"`
}

func toPlanModel(p *plan.Plan) *planModel {
	features, _ := json.Marshal(p.Features) //nolint:errcheck // best-effort
	pricing, _ := json.Marshal(p.Pricing)   //nolint:errcheck // best-effort

	return &planModel{
		ID:          p.ID.String(),
		Name:        p.Name,
		Slug:        p.Slug,
		Description: p.Description,
		Currency:    p.Currency,
		Status:      string(p.Status),
		TrialDays:   p.TrialDays,
		Features:    features,
		Pricing:     pricing,
		AppID:       p.AppID,
		Metadata:    p.Metadata,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}

func fromPlanModel(m *planModel) (*plan.Plan, error) {
	planID, err := id.ParsePlanID(m.ID)
	if err != nil {
		return nil, err
	}

	var features []plan.Feature
	if len(m.Features) > 0 {
		_ = json.Unmarshal(m.Features, &features) //nolint:errcheck // best-effort
	}

	var pricing *plan.Pricing
	if len(m.Pricing) > 0 && string(m.Pricing) != "null" {
		pricing = new(plan.Pricing)
		_ = json.Unmarshal(m.Pricing, pricing) //nolint:errcheck // best-effort
	}

	return &plan.Plan{
		Entity: types.Entity{
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		},
		ID:          planID,
		Name:        m.Name,
		Slug:        m.Slug,
		Description: m.Description,
		Currency:    m.Currency,
		Status:      plan.Status(m.Status),
		TrialDays:   m.TrialDays,
		Features:    features,
		Pricing:     pricing,
		AppID:       m.AppID,
		Metadata:    m.Metadata,
	}, nil
}

// ==================== Subscription models ====================

type subscriptionModel struct {
	grove.BaseModel `grove:"table:ledger_subscriptions"`

	ID                 string            `grove:"id,pk"`
	TenantID           string            `grove:"tenant_id"`
	PlanID             string            `grove:"plan_id"`
	Status             string            `grove:"status"`
	CurrentPeriodStart time.Time         `grove:"current_period_start"`
	CurrentPeriodEnd   time.Time         `grove:"current_period_end"`
	TrialStart         *time.Time        `grove:"trial_start"`
	TrialEnd           *time.Time        `grove:"trial_end"`
	CanceledAt         *time.Time        `grove:"canceled_at"`
	CancelAt           *time.Time        `grove:"cancel_at"`
	EndedAt            *time.Time        `grove:"ended_at"`
	AppID              string            `grove:"app_id"`
	ProviderID         string            `grove:"provider_id"`
	ProviderName       string            `grove:"provider_name"`
	Metadata           map[string]string `grove:"metadata,type:jsonb"`
	CreatedAt          time.Time         `grove:"created_at"`
	UpdatedAt          time.Time         `grove:"updated_at"`
}

func toSubscriptionModel(s *subscription.Subscription) *subscriptionModel {
	return &subscriptionModel{
		ID:                 s.ID.String(),
		TenantID:           s.TenantID,
		PlanID:             s.PlanID.String(),
		Status:             string(s.Status),
		CurrentPeriodStart: s.CurrentPeriodStart,
		CurrentPeriodEnd:   s.CurrentPeriodEnd,
		TrialStart:         s.TrialStart,
		TrialEnd:           s.TrialEnd,
		CanceledAt:         s.CanceledAt,
		CancelAt:           s.CancelAt,
		EndedAt:            s.EndedAt,
		AppID:              s.AppID,
		ProviderID:         s.ProviderID,
		ProviderName:       s.ProviderName,
		Metadata:           s.Metadata,
		CreatedAt:          s.CreatedAt,
		UpdatedAt:          s.UpdatedAt,
	}
}

func fromSubscriptionModel(m *subscriptionModel) (*subscription.Subscription, error) {
	subID, err := id.ParseSubscriptionID(m.ID)
	if err != nil {
		return nil, err
	}
	planID, err := id.ParsePlanID(m.PlanID)
	if err != nil {
		return nil, err
	}

	return &subscription.Subscription{
		Entity: types.Entity{
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		},
		ID:                 subID,
		TenantID:           m.TenantID,
		PlanID:             planID,
		Status:             subscription.Status(m.Status),
		CurrentPeriodStart: m.CurrentPeriodStart,
		CurrentPeriodEnd:   m.CurrentPeriodEnd,
		TrialStart:         m.TrialStart,
		TrialEnd:           m.TrialEnd,
		CanceledAt:         m.CanceledAt,
		CancelAt:           m.CancelAt,
		EndedAt:            m.EndedAt,
		AppID:              m.AppID,
		ProviderID:         m.ProviderID,
		ProviderName:       m.ProviderName,
		Metadata:           m.Metadata,
	}, nil
}

// ==================== Usage Event models ====================

type usageEventModel struct {
	grove.BaseModel `grove:"table:ledger_usage_events"`

	ID             string            `grove:"id,pk"`
	TenantID       string            `grove:"tenant_id"`
	AppID          string            `grove:"app_id"`
	FeatureKey     string            `grove:"feature_key"`
	Quantity       int64             `grove:"quantity"`
	Timestamp      time.Time         `grove:"timestamp"`
	IdempotencyKey string            `grove:"idempotency_key"`
	Metadata       map[string]string `grove:"metadata,type:jsonb"`
	CreatedAt      time.Time         `grove:"created_at"`
}

func toUsageEventModel(e *meter.UsageEvent) *usageEventModel {
	return &usageEventModel{
		ID:             e.ID.String(),
		TenantID:       e.TenantID,
		AppID:          e.AppID,
		FeatureKey:     e.FeatureKey,
		Quantity:       e.Quantity,
		Timestamp:      e.Timestamp,
		IdempotencyKey: e.IdempotencyKey,
		Metadata:       e.Metadata,
		CreatedAt:      time.Now().UTC(),
	}
}

func fromUsageEventModel(m *usageEventModel) (*meter.UsageEvent, error) {
	evtID, err := id.ParseUsageEventID(m.ID)
	if err != nil {
		return nil, err
	}

	return &meter.UsageEvent{
		ID:             evtID,
		TenantID:       m.TenantID,
		AppID:          m.AppID,
		FeatureKey:     m.FeatureKey,
		Quantity:       m.Quantity,
		Timestamp:      m.Timestamp,
		IdempotencyKey: m.IdempotencyKey,
		Metadata:       m.Metadata,
	}, nil
}

// ==================== Entitlement Cache models ====================

type entitlementCacheModel struct {
	grove.BaseModel `grove:"table:ledger_entitlement_cache"`

	CacheKey   string    `grove:"cache_key,pk"`
	TenantID   string    `grove:"tenant_id"`
	AppID      string    `grove:"app_id"`
	FeatureKey string    `grove:"feature_key"`
	Allowed    bool      `grove:"allowed"`
	Feature    string    `grove:"feature"`
	Used       int64     `grove:"used"`
	Limit      int64     `grove:"cache_limit"`
	Remaining  int64     `grove:"remaining"`
	SoftLimit  bool      `grove:"soft_limit"`
	Reason     string    `grove:"reason"`
	ExpiresAt  time.Time `grove:"expires_at"`
	CreatedAt  time.Time `grove:"created_at"`
}

func toEntitlementCacheModel(tenantID, appID, featureKey string, result *entitlement.Result, expiresAt time.Time) *entitlementCacheModel {
	return &entitlementCacheModel{
		CacheKey:   tenantID + ":" + appID + ":" + featureKey,
		TenantID:   tenantID,
		AppID:      appID,
		FeatureKey: featureKey,
		Allowed:    result.Allowed,
		Feature:    result.Feature,
		Used:       result.Used,
		Limit:      result.Limit,
		Remaining:  result.Remaining,
		SoftLimit:  result.SoftLimit,
		Reason:     result.Reason,
		ExpiresAt:  expiresAt,
		CreatedAt:  time.Now().UTC(),
	}
}

func fromEntitlementCacheModel(m *entitlementCacheModel) *entitlement.Result {
	return &entitlement.Result{
		Allowed:   m.Allowed,
		Feature:   m.Feature,
		Used:      m.Used,
		Limit:     m.Limit,
		Remaining: m.Remaining,
		SoftLimit: m.SoftLimit,
		Reason:    m.Reason,
	}
}

// ==================== Invoice models ====================

type invoiceModel struct {
	grove.BaseModel `grove:"table:ledger_invoices"`

	ID                  string            `grove:"id,pk"`
	TenantID            string            `grove:"tenant_id"`
	SubscriptionID      string            `grove:"subscription_id"`
	Status              string            `grove:"status"`
	Currency            string            `grove:"currency"`
	SubtotalAmountCents int64             `grove:"subtotal_amount_cents"`
	SubtotalCurrency    string            `grove:"subtotal_currency"`
	TaxAmountCents      int64             `grove:"tax_amount_cents"`
	TaxCurrency         string            `grove:"tax_currency"`
	DiscountAmountCents int64             `grove:"discount_amount_cents"`
	DiscountCurrency    string            `grove:"discount_currency"`
	TotalAmountCents    int64             `grove:"total_amount_cents"`
	TotalCurrency       string            `grove:"total_currency"`
	LineItems           json.RawMessage   `grove:"line_items,type:jsonb"`
	PeriodStart         time.Time         `grove:"period_start"`
	PeriodEnd           time.Time         `grove:"period_end"`
	DueDate             *time.Time        `grove:"due_date"`
	PaidAt              *time.Time        `grove:"paid_at"`
	VoidedAt            *time.Time        `grove:"voided_at"`
	VoidReason          string            `grove:"void_reason"`
	PaymentRef          string            `grove:"payment_ref"`
	ProviderID          string            `grove:"provider_id"`
	AppID               string            `grove:"app_id"`
	Metadata            map[string]string `grove:"metadata,type:jsonb"`
	CreatedAt           time.Time         `grove:"created_at"`
	UpdatedAt           time.Time         `grove:"updated_at"`
}

func toInvoiceModel(inv *invoice.Invoice) *invoiceModel {
	lineItems, _ := json.Marshal(inv.LineItems) //nolint:errcheck // best-effort

	return &invoiceModel{
		ID:                  inv.ID.String(),
		TenantID:            inv.TenantID,
		SubscriptionID:      inv.SubscriptionID.String(),
		Status:              string(inv.Status),
		Currency:            inv.Currency,
		SubtotalAmountCents: inv.Subtotal.Amount,
		SubtotalCurrency:    inv.Subtotal.Currency,
		TaxAmountCents:      inv.TaxAmount.Amount,
		TaxCurrency:         inv.TaxAmount.Currency,
		DiscountAmountCents: inv.DiscountAmount.Amount,
		DiscountCurrency:    inv.DiscountAmount.Currency,
		TotalAmountCents:    inv.Total.Amount,
		TotalCurrency:       inv.Total.Currency,
		LineItems:           lineItems,
		PeriodStart:         inv.PeriodStart,
		PeriodEnd:           inv.PeriodEnd,
		DueDate:             inv.DueDate,
		PaidAt:              inv.PaidAt,
		VoidedAt:            inv.VoidedAt,
		VoidReason:          inv.VoidReason,
		PaymentRef:          inv.PaymentRef,
		ProviderID:          inv.ProviderID,
		AppID:               inv.AppID,
		Metadata:            inv.Metadata,
		CreatedAt:           inv.CreatedAt,
		UpdatedAt:           inv.UpdatedAt,
	}
}

func fromInvoiceModel(m *invoiceModel) (*invoice.Invoice, error) {
	invID, err := id.ParseInvoiceID(m.ID)
	if err != nil {
		return nil, err
	}
	subID, err := id.ParseSubscriptionID(m.SubscriptionID)
	if err != nil {
		return nil, err
	}

	var lineItems []invoice.LineItem
	if len(m.LineItems) > 0 {
		_ = json.Unmarshal(m.LineItems, &lineItems) //nolint:errcheck // best-effort
	}

	return &invoice.Invoice{
		Entity: types.Entity{
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		},
		ID:             invID,
		TenantID:       m.TenantID,
		SubscriptionID: subID,
		Status:         invoice.Status(m.Status),
		Currency:       m.Currency,
		Subtotal:       types.Money{Amount: m.SubtotalAmountCents, Currency: m.SubtotalCurrency},
		TaxAmount:      types.Money{Amount: m.TaxAmountCents, Currency: m.TaxCurrency},
		DiscountAmount: types.Money{Amount: m.DiscountAmountCents, Currency: m.DiscountCurrency},
		Total:          types.Money{Amount: m.TotalAmountCents, Currency: m.TotalCurrency},
		LineItems:      lineItems,
		PeriodStart:    m.PeriodStart,
		PeriodEnd:      m.PeriodEnd,
		DueDate:        m.DueDate,
		PaidAt:         m.PaidAt,
		VoidedAt:       m.VoidedAt,
		VoidReason:     m.VoidReason,
		PaymentRef:     m.PaymentRef,
		ProviderID:     m.ProviderID,
		AppID:          m.AppID,
		Metadata:       m.Metadata,
	}, nil
}

// ==================== Coupon models ====================

type couponModel struct {
	grove.BaseModel `grove:"table:ledger_coupons"`

	ID             string            `grove:"id,pk"`
	Code           string            `grove:"code"`
	Name           string            `grove:"name"`
	Type           string            `grove:"type"`
	AmountCents    int64             `grove:"amount_cents"`
	AmountCurrency string            `grove:"amount_currency"`
	Percentage     int               `grove:"percentage"`
	Currency       string            `grove:"currency"`
	MaxRedemptions int               `grove:"max_redemptions"`
	TimesRedeemed  int               `grove:"times_redeemed"`
	ValidFrom      *time.Time        `grove:"valid_from"`
	ValidUntil     *time.Time        `grove:"valid_until"`
	AppID          string            `grove:"app_id"`
	Metadata       map[string]string `grove:"metadata,type:jsonb"`
	CreatedAt      time.Time         `grove:"created_at"`
	UpdatedAt      time.Time         `grove:"updated_at"`
}

func toCouponModel(c *coupon.Coupon) *couponModel {
	return &couponModel{
		ID:             c.ID.String(),
		Code:           c.Code,
		Name:           c.Name,
		Type:           string(c.Type),
		AmountCents:    c.Amount.Amount,
		AmountCurrency: c.Amount.Currency,
		Percentage:     c.Percentage,
		Currency:       c.Currency,
		MaxRedemptions: c.MaxRedemptions,
		TimesRedeemed:  c.TimesRedeemed,
		ValidFrom:      c.ValidFrom,
		ValidUntil:     c.ValidUntil,
		AppID:          c.AppID,
		Metadata:       c.Metadata,
		CreatedAt:      c.CreatedAt,
		UpdatedAt:      c.UpdatedAt,
	}
}

func fromCouponModel(m *couponModel) (*coupon.Coupon, error) {
	couponID, err := id.ParseCouponID(m.ID)
	if err != nil {
		return nil, err
	}

	return &coupon.Coupon{
		Entity: types.Entity{
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		},
		ID:             couponID,
		Code:           m.Code,
		Name:           m.Name,
		Type:           coupon.CouponType(m.Type),
		Amount:         types.Money{Amount: m.AmountCents, Currency: m.AmountCurrency},
		Percentage:     m.Percentage,
		Currency:       m.Currency,
		MaxRedemptions: m.MaxRedemptions,
		TimesRedeemed:  m.TimesRedeemed,
		ValidFrom:      m.ValidFrom,
		ValidUntil:     m.ValidUntil,
		AppID:          m.AppID,
		Metadata:       m.Metadata,
	}, nil
}
