package mongo

import (
	"fmt"
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

	ID          string            `grove:"id,pk"       bson:"_id"`
	Name        string            `grove:"name"        bson:"name"`
	Slug        string            `grove:"slug"        bson:"slug"`
	Description string            `grove:"description" bson:"description"`
	Currency    string            `grove:"currency"    bson:"currency"`
	Status      string            `grove:"status"      bson:"status"`
	TrialDays   int               `grove:"trial_days"  bson:"trial_days"`
	Features    []featureModel    `grove:"features"    bson:"features"`
	Pricing     *pricingModel     `grove:"pricing"     bson:"pricing,omitempty"`
	AppID       string            `grove:"app_id"      bson:"app_id"`
	Metadata    map[string]string `grove:"metadata"    bson:"metadata,omitempty"`
	CreatedAt   time.Time         `grove:"created_at"  bson:"created_at"`
	UpdatedAt   time.Time         `grove:"updated_at"  bson:"updated_at"`
}

type featureModel struct {
	ID        string            `bson:"id"`
	Key       string            `bson:"key"`
	Name      string            `bson:"name"`
	Type      string            `bson:"type"`
	Limit     int64             `bson:"limit"`
	Period    string            `bson:"period"`
	SoftLimit bool              `bson:"soft_limit"`
	Metadata  map[string]string `bson:"metadata,omitempty"`
	CreatedAt time.Time         `bson:"created_at"`
	UpdatedAt time.Time         `bson:"updated_at"`
}

type pricingModel struct {
	ID                 string           `bson:"id"`
	PlanID             string           `bson:"plan_id"`
	BaseAmountCents    int64            `bson:"base_amount_cents"`
	BaseAmountCurrency string           `bson:"base_amount_currency"`
	BillingPeriod      string           `bson:"billing_period"`
	Tiers              []priceTierModel `bson:"tiers,omitempty"`
	CreatedAt          time.Time        `bson:"created_at"`
	UpdatedAt          time.Time        `bson:"updated_at"`
}

type priceTierModel struct {
	FeatureKey         string `bson:"feature_key"`
	Type               string `bson:"type"`
	UpTo               int64  `bson:"up_to"`
	UnitAmountCents    int64  `bson:"unit_amount_cents"`
	UnitAmountCurrency string `bson:"unit_amount_currency"`
	FlatAmountCents    int64  `bson:"flat_amount_cents"`
	FlatAmountCurrency string `bson:"flat_amount_currency"`
	Priority           int    `bson:"priority"`
}

func toPlanModel(p *plan.Plan) *planModel {
	features := make([]featureModel, len(p.Features))
	for i, f := range p.Features {
		features[i] = featureModel{
			ID:        f.ID.String(),
			Key:       f.Key,
			Name:      f.Name,
			Type:      string(f.Type),
			Limit:     f.Limit,
			Period:    string(f.Period),
			SoftLimit: f.SoftLimit,
			Metadata:  f.Metadata,
			CreatedAt: f.CreatedAt,
			UpdatedAt: f.UpdatedAt,
		}
	}

	var pricing *pricingModel
	if p.Pricing != nil {
		tiers := make([]priceTierModel, len(p.Pricing.Tiers))
		for i, t := range p.Pricing.Tiers {
			tiers[i] = priceTierModel{
				FeatureKey:         t.FeatureKey,
				Type:               string(t.Type),
				UpTo:               t.UpTo,
				UnitAmountCents:    t.UnitAmount.Amount,
				UnitAmountCurrency: t.UnitAmount.Currency,
				FlatAmountCents:    t.FlatAmount.Amount,
				FlatAmountCurrency: t.FlatAmount.Currency,
				Priority:           t.Priority,
			}
		}
		pricing = &pricingModel{
			ID:                 p.Pricing.ID.String(),
			PlanID:             p.Pricing.PlanID.String(),
			BaseAmountCents:    p.Pricing.BaseAmount.Amount,
			BaseAmountCurrency: p.Pricing.BaseAmount.Currency,
			BillingPeriod:      string(p.Pricing.BillingPeriod),
			Tiers:              tiers,
			CreatedAt:          p.Pricing.CreatedAt,
			UpdatedAt:          p.Pricing.UpdatedAt,
		}
	}

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

	features := make([]plan.Feature, len(m.Features))
	for i, f := range m.Features {
		fID, err := id.ParseFeatureID(f.ID)
		if err != nil {
			// Use the raw string if parsing fails
			fID, err = id.ParseAny(f.ID)
			if err != nil {
				return nil, fmt.Errorf("failed to parse feature ID %q: %w", f.ID, err)
			}
		}
		features[i] = plan.Feature{
			Entity: types.Entity{
				CreatedAt: f.CreatedAt,
				UpdatedAt: f.UpdatedAt,
			},
			ID:        fID,
			Key:       f.Key,
			Name:      f.Name,
			Type:      plan.FeatureType(f.Type),
			Limit:     f.Limit,
			Period:    plan.Period(f.Period),
			SoftLimit: f.SoftLimit,
			Metadata:  f.Metadata,
		}
	}

	var pricing *plan.Pricing
	if m.Pricing != nil {
		priceID, priceErr := id.ParsePriceID(m.Pricing.ID)
		if priceErr != nil {
			return nil, priceErr
		}
		pID, pErr := id.ParsePlanID(m.Pricing.PlanID)
		if pErr != nil {
			return nil, pErr
		}

		tiers := make([]plan.PriceTier, len(m.Pricing.Tiers))
		for i, t := range m.Pricing.Tiers {
			tiers[i] = plan.PriceTier{
				FeatureKey: t.FeatureKey,
				Type:       plan.TierType(t.Type),
				UpTo:       t.UpTo,
				UnitAmount: types.Money{Amount: t.UnitAmountCents, Currency: t.UnitAmountCurrency},
				FlatAmount: types.Money{Amount: t.FlatAmountCents, Currency: t.FlatAmountCurrency},
				Priority:   t.Priority,
			}
		}
		pricing = &plan.Pricing{
			Entity: types.Entity{
				CreatedAt: m.Pricing.CreatedAt,
				UpdatedAt: m.Pricing.UpdatedAt,
			},
			ID:            priceID,
			PlanID:        pID,
			BaseAmount:    types.Money{Amount: m.Pricing.BaseAmountCents, Currency: m.Pricing.BaseAmountCurrency},
			BillingPeriod: plan.Period(m.Pricing.BillingPeriod),
			Tiers:         tiers,
		}
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

	ID                 string            `grove:"id,pk"                bson:"_id"`
	TenantID           string            `grove:"tenant_id"            bson:"tenant_id"`
	PlanID             string            `grove:"plan_id"              bson:"plan_id"`
	Status             string            `grove:"status"               bson:"status"`
	CurrentPeriodStart time.Time         `grove:"current_period_start" bson:"current_period_start"`
	CurrentPeriodEnd   time.Time         `grove:"current_period_end"   bson:"current_period_end"`
	TrialStart         *time.Time        `grove:"trial_start"          bson:"trial_start,omitempty"`
	TrialEnd           *time.Time        `grove:"trial_end"            bson:"trial_end,omitempty"`
	CanceledAt         *time.Time        `grove:"canceled_at"          bson:"canceled_at,omitempty"`
	CancelAt           *time.Time        `grove:"cancel_at"            bson:"cancel_at,omitempty"`
	EndedAt            *time.Time        `grove:"ended_at"             bson:"ended_at,omitempty"`
	AppID              string            `grove:"app_id"               bson:"app_id"`
	ProviderID         string            `grove:"provider_id"          bson:"provider_id"`
	ProviderName       string            `grove:"provider_name"        bson:"provider_name"`
	Metadata           map[string]string `grove:"metadata"             bson:"metadata,omitempty"`
	CreatedAt          time.Time         `grove:"created_at"           bson:"created_at"`
	UpdatedAt          time.Time         `grove:"updated_at"           bson:"updated_at"`
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

	ID             string            `grove:"id,pk"           bson:"_id"`
	TenantID       string            `grove:"tenant_id"       bson:"tenant_id"`
	AppID          string            `grove:"app_id"          bson:"app_id"`
	FeatureKey     string            `grove:"feature_key"     bson:"feature_key"`
	Quantity       int64             `grove:"quantity"        bson:"quantity"`
	Timestamp      time.Time         `grove:"timestamp"       bson:"timestamp"`
	IdempotencyKey string            `grove:"idempotency_key" bson:"idempotency_key,omitempty"`
	Metadata       map[string]string `grove:"metadata"        bson:"metadata,omitempty"`
	CreatedAt      time.Time         `grove:"created_at"      bson:"created_at"`
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

	CacheKey   string    `grove:"cache_key,pk" bson:"_id"`
	TenantID   string    `grove:"tenant_id"    bson:"tenant_id"`
	AppID      string    `grove:"app_id"       bson:"app_id"`
	FeatureKey string    `grove:"feature_key"  bson:"feature_key"`
	Allowed    bool      `grove:"allowed"      bson:"allowed"`
	Feature    string    `grove:"feature"      bson:"feature"`
	Used       int64     `grove:"used"         bson:"used"`
	Limit      int64     `grove:"cache_limit"  bson:"cache_limit"`
	Remaining  int64     `grove:"remaining"    bson:"remaining"`
	SoftLimit  bool      `grove:"soft_limit"   bson:"soft_limit"`
	Reason     string    `grove:"reason"       bson:"reason"`
	ExpiresAt  time.Time `grove:"expires_at"   bson:"expires_at"`
	CreatedAt  time.Time `grove:"created_at"   bson:"created_at"`
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

	ID                  string            `grove:"id,pk"                bson:"_id"`
	TenantID            string            `grove:"tenant_id"            bson:"tenant_id"`
	SubscriptionID      string            `grove:"subscription_id"      bson:"subscription_id"`
	Status              string            `grove:"status"               bson:"status"`
	Currency            string            `grove:"currency"             bson:"currency"`
	SubtotalAmountCents int64             `grove:"subtotal_amount_cents" bson:"subtotal_amount_cents"`
	SubtotalCurrency    string            `grove:"subtotal_currency"    bson:"subtotal_currency"`
	TaxAmountCents      int64             `grove:"tax_amount_cents"     bson:"tax_amount_cents"`
	TaxCurrency         string            `grove:"tax_currency"         bson:"tax_currency"`
	DiscountAmountCents int64             `grove:"discount_amount_cents" bson:"discount_amount_cents"`
	DiscountCurrency    string            `grove:"discount_currency"    bson:"discount_currency"`
	TotalAmountCents    int64             `grove:"total_amount_cents"   bson:"total_amount_cents"`
	TotalCurrency       string            `grove:"total_currency"       bson:"total_currency"`
	LineItems           []lineItemModel   `grove:"line_items"           bson:"line_items"`
	PeriodStart         time.Time         `grove:"period_start"         bson:"period_start"`
	PeriodEnd           time.Time         `grove:"period_end"           bson:"period_end"`
	DueDate             *time.Time        `grove:"due_date"             bson:"due_date,omitempty"`
	PaidAt              *time.Time        `grove:"paid_at"              bson:"paid_at,omitempty"`
	VoidedAt            *time.Time        `grove:"voided_at"            bson:"voided_at,omitempty"`
	VoidReason          string            `grove:"void_reason"          bson:"void_reason"`
	PaymentRef          string            `grove:"payment_ref"          bson:"payment_ref"`
	ProviderID          string            `grove:"provider_id"          bson:"provider_id"`
	AppID               string            `grove:"app_id"               bson:"app_id"`
	Metadata            map[string]string `grove:"metadata"             bson:"metadata,omitempty"`
	CreatedAt           time.Time         `grove:"created_at"           bson:"created_at"`
	UpdatedAt           time.Time         `grove:"updated_at"           bson:"updated_at"`
}

type lineItemModel struct {
	ID                 string            `bson:"id"`
	InvoiceID          string            `bson:"invoice_id"`
	FeatureKey         string            `bson:"feature_key,omitempty"`
	Description        string            `bson:"description"`
	Quantity           int64             `bson:"quantity"`
	UnitAmountCents    int64             `bson:"unit_amount_cents"`
	UnitAmountCurrency string            `bson:"unit_amount_currency"`
	AmountCents        int64             `bson:"amount_cents"`
	AmountCurrency     string            `bson:"amount_currency"`
	Type               string            `bson:"type"`
	Metadata           map[string]string `bson:"metadata,omitempty"`
}

func toInvoiceModel(inv *invoice.Invoice) *invoiceModel {
	lineItems := make([]lineItemModel, len(inv.LineItems))
	for i, li := range inv.LineItems {
		lineItems[i] = lineItemModel{
			ID:                 li.ID.String(),
			InvoiceID:          li.InvoiceID.String(),
			FeatureKey:         li.FeatureKey,
			Description:        li.Description,
			Quantity:           li.Quantity,
			UnitAmountCents:    li.UnitAmount.Amount,
			UnitAmountCurrency: li.UnitAmount.Currency,
			AmountCents:        li.Amount.Amount,
			AmountCurrency:     li.Amount.Currency,
			Type:               string(li.Type),
			Metadata:           li.Metadata,
		}
	}

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

	lineItems := make([]invoice.LineItem, len(m.LineItems))
	for i, li := range m.LineItems {
		liID, liErr := id.ParseLineItemID(li.ID)
		if liErr != nil {
			return nil, liErr
		}
		iID, iErr := id.ParseInvoiceID(li.InvoiceID)
		if iErr != nil {
			return nil, iErr
		}
		lineItems[i] = invoice.LineItem{
			ID:          liID,
			InvoiceID:   iID,
			FeatureKey:  li.FeatureKey,
			Description: li.Description,
			Quantity:    li.Quantity,
			UnitAmount:  types.Money{Amount: li.UnitAmountCents, Currency: li.UnitAmountCurrency},
			Amount:      types.Money{Amount: li.AmountCents, Currency: li.AmountCurrency},
			Type:        invoice.LineItemType(li.Type),
			Metadata:    li.Metadata,
		}
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

	ID             string            `grove:"id,pk"           bson:"_id"`
	Code           string            `grove:"code"            bson:"code"`
	Name           string            `grove:"name"            bson:"name"`
	Type           string            `grove:"type"            bson:"type"`
	AmountCents    int64             `grove:"amount_cents"    bson:"amount_cents"`
	AmountCurrency string            `grove:"amount_currency" bson:"amount_currency"`
	Percentage     int               `grove:"percentage"      bson:"percentage"`
	Currency       string            `grove:"currency"        bson:"currency"`
	MaxRedemptions int               `grove:"max_redemptions" bson:"max_redemptions"`
	TimesRedeemed  int               `grove:"times_redeemed"  bson:"times_redeemed"`
	ValidFrom      *time.Time        `grove:"valid_from"      bson:"valid_from,omitempty"`
	ValidUntil     *time.Time        `grove:"valid_until"     bson:"valid_until,omitempty"`
	AppID          string            `grove:"app_id"          bson:"app_id"`
	Metadata       map[string]string `grove:"metadata"        bson:"metadata,omitempty"`
	CreatedAt      time.Time         `grove:"created_at"      bson:"created_at"`
	UpdatedAt      time.Time         `grove:"updated_at"      bson:"updated_at"`
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
