package dashboard

import (
	"github.com/xraph/forge/extensions/dashboard/contributor"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/plugin"
)

// NewManifest builds a contributor.Manifest for the ledger dashboard.
// It defines core navigation, widgets, and settings, then merges any
// additional contributions from plugins implementing DashboardPlugin.
func NewManifest(engine *ledger.Ledger, plugins []plugin.Plugin) *contributor.Manifest {
	m := &contributor.Manifest{
		Name:        "ledger",
		DisplayName: "Ledger",
		Icon:        "receipt",
		Version:     "0.1.0",
		Layout:      "extension",
		ShowSidebar: boolPtr(true),
		TopbarConfig: &contributor.TopbarConfig{
			Title:       "Ledger",
			LogoIcon:    "receipt",
			AccentColor: "#10b981",
			ShowSearch:  true,
			Actions: []contributor.TopbarAction{
				{Label: "API Docs", Icon: "file-text", Href: "/docs", Variant: "ghost"},
			},
		},
		Nav:      baseNav(),
		Widgets:  baseWidgets(),
		Settings: baseSettings(),
		Capabilities: []string{
			"searchable",
		},
	}

	// Merge plugin-contributed nav items and widgets.
	for _, p := range plugins {
		if dpc, ok := p.(DashboardPageContributor); ok {
			m.Nav = append(m.Nav, dpc.DashboardNavItems()...)
		}

		dp, ok := p.(DashboardPlugin)
		if !ok {
			continue
		}

		for _, pp := range dp.DashboardPages() {
			m.Nav = append(m.Nav, contributor.NavItem{
				Label:    pp.Label,
				Path:     pp.Route,
				Icon:     pp.Icon,
				Group:    "Ledger",
				Priority: 10,
			})
		}

		for _, pw := range dp.DashboardWidgets(nil) {
			m.Widgets = append(m.Widgets, contributor.WidgetDescriptor{
				ID:         pw.ID,
				Title:      pw.Title,
				Size:       pw.Size,
				RefreshSec: pw.RefreshSec,
				Group:      "Ledger",
			})
		}
	}

	return m
}

// baseNav returns the core navigation items for the ledger dashboard.
func baseNav() []contributor.NavItem {
	return []contributor.NavItem{
		// Ledger
		{Label: "Overview", Path: "/", Icon: "layout-dashboard", Group: "Ledger", Priority: 0},

		// Billing
		{Label: "Plans", Path: "/plans", Icon: "layers", Group: "Billing", Priority: 0},
		{Label: "Subscriptions", Path: "/subscriptions", Icon: "credit-card", Group: "Billing", Priority: 1},
		{Label: "Invoices", Path: "/invoices", Icon: "file-text", Group: "Billing", Priority: 2},
		{Label: "Coupons", Path: "/coupons", Icon: "ticket", Group: "Billing", Priority: 3},
		{Label: "Features", Path: "/features", Icon: "puzzle-piece", Group: "Billing", Priority: 4},
		{Label: "Payment Methods", Path: "/payment-methods", Icon: "wallet", Group: "Billing", Priority: 5},

		// Metering
		{Label: "Usage", Path: "/usage", Icon: "bar-chart-3", Group: "Metering", Priority: 0},

		// Configuration
		{Label: "Settings", Path: "/settings", Icon: "settings", Group: "Configuration", Priority: 0},
	}
}

// baseWidgets returns the core widget descriptors for the ledger dashboard.
func baseWidgets() []contributor.WidgetDescriptor {
	return []contributor.WidgetDescriptor{
		{
			ID:          "ledger-stats",
			Title:       "Billing Stats",
			Description: "Plans, subscriptions, and invoice counts",
			Size:        "md",
			RefreshSec:  60,
			Group:       "Ledger",
		},
		{
			ID:          "ledger-recent-invoices",
			Title:       "Recent Invoices",
			Description: "Latest generated invoices",
			Size:        "md",
			RefreshSec:  30,
			Group:       "Ledger",
		},
	}
}

// baseSettings returns the core settings descriptors for the ledger dashboard.
func baseSettings() []contributor.SettingsDescriptor {
	return []contributor.SettingsDescriptor{
		{
			ID:          "ledger-config",
			Title:       "Billing Settings",
			Description: "Configure billing behavior",
			Group:       "Ledger",
			Icon:        "receipt",
		},
	}
}

func boolPtr(b bool) *bool { return &b }
