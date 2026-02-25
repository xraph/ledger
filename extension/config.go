package extension

import "time"

// Config holds the Ledger extension configuration.
// Fields can be set programmatically via Option functions or loaded from
// YAML configuration files (under "extensions.ledger" or "ledger" keys).
type Config struct {
	// DisableRoutes prevents HTTP route registration.
	DisableRoutes bool `json:"disable_routes" mapstructure:"disable_routes" yaml:"disable_routes"`

	// DisableMigrate prevents auto-migration on start.
	DisableMigrate bool `json:"disable_migrate" mapstructure:"disable_migrate" yaml:"disable_migrate"`

	// BasePath is the URL prefix for ledger routes (default: "/ledger").
	BasePath string `json:"base_path" mapstructure:"base_path" yaml:"base_path"`

	// MeterBatchSize is the number of usage events to buffer before flushing
	// to the store (default: 100).
	MeterBatchSize int `json:"meter_batch_size" mapstructure:"meter_batch_size" yaml:"meter_batch_size"`

	// MeterFlushInterval is how frequently the meter buffer is flushed
	// even if the batch size has not been reached (default: 5s).
	MeterFlushInterval time.Duration `json:"meter_flush_interval" mapstructure:"meter_flush_interval" yaml:"meter_flush_interval"`

	// EntitlementCacheTTL controls how long entitlement check results are
	// cached in-process before re-evaluating against the store (default: 30s).
	EntitlementCacheTTL time.Duration `json:"entitlement_cache_ttl" mapstructure:"entitlement_cache_ttl" yaml:"entitlement_cache_ttl"`

	// GroveDatabase is the name of a grove.DB registered in the DI container.
	// When set, the extension resolves this named database and auto-constructs
	// the appropriate store based on the driver type (pg/sqlite/mongo).
	// When empty and WithGroveDatabase was called, the default (unnamed) DB is used.
	GroveDatabase string `json:"grove_database" mapstructure:"grove_database" yaml:"grove_database"`

	// RequireConfig requires config to be present in YAML files.
	// If true and no config is found, Register returns an error.
	RequireConfig bool `json:"-" yaml:"-"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		MeterBatchSize:      100,
		MeterFlushInterval:  5 * time.Second,
		EntitlementCacheTTL: 30 * time.Second,
	}
}
