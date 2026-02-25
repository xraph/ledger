package postgres

import (
	"context"

	"github.com/xraph/grove/migrate"
)

// Migrations is the grove migration group for the Ledger store.
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
    trial_days  INT NOT NULL DEFAULT 0,
    features    JSONB NOT NULL DEFAULT '[]',
    pricing     JSONB,
    app_id      TEXT NOT NULL DEFAULT '',
    metadata    JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
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
    current_period_start TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    current_period_end   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    trial_start          TIMESTAMPTZ,
    trial_end            TIMESTAMPTZ,
    canceled_at          TIMESTAMPTZ,
    cancel_at            TIMESTAMPTZ,
    ended_at             TIMESTAMPTZ,
    app_id               TEXT NOT NULL DEFAULT '',
    provider_id          TEXT NOT NULL DEFAULT '',
    provider_name        TEXT NOT NULL DEFAULT '',
    metadata             JSONB NOT NULL DEFAULT '{}',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
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
    quantity        BIGINT NOT NULL DEFAULT 0,
    timestamp       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    idempotency_key TEXT NOT NULL DEFAULT '',
    metadata        JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
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
    allowed     BOOLEAN NOT NULL DEFAULT FALSE,
    feature     TEXT NOT NULL DEFAULT '',
    used        BIGINT NOT NULL DEFAULT 0,
    cache_limit BIGINT NOT NULL DEFAULT 0,
    remaining   BIGINT NOT NULL DEFAULT 0,
    soft_limit  BOOLEAN NOT NULL DEFAULT FALSE,
    reason      TEXT NOT NULL DEFAULT '',
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
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
    subtotal_amount_cents BIGINT NOT NULL DEFAULT 0,
    subtotal_currency     TEXT NOT NULL DEFAULT '',
    tax_amount_cents      BIGINT NOT NULL DEFAULT 0,
    tax_currency          TEXT NOT NULL DEFAULT '',
    discount_amount_cents BIGINT NOT NULL DEFAULT 0,
    discount_currency     TEXT NOT NULL DEFAULT '',
    total_amount_cents    BIGINT NOT NULL DEFAULT 0,
    total_currency        TEXT NOT NULL DEFAULT '',
    line_items            JSONB NOT NULL DEFAULT '[]',
    period_start          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    period_end            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    due_date              TIMESTAMPTZ,
    paid_at               TIMESTAMPTZ,
    voided_at             TIMESTAMPTZ,
    void_reason           TEXT NOT NULL DEFAULT '',
    payment_ref           TEXT NOT NULL DEFAULT '',
    provider_id           TEXT NOT NULL DEFAULT '',
    app_id                TEXT NOT NULL DEFAULT '',
    metadata              JSONB NOT NULL DEFAULT '{}',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
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
    amount_cents    BIGINT NOT NULL DEFAULT 0,
    amount_currency TEXT NOT NULL DEFAULT '',
    percentage      INT NOT NULL DEFAULT 0,
    currency        TEXT NOT NULL DEFAULT '',
    max_redemptions INT NOT NULL DEFAULT 0,
    times_redeemed  INT NOT NULL DEFAULT 0,
    valid_from      TIMESTAMPTZ,
    valid_until     TIMESTAMPTZ,
    app_id          TEXT NOT NULL DEFAULT '',
    metadata        JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
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
