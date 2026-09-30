package contract

import (
	"context"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/subscription"
)

// statsScanLimit bounds each count, because the store has no count methods.
const statsScanLimit = 5000

// recentInvoicesDefault and recentInvoicesMax bound overview.recentInvoices.
const (
	recentInvoicesDefault = 10
	recentInvoicesMax     = 50
)

func registerOverview(b *binder) {
	query(b, "overview.stats", overviewStats)
	query(b, "overview.recentInvoices", overviewRecentInvoices)
	// settings.detail reports configuration and reads no app data, so it is
	// the one intent besides the feature catalog that runs without an app.
	platformQuery(b, "settings.detail", settingsDetailFor(b.deps))
}

type OverviewStats struct {
	Plans                 int            `json:"plans"`
	ActivePlans           int            `json:"active_plans"`
	SubscriptionsByStatus map[string]int `json:"subscriptions_by_status"`
	PendingInvoices       int            `json:"pending_invoices"`
	PastDueInvoices       int            `json:"past_due_invoices"`
	Coupons               int            `json:"coupons"`
	// Capped is true when a count reached the scan bound; the counts are then
	// lower bounds and the page must say so.
	Capped bool `json:"capped"`
}

// bounded trims rows that a scan of statsScanLimit+1 returned, and reports
// whether the bound was hit.
func bounded[T any](rows []T) ([]T, bool) {
	if len(rows) > statsScanLimit {
		return rows[:statsScanLimit], true
	}
	return rows, false
}

func overviewStats(ctx context.Context, eng *ledger.Ledger, sc scope, _ struct{}) (OverviewStats, error) {
	st := eng.Store()
	out := OverviewStats{SubscriptionsByStatus: map[string]int{}}

	plans, err := st.ListPlans(ctx, sc.AppID, plan.ListOpts{Limit: statsScanLimit + 1})
	if err != nil {
		return OverviewStats{}, err
	}
	plans, capped := bounded(plans)
	out.Capped = out.Capped || capped
	out.Plans = len(plans)
	for _, p := range plans {
		if p.Status == plan.StatusActive {
			out.ActivePlans++
		}
	}

	subs, err := st.ListSubscriptions(ctx, "", sc.AppID, subscription.ListOpts{Limit: statsScanLimit + 1})
	if err != nil {
		return OverviewStats{}, err
	}
	subs, capped = bounded(subs)
	out.Capped = out.Capped || capped
	for _, s := range subs {
		out.SubscriptionsByStatus[string(s.Status)]++
	}

	// ListPendingInvoices takes no limit, so it is trimmed after the fact.
	pending, err := st.ListPendingInvoices(ctx, sc.AppID)
	if err != nil {
		return OverviewStats{}, err
	}
	pending, capped = bounded(pending)
	out.Capped = out.Capped || capped
	out.PendingInvoices = len(pending)

	// Past-due invoices are counted apart: the lifecycle clock moves overdue
	// invoices out of pending, so without this they would vanish from the
	// overview.
	pastDue, err := st.ListInvoices(ctx, "", sc.AppID, invoice.ListOpts{Status: invoice.StatusPastDue, Limit: statsScanLimit + 1})
	if err != nil {
		return OverviewStats{}, err
	}
	pastDue, capped = bounded(pastDue)
	out.Capped = out.Capped || capped
	out.PastDueInvoices = len(pastDue)

	coupons, err := st.ListCoupons(ctx, sc.AppID, coupon.ListOpts{Limit: statsScanLimit + 1})
	if err != nil {
		return OverviewStats{}, err
	}
	coupons, capped = bounded(coupons)
	out.Capped = out.Capped || capped
	out.Coupons = len(coupons)

	return out, nil
}

type RecentInvoicesInput struct {
	Limit int `json:"limit"`
}

func overviewRecentInvoices(ctx context.Context, eng *ledger.Ledger, sc scope, in RecentInvoicesInput) ([]*invoice.Invoice, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = recentInvoicesDefault
	}
	if limit > recentInvoicesMax {
		limit = recentInvoicesMax
	}
	rows, err := eng.Store().ListInvoices(ctx, "", sc.AppID, invoice.ListOpts{Limit: limit})
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []*invoice.Invoice{}
	}
	for _, inv := range rows {
		withLineItems(inv)
	}
	return rows, nil
}

type SettingsDetail struct {
	SettingsView
	AppID           string   `json:"app_id"`
	RequireAppClaim bool     `json:"require_app_claim"`
	Providers       []string `json:"providers"`
	InvoiceFormats  []string `json:"invoice_formats"`
}

// settingsDetailFor reports the engine's configuration as it is running. The
// templ settings page showed three hardcoded literals; this reads the values.
// It needs the Deps, which a handlerFn does not receive, so it closes over them.
func settingsDetailFor(deps Deps) handlerFn[struct{}, SettingsDetail] {
	return func(_ context.Context, eng *ledger.Ledger, _ scope, _ struct{}) (SettingsDetail, error) {
		var view SettingsView
		if deps.Settings != nil {
			view = deps.Settings()
		}
		return SettingsDetail{
			SettingsView:    view,
			AppID:           deps.AppID,
			RequireAppClaim: deps.RequireAppClaim,
			Providers:       eng.ProviderNames(),
			InvoiceFormats:  eng.InvoiceFormats(),
		}, nil
	}
}
