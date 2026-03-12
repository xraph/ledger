package pages

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/feature"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/provider"
	"github.com/xraph/ledger/subscription"
	"github.com/xraph/ledger/types"
)

// formatTimeAgo returns a human-readable relative time string.
func formatTimeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%dy ago", int(d.Hours()/(24*365)))
	}
}

// truncateString shortens s to maxLen characters and appends "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// formatLimit returns a display string for a feature limit.
func formatLimit(limit int64) string {
	if limit == -1 {
		return "Unlimited"
	}
	return fmt.Sprintf("%d", limit)
}

// OverviewStats holds aggregate stats for the overview page.
type OverviewStats struct {
	TotalPlans          int
	ActiveSubscriptions int
	PendingInvoices     int
	ActiveCoupons       int
}

// PlanDetailData holds all data for the plan detail view.
type PlanDetailData struct {
	Plan         *plan.Plan
	HasProviders bool
	SyncResult   *provider.SyncResult
	SyncError    string
}

// SubscriptionDetailData holds all data for the subscription detail view.
type SubscriptionDetailData struct {
	Subscription *subscription.Subscription
	Plan         *plan.Plan
	Invoices     []*invoice.Invoice
	HasProviders bool
	SyncResult   *provider.SyncResult
	SyncError    string
}

// InvoiceDetailData holds all data for the invoice detail view.
type InvoiceDetailData struct {
	Invoice      *invoice.Invoice
	Subscription *subscription.Subscription
	HasProviders bool
	SyncResult   *provider.SyncResult
	SyncError    string
}

// SettingsPageData holds data for the settings page.
type SettingsPageData struct {
	MeterBatchSize      int
	MeterFlushInterval  string
	EntitlementCacheTTL string
	HasProviders        bool
	ProviderNames       []string
}

// PaymentMethodsData holds all data for the payment methods page.
type PaymentMethodsData struct {
	Methods      []provider.PaymentMethod
	TenantID     string
	HasProviders bool
	Error        string
}

// PlanFormData holds all data for the plan create/edit form page.
type PlanFormData struct {
	Plan   *plan.Plan // nil for create, populated for edit
	IsEdit bool
	Error  string // validation/submission error message
	AppID  string // default app ID from contributor config
}

// CouponFormData holds all data for the coupon create/edit form page.
type CouponFormData struct {
	Coupon *coupon.Coupon // nil for create, populated for edit
	IsEdit bool
	Error  string // validation/submission error message
	AppID  string // default app ID from contributor config
}

// SubscriptionFormData holds all data for the subscription create form page.
type SubscriptionFormData struct {
	Plans []*plan.Plan // available plans for selection
	Error string       // validation/submission error message
	AppID string       // default app ID from contributor config
}

// FeatureListData holds all data for the feature catalog list page.
type FeatureListData struct {
	Features []*feature.Feature
	AppID    string
}

// FeatureDetailData holds all data for the feature catalog detail view.
type FeatureDetailData struct {
	Feature      *feature.Feature
	HasProviders bool
	SyncResult   *provider.SyncResult
	SyncError    string
}

// FeatureFormData holds all data for the feature catalog create/edit form page.
type FeatureFormData struct {
	Feature *feature.Feature // nil for create, populated for edit
	IsEdit  bool
	Error   string
	AppID   string
}

// featureStatusClass returns a CSS-friendly class name for a feature status.
func featureStatusClass(status feature.Status) string {
	switch status {
	case feature.StatusActive:
		return "default"
	case feature.StatusDraft:
		return "secondary"
	case feature.StatusArchived:
		return "outline"
	default:
		return "secondary"
	}
}

// featureTypeLabel returns a display label for a feature type.
func featureTypeLabel(t feature.FeatureType) string {
	switch t {
	case feature.FeatureMetered:
		return "Metered"
	case feature.FeatureBoolean:
		return "Boolean"
	case feature.FeatureSeat:
		return "Seat"
	default:
		return string(t)
	}
}

// ─── Form Action URL Helpers ─────────────────────────────────────────────────

// planFormAction returns the correct hx-post URL for the plan form.
func planFormAction(data PlanFormData) string {
	if data.IsEdit && data.Plan != nil {
		return "../plans/edit?id=" + data.Plan.ID.String()
	}
	return "../plans/new"
}

// couponFormAction returns the correct hx-post URL for the coupon form.
func couponFormAction(data CouponFormData) string {
	if data.IsEdit && data.Coupon != nil {
		return "../coupons/edit?id=" + data.Coupon.ID.String()
	}
	return "../coupons/new"
}

// featureFormAction returns the correct hx-post URL for the feature form.
func featureFormAction(data FeatureFormData) string {
	if data.IsEdit && data.Feature != nil {
		return "../features/edit?id=" + data.Feature.ID.String()
	}
	return "../features/new"
}

// ─── Feature Catalog Form Parsing Helpers ────────────────────────────────────

// ParseFeatureFromFormData constructs a *feature.Feature from form data.
func ParseFeatureFromFormData(fd map[string]string) (*feature.Feature, error) {
	f := &feature.Feature{
		Key:         strings.TrimSpace(fd["key"]),
		Name:        strings.TrimSpace(fd["name"]),
		Description: strings.TrimSpace(fd["description"]),
		Type:        feature.FeatureType(strings.TrimSpace(fd["type"])),
		Period:      feature.Period(strings.TrimSpace(fd["period"])),
		Status:      feature.Status(strings.TrimSpace(fd["status"])),
		AppID:       strings.TrimSpace(fd["app_id"]),
	}

	if f.Key == "" {
		return nil, fmt.Errorf("feature key is required")
	}
	if f.Name == "" {
		return nil, fmt.Errorf("feature name is required")
	}
	if f.Type == "" {
		f.Type = feature.FeatureMetered
	}
	if f.Period == "" {
		f.Period = feature.PeriodMonthly
	}
	if f.Status == "" {
		f.Status = feature.StatusDraft
	}

	// Default limit
	if v := fd["default_limit"]; v != "" {
		limit, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid default limit: %w", err)
		}
		f.DefaultLimit = limit
	}

	// Boolean features default to limit=1 (enabled)
	if f.Type == feature.FeatureBoolean && f.DefaultLimit == 0 {
		f.DefaultLimit = 1
	}

	// Soft limit
	if fd["soft_limit"] == "on" || fd["soft_limit"] == "true" {
		f.SoftLimit = true
	}

	// Metadata (JSON)
	if metaJSON := fd["metadata_json"]; metaJSON != "" {
		metadata, err := parseMetadataJSON(metaJSON)
		if err != nil {
			return nil, fmt.Errorf("invalid metadata: %w", err)
		}
		if len(metadata) > 0 {
			f.Metadata = metadata
		}
	}

	return f, nil
}

// ─── Plan Form Parsing Helpers ───────────────────────────────────────────────

// parsePlanFromFormData constructs a *plan.Plan from form data.
// Features, tiers, and metadata are passed as JSON strings via hidden inputs.
func ParsePlanFromFormData(fd map[string]string) (*plan.Plan, error) {
	p := &plan.Plan{
		Name:        strings.TrimSpace(fd["name"]),
		Slug:        strings.TrimSpace(fd["slug"]),
		Description: strings.TrimSpace(fd["description"]),
		Currency:    strings.TrimSpace(fd["currency"]),
		Status:      plan.Status(strings.TrimSpace(fd["status"])),
		AppID:       strings.TrimSpace(fd["app_id"]),
	}

	if p.Name == "" {
		return nil, fmt.Errorf("plan name is required")
	}
	if p.Slug == "" {
		return nil, fmt.Errorf("plan slug is required")
	}
	if p.Currency == "" {
		p.Currency = "usd"
	}
	if p.Status == "" {
		p.Status = plan.StatusDraft
	}

	// Trial days
	if v := fd["trial_days"]; v != "" {
		td, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid trial days: %w", err)
		}
		p.TrialDays = td
	}

	// Features (JSON)
	if featJSON := fd["features_json"]; featJSON != "" {
		features, err := parseFeaturesJSON(featJSON)
		if err != nil {
			return nil, fmt.Errorf("invalid features: %w", err)
		}
		p.Features = features
	}

	// Pricing
	baseAmountStr := fd["base_amount"]
	billingPeriod := fd["billing_period"]
	tiersJSON := fd["tiers_json"]

	if baseAmountStr != "" || billingPeriod != "" || tiersJSON != "" {
		pricing := &plan.Pricing{
			BillingPeriod: plan.Period(billingPeriod),
		}
		if pricing.BillingPeriod == "" {
			pricing.BillingPeriod = plan.PeriodMonthly
		}

		if baseAmountStr != "" {
			amount, err := strconv.ParseInt(baseAmountStr, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid base amount: %w", err)
			}
			pricing.BaseAmount = types.Money{Amount: amount, Currency: p.Currency}
		}

		if tiersJSON != "" {
			tiers, err := parseTiersJSON(tiersJSON, p.Currency)
			if err != nil {
				return nil, fmt.Errorf("invalid pricing tiers: %w", err)
			}
			pricing.Tiers = tiers
		}

		p.Pricing = pricing
	}

	// Metadata (JSON)
	if metaJSON := fd["metadata_json"]; metaJSON != "" {
		metadata, err := parseMetadataJSON(metaJSON)
		if err != nil {
			return nil, fmt.Errorf("invalid metadata: %w", err)
		}
		if len(metadata) > 0 {
			p.Metadata = metadata
		}
	}

	return p, nil
}

// featureJSON is the JSON shape for a feature from the form.
type featureJSON struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Limit     string `json:"limit"`
	Period    string `json:"period"`
	SoftLimit bool   `json:"soft_limit"`
}

// parseFeaturesJSON parses the features_json hidden input.
func parseFeaturesJSON(jsonStr string) ([]plan.Feature, error) {
	var raw []featureJSON
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return nil, err
	}

	features := make([]plan.Feature, 0, len(raw))
	for _, r := range raw {
		if r.Key == "" {
			continue // skip empty rows
		}

		f := plan.Feature{
			Key:       r.Key,
			Name:      r.Name,
			Type:      plan.FeatureType(r.Type),
			Period:    plan.Period(r.Period),
			SoftLimit: r.SoftLimit,
		}

		if f.Type == "" {
			f.Type = plan.FeatureMetered
		}
		if f.Period == "" {
			f.Period = plan.PeriodMonthly
		}

		// Parse limit
		if r.Limit != "" {
			limit, err := strconv.ParseInt(r.Limit, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid limit for feature %q: %w", r.Key, err)
			}
			f.Limit = limit
		}

		// Boolean features default to limit=1 (enabled)
		if f.Type == plan.FeatureBoolean && f.Limit == 0 {
			f.Limit = 1
		}

		features = append(features, f)
	}

	return features, nil
}

// tierJSON is the JSON shape for a pricing tier from the form.
type tierJSON struct {
	FeatureKey string `json:"feature_key"`
	Type       string `json:"type"`
	UpTo       string `json:"up_to"`
	UnitAmount string `json:"unit_amount"`
	FlatAmount string `json:"flat_amount"`
	Priority   string `json:"priority"`
}

// parseTiersJSON parses the tiers_json hidden input.
func parseTiersJSON(jsonStr, currency string) ([]plan.PriceTier, error) {
	var raw []tierJSON
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return nil, err
	}

	tiers := make([]plan.PriceTier, 0, len(raw))
	for i, r := range raw {
		if r.FeatureKey == "" {
			continue // skip empty rows
		}

		t := plan.PriceTier{
			FeatureKey: r.FeatureKey,
			Type:       plan.TierType(r.Type),
		}

		if t.Type == "" {
			t.Type = plan.TierGraduated
		}

		if r.UpTo != "" {
			upTo, err := strconv.ParseInt(r.UpTo, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid up_to for tier %d: %w", i, err)
			}
			t.UpTo = upTo
		}

		if r.UnitAmount != "" {
			amt, err := strconv.ParseInt(r.UnitAmount, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid unit_amount for tier %d: %w", i, err)
			}
			t.UnitAmount = types.Money{Amount: amt, Currency: currency}
		}

		if r.FlatAmount != "" {
			amt, err := strconv.ParseInt(r.FlatAmount, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid flat_amount for tier %d: %w", i, err)
			}
			t.FlatAmount = types.Money{Amount: amt, Currency: currency}
		}

		if r.Priority != "" {
			priority, err := strconv.Atoi(r.Priority)
			if err != nil {
				return nil, fmt.Errorf("invalid priority for tier %d: %w", i, err)
			}
			t.Priority = priority
		} else {
			t.Priority = i
		}

		tiers = append(tiers, t)
	}

	return tiers, nil
}

// metadataJSON is the JSON shape for a metadata entry from the form.
type metadataJSON struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// parseMetadataJSON parses the metadata_json hidden input.
func parseMetadataJSON(jsonStr string) (map[string]string, error) {
	var raw []metadataJSON
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return nil, err
	}

	result := make(map[string]string, len(raw))
	for _, r := range raw {
		if r.Key != "" {
			result[r.Key] = r.Value
		}
	}

	return result, nil
}

// ─── Coupon Form Parsing Helpers ─────────────────────────────────────────────

// ParseCouponFromFormData constructs a *coupon.Coupon from form data.
func ParseCouponFromFormData(fd map[string]string) (*coupon.Coupon, error) {
	c := &coupon.Coupon{
		Code:     strings.TrimSpace(fd["code"]),
		Name:     strings.TrimSpace(fd["name"]),
		Type:     coupon.CouponType(strings.TrimSpace(fd["type"])),
		Currency: strings.TrimSpace(fd["currency"]),
	}

	if c.Code == "" {
		return nil, fmt.Errorf("coupon code is required")
	}
	if c.Name == "" {
		return nil, fmt.Errorf("coupon name is required")
	}
	if c.Type == "" {
		c.Type = coupon.CouponTypePercentage
	}
	if c.Currency == "" {
		c.Currency = "usd"
	}

	// Percentage
	if v := fd["percentage"]; v != "" {
		pct, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid percentage: %w", err)
		}
		c.Percentage = pct
	}

	// Amount (in cents)
	if v := fd["amount"]; v != "" {
		amt, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid amount: %w", err)
		}
		c.Amount = types.Money{Amount: amt, Currency: c.Currency}
	}

	// Max redemptions
	if v := fd["max_redemptions"]; v != "" {
		mr, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid max redemptions: %w", err)
		}
		c.MaxRedemptions = mr
	}

	return c, nil
}

// ─── Subscription Form Parsing Helpers ───────────────────────────────────────

// ParseSubscriptionFromFormData constructs a *subscription.Subscription from form data.
func ParseSubscriptionFromFormData(fd map[string]string) (*subscription.Subscription, error) {
	sub := &subscription.Subscription{
		TenantID: strings.TrimSpace(fd["tenant_id"]),
		Status:   subscription.Status(strings.TrimSpace(fd["status"])),
	}

	if sub.TenantID == "" {
		return nil, fmt.Errorf("tenant ID is required")
	}

	planIDStr := strings.TrimSpace(fd["plan_id"])
	if planIDStr == "" {
		return nil, fmt.Errorf("plan selection is required")
	}

	planID, err := id.ParsePlanID(planIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid plan ID: %w", err)
	}
	sub.PlanID = planID

	if sub.Status == "" {
		sub.Status = subscription.StatusActive
	}

	return sub, nil
}
