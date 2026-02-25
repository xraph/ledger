package sqlite

import (
	"context"

	"github.com/xraph/grove/migrate"
)

// Migrations is the grove migration group for the Ledger store (SQLite).
var Migrations = migrate.NewGroup("ledger")

func init() {
	Migrations.MustRegister(
		&migrate.Migration{
			Name:    "create_ledger_plans",
			Version: "20240101000001",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS ledger_plans (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL DEFAULT '',
    slug        TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    currency    TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'draft',
    trial_days  INTEGER NOT NULL DEFAULT 0,
    features    TEXT NOT NULL DEFAULT '[]',
    pricing     TEXT,
    app_id      TEXT NOT NULL DEFAULT '',
    metadata    TEXT NOT NULL DEFAULT '{}',
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_ledger_plans_app_id ON ledger_plans (app_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_ledger_plans_slug_app ON ledger_plans (slug, app_id);
CREATE INDEX IF NOT EXISTS idx_ledger_plans_status ON ledger_plans (app_id, status);
`)
				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS ledger_plans`)
				return err
			},
		},
		&migrate.Migration{
			Name:    "create_ledger_subscriptions",
			Version: "20240101000002",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS ledger_subscriptions (
    id                   TEXT PRIMARY KEY,
    tenant_id            TEXT NOT NULL DEFAULT '',
    plan_id              TEXT NOT NULL DEFAULT '',
    status               TEXT NOT NULL DEFAULT 'active',
    current_period_start TEXT NOT NULL DEFAULT (datetime('now')),
    current_period_end   TEXT NOT NULL DEFAULT (datetime('now')),
    trial_start          TEXT,
    trial_end            TEXT,
    canceled_at          TEXT,
    cancel_at            TEXT,
    ended_at             TEXT,
    app_id               TEXT NOT NULL DEFAULT '',
    provider_id          TEXT NOT NULL DEFAULT '',
    provider_name        TEXT NOT NULL DEFAULT '',
    metadata             TEXT NOT NULL DEFAULT '{}',
    created_at           TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at           TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_ledger_subs_tenant_app ON ledger_subscriptions (tenant_id, app_id);
CREATE INDEX IF NOT EXISTS idx_ledger_subs_status ON ledger_subscriptions (tenant_id, app_id, status);
CREATE INDEX IF NOT EXISTS idx_ledger_subs_plan ON ledger_subscriptions (plan_id);
`)
				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS ledger_subscriptions`)
				return err
			},
		},
		&migrate.Migration{
			Name:    "create_ledger_usage_events",
			Version: "20240101000003",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS ledger_usage_events (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL DEFAULT '',
    app_id          TEXT NOT NULL DEFAULT '',
    feature_key     TEXT NOT NULL DEFAULT '',
    quantity        INTEGER NOT NULL DEFAULT 0,
    timestamp       TEXT NOT NULL DEFAULT (datetime('now')),
    idempotency_key TEXT NOT NULL DEFAULT '',
    metadata        TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_ledger_usage_tenant_app_feature ON ledger_usage_events (tenant_id, app_id, feature_key, timestamp);
CREATE INDEX IF NOT EXISTS idx_ledger_usage_timestamp ON ledger_usage_events (timestamp);
CREATE UNIQUE INDEX IF NOT EXISTS idx_ledger_usage_idempotency ON ledger_usage_events (idempotency_key) WHERE idempotency_key != '';
`)
				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS ledger_usage_events`)
				return err
			},
		},
		&migrate.Migration{
			Name:    "create_ledger_entitlement_cache",
			Version: "20240101000004",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS ledger_entitlement_cache (
    cache_key   TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL DEFAULT '',
    app_id      TEXT NOT NULL DEFAULT '',
    feature_key TEXT NOT NULL DEFAULT '',
    allowed     INTEGER NOT NULL DEFAULT 0,
    feature     TEXT NOT NULL DEFAULT '',
    used        INTEGER NOT NULL DEFAULT 0,
    cache_limit INTEGER NOT NULL DEFAULT 0,
    remaining   INTEGER NOT NULL DEFAULT 0,
    soft_limit  INTEGER NOT NULL DEFAULT 0,
    reason      TEXT NOT NULL DEFAULT '',
    expires_at  TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_ledger_cache_tenant_app ON ledger_entitlement_cache (tenant_id, app_id);
CREATE INDEX IF NOT EXISTS idx_ledger_cache_expires ON ledger_entitlement_cache (expires_at);
`)
				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS ledger_entitlement_cache`)
				return err
			},
		},
		&migrate.Migration{
			Name:    "create_ledger_invoices",
			Version: "20240101000005",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS ledger_invoices (
    id                    TEXT PRIMARY KEY,
    tenant_id             TEXT NOT NULL DEFAULT '',
    subscription_id       TEXT NOT NULL DEFAULT '',
    status                TEXT NOT NULL DEFAULT 'draft',
    currency              TEXT NOT NULL DEFAULT '',
    subtotal_amount_cents INTEGER NOT NULL DEFAULT 0,
    subtotal_currency     TEXT NOT NULL DEFAULT '',
    tax_amount_cents      INTEGER NOT NULL DEFAULT 0,
    tax_currency          TEXT NOT NULL DEFAULT '',
    discount_amount_cents INTEGER NOT NULL DEFAULT 0,
    discount_currency     TEXT NOT NULL DEFAULT '',
    total_amount_cents    INTEGER NOT NULL DEFAULT 0,
    total_currency        TEXT NOT NULL DEFAULT '',
    line_items            TEXT NOT NULL DEFAULT '[]',
    period_start          TEXT NOT NULL DEFAULT (datetime('now')),
    period_end            TEXT NOT NULL DEFAULT (datetime('now')),
    due_date              TEXT,
    paid_at               TEXT,
    voided_at             TEXT,
    void_reason           TEXT NOT NULL DEFAULT '',
    payment_ref           TEXT NOT NULL DEFAULT '',
    provider_id           TEXT NOT NULL DEFAULT '',
    app_id                TEXT NOT NULL DEFAULT '',
    metadata              TEXT NOT NULL DEFAULT '{}',
    created_at            TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at            TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_ledger_invoices_tenant_app ON ledger_invoices (tenant_id, app_id);
CREATE INDEX IF NOT EXISTS idx_ledger_invoices_status ON ledger_invoices (app_id, status);
CREATE INDEX IF NOT EXISTS idx_ledger_invoices_period ON ledger_invoices (tenant_id, app_id, period_start, period_end);
CREATE INDEX IF NOT EXISTS idx_ledger_invoices_sub ON ledger_invoices (subscription_id);
`)
				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS ledger_invoices`)
				return err
			},
		},
		&migrate.Migration{
			Name:    "create_ledger_coupons",
			Version: "20240101000006",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS ledger_coupons (
    id              TEXT PRIMARY KEY,
    code            TEXT NOT NULL DEFAULT '',
    name            TEXT NOT NULL DEFAULT '',
    type            TEXT NOT NULL DEFAULT '',
    amount_cents    INTEGER NOT NULL DEFAULT 0,
    amount_currency TEXT NOT NULL DEFAULT '',
    percentage      INTEGER NOT NULL DEFAULT 0,
    currency        TEXT NOT NULL DEFAULT '',
    max_redemptions INTEGER NOT NULL DEFAULT 0,
    times_redeemed  INTEGER NOT NULL DEFAULT 0,
    valid_from      TEXT,
    valid_until     TEXT,
    app_id          TEXT NOT NULL DEFAULT '',
    metadata        TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_ledger_coupons_code_app ON ledger_coupons (code, app_id);
CREATE INDEX IF NOT EXISTS idx_ledger_coupons_app ON ledger_coupons (app_id);
`)
				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS ledger_coupons`)
				return err
			},
		},
	)
}
