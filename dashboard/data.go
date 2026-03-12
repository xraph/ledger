package dashboard

import (
	"context"
	"fmt"
	"time"

	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/store"
	"github.com/xraph/ledger/subscription"
)

// fetchPlanStats returns plan counts for the given app.
func fetchPlanStats(ctx context.Context, s store.Store, appID string) (total, active int, err error) {
	plans, err := s.ListPlans(ctx, appID, plan.ListOpts{Limit: 1000})
	if err != nil {
		return 0, 0, fmt.Errorf("dashboard: fetch plan stats: %w", err)
	}
	total = len(plans)
	for _, p := range plans {
		if p.Status == plan.StatusActive {
			active++
		}
	}
	return total, active, nil
}

// fetchSubscriptionStats returns subscription counts for the given app.
func fetchSubscriptionStats(ctx context.Context, s store.Store, appID string) (total, active, trialing int, err error) {
	subs, err := s.ListSubscriptions(ctx, "", appID, subscription.ListOpts{Limit: 1000})
	if err != nil {
		return 0, 0, 0, fmt.Errorf("dashboard: fetch subscription stats: %w", err)
	}
	total = len(subs)
	for _, sub := range subs {
		switch sub.Status {
		case subscription.StatusActive:
			active++
		case subscription.StatusTrialing:
			trialing++
		}
	}
	return total, active, trialing, nil
}

// fetchInvoiceStats returns invoice counts for the given app.
func fetchInvoiceStats(ctx context.Context, s store.Store, appID string) (pending int, err error) {
	invoices, err := s.ListPendingInvoices(ctx, appID)
	if err != nil {
		return 0, fmt.Errorf("dashboard: fetch invoice stats: %w", err)
	}
	return len(invoices), nil
}

// fetchPlans returns plans for the given app.
func fetchPlans(ctx context.Context, s store.Store, appID string, opts plan.ListOpts) ([]*plan.Plan, error) {
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	plans, err := s.ListPlans(ctx, appID, opts)
	if err != nil {
		return nil, fmt.Errorf("dashboard: fetch plans: %w", err)
	}
	return plans, nil
}

// fetchSubscriptions returns subscriptions for the given app.
func fetchSubscriptions(ctx context.Context, s store.Store, tenantID, appID string, opts subscription.ListOpts) ([]*subscription.Subscription, error) {
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	subs, err := s.ListSubscriptions(ctx, tenantID, appID, opts)
	if err != nil {
		return nil, fmt.Errorf("dashboard: fetch subscriptions: %w", err)
	}
	return subs, nil
}

// fetchInvoices returns invoices for the given app.
func fetchInvoices(ctx context.Context, s store.Store, tenantID, appID string, opts invoice.ListOpts) ([]*invoice.Invoice, error) {
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	invoices, err := s.ListInvoices(ctx, tenantID, appID, opts)
	if err != nil {
		return nil, fmt.Errorf("dashboard: fetch invoices: %w", err)
	}
	return invoices, nil
}

// fetchCoupons returns coupons for the given app.
func fetchCoupons(ctx context.Context, s store.Store, appID string, opts coupon.ListOpts) ([]*coupon.Coupon, error) {
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	coupons, err := s.ListCoupons(ctx, appID, opts)
	if err != nil {
		return nil, fmt.Errorf("dashboard: fetch coupons: %w", err)
	}
	return coupons, nil
}

// fetchUsageEvents returns usage events for the given tenant/app.
func fetchUsageEvents(ctx context.Context, s store.Store, tenantID, appID string, opts meter.QueryOpts) ([]*meter.UsageEvent, error) {
	events, err := s.QueryUsage(ctx, tenantID, appID, opts)
	if err != nil {
		return nil, fmt.Errorf("dashboard: fetch usage events: %w", err)
	}
	return events, nil
}

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

// truncateString shortens s to max characters and appends "..." if truncated.
func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
