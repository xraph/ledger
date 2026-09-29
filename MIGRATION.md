# Migrating off the templ dashboard

Ledger's templ dashboard is being retired. The pages under `dashboard/` will be replaced by contract-driven surfaces in the Forge dashboard, and the directory itself gets deleted in a later phase. This file is the record of what those pages did and what changed in the engine underneath them.

This is written for you if you're the one switching the templ dashboard off, or if you run Ledger in production and want to know what the engine does now. If you're doing the migration work itself, the plans live elsewhere. This file is what's left once they're done.

The whole templ dashboard is wired in at one place: `Extension.DashboardContributor` in `extension/extension.go`, which builds `dashboard.New(dashboard.NewManifest(...), ...)`. Remove that and the pages are gone.

## Status per surface

One row per templ page or widget. A surface is migrated when a contract-backed page replaces it, dropped when we decide nobody needs it, and blocked until then. At the end of Phase A nothing has a contract, so every row is blocked. Fill the rows in as Phases B and C land.

| Surface | Route or ID | Status |
|---|---|---|
| Overview | `/` | blocked: no contract yet |
| Plans list | `/plans` | blocked: no contract yet |
| Plan detail and provider sync | `/plans/detail`, `/plans/sync` | blocked: no contract yet |
| Plan form | `/plans/new`, `/plans/edit` | blocked: no contract yet |
| Subscriptions list | `/subscriptions` | blocked: no contract yet |
| Subscription detail and provider sync | `/subscriptions/detail`, `/subscriptions/sync` | blocked: no contract yet |
| Subscription form | `/subscriptions/new` | blocked: no contract yet |
| Invoices list | `/invoices` | blocked: no contract yet |
| Invoice detail and provider sync | `/invoices/detail`, `/invoices/sync` | blocked: no contract yet |
| Coupons list | `/coupons` | blocked: no contract yet |
| Coupon detail | `/coupons/detail` | blocked: no contract yet |
| Coupon form | `/coupons/new`, `/coupons/edit` | blocked: no contract yet |
| Features list | `/features` | blocked: no contract yet |
| Feature detail and provider sync | `/features/detail`, `/features/sync` | blocked: no contract yet |
| Feature form | `/features/new`, `/features/edit` | blocked: no contract yet |
| Usage events | `/usage` | blocked: no contract yet |
| Payment methods | `/payment-methods` | blocked: no contract yet |
| Settings page and settings panel | `/settings`, settings ID `ledger-config` | blocked: no contract yet |
| Billing Stats widget | widget ID `ledger-stats` | blocked: no contract yet |
| Recent Invoices widget | widget ID `ledger-recent-invoices` | blocked: no contract yet |

## The templ dashboard, as it was

Everything below was read from the templ sources and from `dashboard/contributor.go`, `dashboard/data.go`, `dashboard/manifest.go`, `dashboard/plugin_iface.go` and `dashboard/pages/stubs.go` at the end of Phase A. There are 30 `.templ` files: 19 under `pages/` (`plan_form_script.templ` is the plan form's inline JavaScript, not a page of its own), 9 under `components/` and 2 under `widgets/`. When the directory is deleted this section is the only description left, so it errs on the side of detail.

### How it was put together

Every route is resolved by `Contributor.RenderPage`, a switch on the route string. `knownPageRoutes` lists the same 25 routes. Detail and edit routes check `PathParams["id"]` first and fall back to `?id=`. Every link the templates build uses `?id=`, and the sync routes read only `?id=`. A missing or unparseable id returns `contributor.ErrPageNotFound`.

Navigation is htmx. Rows and buttons issue `hx-get` with `hx-target="#content"`, `hx-swap="innerHTML"` and `hx-push-url="true"`, using relative paths (`./plans/new` from a list page, `../plans` from a detail page). Forms `hx-post` back to their own route. There's no separate POST handler: the render function treats a request as a submission when a known form field is non-empty (`name` for plans, `code` for coupons, `key` for features, `tenant_id` for subscriptions).

On success, a form renders the detail page directly in the same response. It doesn't redirect, so the URL stays on `/new` or `/edit`. On failure it re-renders the form with the error in a red card above it.

Every list has a fixed limit set in the contributor, and no page has a pagination control:

| Call | Limit |
|---|---|
| Plans, subscriptions, invoices, coupons, features, usage lists | 50 |
| Subscription detail's invoice list | 20 |
| Overview recent invoices, Recent Invoices widget | 5 |
| Plan count, active subscription count, coupon count (overview and stats widget) | 1000 |
| Plans offered in the subscription form | 100, active only |

Dates render as `Jan 02, 2006` in tables and `Jan 02, 2006 15:04` on detail pages, in whatever zone the stored time carries. Long ids and tenant ids are cut to 16 or 20 characters with `...` appended. Money renders through `types.Money.String()`.

The dashboard reads tenant-scoped lists with an empty tenant id. `renderSubscriptions`, `renderInvoices`, the overview's recent invoices, the Recent Invoices widget and `fetchSubscriptionStats` all pass `""` as the tenant, and `/usage` passes whatever `?tenant_id=` holds, which is usually nothing. Every backend treats an empty tenant as "every tenant in this app" (see the known constraints below), so the old dashboard showed all tenants' subscriptions, invoices and usage for the configured app on one screen. It gets wider than that. The extension's `Config.AppID` defaults to `""` (`extension/config.go`), and every store skips the app filter when the app id is empty, so a deployment that never set an app id saw every row of every app. Don't carry any of that into a contract a tenant-scoped caller can reach.

### Manifest

`NewManifest` produced:

- Name `ledger`, display name `Ledger`, icon `receipt`, version `0.1.0`, layout `extension`, sidebar shown.
- A top bar titled `Ledger` with logo icon `receipt`, accent colour `#10b981`, search enabled, and one ghost action, `API Docs`, linking to `/docs`.
- Capability `searchable`.
- Nav groups and items, in priority order:

| Group | Label | Path | Icon |
|---|---|---|---|
| Ledger | Overview | `/` | `layout-dashboard` |
| Billing | Plans | `/plans` | `layers` |
| Billing | Subscriptions | `/subscriptions` | `credit-card` |
| Billing | Invoices | `/invoices` | `file-text` |
| Billing | Coupons | `/coupons` | `ticket` |
| Billing | Features | `/features` | `puzzle-piece` |
| Billing | Payment Methods | `/payment-methods` | `wallet` |
| Metering | Usage | `/usage` | `bar-chart-3` |
| Configuration | Settings | `/settings` | `settings` |

- Two widgets, both size `md` in group `Ledger`: `ledger-stats` ("Billing Stats", "Plans, subscriptions, and invoice counts", refresh every 60 seconds) and `ledger-recent-invoices` ("Recent Invoices", "Latest generated invoices", refresh every 30 seconds).
- One settings descriptor, `ledger-config` ("Billing Settings", "Configure billing behavior", icon `receipt`).

### Sub-plugin extension points

`dashboard/plugin_iface.go` declares five interfaces a Ledger plugin could implement to put its own UI into these pages:

| Interface | What it contributed |
|---|---|
| `DashboardPlugin` | Widgets (appended to the manifest and rendered as sections at the bottom of the overview), a settings panel (appended below the Settings page), and extra pages with their own nav items, all in the `Ledger` nav group at priority 10 |
| `DashboardPageContributor` | Nav items plus a `DashboardRenderPage(ctx, route, params)` that is asked first for every route, before the built-in switch |
| `PlanDetailContributor` | A section at the bottom of the plan detail page |
| `SubscriptionDetailContributor` | A section at the bottom of the subscription detail page |
| `InvoiceDetailContributor` | A section at the bottom of the invoice detail page |

Nothing in this repository implements any of them. They're built on `templ.Component`, so they die with the package, and there's no replacement for them in Phase A.

### Shared components

`components/` holds the tables and badges the pages reuse. The tables are described with the pages that use them. The badges map as follows (variants are ForgeUI's):

| Badge | Value | Label | Variant |
|---|---|---|---|
| Plan status | `active` | Active | default |
| | `draft` | Draft | secondary |
| | `archived` | Archived | outline |
| Subscription status | `active` | Active | default |
| | `trialing` | Trialing | secondary |
| | `past_due` | Past Due | destructive |
| | `canceled` | Canceled | outline |
| | `expired` | Expired | destructive |
| | `paused` | Paused | secondary |
| Invoice status | `paid` | Paid | default |
| | `pending` | Pending | outline |
| | `draft` | Draft | secondary |
| | `past_due` | Past Due | destructive |
| | `voided` | Voided | destructive |
| Plan feature type | `metered` | Metered | default |
| | `boolean` | Boolean | secondary |
| | `seat` | Seat | outline |
| Coupon type | `percentage` | Percentage | secondary |
| | `amount` | Fixed Amount | default |
| Line item type | `base` | Base | default |
| | `usage` | Usage | secondary |
| | `overage` | Overage | destructive |
| | `seat` | Seat | outline |
| | `discount` | Discount | secondary |
| | `tax` | Tax | outline |
| Tier type (plan detail only) | `graduated` | Graduated | default |
| | `volume` | Volume | secondary |
| | `flat` | Flat | outline |
| Catalog feature status (features pages) | `active` / `draft` / `archived` | the raw value, capitalised | default / secondary / outline |

Any value not listed falls back to the raw string on a secondary badge.

`EmptyState` is a centred block with a 48px muted icon, a title and an optional one-line description. `StatCard` is a card with a label, a 16px icon, a large value and an optional subtitle. `PluginSections` renders plugin components with a separator between each. `ResolveIcon` maps eleven icon names to lucide icons and falls back to `info`.

### Overview (`/`)

| | |
|---|---|
| Template | `pages/overview.templ` |
| Heading | "Billing Overview", "Monitor your billing system at a glance." |
| Stat cards | Total Plans (count of up to 1000 plans, "Billing plans configured"); Active Subscriptions (subscriptions with status `active` among the first 1000 across all tenants, "Currently active"); Pending Invoices (`ListPendingInvoices` for the app, "Awaiting payment"); Active Coupons ("Discount codes available") |
| Recent invoices card | Last 5 invoices across all tenants. Columns: Invoice (id, truncated), Tenant, Status (badge), Total, Created. Row click opens invoice detail. "View all" link to `/invoices` |
| Quick actions | Create Plan (`/plans/new`), New Subscription (`/subscriptions/new`), Create Coupon (`/coupons/new`) |
| Filters | None |
| Empty state | "No invoices yet." in the recent invoices card |
| Plugin slot | `DashboardPlugin` widgets rendered at the bottom |

Active Coupons counts every coupon the app has, up to 1000, whatever its validity window or redemption count. Any stat whose query fails shows 0 without an error.

### Plans list (`/plans`)

| | |
|---|---|
| Template | `pages/plans.templ`, `components/plan_table.templ` |
| Heading | "Plans" with a count badge |
| Columns | Name, Slug, Status (badge), Currency (upper-cased), Features (count), Price (`Pricing.BaseAmount`, or `-` when there's no pricing), Created |
| Actions | Create Plan button; row click opens plan detail |
| Filters | `?status=` query parameter passed to `ListPlans`. No control on the page sets it |
| Empty state | Icon `layers`, "No plans yet", "Create your first billing plan to get started." |

### Plan detail (`/plans/detail?id=`, `/plans/sync?id=`)

| | |
|---|---|
| Template | `pages/plan_detail.templ` |
| Header | Plan name, slug, a scope badge ("App: <id>" or "Global"), status badge, Edit button to `/plans/edit` |
| Fields | Plan ID, Name, Slug, Description (if set), Currency, Status, Trial Days, App ID (if set), Created, Updated |
| Features card | Count badge. Columns: Key, Name, Type (badge), Limit (`Unlimited` for -1; a dash for boolean features), Period (a dash for boolean), Metering (a "Soft" badge with "overage tracked", or a "Hard" badge; a dash for boolean). Empty: "No features defined." |
| Pricing card | Only when the plan has pricing. Base Amount and Billing Period, then a Pricing Tiers table: Feature, Type (tier badge), Up To (`∞` when 0, the number otherwise, so a `-1` tier shows `-1`), Unit Amount, Flat Amount, Priority |
| Metadata card | Only when metadata exists; one field per key |
| Provider sync card | Only when a payment provider is registered. Shows the current provider name and id, or "Not yet synced to a provider.", and a Sync to Provider button that posts to `/plans/sync?id=`, which calls `Ledger.SyncPlanToProvider` and re-renders this page with a green success line or a red error line |
| Actions | Back to Plans, Edit, Sync to Provider |
| Plugin slot | `PlanDetailContributor` sections at the bottom |

### Plan form (`/plans/new`, `/plans/edit?id=`)

| | |
|---|---|
| Templates | `pages/plan_form.templ`, `pages/plan_form_script.templ` |
| Heading | "Create Plan" / "Edit Plan" with a one-line description |
| Layout | Four tabs: Details, Features, Pricing, Metadata |
| Details tab | Plan Name (required), Slug (required, "URL-friendly identifier. Must be unique per app."), Description, Currency (USD, EUR, GBP), Status (Draft, Active, Archived), Trial Days, Application Scope (App ID text input with a live "App: <id>" / "Global" badge; defaults to the contributor's app id) |
| Features tab | Repeating cards with Add Feature and a remove button per card. Each card: Key, Name, Type (Metered, Boolean, Seat, with a one-line description that changes with the type), Limit ("-1 = unlimited", hidden for boolean), Reset Period (Monthly, Yearly, None; hidden for boolean), Soft Limit checkbox (hidden for boolean and seat). A reference block explains the three types |
| Pricing tab | Base Amount in cents, Billing Period (Monthly, Yearly), then repeating tier cards with Add Pricing Tier: Feature Key (a select of the keys entered on the Features tab), Tier Type (Graduated, Volume, Flat), Up To ("0 = unlimited"), Unit Amount (cents), Flat Amount (cents), Priority. A reference block explains the three tier types |
| Metadata tab | Repeating key/value rows with Add Metadata and remove |
| Actions | Back to Plans, Cancel, Create Plan / Save Changes |
| Submission | `ParsePlanFromFormData` reads the hidden `features_json`, `tiers_json` and `metadata_json` inputs. The per-row inputs (`features[0].key` and so on) are posted too, and ignored. The script tries to refill the hidden inputs on `htmx:configRequest`, which is too late (see below). Rows with an empty key are dropped. A missing tier priority becomes the row index. Boolean features with limit 0 are saved with limit 1 |
| Create | `Ledger.CreatePlan` |
| Edit | `store.UpdatePlan` directly, not through the engine |

As far as the code and htmx's documentation show, nothing you change on the Features, Pricing tiers or Metadata tabs ever reached the server. The hidden inputs are rendered with the stored plan's values (`plan_form.templ` lines 69 to 71), and they're empty on create. The script's listener on `htmx:configRequest` (`plan_form_script.templ` line 394) calls `syncPlanFormData`, which only assigns each hidden input's `.value` (lines 341, 369 and 388) and never touches `event.detail.parameters`. htmx documents `htmx:configRequest` as firing after it has collected the request's parameters, and the Forge dashboard shell loads htmx 2.0.4, so the fresh JSON lands in the DOM after the request body is already built. On create, that gives you a plan with no features, no tiers and no metadata (Base Amount and Billing Period are plain inputs and do get through). On edit, the stored features, tiers and metadata are posted back as they were, and every change made on those three tabs is dropped without an error. We haven't watched this happen in a browser. It follows from the code and the documented event order.

On edit, the handler copies only `ID` and the `Entity` timestamps from the stored plan onto the one it parsed from the form, and every backend's `UpdatePlan` writes the whole record. Anything the form does not carry is saved empty: the plan's `ProviderID` and `ProviderName`, the pricing's `ID` and `PlanID`, and each plan feature's `ID`, `CatalogID` and `Metadata`. That last group goes because `featuresJSONValue` and `parseFeaturesJSON` carry only key, name, type, limit, period and soft limit. Plan-level `Metadata` survives, since it round-trips through `metadata_json`. So saving a plan from this form unlinks it from its payment provider, cuts each feature's link to the catalog, and drops any `pricing_strategy` or `aggregator` a feature named in its own metadata.

### Subscriptions list (`/subscriptions`)

| | |
|---|---|
| Template | `pages/subscriptions.templ`, `components/subscription_table.templ` |
| Heading | "Subscriptions" with a count badge |
| Columns | Subscription (id, truncated), Tenant, Plan (plan id, truncated), Status (badge), Period Start, Period End, Created |
| Actions | Create Subscription button; row click opens subscription detail |
| Filters | `?status=` query parameter. No control on the page sets it. Tenant is always empty, so every tenant in the app is listed |
| Empty state | Icon `credit-card`, "No subscriptions yet", "Create your first subscription to start billing." |

### Subscription detail (`/subscriptions/detail?id=`, `/subscriptions/sync?id=`)

| | |
|---|---|
| Template | `pages/subscription_detail.templ` |
| Header | "Subscription", the full id, status badge |
| Fields | Subscription ID, Tenant ID, Plan (plan name linking to plan detail plus the id, or just the id if the plan cannot be loaded), Status, App ID, Period Start, Period End, then Trial Start, Trial End, Canceled At, Cancel At, Ended At, Provider, Provider ID when set, Created, Updated |
| Plan Details card | Only when the plan loads: Plan Name, Slug, Currency, Base Price and Billing Period (when priced), Features (count), View Full Plan button |
| Invoices card | Count badge, described as "Invoices generated for this subscription". Columns: Invoice, Status (badge), Total, Period, Created. Row click opens invoice detail. Empty: "No invoices generated yet." |
| Metadata card | Only when metadata exists |
| Provider sync card | As on plan detail, posting to `/subscriptions/sync?id=` (`Ledger.SyncSubscriptionToProvider`) |
| Actions | Back to Subscriptions, plan links, Sync to Provider |
| Plugin slot | `SubscriptionDetailContributor` sections at the bottom |

The Invoices card does not filter by subscription. It calls `ListInvoices(sub.TenantID, sub.AppID, Limit 20)`, so it shows up to 20 of the tenant's invoices from every subscription that tenant has in the app. The page has no cancel, pause or resume action, does not show `Subscription.Quantity` (added in Phase A), and does not list applied coupons.

### Subscription form (`/subscriptions/new`)

| | |
|---|---|
| Template | `pages/subscription_form.templ` |
| Heading | "Create Subscription", "Set up a new subscription for a tenant." |
| Fields | Tenant ID (required, free text), Plan (required select of up to 100 active plans, "Name (slug)"), Initial Status (Active, Trialing) |
| Actions | Back to Subscriptions, Cancel, Create Subscription |
| Submission | `ParseSubscriptionFromFormData`, then `Ledger.CreateSubscription`; renders subscription detail on success |

There is no edit route for subscriptions. The form cannot set quantities, trial dates or metadata.

### Invoices list (`/invoices`)

| | |
|---|---|
| Template | `pages/invoices.templ`, `components/invoice_table.templ` |
| Heading | "Invoices" with a count badge |
| Columns | Invoice (id, truncated), Tenant, Status (badge), Total, Period (`Jan 02 - Jan 02, 2006`), Created |
| Actions | Row click opens invoice detail. No create button: invoices cannot be generated from the dashboard |
| Filters | `?status=` query parameter. No control on the page sets it. Tenant is always empty |
| Empty state | Icon `file-text`, "No invoices yet", "Invoices will appear here as subscriptions are billed." |

### Invoice detail (`/invoices/detail?id=`, `/invoices/sync?id=`)

| | |
|---|---|
| Template | `pages/invoice_detail.templ` |
| Header | "Invoice", the full id, status badge, total and currency |
| Fields | Invoice ID, Tenant ID, Subscription (link to subscription detail when it loads, otherwise the id), Status, Currency, Period Start, Period End, then Due Date, Paid At, Voided At, Void Reason, Payment Reference, Provider ID when set, Created, Updated |
| Line items card | Columns: Description, Feature (key, or `-`), Type (line item badge), Quantity, Unit Amount, Amount. Empty: "No line items." |
| Summary card | Subtotal; Discount (green, prefixed `-`) only when the discount is above zero; Tax only when above zero; Total |
| Metadata card | Only when metadata exists |
| Provider sync card | As on plan detail, posting to `/invoices/sync?id=` (`Ledger.SyncInvoiceToProvider`) |
| Actions | Back to Invoices, Sync to Provider. No finalize, mark paid or void action, although the engine has `FinalizeInvoice`, `MarkInvoicePaid` and `MarkInvoiceVoided` |
| Plugin slot | `InvoiceDetailContributor` sections at the bottom |

### Coupons list (`/coupons`)

| | |
|---|---|
| Template | `pages/coupons.templ`, `components/coupon_table.templ` |
| Heading | "Coupons" with a count badge |
| Columns | Code, Name, Type (badge), Value (`20%` or the money amount), Redemptions (`times redeemed / max`, or just the count when unlimited), Valid Until (date, or "No expiry"), Created |
| Actions | Create Coupon button; row click opens coupon detail |
| Filters | None |
| Empty state | Icon `ticket`, "No coupons yet", "Create your first coupon to offer discounts." |

### Coupon detail (`/coupons/detail?id=`)

| | |
|---|---|
| Template | `pages/coupon_detail.templ` |
| Header | Coupon name, code, type badge, Edit button |
| Fields | Coupon ID, Code, Name, Type, Percentage or Amount, Currency |
| Redemption Stats card | Times Redeemed, Max Redemptions ("Unlimited" when 0), Remaining (max minus redeemed, or "Unlimited") |
| Validity Period card | Valid From ("No start date"), Valid Until ("No expiry"), Created, Updated |
| Metadata card | Only when metadata exists |
| Actions | Back to Coupons, Edit. No delete, no provider sync, and no list of the subscriptions a coupon is applied to |

### Coupon form (`/coupons/new`, `/coupons/edit?id=`)

| | |
|---|---|
| Template | `pages/coupon_form.templ` |
| Heading | "Create Coupon" / "Edit Coupon" |
| Fields | Coupon Code (required), Name (required), Discount Type (Percentage, Fixed Amount), Currency (USD, EUR, GBP), Percentage, Amount in cents, Max Redemptions ("0 for unlimited") |
| Actions | Back to Coupons, Cancel, Create Coupon / Save Changes |
| Create | `store.CreateCoupon` directly |
| Edit | `store.UpdateCoupon` directly |

The form has no inputs for `ValidFrom`, `ValidUntil` or `Metadata`. On edit the handler copies `ID`, the `Entity` timestamps and `TimesRedeemed` from the stored coupon and nothing else, and `UpdateCoupon` writes every column. Saving a coupon from this form clears its validity window and its metadata. It also writes back the `TimesRedeemed` it read at the start of the same request (`renderCouponForm` re-reads the coupon on the POST), so a redemption that commits between that read and the write is undone. That's constraint 6 below in its most direct form. Nothing in the dashboard applies a coupon to a subscription.

### Features list (`/features`)

| | |
|---|---|
| Template | `pages/features.templ` |
| Heading | "Features" with a count badge |
| Data | The app's catalog features, plus the global ones (`ListGlobalFeatures`) when the contributor has an app id |
| Columns | Key, Name, Type (badge), Default Limit (`Unlimited` for -1; a dash for boolean), Status (badge), Scope ("App: <id>" or "Global"), Created (relative: "just now", "5m ago", "3d ago" and so on) |
| Actions | New Feature button; row click opens feature detail |
| Filters | `?status=` query parameter. No control on the page sets it |
| Empty state | Puzzle icon, "No features yet", "Create your first catalog feature to share across plans." (drawn inline, not with `EmptyState`) |

### Feature detail (`/features/detail?id=`, `/features/sync?id=`)

| | |
|---|---|
| Template | `pages/feature_detail.templ` |
| Header | Feature name, key, scope badge, status badge, Edit button |
| Fields | Feature ID, Key, Name, Description (if set), Type, then for non-boolean features Default Limit, Period and Soft Limit (overage allowed, or hard limit enforced); Status, App ID or Scope "Global", Created, Updated |
| Metadata card | Only when metadata exists, with a count badge |
| Provider sync card | As on plan detail, posting to `/features/sync?id=` (`Ledger.SyncFeatureToProvider`) |
| Actions | Back to Features, Edit, Sync to Provider. No archive or delete, although the engine has `ArchiveFeature` and `DeleteFeature` |

### Feature form (`/features/new`, `/features/edit?id=`)

| | |
|---|---|
| Template | `pages/feature_form.templ` |
| Heading | "Create Feature" / "Edit Feature" |
| Fields | Feature Key (required), Name (required), Description, Feature Type (Metered, Boolean, Seat), Status (Draft, Active, Archived), Default Limit ("-1 for unlimited"), Reset Period (Monthly, Yearly, None), Allow overage (soft limit) checkbox, App ID (optional, empty for global) |
| Actions | Back to Features, Cancel, Create Feature / Save Changes |
| Create | `Ledger.CreateFeature` |
| Edit | `store.UpdateFeature` directly |

The form has no metadata input, and on edit the handler keeps only `ID` and the `Entity` timestamps. Saving a feature from this form clears its metadata and its `ProviderID` and `ProviderName`.

### Usage events (`/usage`)

| | |
|---|---|
| Template | `pages/usage.templ`, `components/usage_table.templ` |
| Heading | "Usage Events", "View metered usage events across all tenants." |
| Columns | Event ID (truncated), Tenant, Feature Key, Quantity, Timestamp (`Jan 02, 2006 15:04`) |
| Actions | None. Rows are not clickable |
| Filters | `?tenant_id=` and `?feature_key=` query parameters, passed to `QueryUsage` with limit 50. No control on the page sets them |
| Empty state | Icon `bar-chart-3`, "No usage events", "Usage events will appear here as features are consumed." A failed query also shows this, without an error |

### Payment methods (`/payment-methods`)

| | |
|---|---|
| Template | `pages/payment_methods.templ` |
| Heading | "Payment Methods", with a note that methods are fetched live from the provider and never stored |
| No provider | A card: "No Payment Providers Configured", explaining that a payment provider plugin is needed |
| With a provider | A Tenant ID text box and a Look Up button (a `GET` form back to `/payment-methods?tenant_id=`). With a tenant, `Ledger.ListPaymentMethods` fills a table: Type (badge), Brand, Last 4 (masked), Expiry (`MM/YYYY`, or a dash), Default (badge or a dash), Provider (badge) |
| Empty state | "No payment methods found for this tenant." A provider error shows in a red card |

This is the only page with a working filter control.

### Settings (`/settings`, settings panel `ledger-config`)

| | |
|---|---|
| Template | `pages/settings.templ` |
| Heading | "Settings", "Ledger configuration and runtime settings." |
| Metering Configuration | Batch Size `100`, Flush Interval `5s` |
| Entitlement Cache | Cache TTL `30s` |
| Payment Providers | A chip per registered provider name and a note that sync works for plans, features, subscriptions and invoices; or "Dev Mode" with "No payment providers configured. Sync and payment features are disabled." |
| Plugin slot | `DashboardPlugin` settings panels below a separator (the settings panel only; the `/settings` page renders the same template without them) |
| Actions | None. Read only |

The three numbers are literals in `renderSettings` and `renderSettingsPanel`. They are not read from the engine, so the page shows 100, 5s and 30s whatever the engine was configured with.

### Billing Stats widget (`ledger-stats`)

`widgets/stats.templ`. A two-by-two grid of stat cards: Plans ("Total plans"), Subscriptions ("Active"), Invoices ("Pending"), Coupons ("Available"). Same queries as the overview's stat cards, with the same caveats.

### Recent Invoices widget (`ledger-recent-invoices`)

`widgets/recent_invoices.templ`. The last 5 invoices across all tenants. Columns: Tenant, Status (badge), Total. Rows are not clickable. Empty or failed: "No invoices yet."

## What the engine could not do before Phase A

The templ dashboard showed coupons, tiers, tax lines and seat features. Before Phase A the engine behind it didn't do much with any of them.

Coupons were stored and never redeemed. You could create one, edit it and see its redemption count, and nothing ever attached it to a subscription or took it off an invoice.

Tier pricing produced zero-amount overage lines. A plan could define a graduated, volume or flat ladder and the invoice would still charge nothing for usage past the allowance.

Tax was always zero. Seats were a label on a feature with no count behind them, so there was nothing to price.

Four plugin hooks, `TaxCalculator`, `CouponValidator`, `UsageAggregator` and `PricingStrategy`, were registered by the plugin registry and never called by anything.

## What Phase A changed

Each line names the commits, oldest first, from `git log --oneline 7fe72a3..HEAD`.

- Task 0, a conformance suite in `store/storetest` that every backend runs (`4724279` to `da9c917`).
- Task 1, `Money.Percent` for discount arithmetic (`6284844`, `470682f`).
- Tasks 2 and 3, graduated, volume and flat tier pricing behind `invoice.ComputeOverage`, and `invoice.ValidateTiers` (`a56e086` to `77ebf76`).
- Tasks 4 to 6, recording coupon applications on memory, sqlite, postgres and mongo (`55353d1` to `c448c73`).
- Task 7, `Ledger.ApplyCoupon`, which runs the built-in checks and then every registered `CouponValidator` (`0eedc49`, `4613ccc`).
- Task 12, atomic redemption through `store.RedeemCoupon`, so the redemption cap holds under concurrency (`51fd9bd`, `c01c390`).
- Task 13, the mongo fixes the conformance suite forced, plus memory's idempotency-key dedup (`11489da` to `7b4c933`).
- Task 8, per-feature quantities on a subscription (`Subscription.Quantity`) for seat pricing (`e52dc29`).
- Task 9, `GenerateInvoice` pricing overage, seats, discounts and tax, calling every registered `TaxCalculator` (`38e8dcd` to `06a9f6c`).
- Task 10, `UsageAggregator` and `PricingStrategy` called during billing, chosen by the `aggregator` key in a feature's metadata and by `pricing_strategy` in the feature's metadata, falling back to the plan's (`9cb30a4`, `a0525b4`).
- Task 14, overflow-checked money arithmetic and half-open usage windows (`edbb399`).
- Task 15, times normalised to UTC on write in every backend (`edc6224`, `4addffe`).

### Pricing semantics you need to know

If you set up plans for merchants, these are the rules `GenerateInvoice` follows now.

- A tier's `UpTo` counts total usage for the period, not units past the allowance. An `UpTo` of `-1` or `0` means unbounded.
- A metered feature's included allowance (its `Limit`) is honoured once, and seat features have no allowance. Graduated pricing charges the price of total usage minus the price of the allowance. Volume takes its rate from the tier that total usage reaches and applies it to the units past the allowance.
- A ladder with no unbounded tier extends its last tier to any usage above its highest `UpTo`.
- Flat overage is the band fee at total usage minus the band fee at the allowance, never below zero.
- Percentage coupons are computed against the pre-discount subtotal. Two percentage coupons don't compound.
- Amount coupons subtract their flat amount.
- The discount line records the full discount even when it exceeds the subtotal, and the net amount clamps at zero.
- Tax is charged on the net amount, after discounts.

## Bugs the conformance suite found in the existing backends

Running the same suite against all four backends turned up these. All are fixed in Phase A unless the entry says otherwise.

- Mongo's `IngestBatch` silently dropped every usage event without an idempotency key after the first one. The index on the key was sparse and unique, and grove writes the empty key as `""`, which a sparse index still indexes. A partial index replaces it.
- Mongo couldn't read back plans whose features or pricing had no id. Fixed.
- Mongo's `Migrate()` ignored `migrations.go` and built its indexes from `migrationIndexes()`. The two are now kept in step, and there's a new versioned migration for the index change.
- Memory's `IngestBatch` never deduplicated idempotency keys. Fixed.
- Sqlite couldn't read back a bare `time.Now()`, and `Ledger.Meter` and `Ledger.CreateSubscription` both write one. Every subscription created through `Ledger` on sqlite was unreadable. Task 15 fixed this for new writes. **Rows written before the fix are still unreadable and need manual repair.** If you ran Ledger on sqlite before `edc6224`, check your subscriptions and usage events before you upgrade.
- Usage windows were closed at both ends on sqlite, postgres and mongo and open at both ends on memory, so an event exactly on a boundary was billed twice or never. Every backend now uses `[Start, End)`.

## Known constraints and open problems

None of these are fixed. Read them before you assume Ledger handles the case for you.

1. An empty tenant id on `ListSubscriptions`, `ListInvoices` or `QueryUsage` matches every tenant's rows for the app, on all four backends. The conformance suite pins this (`EmptyTenantIDBehavior` in `store/storetest/storetest.go`), so it can't change silently. Any caller that fails to resolve a tenant and passes `""` through leaks every tenant's data. If the app id is empty too, the stores skip the app filter as well, and the extension's `Config.AppID` defaults to empty, so a deployment with no app id leaks every row of every app. The templ dashboard relies on it (see above). The dashboard contract layer must refuse an unresolvable tenant, because the stores won't.
2. Sub-cent unit prices can't be represented. `types.Money` holds integer minor units. A plan that needs a fraction of a cent per unit needs a scaled money type, which touches every price in the system and wants its own spec.
3. `store.Aggregate` sums usage from the start of the current calendar period (worked out from the time of the call), not from the subscription's `CurrentPeriodStart`. A plugin `UsageAggregator` is given the subscription's own `CurrentPeriodStart` and `CurrentPeriodEnd`. The two agree only for calendar-aligned subscriptions. `Entitled()` also uses `store.Aggregate`, so a feature with a custom aggregator is counted one way for quota enforcement and another way for billing. This behaviour predates Phase A on all four backends and Phase A didn't change it.
4. An applied coupon applies to every invoice for that subscription, indefinitely. There's no once or repeating duration. There's also no API to detach an applied coupon, so a coupon that later becomes invalid (after the plan's currency changes, for example) blocks that subscription's billing until the coupon is edited or deleted, and that edit or delete hits every subscriber who has it.
5. `GenerateInvoice` has no per-period idempotency. Call it twice for the same period and you get two invoices.
6. `UpdateCoupon` writes every column, `times_redeemed` included. An edit that races a redemption can roll the count back and let the cap be exceeded.
7. The mongo store doesn't persist `plan.Feature.CatalogID`. Its `featureModel` in `store/mongo/models.go` has no field for it, so on mongo the link from a plan feature to its catalog feature is lost on write. Memory, sqlite and postgres keep it: memory holds the struct as it is, and sqlite and postgres store plan features as a JSON column where the `catalog_id` tag round-trips. The templ plan edit form drops it on every backend (see the plan form above).
8. `provider.Provider.HandleWebhook(ctx, payload)` takes no signature parameter, and `Ledger.HandleWebhook` passes the payload to the named provider without verifying anything. Nothing in Ledger signs or hashes today, so there's no bug to fix yet, and the cost lands on whoever writes the first real payment provider. Interfaces shaped like this tend to stay that way: the first implementer verifies inside its own `HandleWebhook` instead of changing a method every other provider already implements, and the second one forgets to.
9. Sqlite needs a busy timeout in its DSN, or concurrent coupon redemptions return a raw `database is locked` error instead of `ErrCouponExhausted`. The documented DSN in `docs/content/docs/stores/sqlite.mdx` now carries `?_pragma=busy_timeout(5000)`. If you copied the old one, add it.
10. Mongo coupon redemption is compensating, not transactional, because Ledger doesn't assume a replica set. Between the application insert and a compensating delete, a concurrent invoice can see the application. A crash inside that window leaves it there.
11. Rolling deploys on mongo: if an old binary runs its `Migrate` after a new one has, it recreates the old sparse index beside the new partial one, and keyless events start dropping again until a new binary migrates. Finish the rollout before you trust keyless ingestion.
12. Store coverage is real but uneven. Memory and sqlite run the conformance suite on every `go test ./...`. Postgres runs it only when `LEDGER_TEST_POSTGRES_DSN` names a server, and mongo only when `LEDGER_TEST_MONGO_URI` does. Otherwise they skip. During Phase A both were exercised live against real servers. A skipped suite looks exactly like a passing one in `go test` output, so run with `-v` and look for `SKIP` before you believe a green run covered postgres or mongo.
13. `InvoiceFormatter` is still registered and never called. It gets a caller in the dashboard contract phase.
14. `ListInvoices` filters by containment on every backend: the invoice's period must lie inside `[Start, End]`. That deliberately differs from `QueryUsage`'s half-open `[Start, End)`, because an invoice period is a range and a usage event is an instant.
15. The memory store ignores `Limit` and `Offset` on `ListInvoices`. It's a test double, not a production backend.
