package extension

import (
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/plugin"
	"github.com/xraph/ledger/store"
)

// Option configures the Ledger Forge extension.
type Option func(*Extension)

// WithStore sets the store for the ledger engine.
func WithStore(s store.Store) Option {
	return func(e *Extension) {
		e.store = s
	}
}

// WithLedgerOption passes a ledger.Option through to the underlying engine.
func WithLedgerOption(opt ledger.Option) Option {
	return func(e *Extension) {
		e.ledgerOpts = append(e.ledgerOpts, opt)
	}
}

// WithPlugin registers a ledger plugin.
func WithPlugin(p plugin.Plugin) Option {
	return func(e *Extension) {
		e.ledgerOpts = append(e.ledgerOpts, ledger.WithPlugin(p))
	}
}

// WithConfig sets the Forge extension configuration.
func WithConfig(cfg Config) Option {
	return func(e *Extension) { e.config = cfg }
}

// WithDisableRoutes prevents HTTP route registration.
func WithDisableRoutes() Option {
	return func(e *Extension) { e.config.DisableRoutes = true }
}

// WithDisableMigrate prevents auto-migration on start.
func WithDisableMigrate() Option {
	return func(e *Extension) { e.config.DisableMigrate = true }
}

// WithBasePath sets the URL prefix for ledger routes.
func WithBasePath(path string) Option {
	return func(e *Extension) { e.config.BasePath = path }
}

// WithRequireConfig requires config to be present in YAML files.
// If true and no config is found, Register returns an error.
func WithRequireConfig(require bool) Option {
	return func(e *Extension) { e.config.RequireConfig = require }
}

// WithMeterBatchSize sets the number of usage events to buffer before flushing.
func WithMeterBatchSize(size int) Option {
	return func(e *Extension) { e.config.MeterBatchSize = size }
}

// WithMeterFlushInterval sets how frequently the meter buffer is flushed.
func WithMeterFlushInterval(d time.Duration) Option {
	return func(e *Extension) { e.config.MeterFlushInterval = d }
}

// WithEntitlementCacheTTL sets the entitlement check cache duration.
func WithEntitlementCacheTTL(d time.Duration) Option {
	return func(e *Extension) { e.config.EntitlementCacheTTL = d }
}

// WithGroveDatabase sets the name of the grove.DB to resolve from the DI container.
// The extension will auto-construct the appropriate store backend (postgres/sqlite/mongo)
// based on the grove driver type. Pass an empty string to use the default (unnamed) grove.DB.
func WithGroveDatabase(name string) Option {
	return func(e *Extension) {
		e.config.GroveDatabase = name
		e.useGrove = true
	}
}
