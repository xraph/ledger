package dashboard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/a-h/templ"

	"github.com/xraph/forge/extensions/dashboard/contributor"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/dashboard/components"
	"github.com/xraph/ledger/dashboard/pages"
	"github.com/xraph/ledger/dashboard/widgets"
	"github.com/xraph/ledger/feature"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/plugin"
	"github.com/xraph/ledger/store"
	"github.com/xraph/ledger/subscription"
)

// Ensure Contributor implements the required interfaces at compile time.
var _ contributor.LocalContributor = (*Contributor)(nil)

// Contributor implements the dashboard LocalContributor interface for the
// ledger extension. It renders pages, widgets, and settings using templ
// components and ForgeUI.
type Contributor struct {
	manifest   *contributor.Manifest
	engine     *ledger.Ledger
	store      store.Store
	plugins    []plugin.Plugin
	appID      string
	pageRoutes map[string]bool
}

// New creates a new ledger dashboard contributor.
func New(manifest *contributor.Manifest, engine *ledger.Ledger, s store.Store, plugins []plugin.Plugin, appID string) *Contributor {
	c := &Contributor{
		manifest: manifest,
		engine:   engine,
		store:    s,
		plugins:  plugins,
		appID:    appID,
	}
	c.pageRoutes = c.buildPageRoutes()
	return c
}

// buildPageRoutes merges core knownPageRoutes with routes contributed by plugins.
func (c *Contributor) buildPageRoutes() map[string]bool {
	routes := make(map[string]bool, len(knownPageRoutes))
	for k, v := range knownPageRoutes {
		routes[k] = v
	}
	for _, p := range c.plugins {
		if dpc, ok := p.(DashboardPageContributor); ok {
			for _, nav := range dpc.DashboardNavItems() {
				routes[nav.Path] = true
			}
		}
		if dp, ok := p.(DashboardPlugin); ok {
			for _, pp := range dp.DashboardPages() {
				routes[pp.Route] = true
			}
		}
	}
	return routes
}

// Manifest returns the contributor manifest.
func (c *Contributor) Manifest() *contributor.Manifest { return c.manifest }

// RenderPage renders a page for the given route.
func (c *Contributor) RenderPage(ctx context.Context, route string, params contributor.Params) (templ.Component, error) {
	// Check plugin-contributed pages first (DashboardPageContributor).
	for _, p := range c.plugins {
		if dpc, ok := p.(DashboardPageContributor); ok {
			comp, err := dpc.DashboardRenderPage(ctx, route, params)
			if err != nil && !errors.Is(err, contributor.ErrPageNotFound) {
				return nil, err
			}
			if comp != nil {
				return comp, nil
			}
		}
	}

	// Check plugin-contributed pages (DashboardPlugin).
	for _, dp := range c.dashboardPlugins() {
		for _, pp := range dp.DashboardPages() {
			if pp.Route == route {
				return pp.Render(ctx), nil
			}
		}
	}

	switch route {
	case "/", "":
		return c.renderOverview(ctx)
	case "/plans":
		return c.renderPlans(ctx, params)
	case "/plans/detail":
		return c.renderPlanDetail(ctx, params)
	case "/plans/new":
		return c.renderPlanForm(ctx, params, false)
	case "/plans/edit":
		return c.renderPlanForm(ctx, params, true)
	case "/subscriptions":
		return c.renderSubscriptions(ctx, params)
	case "/subscriptions/detail":
		return c.renderSubscriptionDetail(ctx, params)
	case "/subscriptions/new":
		return c.renderSubscriptionForm(ctx, params)
	case "/invoices":
		return c.renderInvoices(ctx, params)
	case "/invoices/detail":
		return c.renderInvoiceDetail(ctx, params)
	case "/coupons":
		return c.renderCoupons(ctx, params)
	case "/coupons/detail":
		return c.renderCouponDetail(ctx, params)
	case "/coupons/new":
		return c.renderCouponForm(ctx, params, false)
	case "/coupons/edit":
		return c.renderCouponForm(ctx, params, true)
	case "/features":
		return c.renderFeatures(ctx, params)
	case "/features/detail":
		return c.renderFeatureDetail(ctx, params)
	case "/features/new":
		return c.renderFeatureForm(ctx, params, false)
	case "/features/edit":
		return c.renderFeatureForm(ctx, params, true)
	case "/usage":
		return c.renderUsage(ctx, params)
	case "/payment-methods":
		return c.renderPaymentMethods(ctx, params)
	case "/plans/sync":
		return c.handlePlanSync(ctx, params)
	case "/features/sync":
		return c.handleFeatureSync(ctx, params)
	case "/subscriptions/sync":
		return c.handleSubscriptionSync(ctx, params)
	case "/invoices/sync":
		return c.handleInvoiceSync(ctx, params)
	case "/settings":
		return c.renderSettings(ctx)
	default:
		return nil, contributor.ErrPageNotFound
	}
}

// RenderWidget renders a widget by ID.
func (c *Contributor) RenderWidget(ctx context.Context, widgetID string) (templ.Component, error) {
	// Check plugin-contributed widgets first.
	for _, dp := range c.dashboardPlugins() {
		for _, w := range dp.DashboardWidgets(ctx) {
			if w.ID == widgetID {
				return w.Render(ctx), nil
			}
		}
	}

	switch widgetID {
	case "ledger-stats":
		return c.renderStatsWidget(ctx)
	case "ledger-recent-invoices":
		return c.renderRecentInvoicesWidget(ctx)
	default:
		return nil, contributor.ErrWidgetNotFound
	}
}

// RenderSettings renders a settings panel by ID.
func (c *Contributor) RenderSettings(ctx context.Context, settingID string) (templ.Component, error) {
	pluginSettings := c.collectPluginSettings(ctx)

	switch settingID {
	case "ledger-config":
		return c.renderSettingsPanel(ctx, pluginSettings)
	default:
		return nil, contributor.ErrSettingNotFound
	}
}

// ─── Private Render Helpers ──────────────────────────────────────────────────

func (c *Contributor) renderOverview(ctx context.Context) (templ.Component, error) {
	totalPlans, _, err := fetchPlanStats(ctx, c.store, c.appID)
	if err != nil {
		totalPlans = 0
	}

	_, activeSubs, _, err := fetchSubscriptionStats(ctx, c.store, c.appID)
	if err != nil {
		activeSubs = 0
	}

	pendingInv, err := fetchInvoiceStats(ctx, c.store, c.appID)
	if err != nil {
		pendingInv = 0
	}

	coupons, err := fetchCoupons(ctx, c.store, c.appID, coupon.ListOpts{Limit: 1000})
	if err != nil {
		coupons = nil
	}

	recentInvoices, err := fetchInvoices(ctx, c.store, "", c.appID, invoice.ListOpts{Limit: 5})
	if err != nil {
		recentInvoices = nil
	}

	stats := pages.OverviewStats{
		TotalPlans:          totalPlans,
		ActiveSubscriptions: activeSubs,
		PendingInvoices:     pendingInv,
		ActiveCoupons:       len(coupons),
	}

	pluginSections := c.collectPluginSections(ctx)

	return templ.ComponentFunc(func(tCtx context.Context, w io.Writer) error {
		childCtx := templ.WithChildren(tCtx, components.PluginSections(pluginSections))
		return pages.OverviewPage(stats, recentInvoices).Render(childCtx, w)
	}), nil
}

func (c *Contributor) renderPlans(ctx context.Context, params contributor.Params) (templ.Component, error) {
	statusFilter := plan.Status(params.QueryParams["status"])
	opts := plan.ListOpts{Limit: 50, Status: statusFilter}

	plans, err := fetchPlans(ctx, c.store, c.appID, opts)
	if err != nil {
		return nil, fmt.Errorf("dashboard: render plans: %w", err)
	}

	return pages.PlansPage(plans), nil
}

func (c *Contributor) renderPlanDetail(ctx context.Context, params contributor.Params) (templ.Component, error) {
	planIDStr := params.PathParams["id"]
	if planIDStr == "" {
		planIDStr = params.QueryParams["id"]
	}
	if planIDStr == "" {
		return nil, contributor.ErrPageNotFound
	}

	planID, err := id.ParsePlanID(planIDStr)
	if err != nil {
		return nil, contributor.ErrPageNotFound
	}

	p, err := c.store.GetPlan(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: resolve plan: %w", err)
	}

	// Collect plugin sections.
	pluginSections := c.collectPlanDetailSections(ctx, planID)

	data := pages.PlanDetailData{
		Plan:         p,
		HasProviders: c.engine.HasProviders(),
	}

	return templ.ComponentFunc(func(tCtx context.Context, w io.Writer) error {
		childCtx := templ.WithChildren(tCtx, components.PluginSections(pluginSections))
		return pages.PlanDetailPage(data).Render(childCtx, w)
	}), nil
}

func (c *Contributor) renderPlanForm(ctx context.Context, params contributor.Params, isEdit bool) (templ.Component, error) {
	var p *plan.Plan
	if isEdit {
		planIDStr := params.PathParams["id"]
		if planIDStr == "" {
			planIDStr = params.QueryParams["id"]
		}
		if planIDStr == "" {
			return nil, contributor.ErrPageNotFound
		}
		planID, err := id.ParsePlanID(planIDStr)
		if err != nil {
			return nil, contributor.ErrPageNotFound
		}
		p, err = c.store.GetPlan(ctx, planID)
		if err != nil {
			return nil, fmt.Errorf("dashboard: resolve plan for edit: %w", err)
		}
	}

	// Handle form submission (POST).
	if name := params.FormData["name"]; name != "" {
		newPlan, err := pages.ParsePlanFromFormData(params.FormData)
		if err != nil {
			return pages.PlanFormPage(pages.PlanFormData{
				Plan: p, IsEdit: isEdit, Error: err.Error(), AppID: c.appID,
			}), nil
		}

		// Default app scope to contributor's configured appID.
		if newPlan.AppID == "" {
			newPlan.AppID = c.appID
		}

		if isEdit {
			newPlan.ID = p.ID
			newPlan.Entity = p.Entity
			newPlan.Entity.UpdatedAt = time.Now()
			if err := c.store.UpdatePlan(ctx, newPlan); err != nil {
				return pages.PlanFormPage(pages.PlanFormData{
					Plan: newPlan, IsEdit: true, Error: err.Error(), AppID: c.appID,
				}), nil
			}
			return pages.PlanDetailPage(pages.PlanDetailData{
				Plan:         newPlan,
				HasProviders: c.engine.HasProviders(),
			}), nil
		}

		if err := c.engine.CreatePlan(ctx, newPlan); err != nil {
			return pages.PlanFormPage(pages.PlanFormData{
				Plan: newPlan, IsEdit: false, Error: err.Error(), AppID: c.appID,
			}), nil
		}
		return pages.PlanDetailPage(pages.PlanDetailData{
			Plan:         newPlan,
			HasProviders: c.engine.HasProviders(),
		}), nil
	}

	// Initial render (GET).
	return pages.PlanFormPage(pages.PlanFormData{
		Plan: p, IsEdit: isEdit, AppID: c.appID,
	}), nil
}

func (c *Contributor) renderSubscriptions(ctx context.Context, params contributor.Params) (templ.Component, error) {
	statusFilter := subscription.Status(params.QueryParams["status"])
	opts := subscription.ListOpts{Limit: 50, Status: statusFilter}

	subs, err := fetchSubscriptions(ctx, c.store, "", c.appID, opts)
	if err != nil {
		return nil, fmt.Errorf("dashboard: render subscriptions: %w", err)
	}

	return pages.SubscriptionsPage(subs), nil
}

func (c *Contributor) renderSubscriptionDetail(ctx context.Context, params contributor.Params) (templ.Component, error) {
	subIDStr := params.PathParams["id"]
	if subIDStr == "" {
		subIDStr = params.QueryParams["id"]
	}
	if subIDStr == "" {
		return nil, contributor.ErrPageNotFound
	}

	subID, err := id.ParseSubscriptionID(subIDStr)
	if err != nil {
		return nil, contributor.ErrPageNotFound
	}

	sub, err := c.store.GetSubscription(ctx, subID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: resolve subscription: %w", err)
	}

	// Fetch associated plan.
	var p *plan.Plan
	if !sub.PlanID.IsNil() {
		p, _ = c.store.GetPlan(ctx, sub.PlanID)
	}

	// Fetch invoices for this subscription.
	invoices, _ := c.store.ListInvoices(ctx, sub.TenantID, sub.AppID, invoice.ListOpts{Limit: 20})

	data := pages.SubscriptionDetailData{
		Subscription: sub,
		Plan:         p,
		Invoices:     invoices,
		HasProviders: c.engine.HasProviders(),
	}

	pluginSections := c.collectSubscriptionDetailSections(ctx, subID)

	return templ.ComponentFunc(func(tCtx context.Context, w io.Writer) error {
		childCtx := templ.WithChildren(tCtx, components.PluginSections(pluginSections))
		return pages.SubscriptionDetailPage(data).Render(childCtx, w)
	}), nil
}

func (c *Contributor) renderSubscriptionForm(ctx context.Context, params contributor.Params) (templ.Component, error) {
	// Fetch available plans for the select dropdown.
	plans, err := fetchPlans(ctx, c.store, c.appID, plan.ListOpts{Limit: 100, Status: plan.StatusActive})
	if err != nil {
		plans = nil
	}

	// Handle form submission (POST).
	if tenantID := params.FormData["tenant_id"]; tenantID != "" {
		newSub, err := pages.ParseSubscriptionFromFormData(params.FormData)
		if err != nil {
			return pages.SubscriptionFormPage(pages.SubscriptionFormData{
				Plans: plans, Error: err.Error(), AppID: c.appID,
			}), nil
		}

		// Default app scope to contributor's configured appID.
		if newSub.AppID == "" {
			newSub.AppID = c.appID
		}

		if err := c.engine.CreateSubscription(ctx, newSub); err != nil {
			return pages.SubscriptionFormPage(pages.SubscriptionFormData{
				Plans: plans, Error: err.Error(), AppID: c.appID,
			}), nil
		}

		// Fetch the plan for the detail page.
		var subPlan *plan.Plan
		if !newSub.PlanID.IsNil() {
			subPlan, _ = c.store.GetPlan(ctx, newSub.PlanID)
		}

		return pages.SubscriptionDetailPage(pages.SubscriptionDetailData{
			Subscription: newSub,
			Plan:         subPlan,
			HasProviders: c.engine.HasProviders(),
		}), nil
	}

	// Initial render (GET).
	return pages.SubscriptionFormPage(pages.SubscriptionFormData{
		Plans: plans, AppID: c.appID,
	}), nil
}

func (c *Contributor) renderInvoices(ctx context.Context, params contributor.Params) (templ.Component, error) {
	statusFilter := invoice.Status(params.QueryParams["status"])
	opts := invoice.ListOpts{Limit: 50, Status: statusFilter}

	invoices, err := fetchInvoices(ctx, c.store, "", c.appID, opts)
	if err != nil {
		return nil, fmt.Errorf("dashboard: render invoices: %w", err)
	}

	return pages.InvoicesPage(invoices), nil
}

func (c *Contributor) renderInvoiceDetail(ctx context.Context, params contributor.Params) (templ.Component, error) {
	invIDStr := params.PathParams["id"]
	if invIDStr == "" {
		invIDStr = params.QueryParams["id"]
	}
	if invIDStr == "" {
		return nil, contributor.ErrPageNotFound
	}

	invID, err := id.ParseInvoiceID(invIDStr)
	if err != nil {
		return nil, contributor.ErrPageNotFound
	}

	inv, err := c.store.GetInvoice(ctx, invID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: resolve invoice: %w", err)
	}

	// Fetch subscription details.
	var sub *subscription.Subscription
	if !inv.SubscriptionID.IsNil() {
		sub, _ = c.store.GetSubscription(ctx, inv.SubscriptionID)
	}

	data := pages.InvoiceDetailData{
		Invoice:      inv,
		Subscription: sub,
		HasProviders: c.engine.HasProviders(),
	}

	pluginSections := c.collectInvoiceDetailSections(ctx, invID)

	return templ.ComponentFunc(func(tCtx context.Context, w io.Writer) error {
		childCtx := templ.WithChildren(tCtx, components.PluginSections(pluginSections))
		return pages.InvoiceDetailPage(data).Render(childCtx, w)
	}), nil
}

func (c *Contributor) renderCoupons(ctx context.Context, params contributor.Params) (templ.Component, error) {
	opts := coupon.ListOpts{Limit: 50}

	coupons, err := fetchCoupons(ctx, c.store, c.appID, opts)
	if err != nil {
		return nil, fmt.Errorf("dashboard: render coupons: %w", err)
	}

	return pages.CouponsPage(coupons), nil
}

func (c *Contributor) renderCouponDetail(ctx context.Context, params contributor.Params) (templ.Component, error) {
	couponIDStr := params.PathParams["id"]
	if couponIDStr == "" {
		couponIDStr = params.QueryParams["id"]
	}
	if couponIDStr == "" {
		return nil, contributor.ErrPageNotFound
	}

	couponID, err := id.ParseCouponID(couponIDStr)
	if err != nil {
		return nil, contributor.ErrPageNotFound
	}

	cpn, err := c.store.GetCouponByID(ctx, couponID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: resolve coupon: %w", err)
	}

	return pages.CouponDetailPage(cpn), nil
}

func (c *Contributor) renderCouponForm(ctx context.Context, params contributor.Params, isEdit bool) (templ.Component, error) {
	var cpn *coupon.Coupon
	if isEdit {
		couponIDStr := params.PathParams["id"]
		if couponIDStr == "" {
			couponIDStr = params.QueryParams["id"]
		}
		if couponIDStr == "" {
			return nil, contributor.ErrPageNotFound
		}
		couponID, err := id.ParseCouponID(couponIDStr)
		if err != nil {
			return nil, contributor.ErrPageNotFound
		}
		cpn, err = c.store.GetCouponByID(ctx, couponID)
		if err != nil {
			return nil, fmt.Errorf("dashboard: resolve coupon for edit: %w", err)
		}
	}

	// Handle form submission (POST).
	if code := params.FormData["code"]; code != "" {
		newCoupon, err := pages.ParseCouponFromFormData(params.FormData)
		if err != nil {
			return pages.CouponFormPage(pages.CouponFormData{
				Coupon: cpn, IsEdit: isEdit, Error: err.Error(), AppID: c.appID,
			}), nil
		}

		// Default app scope to contributor's configured appID.
		if newCoupon.AppID == "" {
			newCoupon.AppID = c.appID
		}

		if isEdit {
			newCoupon.ID = cpn.ID
			newCoupon.Entity = cpn.Entity
			newCoupon.Entity.UpdatedAt = time.Now()
			newCoupon.TimesRedeemed = cpn.TimesRedeemed
			if err := c.store.UpdateCoupon(ctx, newCoupon); err != nil {
				return pages.CouponFormPage(pages.CouponFormData{
					Coupon: newCoupon, IsEdit: true, Error: err.Error(), AppID: c.appID,
				}), nil
			}
			return pages.CouponDetailPage(newCoupon), nil
		}

		if err := c.store.CreateCoupon(ctx, newCoupon); err != nil {
			return pages.CouponFormPage(pages.CouponFormData{
				Coupon: newCoupon, IsEdit: false, Error: err.Error(), AppID: c.appID,
			}), nil
		}
		return pages.CouponDetailPage(newCoupon), nil
	}

	// Initial render (GET).
	return pages.CouponFormPage(pages.CouponFormData{
		Coupon: cpn, IsEdit: isEdit, AppID: c.appID,
	}), nil
}

func (c *Contributor) renderFeatures(ctx context.Context, params contributor.Params) (templ.Component, error) {
	opts := feature.ListOpts{Limit: 50}
	if statusStr := params.QueryParams["status"]; statusStr != "" {
		opts.Status = feature.Status(statusStr)
	}

	features, err := c.store.ListFeatures(ctx, c.appID, opts)
	if err != nil {
		return nil, fmt.Errorf("dashboard: render features: %w", err)
	}

	// Also include global features.
	globalFeatures, err := c.store.ListGlobalFeatures(ctx, opts)
	if err != nil {
		globalFeatures = nil
	}

	// Merge: app-scoped first, then global.
	allFeatures := make([]*feature.Feature, 0, len(features)+len(globalFeatures))
	allFeatures = append(allFeatures, features...)
	allFeatures = append(allFeatures, globalFeatures...)

	return pages.FeaturesPage(pages.FeatureListData{
		Features: allFeatures,
		AppID:    c.appID,
	}), nil
}

func (c *Contributor) renderFeatureDetail(ctx context.Context, params contributor.Params) (templ.Component, error) {
	featureIDStr := params.PathParams["id"]
	if featureIDStr == "" {
		featureIDStr = params.QueryParams["id"]
	}
	if featureIDStr == "" {
		return nil, contributor.ErrPageNotFound
	}

	featureID, err := id.ParseFeatureID(featureIDStr)
	if err != nil {
		return nil, contributor.ErrPageNotFound
	}

	f, err := c.store.GetFeature(ctx, featureID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: resolve feature: %w", err)
	}

	return pages.FeatureDetailPage(pages.FeatureDetailData{
		Feature:      f,
		HasProviders: c.engine.HasProviders(),
	}), nil
}

func (c *Contributor) renderFeatureForm(ctx context.Context, params contributor.Params, isEdit bool) (templ.Component, error) {
	var f *feature.Feature
	if isEdit {
		featureIDStr := params.PathParams["id"]
		if featureIDStr == "" {
			featureIDStr = params.QueryParams["id"]
		}
		if featureIDStr == "" {
			return nil, contributor.ErrPageNotFound
		}
		featureID, err := id.ParseFeatureID(featureIDStr)
		if err != nil {
			return nil, contributor.ErrPageNotFound
		}
		f, err = c.store.GetFeature(ctx, featureID)
		if err != nil {
			return nil, fmt.Errorf("dashboard: resolve feature for edit: %w", err)
		}
	}

	// Handle form submission (POST).
	if key := params.FormData["key"]; key != "" {
		newFeature, err := pages.ParseFeatureFromFormData(params.FormData)
		if err != nil {
			return pages.FeatureFormPage(pages.FeatureFormData{
				Feature: f, IsEdit: isEdit, Error: err.Error(), AppID: c.appID,
			}), nil
		}

		// Default app scope to contributor's configured appID.
		if newFeature.AppID == "" {
			newFeature.AppID = c.appID
		}

		if isEdit {
			newFeature.ID = f.ID
			newFeature.Entity = f.Entity
			newFeature.Entity.UpdatedAt = time.Now()
			if err := c.store.UpdateFeature(ctx, newFeature); err != nil {
				return pages.FeatureFormPage(pages.FeatureFormData{
					Feature: newFeature, IsEdit: true, Error: err.Error(), AppID: c.appID,
				}), nil
			}
			return pages.FeatureDetailPage(pages.FeatureDetailData{
				Feature:      newFeature,
				HasProviders: c.engine.HasProviders(),
			}), nil
		}

		if err := c.engine.CreateFeature(ctx, newFeature); err != nil {
			return pages.FeatureFormPage(pages.FeatureFormData{
				Feature: newFeature, IsEdit: false, Error: err.Error(), AppID: c.appID,
			}), nil
		}
		return pages.FeatureDetailPage(pages.FeatureDetailData{
			Feature:      newFeature,
			HasProviders: c.engine.HasProviders(),
		}), nil
	}

	// Initial render (GET).
	return pages.FeatureFormPage(pages.FeatureFormData{
		Feature: f, IsEdit: isEdit, AppID: c.appID,
	}), nil
}

func (c *Contributor) renderUsage(ctx context.Context, params contributor.Params) (templ.Component, error) {
	tenantID := params.QueryParams["tenant_id"]
	featureKey := params.QueryParams["feature_key"]

	opts := meter.QueryOpts{
		FeatureKey: featureKey,
		Limit:      50,
	}

	events, err := fetchUsageEvents(ctx, c.store, tenantID, c.appID, opts)
	if err != nil {
		events = nil
	}

	return pages.UsagePage(events), nil
}

func (c *Contributor) renderSettings(_ context.Context) (templ.Component, error) {
	data := pages.SettingsPageData{
		MeterBatchSize:      100,
		MeterFlushInterval:  "5s",
		EntitlementCacheTTL: "30s",
		HasProviders:        c.engine.HasProviders(),
		ProviderNames:       c.getProviderNames(),
	}
	return pages.SettingsPage(data), nil
}

// ─── Widget Render Helpers ───────────────────────────────────────────────────

func (c *Contributor) renderStatsWidget(ctx context.Context) (templ.Component, error) {
	totalPlans, _, err := fetchPlanStats(ctx, c.store, c.appID)
	if err != nil {
		totalPlans = 0
	}

	_, activeSubs, _, err := fetchSubscriptionStats(ctx, c.store, c.appID)
	if err != nil {
		activeSubs = 0
	}

	pendingInv, err := fetchInvoiceStats(ctx, c.store, c.appID)
	if err != nil {
		pendingInv = 0
	}

	coupons, err := fetchCoupons(ctx, c.store, c.appID, coupon.ListOpts{Limit: 1000})
	if err != nil {
		coupons = nil
	}

	return widgets.StatsWidget(totalPlans, activeSubs, pendingInv, len(coupons)), nil
}

func (c *Contributor) renderRecentInvoicesWidget(ctx context.Context) (templ.Component, error) {
	invoices, err := fetchInvoices(ctx, c.store, "", c.appID, invoice.ListOpts{Limit: 5})
	if err != nil || invoices == nil {
		return widgets.RecentInvoicesWidget(nil), nil
	}
	return widgets.RecentInvoicesWidget(invoices), nil
}

// ─── Settings Render Helper ──────────────────────────────────────────────────

func (c *Contributor) renderSettingsPanel(_ context.Context, pluginSettings []templ.Component) (templ.Component, error) {
	data := pages.SettingsPageData{
		MeterBatchSize:      100,
		MeterFlushInterval:  "5s",
		EntitlementCacheTTL: "30s",
		HasProviders:        c.engine.HasProviders(),
		ProviderNames:       c.getProviderNames(),
	}

	return templ.ComponentFunc(func(tCtx context.Context, w io.Writer) error {
		childCtx := templ.WithChildren(tCtx, components.PluginSections(pluginSettings))
		return pages.SettingsPage(data).Render(childCtx, w)
	}), nil
}

// ─── Plugin Helpers ──────────────────────────────────────────────────────────

func (c *Contributor) dashboardPlugins() []DashboardPlugin {
	var dps []DashboardPlugin
	for _, p := range c.plugins {
		if dp, ok := p.(DashboardPlugin); ok {
			dps = append(dps, dp)
		}
	}
	return dps
}

func (c *Contributor) collectPluginSections(ctx context.Context) []templ.Component {
	var sections []templ.Component
	for _, dp := range c.dashboardPlugins() {
		for _, w := range dp.DashboardWidgets(ctx) {
			sections = append(sections, w.Render(ctx))
		}
	}
	return sections
}

func (c *Contributor) collectPluginSettings(ctx context.Context) []templ.Component {
	var panels []templ.Component
	for _, dp := range c.dashboardPlugins() {
		if panel := dp.DashboardSettingsPanel(ctx); panel != nil {
			panels = append(panels, panel)
		}
	}
	return panels
}

func (c *Contributor) collectPlanDetailSections(ctx context.Context, planID id.PlanID) []templ.Component {
	var sections []templ.Component
	for _, p := range c.plugins {
		if pdc, ok := p.(PlanDetailContributor); ok {
			if section := pdc.DashboardPlanDetailSection(ctx, planID); section != nil {
				sections = append(sections, section)
			}
		}
	}
	return sections
}

func (c *Contributor) collectSubscriptionDetailSections(ctx context.Context, subID id.SubscriptionID) []templ.Component {
	var sections []templ.Component
	for _, p := range c.plugins {
		if sdc, ok := p.(SubscriptionDetailContributor); ok {
			if section := sdc.DashboardSubscriptionDetailSection(ctx, subID); section != nil {
				sections = append(sections, section)
			}
		}
	}
	return sections
}

func (c *Contributor) collectInvoiceDetailSections(ctx context.Context, invID id.InvoiceID) []templ.Component {
	var sections []templ.Component
	for _, p := range c.plugins {
		if idc, ok := p.(InvoiceDetailContributor); ok {
			if section := idc.DashboardInvoiceDetailSection(ctx, invID); section != nil {
				sections = append(sections, section)
			}
		}
	}
	return sections
}

// ─── Provider Sync & Payment Methods Handlers ───────────────────────────────

func (c *Contributor) renderPaymentMethods(ctx context.Context, params contributor.Params) (templ.Component, error) {
	tenantID := params.QueryParams["tenant_id"]

	data := pages.PaymentMethodsData{
		TenantID:     tenantID,
		HasProviders: c.engine.HasProviders(),
	}

	if tenantID != "" && data.HasProviders {
		methods, err := c.engine.ListPaymentMethods(ctx, tenantID)
		if err != nil {
			data.Error = err.Error()
		} else {
			data.Methods = methods
		}
	}

	return pages.PaymentMethodsPage(data), nil
}

func (c *Contributor) handlePlanSync(ctx context.Context, params contributor.Params) (templ.Component, error) {
	planIDStr := params.QueryParams["id"]
	if planIDStr == "" {
		return nil, contributor.ErrPageNotFound
	}

	planID, err := id.ParsePlanID(planIDStr)
	if err != nil {
		return nil, contributor.ErrPageNotFound
	}

	result, syncErr := c.engine.SyncPlanToProvider(ctx, planID)

	p, err := c.store.GetPlan(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: resolve plan after sync: %w", err)
	}

	data := pages.PlanDetailData{
		Plan:         p,
		HasProviders: c.engine.HasProviders(),
		SyncResult:   result,
	}
	if syncErr != nil {
		data.SyncError = syncErr.Error()
	}

	pluginSections := c.collectPlanDetailSections(ctx, planID)

	return templ.ComponentFunc(func(tCtx context.Context, w io.Writer) error {
		childCtx := templ.WithChildren(tCtx, components.PluginSections(pluginSections))
		return pages.PlanDetailPage(data).Render(childCtx, w)
	}), nil
}

func (c *Contributor) handleFeatureSync(ctx context.Context, params contributor.Params) (templ.Component, error) {
	featureIDStr := params.QueryParams["id"]
	if featureIDStr == "" {
		return nil, contributor.ErrPageNotFound
	}

	featureID, err := id.ParseFeatureID(featureIDStr)
	if err != nil {
		return nil, contributor.ErrPageNotFound
	}

	result, syncErr := c.engine.SyncFeatureToProvider(ctx, featureID)

	f, err := c.store.GetFeature(ctx, featureID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: resolve feature after sync: %w", err)
	}

	data := pages.FeatureDetailData{
		Feature:      f,
		HasProviders: c.engine.HasProviders(),
		SyncResult:   result,
	}
	if syncErr != nil {
		data.SyncError = syncErr.Error()
	}

	return pages.FeatureDetailPage(data), nil
}

func (c *Contributor) handleSubscriptionSync(ctx context.Context, params contributor.Params) (templ.Component, error) {
	subIDStr := params.QueryParams["id"]
	if subIDStr == "" {
		return nil, contributor.ErrPageNotFound
	}

	subID, err := id.ParseSubscriptionID(subIDStr)
	if err != nil {
		return nil, contributor.ErrPageNotFound
	}

	result, syncErr := c.engine.SyncSubscriptionToProvider(ctx, subID)

	sub, err := c.store.GetSubscription(ctx, subID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: resolve subscription after sync: %w", err)
	}

	var p *plan.Plan
	if !sub.PlanID.IsNil() {
		p, _ = c.store.GetPlan(ctx, sub.PlanID)
	}

	invoices, _ := c.store.ListInvoices(ctx, sub.TenantID, sub.AppID, invoice.ListOpts{Limit: 20})

	data := pages.SubscriptionDetailData{
		Subscription: sub,
		Plan:         p,
		Invoices:     invoices,
		HasProviders: c.engine.HasProviders(),
		SyncResult:   result,
	}
	if syncErr != nil {
		data.SyncError = syncErr.Error()
	}

	pluginSections := c.collectSubscriptionDetailSections(ctx, subID)

	return templ.ComponentFunc(func(tCtx context.Context, w io.Writer) error {
		childCtx := templ.WithChildren(tCtx, components.PluginSections(pluginSections))
		return pages.SubscriptionDetailPage(data).Render(childCtx, w)
	}), nil
}

func (c *Contributor) handleInvoiceSync(ctx context.Context, params contributor.Params) (templ.Component, error) {
	invIDStr := params.QueryParams["id"]
	if invIDStr == "" {
		return nil, contributor.ErrPageNotFound
	}

	invID, err := id.ParseInvoiceID(invIDStr)
	if err != nil {
		return nil, contributor.ErrPageNotFound
	}

	result, syncErr := c.engine.SyncInvoiceToProvider(ctx, invID)

	inv, err := c.store.GetInvoice(ctx, invID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: resolve invoice after sync: %w", err)
	}

	var sub *subscription.Subscription
	if !inv.SubscriptionID.IsNil() {
		sub, _ = c.store.GetSubscription(ctx, inv.SubscriptionID)
	}

	data := pages.InvoiceDetailData{
		Invoice:      inv,
		Subscription: sub,
		HasProviders: c.engine.HasProviders(),
		SyncResult:   result,
	}
	if syncErr != nil {
		data.SyncError = syncErr.Error()
	}

	pluginSections := c.collectInvoiceDetailSections(ctx, invID)

	return templ.ComponentFunc(func(tCtx context.Context, w io.Writer) error {
		childCtx := templ.WithChildren(tCtx, components.PluginSections(pluginSections))
		return pages.InvoiceDetailPage(data).Render(childCtx, w)
	}), nil
}

// getProviderNames returns the names of all registered payment providers.
func (c *Contributor) getProviderNames() []string {
	var names []string
	for _, p := range c.plugins {
		if pp, ok := p.(plugin.PaymentProviderPlugin); ok {
			names = append(names, pp.Provider().Name())
		}
	}
	return names
}

// knownPageRoutes is the set of top-level page routes that the dashboard handles.
var knownPageRoutes = map[string]bool{
	"/":                     true,
	"/plans":                true,
	"/plans/detail":         true,
	"/plans/new":            true,
	"/plans/edit":           true,
	"/plans/sync":           true,
	"/subscriptions":        true,
	"/subscriptions/detail": true,
	"/subscriptions/new":    true,
	"/subscriptions/sync":   true,
	"/invoices":             true,
	"/invoices/detail":      true,
	"/invoices/sync":        true,
	"/coupons":              true,
	"/coupons/detail":       true,
	"/coupons/new":          true,
	"/coupons/edit":         true,
	"/features":             true,
	"/features/detail":      true,
	"/features/new":         true,
	"/features/edit":        true,
	"/features/sync":        true,
	"/payment-methods":      true,
	"/usage":                true,
	"/settings":             true,
}
