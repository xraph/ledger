package dashboard

import (
	"context"

	"github.com/a-h/templ"

	"github.com/xraph/forge/extensions/dashboard/contributor"

	"github.com/xraph/ledger/id"
)

// PluginWidget describes a widget contributed by a ledger plugin.
type PluginWidget struct {
	ID         string
	Title      string
	Size       string // "sm", "md", "lg"
	RefreshSec int
	Render     func(ctx context.Context) templ.Component
}

// PluginPage describes an extra page route contributed by a plugin.
type PluginPage struct {
	Route  string // e.g. "/providers"
	Label  string // nav label
	Icon   string // lucide icon name
	Render func(ctx context.Context) templ.Component
}

// DashboardPlugin is optionally implemented by ledger plugins
// to contribute UI sections to the ledger dashboard contributor.
type DashboardPlugin interface {
	// DashboardWidgets returns widgets this plugin contributes.
	DashboardWidgets(ctx context.Context) []PluginWidget
	// DashboardSettingsPanel returns a settings templ component, or nil.
	DashboardSettingsPanel(ctx context.Context) templ.Component
	// DashboardPages returns extra page routes this plugin handles.
	DashboardPages() []PluginPage
}

// SubscriptionDetailContributor is optionally implemented by plugins that want
// to contribute a section to the subscription detail page.
type SubscriptionDetailContributor interface {
	DashboardSubscriptionDetailSection(ctx context.Context, subID id.SubscriptionID) templ.Component
}

// PlanDetailContributor is optionally implemented by plugins that want
// to contribute a section to the plan detail page.
type PlanDetailContributor interface {
	DashboardPlanDetailSection(ctx context.Context, planID id.PlanID) templ.Component
}

// InvoiceDetailContributor is optionally implemented by plugins that want
// to contribute a section to the invoice detail page.
type InvoiceDetailContributor interface {
	DashboardInvoiceDetailSection(ctx context.Context, invID id.InvoiceID) templ.Component
}

// DashboardPageContributor is an enhanced interface for plugins that need
// access to route parameters when rendering dashboard pages.
type DashboardPageContributor interface {
	DashboardNavItems() []contributor.NavItem
	DashboardRenderPage(ctx context.Context, route string, params contributor.Params) (templ.Component, error)
}
